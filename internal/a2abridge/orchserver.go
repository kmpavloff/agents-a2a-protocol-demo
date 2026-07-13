package a2abridge

import (
	"context"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

// attachedFile is a downloadable file the worker attached to an artifact,
// passed through to the browser as an A2A raw part.
type attachedFile struct {
	name      string
	mediaType string
	data      []byte
}

type orchExecutor struct {
	runner *runner.Runner
	oc     *OrdersClient
	trace  *Tracer

	mu      sync.Mutex
	widgets map[string][]map[string]any // sessionID → widgets produced this turn
	files   map[string][]attachedFile   // sessionID → files produced this turn
}

// NewOrchestratorExecutor wraps the orchestrator runner as an A2A server that
// speaks the A2UI extension: it maps worker widgets to A2UI JSON and translates
// incoming A2UI button actions into task resumes. trace may be nil.
func NewOrchestratorExecutor(r *runner.Runner, oc *OrdersClient, trace *Tracer) a2asrv.AgentExecutor {
	e := &orchExecutor{
		runner:  r,
		oc:      oc,
		trace:   trace,
		widgets: make(map[string][]map[string]any),
		files:   make(map[string][]attachedFile),
	}
	// One global handler routes each widget/file to its session's slot.
	oc.SetWidgetHandler(func(sessionID string, w map[string]any) {
		e.mu.Lock()
		e.widgets[sessionID] = append(e.widgets[sessionID], w)
		e.mu.Unlock()
	})
	oc.SetFileHandler(func(sessionID, filename, mediaType string, data []byte) {
		e.mu.Lock()
		e.files[sessionID] = append(e.files[sessionID], attachedFile{name: filename, mediaType: mediaType, data: data})
		e.mu.Unlock()
	})
	return e
}

// actionToText maps an A2UI button action name to the user text the orchestrator
// LLM should process. Unknown actions fall back to a descriptive line.
func actionToText(name string, ctx map[string]any) string {
	switch name {
	case "approve_refund":
		return "да"
	case "decline_refund":
		return "нет"
	case "submit_refund_details":
		// The card number from the form's TextField ({path} binding resolved by
		// the renderer at click time). Resumed directly — never via the LLM.
		if s, _ := ctx["card_number"].(string); s != "" {
			return s
		}
		return ""
	default:
		return "Пользователь нажал действие: " + name
	}
}

// safeActionEcho renders an action's user text for traces, masking payment
// details so a full card number never lands in the protocol log.
func safeActionEcho(name, userText string) string {
	if name == "submit_refund_details" {
		return maskCard(cardDigits(userText))
	}
	return userText
}

func (e *orchExecutor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		reqStart := time.Now()
		sessionID := ec.ContextID
		nParts := 0
		if ec.Message != nil {
			nParts = len(ec.Message.Parts)
		}

		// Activate the A2UI extension if the client requested it.
		a2uiActive := false
		ext := &a2a.AgentExtension{URI: a2ui.ExtensionURI}
		if exts, ok := a2asrv.ExtensionsFrom(ctx); ok && exts.Requested(ext) {
			exts.Activate(ext)
			a2uiActive = true
		}
		e.trace.Logf("▶ orchestrator A2A request | contextID=%s a2ui=%v inParts=%d stored=%v",
			sessionID, a2uiActive, nParts, ec.StoredTask != nil)

		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// Parse input: an A2UI action DataPart, or plain text.
		userText := ""
		actionName := ""
		if ec.Message != nil {
			for _, p := range ec.Message.Parts {
				if data, ok := p.Data().(map[string]any); ok {
					if name, actx, ok := a2ui.ParseAction(data); ok {
						actionName = name
						userText = actionToText(name, actx)
						if name == "submit_refund_details" {
							delete(actx, "card_number") // keep the number out of the trace
						}
						e.trace.Logf("  A2UI action %q ctx=%s → user text %q",
							name, compactArgs(actx), safeActionEcho(name, userText))
						break
					}
				}
				if t := p.Text(); t != "" {
					userText = t
				}
			}
		}

		// Reset this session's widget/file slots before the run.
		e.mu.Lock()
		delete(e.widgets, sessionID)
		delete(e.files, sessionID)
		e.mu.Unlock()

		// A button on a pending HITL step (yes/no confirmation, or the card
		// form) resumes the worker task DIRECTLY with the canonical answer,
		// bypassing the orchestrator LLM. The LLM tends to paraphrase "да" into
		// a full sentence, which the worker's fail-closed confirmation parser
		// rejects; and card details should never pass through an LLM at all.
		directResume := actionName == "approve_refund" || actionName == "decline_refund" ||
			actionName == "submit_refund_details"
		if directResume && e.oc.pendingTaskID(sessionID) != "" {
			e.trace.Logf("  confirmation button %q → resuming worker directly with %q (LLM bypassed)",
				actionName, safeActionEcho(actionName, userText))
			result, err := e.oc.ask(ctx, sessionID, userText)
			if err != nil {
				e.trace.Logf("  ✖ direct resume error: %v", err)
				yield(nil, err)
				return
			}
			// The resume may complete the task (receipt widget + file) or pause
			// it again (the card form after "да") — both carry widgets, and a
			// completion may carry files; emit them like a normal turn.
			e.mu.Lock()
			ws := e.widgets[sessionID]
			fs := e.files[sessionID]
			delete(e.widgets, sessionID)
			delete(e.files, sessionID)
			e.mu.Unlock()
			parts := []*a2a.Part{a2a.NewTextPart(strings.TrimSpace(orDefault(stripNeedsInput(result), "Готово.")))}
			parts = append(parts, e.a2uiParts(a2uiActive, ws)...)
			parts = append(parts, fileParts(fs)...)
			e.trace.Logf("  → emit: artifact + completed | direct HITL resume | parts=%d", len(parts))
			if !yield(a2a.NewArtifactEvent(ec, parts...), nil) {
				return
			}
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
			return
		}

		// Run the orchestrator LLM. The ask_orders_agent tool delegates to the
		// worker and forwards any widget through the session handler above.
		e.trace.Logf("%s  · оркестратор → LLM: %q%s", gray, userText, reset)
		llmStart := time.Now()
		msg := genai.NewContentFromText(userText, genai.RoleUser)
		var finalText string
		toolCalls := 0
		limitHit := false
		for event, err := range e.runner.Run(ctx, "a2ui-user", sessionID, msg, agent.RunConfig{}) {
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
				case p.FunctionCall != nil:
					toolCalls++
					e.trace.Logf("%s  · LLM → инструмент: %s(%s) [#%d]%s",
						gray, p.FunctionCall.Name, compactArgs(p.FunctionCall.Args), toolCalls, reset)
					if toolCalls > maxToolCallsPerTurn {
						limitHit = true
					}
				case p.FunctionResponse != nil:
					e.trace.Logf("%s  · инструмент %s → LLM: результат%s", gray, p.FunctionResponse.Name, reset)
				case p.Text != "":
					finalText = p.Text
				}
			}
			// Force-stop before the over-limit call executes: breaking the range
			// makes the runner's next yield return false, so adk halts the loop.
			if limitHit {
				e.trace.Logf("✖ tool-call limit (%d) exceeded — force-stopping the agent loop | session=%s",
					maxToolCallsPerTurn, sessionID)
				break
			}
		}
		if limitHit && strings.TrimSpace(finalText) == "" {
			finalText = "Не удалось обработать запрос за отведённое число шагов — возможно, модель зациклилась. Попробуйте переформулировать запрос."
		}
		e.trace.Logf("  LLM finished in %s | toolCalls=%d limitHit=%v finalText=%q",
			time.Since(llmStart).Round(time.Millisecond), toolCalls, limitHit, strings.TrimSpace(finalText))

		// Drain this session's widget/file slots unconditionally so text-only
		// sessions don't leak map entries; only emit A2UI parts when active.
		e.mu.Lock()
		ws := e.widgets[sessionID]
		fs := e.files[sessionID]
		delete(e.widgets, sessionID)
		delete(e.files, sessionID)
		e.mu.Unlock()

		// Assemble the artifact: text first (fallback), then A2UI parts, then files.
		parts := []*a2a.Part{a2a.NewTextPart(strings.TrimSpace(orDefault(finalText, "Готово.")))}
		parts = append(parts, e.a2uiParts(a2uiActive, ws)...)
		parts = append(parts, fileParts(fs)...)
		e.trace.Logf("  → emit: artifact + completed | textPart=1 extraParts=%d requestTook=%s",
			len(parts)-1, time.Since(reqStart).Round(time.Millisecond))
		if !yield(a2a.NewArtifactEvent(ec, parts...), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
}

func (e *orchExecutor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// stripNeedsInput removes the delegating tool's NEEDS_USER_INPUT sentinel from
// a directly-resumed result, leaving the bare question for the browser (the
// accompanying widget carries the interactive form).
func stripNeedsInput(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "NEEDS_USER_INPUT:"))
}

// a2uiParts maps collected widgets to A2UI DataParts when the extension is
// active; otherwise it logs the drop and returns nothing.
func (e *orchExecutor) a2uiParts(a2uiActive bool, ws []map[string]any) []*a2a.Part {
	if !a2uiActive {
		if len(ws) > 0 {
			e.trace.Logf("  A2UI inactive — %d widget(s) dropped, text-only response", len(ws))
		}
		return nil
	}
	var parts []*a2a.Part
	for _, w := range ws {
		if msgs, ok := a2ui.FromWidget(w); ok {
			e.trace.Logf("  A2UI: widget %v → %d message(s) (application/a2ui+json)", w["_kind"], len(msgs))
			for _, m := range msgs {
				part := a2a.NewDataPart(m)
				part.MediaType = a2ui.MIMEType
				parts = append(parts, part)
			}
		}
	}
	return parts
}

// fileParts passes worker-attached files through as A2A raw parts (attached
// regardless of A2UI: files are plain protocol parts, not generative UI).
func fileParts(fs []attachedFile) []*a2a.Part {
	var parts []*a2a.Part
	for _, f := range fs {
		p := a2a.NewRawPart(f.data)
		p.Filename = f.name
		p.MediaType = f.mediaType
		parts = append(parts, p)
	}
	return parts
}

// withExt returns a context carrying a service-param request to activate the
// A2UI A2A extension, the mechanism a2a-go v2.3.1 clients use to convey
// requested extensions (there is no field on SendMessageRequest for this).
func withExt(ctx context.Context) context.Context {
	return a2aclient.AttachServiceParams(ctx, a2aclient.ServiceParams{
		a2a.SvcParamExtensions: []string{a2ui.ExtensionURI},
	})
}

// A2UIProbe is a minimal A2A client that activates the A2UI extension and
// returns the parts of the resulting task artifact. Used by the browser bridge
// tests and available for local diagnostics. It remembers the contextID from
// the last response so a follow-up turn (e.g. an A2UI action) lands on the
// same orchestrator session.
//
// It deliberately does NOT thread the previous taskID through: the
// orchestrator executor always drives its own task to TaskStateCompleted at
// the end of a turn (unlike the worker, which can leave a task
// input-required), so a2asrv rejects a follow-up message that references that
// task ("task in a terminal state"). Reusing only the ContextID lets a2asrv
// start a fresh task within the same context, which is what keeps
// ec.ContextID — and therefore the runner session and OrdersClient's pending
// map — stable across turns.
type A2UIProbe struct {
	client    *a2aclient.Client
	contextID string
}

func NewA2UIProbe(ctx context.Context, url string) (*A2UIProbe, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, url)
	if err != nil {
		return nil, err
	}
	cl, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return &A2UIProbe{client: cl}, nil
}

// send delivers part to the agent, carrying over the previously seen
// contextID (if any) so the orchestrator resumes the same session, and
// returns the parts of the resulting task's last artifact.
func (p *A2UIProbe) send(ctx context.Context, part *a2a.Part) ([]*a2a.Part, error) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, part)
	if p.contextID != "" {
		msg.ContextID = p.contextID
	}
	res, err := p.client.SendMessage(withExt(ctx), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return nil, err
	}
	task, ok := res.(*a2a.Task)
	if !ok {
		return nil, nil
	}
	if task.ContextID != "" {
		p.contextID = task.ContextID
	}
	if len(task.Artifacts) > 0 {
		return task.Artifacts[len(task.Artifacts)-1].Parts, nil
	}
	return nil, nil
}

// SendText sends a plain user-text message to the agent.
func (p *A2UIProbe) SendText(ctx context.Context, text string) ([]*a2a.Part, error) {
	return p.send(ctx, a2a.NewTextPart(text))
}

// SendAction sends an A2UI button action back to the agent over A2A.
func (p *A2UIProbe) SendAction(ctx context.Context, name string, actx map[string]any) ([]*a2a.Part, error) {
	part := a2a.NewDataPart(map[string]any{
		"version": a2ui.Version,
		"action":  map[string]any{"name": name, "context": actx},
	})
	part.MediaType = a2ui.MIMEType
	return p.send(ctx, part)
}
