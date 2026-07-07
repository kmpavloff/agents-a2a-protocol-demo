// Package a2abridge connects adk agents to the a2a-go server and client.
package a2abridge

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"sync"
	"unicode"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"
)

// needInputPrefix is the sentinel the worker agent emits when it needs
// clarification from the caller.
const needInputPrefix = "NEED_INPUT:"

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

type executor struct {
	runner *runner.Runner
	trace  *Tracer

	mu             sync.Mutex
	pendingConfirm map[string]string // A2A contextID → adk_request_confirmation call ID
}

// NewExecutor wraps an adk Runner as an a2asrv.AgentExecutor.
// trace may be nil to disable protocol tracing.
func NewExecutor(r *runner.Runner, trace *Tracer) a2asrv.AgentExecutor {
	return &executor{runner: r, trace: trace, pendingConfirm: make(map[string]string)}
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
		confirmCallID, awaitingConfirm := e.pendingConfirm[sessionID]
		if awaitingConfirm {
			delete(e.pendingConfirm, sessionID)
		}
		e.mu.Unlock()

		var msg *genai.Content
		if awaitingConfirm {
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
			fr := &genai.FunctionResponse{
				Name:     toolconfirmation.FunctionCallName,
				ID:       confirmCallID,
				Response: map[string]any{"confirmed": true},
			}
			msg = &genai.Content{Role: string(genai.RoleUser), Parts: []*genai.Part{{FunctionResponse: fr}}}
		} else {
			e.trace.Logf("  user text=%q — running orders agent (LLM + tools)", userText)
			msg = genai.NewContentFromText(userText, genai.RoleUser)
		}

		// 4. Run the adk runner, watching for a HITL confirmation request.
		var finalText, confirmQuestion, capturedCallID string
		e.trace.Logf("%s  · агент → LLM: запрос%s", gray, reset)
		for event, err := range e.runner.Run(ctx, "a2a-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				e.trace.Logf("  ✖ runner error: %v", err)
				yield(nil, err)
				return
			}
			if event == nil || event.Content == nil {
				continue
			}
			for _, p := range event.Content.Parts {
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
		}

		// 5. Confirmation requested → pause the task as input-required.
		if capturedCallID != "" {
			e.mu.Lock()
			e.pendingConfirm[sessionID] = capturedCallID
			e.mu.Unlock()
			e.trace.Logf("  → emit: input-required (confirmation) | question=%q", confirmQuestion)
			ask := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(confirmQuestion))
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
		e.trace.Logf("  → emit: artifact + completed | artifact=%q", artifactText)
		if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(artifactText)), nil) {
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

// refundConfirmQuestion builds the Russian confirmation prompt from the original
// initiate_refund call captured inside the adk_request_confirmation event.
func refundConfirmQuestion(orig *genai.FunctionCall) string {
	id := ""
	if orig != nil && orig.Args != nil {
		for _, k := range []string{"order_id", "order_number", "number", "id"} {
			if v, ok := orig.Args[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					id = strings.TrimSpace(s)
					break
				}
			}
		}
	}
	if id != "" {
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
