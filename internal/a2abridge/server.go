// Package a2abridge connects adk agents to the a2a-go server and client.
package a2abridge

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"html"
	"iter"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"
)

// needInputPrefix is the sentinel the worker agent emits when it needs
// clarification from the caller.
const needInputPrefix = "NEED_INPUT:"

// maxToolCallsPerTurn caps how many tool calls one agent turn may make before
// the executor force-stops the runner loop. adk (v1.4.0) has no iteration cap
// of its own, so a looping model would otherwise call tools forever. The limit
// is generous — a legitimate turn uses at most a handful of tools.
const maxToolCallsPerTurn = 12

// The a2asrv in-memory task store gob-encodes tasks to persist them. A widget
// DataPart's Data is an interface value, so gob needs the concrete container
// types our widgets carry (nested maps and slices-of-maps) registered up front,
// or task persistence fails with "type not registered for interface".
func init() {
	gob.Register(map[string]any{})
	gob.Register([]map[string]any{})
	gob.Register([]any{})
}

// ANSI colors for the (gray) agent↔LLM loop trace lines on the worker's output.
const (
	gray  = "\033[90m"
	reset = "\033[0m"
)

// compactArgs renders function-call arguments as compact JSON for a one-line
// trace, falling back to Go's default formatting if marshaling fails.
func compactArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	if b, err := json.Marshal(args); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", args)
}

// pendingHITL tracks the two-step refund pause for a session: first the yes/no
// confirmation, then (after approval) the card details for the refund.
type pendingHITL struct {
	awaitingCard bool   // false: awaiting yes/no; true: awaiting the card number
	callID       string // adk_request_confirmation call ID to resume
	orderID      string // order the refund applies to (for prompts/widgets)
}

type executor struct {
	runner *runner.Runner
	trace  *Tracer

	mu             sync.Mutex
	pendingConfirm map[string]pendingHITL // keyed by A2A contextID
}

// NewExecutor wraps an adk Runner as an a2asrv.AgentExecutor.
// trace may be nil to disable protocol tracing.
func NewExecutor(r *runner.Runner, trace *Tracer) a2asrv.AgentExecutor {
	return &executor{runner: r, trace: trace, pendingConfirm: make(map[string]pendingHITL)}
}

// Execute implements a2asrv.AgentExecutor.
func (e *executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		newTask := ec.StoredTask == nil
		sessionID := ec.ContextID // = A2A contextID for history continuity across turns
		e.trace.Logf("▶ incoming A2A request | contextID=%s newTask=%v", ec.ContextID, newTask)

		// 1. Emit a submitted task if this is a new task.
		if newTask {
			e.trace.Logf("  → emit: submitted task")
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		} else {
			e.trace.Logf("  resuming stored task | id=%s", ec.StoredTask.ID)
		}

		// 2. Emit working status.
		e.trace.Logf("  → emit: working")
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// 3. Build the runner input. If a confirmation is pending for this session,
		//    this turn carries the user's yes/no answer, not a fresh request.
		var userText string
		if ec.Message != nil && len(ec.Message.Parts) > 0 {
			userText = ec.Message.Parts[0].Text()
		}

		e.mu.Lock()
		p, hasPending := e.pendingConfirm[sessionID]
		if hasPending {
			delete(e.pendingConfirm, sessionID)
		}
		e.mu.Unlock()

		var msg *genai.Content
		var cardLast4 string // set when this turn carries validated card details
		switch {
		case hasPending && !p.awaitingCard:
			// Step 1 of the refund HITL: the yes/no confirmation answer.
			approved := parseAffirmative(userText)
			e.trace.Logf("  confirmation answer=%q → approved=%v", userText, approved)
			if !approved {
				// Short-circuit: never feed a rejection to adk (avoids the
				// ErrConfirmationRejected tool-error path); tell the user directly.
				e.trace.Logf("  → emit: artifact + completed | refund declined by user")
				if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart("Возврат отменён по вашему решению.")), nil) {
					return
				}
				yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
				return
			}
			// Approved → the refund still does NOT run: ask for the card the
			// money goes to. Same task pauses input-required a second time.
			e.mu.Lock()
			e.pendingConfirm[sessionID] = pendingHITL{awaitingCard: true, callID: p.callID, orderID: p.orderID}
			e.mu.Unlock()
			question := refundCardQuestion(p.orderID)
			e.trace.Logf("  → emit: input-required (card details) | order=%s", p.orderID)
			ask := a2a.NewMessage(a2a.MessageRoleAgent,
				a2a.NewTextPart(question),
				refundFormWidget(p.orderID, question, false),
			)
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, ask), nil)
			return

		case hasPending && p.awaitingCard:
			// Step 2: the card-number reply. Validated by CODE (Luhn), never by
			// the LLM; the full number is neither logged nor echoed back.
			digits := cardDigits(userText)
			if digits == "" {
				e.trace.Logf("  card reply carries no digits → treating as cancellation")
				e.trace.Logf("  → emit: artifact + completed | refund cancelled at card step")
				if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart("Возврат отменён: реквизиты карты не получены.")), nil) {
					return
				}
				yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
				return
			}
			if !validCard(digits) {
				e.mu.Lock()
				e.pendingConfirm[sessionID] = p // stay at the card step
				e.mu.Unlock()
				e.trace.Logf("  card %s failed validation → re-asking", maskCard(digits))
				errText := "Номер карты некорректен. Проверьте его и отправьте ещё раз (13–19 цифр)."
				ask := a2a.NewMessage(a2a.MessageRoleAgent,
					a2a.NewTextPart(errText),
					refundFormWidget(p.orderID, errText, true),
				)
				yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, ask), nil)
				return
			}
			cardLast4 = digits[len(digits)-4:]
			e.trace.Logf("  card %s accepted → executing refund", maskCard(digits))
			fr := &genai.FunctionResponse{
				Name:     toolconfirmation.FunctionCallName,
				ID:       p.callID,
				Response: map[string]any{"confirmed": true},
			}
			msg = &genai.Content{Role: string(genai.RoleUser), Parts: []*genai.Part{{FunctionResponse: fr}}}

		default:
			e.trace.Logf("  user text=%q — running orders agent (LLM + tools)", userText)
			msg = genai.NewContentFromText(userText, genai.RoleUser)
		}

		// 4. Run the adk runner, watching for a HITL confirmation request and for
		//    any structured widget a read tool stashed in session state.
		var finalText, confirmQuestion, capturedCallID, capturedOrderID string
		var capturedWidget map[string]any
		toolCalls, limitHit := 0, false
		e.trace.Logf("%s  · агент → LLM: запрос%s", gray, reset)
		llmStart := time.Now()
		for event, err := range e.runner.Run(ctx, "a2a-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				e.trace.Logf("  ✖ runner error: %v", err)
				yield(nil, err)
				return
			}
			// A read tool may have stashed a structured widget in state; the
			// delta rides on the tool's function-response event.
			if event != nil {
				if raw, ok := event.Actions.StateDelta[orders.WidgetStateKey]; ok {
					if w, ok := raw.(map[string]any); ok {
						capturedWidget = w
						e.trace.Logf("%s  · инструмент → виджет %v%s", gray, w["kind"], reset)
					}
				}
			}
			if event == nil || event.Content == nil {
				continue
			}
			for _, p := range event.Content.Parts {
				if p.FunctionCall != nil {
					toolCalls++
					if toolCalls > maxToolCallsPerTurn {
						limitHit = true
					}
				}
				switch {
				case p.FunctionCall != nil && p.FunctionCall.Name == toolconfirmation.FunctionCallName:
					// adk asks for human approval before running a guarded tool.
					// A turn is expected to carry at most one confirmation request
					// (initiate_refund is the only guarded tool); if several ever
					// appeared, only the last is paused on — acceptable for this demo.
					capturedCallID = p.FunctionCall.ID
					orig, oerr := toolconfirmation.OriginalCallFrom(p.FunctionCall)
					if oerr != nil {
						e.trace.Logf("  ✖ OriginalCallFrom: %v", oerr)
						confirmQuestion = "Подтвердите выполнение действия? (да/нет)"
					} else {
						confirmQuestion = refundConfirmQuestion(orig)
						capturedOrderID = refundOrderID(orig)
					}
					e.trace.Logf("  ⏸ tool confirmation requested | callID=%s question=%q", capturedCallID, confirmQuestion)
				case p.FunctionCall != nil:
					e.trace.Logf("%s  · LLM → агент: вызвать %s(%s)%s",
						gray, p.FunctionCall.Name, compactArgs(p.FunctionCall.Args), reset)
				case p.FunctionResponse != nil:
					e.trace.Logf("%s  · инструмент %s → LLM: результат, снова спрашиваю LLM%s",
						gray, p.FunctionResponse.Name, reset)
				case p.Text != "":
					finalText = p.Text
				}
			}
			if limitHit {
				e.trace.Logf("✖ tool-call limit (%d) exceeded — force-stopping the agent loop | session=%s",
					maxToolCallsPerTurn, sessionID)
				break
			}
		}
		e.trace.Logf("  LLM+tools finished in %s (toolCalls=%d limitHit=%v)",
			time.Since(llmStart).Round(time.Millisecond), toolCalls, limitHit)

		// If the tool-call limit was hit the model was likely looping — emit a
		// plain completion instead of running the confirmation/clarification paths.
		if limitHit {
			e.trace.Logf("  → emit: artifact + completed | tool-call limit fallback")
			if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(
				"Не удалось обработать запрос за отведённое число шагов — возможно, модель зациклилась. Попробуйте переформулировать запрос.")), nil) {
				return
			}
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
			return
		}

		// 5. Confirmation requested → pause the task as input-required.
		if capturedCallID != "" {
			e.mu.Lock()
			e.pendingConfirm[sessionID] = pendingHITL{callID: capturedCallID, orderID: capturedOrderID}
			e.mu.Unlock()
			e.trace.Logf("  → emit: input-required (confirmation) | question=%q", confirmQuestion)
			// Text part is the fallback for UI-less clients; the DataPart carries
			// the confirmation-widget spec, built by code from the captured call.
			ask := a2a.NewMessage(a2a.MessageRoleAgent,
				a2a.NewTextPart(confirmQuestion),
				refundConfirmWidget(capturedOrderID, confirmQuestion),
			)
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, ask), nil)
			return
		}

		// 6. Check for the NEED_INPUT clarification sentinel.
		trimmed := strings.TrimSpace(finalText)
		e.trace.Logf("  agent produced finalText=%q", trimmed)
		if strings.HasPrefix(trimmed, needInputPrefix) {
			question := strings.TrimSpace(strings.TrimPrefix(trimmed, needInputPrefix))
			e.trace.Logf("  → emit: input-required | question=%q", question)
			ask := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(question))
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, ask), nil)
			return
		}

		// 7. Emit artifact + completed. Guard against an empty finalText.
		artifactText := trimmed
		if artifactText == "" {
			artifactText = "Готово."
		}
		// A refund executed with card details enriches the tool's receipt widget
		// with the payment context and attaches the receipt as a downloadable
		// file (a plain A2A raw part).
		var filePart *a2a.Part
		if cardLast4 != "" && capturedWidget != nil && capturedWidget["kind"] == "widget/refund_receipt" {
			filePart = enrichReceipt(capturedWidget, cardLast4)
			artifactText = fmt.Sprintf("Возврат оформлен. Средства поступят на карту •••• %s. Квитанция — во вложении.", cardLast4)
		}
		// Text part first (human/LLM fallback); append a widget DataPart when a
		// read tool produced structured data this turn.
		parts := []*a2a.Part{a2a.NewTextPart(artifactText)}
		if w := widgetPartFromState(capturedWidget); w != nil {
			e.trace.Logf("  → including %v DataPart in artifact", capturedWidget["kind"])
			parts = append(parts, w)
		}
		if filePart != nil {
			e.trace.Logf("  → including receipt file %q (%s, %d bytes)", filePart.Filename, filePart.MediaType, len(filePart.Raw()))
			parts = append(parts, filePart)
		}
		e.trace.Logf("  → emit: artifact + completed | artifact=%q", artifactText)
		if !yield(a2a.NewArtifactEvent(ec, parts...), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
}

// parseAffirmative interprets a free-text reply to a yes/no confirmation for a
// money-moving refund. It fails CLOSED via an allowlist: the refund is approved
// only when the reply is non-empty and EVERY word is a recognised affirmative or
// confirm word. Any unrecognised token — a cancel/deferral verb ("отмени",
// "потом"), a negation ("не", "нет"), or a hedge ("наверное") — makes the whole
// reply a refusal. New ways of saying "no" are rejected by default instead of
// slipping through, which is the safe posture for moving money.
func parseAffirmative(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(text)), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	if len(words) == 0 {
		return false
	}
	affirmative := func(w string) bool {
		switch w {
		case "да", "ага", "угу", "давай", "конечно", "ладно", "хорошо",
			"yes", "yeah", "yep", "y", "ок", "ok", "okay":
			return true
		}
		// Confirm/refund verbs: подтверждаю/подтверди/подтверждай, оформляй/оформи.
		return strings.HasPrefix(w, "подтвер") || strings.HasPrefix(w, "оформ")
	}
	for _, w := range words {
		if !affirmative(w) {
			return false
		}
	}
	return true
}

// widgetKindConfirmation is the metadata.kind contract that tells a UI consumer
// this DataPart is a confirmation dialog. The A2A protocol assigns DataPart no
// meaning of its own — this label is the producer↔consumer agreement.
const widgetKindConfirmation = "widget/confirmation"

// widgetPart wraps structured data as an A2A DataPart tagged with the given kind
// in metadata — the field a UI consumer selects a renderer by.
func widgetPart(kind string, data map[string]any) *a2a.Part {
	p := a2a.NewDataPart(data)
	p.Metadata = map[string]any{"kind": kind, "version": 1}
	return p
}

// widgetPartFromState turns a tool-stashed widget map (which carries its kind
// under "kind") into a DataPart, or nil if there is none. The kind moves to the
// part's metadata; the remaining entries become the widget payload.
func widgetPartFromState(w map[string]any) *a2a.Part {
	if w == nil {
		return nil
	}
	kind, _ := w["kind"].(string)
	if kind == "" {
		return nil
	}
	payload := make(map[string]any, len(w))
	for k, v := range w {
		if k != "kind" {
			payload[k] = v
		}
	}
	return widgetPart(kind, payload)
}

// refundConfirmWidget builds the confirmation-dialog DataPart. Its params are
// assembled by CODE from the captured initiate_refund call, never invented by
// the LLM — the safe posture for a money-moving action. The refund has NOT run
// yet at HITL time, so the amount is unknown here and intentionally omitted.
func refundConfirmWidget(orderID, message string) *a2a.Part {
	return widgetPart(widgetKindConfirmation, map[string]any{
		"type":     "confirmation",
		"title":    "Подтверждение возврата",
		"message":  message,
		"order_id": orderID,
		"actions": []map[string]any{
			{"id": "approve", "label": "Оформить возврат", "style": "danger"},
			{"id": "decline", "label": "Отмена", "style": "secondary"},
		},
	})
}

// widgetKindRefundForm marks the card-details form widget shown after the
// refund confirmation; widgetKindRefundReceipt marks the final receipt.
const (
	widgetKindRefundForm    = "widget/refund_form"
	widgetKindRefundReceipt = "widget/refund_receipt"
)

// refundCardQuestion is the text prompt of the card-details step.
func refundCardQuestion(orderID string) string {
	if orderID != "" {
		return fmt.Sprintf("Укажите номер карты для возврата средств по заказу %s (13–19 цифр).", orderID)
	}
	return "Укажите номер карты для возврата средств (13–19 цифр)."
}

// refundFormWidget builds the card-details form DataPart shown after the user
// confirmed the refund. isError marks a re-ask after failed validation.
func refundFormWidget(orderID, message string, isError bool) *a2a.Part {
	severity := "info"
	if isError {
		severity = "error"
	}
	return widgetPart(widgetKindRefundForm, map[string]any{
		"type":     "form",
		"title":    "Реквизиты для возврата",
		"message":  message,
		"severity": severity,
		"order_id": orderID,
		"fields": []map[string]any{
			{"id": "card_number", "label": "Номер карты", "placeholder": "0000 0000 0000 0000"},
		},
		"actions": []map[string]any{
			{"id": "submit_refund_details", "label": "Вернуть на карту", "style": "primary"},
			{"id": "decline", "label": "Отмена", "style": "secondary"},
		},
	})
}

// enrichReceipt stamps the payment context (masked card, receipt id, creation
// time, filename) onto the tool-built receipt widget and returns the matching
// downloadable receipt as a self-contained HTML file (an A2A raw part).
func enrichReceipt(w map[string]any, cardLast4 string) *a2a.Part {
	now := time.Now()
	receiptID := fmt.Sprintf("RF-%v-%s", w["order_id"], now.Format("20060102-150405"))
	filename := fmt.Sprintf("receipt-%v.html", w["order_id"])
	w["receipt_id"] = receiptID
	w["card_last4"] = cardLast4
	w["created"] = now.Format("2006-01-02 15:04:05")
	w["filename"] = filename

	part := a2a.NewRawPart(receiptHTML(w, receiptID, cardLast4))
	part.Filename = filename
	part.MediaType = "text/html"
	return part
}

// receiptHTML renders the refund receipt as a standalone printable HTML page.
// All values are HTML-escaped: widget data comes from the store, but a receipt
// must stay safe even if the domain data ever carries markup.
func receiptHTML(w map[string]any, receiptID, cardLast4 string) []byte {
	esc := func(v any) string { return html.EscapeString(fmt.Sprintf("%v", v)) }
	row := func(label, value string) string {
		return fmt.Sprintf("<tr><td>%s</td><td>%s</td></tr>\n", label, value)
	}
	var rows strings.Builder
	rows.WriteString(row("Квитанция №", esc(receiptID)))
	rows.WriteString(row("Дата", esc(w["created"])))
	rows.WriteString(row("Заказ", "#"+esc(w["order_id"])+" — "+esc(w["item"])))
	rows.WriteString(row("Сумма возврата", esc(w["amount"])+" "+esc(w["currency"])))
	rows.WriteString(row("Карта получателя", "•••• "+esc(cardLast4)))
	rows.WriteString(row("Статус", "возврат оформлен"))

	page := fmt.Sprintf(`<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>Квитанция о возврате %[1]s</title>
<style>
  body { font-family: system-ui, sans-serif; background: #f0f1f3; margin: 0; padding: 32px 16px; color: #222; }
  .receipt { max-width: 480px; margin: 0 auto; background: #fff; border: 1px solid #d0d7de;
             border-radius: 14px; padding: 28px 32px; box-shadow: 0 1px 4px rgba(0,0,0,.07); }
  h1 { font-size: 20px; margin: 0 0 4px; }
  .sub { color: #57606a; font-size: 13px; margin: 0 0 20px; }
  table { width: 100%%; border-collapse: collapse; font-size: 15px; }
  td { padding: 8px 0; border-bottom: 1px solid #eef1f4; vertical-align: top; }
  td:first-child { color: #57606a; width: 45%%; padding-right: 12px; }
  td:last-child { font-weight: 600; }
  .note { color: #8b949e; font-size: 12px; margin-top: 20px; }
  @media print { body { background: #fff; padding: 0; } .receipt { border: none; box-shadow: none; } }
</style>
</head>
<body>
<div class="receipt">
<h1>Квитанция о возврате</h1>
<p class="sub">%[1]s</p>
<table>
%[2]s</table>
<p class="note">Документ сформирован автоматически агентом orders-agent (A2A demo).</p>
</div>
</body>
</html>
`, esc(receiptID), rows.String())
	return []byte(page)
}

// refundOrderID extracts the order id from the captured initiate_refund call,
// tolerating the synonym keys small models emit.
func refundOrderID(orig *genai.FunctionCall) string {
	if orig != nil && orig.Args != nil {
		for _, k := range []string{"order_id", "order_number", "number", "id"} {
			if s, ok := orig.Args[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// refundConfirmQuestion builds the Russian confirmation prompt from the original
// initiate_refund call captured inside the adk_request_confirmation event.
func refundConfirmQuestion(orig *genai.FunctionCall) string {
	if id := refundOrderID(orig); id != "" {
		return fmt.Sprintf("Подтвердите оформление возврата по заказу %s? (да/нет)", id)
	}
	return "Подтвердите оформление возврата? (да/нет)"
}

// Cancel implements a2asrv.AgentExecutor.
func (e *executor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

// AgentCard returns a minimal A2A AgentCard for the orders agent.
// publicURL is the base URL where the JSON-RPC handler is mounted (e.g. "http://localhost:8080").
// The card advertises a single JSONRPC interface at publicURL/invoke,
// which matches the mux path registered in cmd/worker/main.go.
func AgentCard(publicURL string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "orders-agent",
		Description:        "Управляет заказами, статусами, статистикой и возвратами.",
		Version:            "0.1.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(publicURL+"/invoke", a2a.TransportProtocolJSONRPC),
		},
		Skills: []a2a.AgentSkill{{
			ID:          "manage_orders",
			Name:        "Управление заказами",
			Description: "Поиск заказов по номеру или товару, статусы, статистика продаж за период и оформление возвратов.",
			Tags:        []string{"заказы", "статусы", "статистика продаж", "возвраты", "поиск"},
			Examples: []string{
				"верни деньги за заказ 1041",
				"статус заказа 1041",
				"последние заказы alice",
				"статистика продаж за 2026-06",
			},
		}},
	}
}
