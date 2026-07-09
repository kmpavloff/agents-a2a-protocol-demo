package a2abridge

import (
	"context"
	"iter"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

type orchExecutor struct {
	runner *runner.Runner
	trace  *Tracer

	mu      sync.Mutex
	widgets map[string][]map[string]any // sessionID → widgets produced this turn
}

// NewOrchestratorExecutor wraps the orchestrator runner as an A2A server that
// speaks the A2UI extension: it maps worker widgets to A2UI JSON and translates
// incoming A2UI button actions into task resumes. trace may be nil.
func NewOrchestratorExecutor(r *runner.Runner, oc *OrdersClient, trace *Tracer) a2asrv.AgentExecutor {
	e := &orchExecutor{runner: r, trace: trace, widgets: make(map[string][]map[string]any)}
	// One global handler routes each widget to its session's slot.
	oc.SetWidgetHandler(func(sessionID string, w map[string]any) {
		e.mu.Lock()
		e.widgets[sessionID] = append(e.widgets[sessionID], w)
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
	default:
		return "Пользователь нажал действие: " + name
	}
}

func (e *orchExecutor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		sessionID := ec.ContextID

		// Activate the A2UI extension if the client requested it.
		a2uiActive := false
		ext := &a2a.AgentExtension{URI: a2ui.ExtensionURI}
		if exts, ok := a2asrv.ExtensionsFrom(ctx); ok && exts.Requested(ext) {
			exts.Activate(ext)
			a2uiActive = true
		}
		e.trace.Logf("▶ orchestrator A2A request | contextID=%s a2ui=%v", sessionID, a2uiActive)

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
		if ec.Message != nil {
			for _, p := range ec.Message.Parts {
				if data, ok := p.Data().(map[string]any); ok {
					if name, actx, ok := a2ui.ParseAction(data); ok {
						userText = actionToText(name, actx)
						e.trace.Logf("  A2UI action %q → text %q", name, userText)
						break
					}
				}
				if t := p.Text(); t != "" {
					userText = t
				}
			}
		}

		// Reset this session's widget slot before the run.
		e.mu.Lock()
		delete(e.widgets, sessionID)
		e.mu.Unlock()

		// Run the orchestrator LLM. The ask_orders_agent tool delegates to the
		// worker and forwards any widget through the session handler above.
		msg := genai.NewContentFromText(userText, genai.RoleUser)
		var finalText string
		for event, err := range e.runner.Run(ctx, "a2ui-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				yield(nil, err)
				return
			}
			if event == nil || event.Content == nil {
				continue
			}
			for _, p := range event.Content.Parts {
				if p.Text != "" {
					finalText = p.Text
				}
			}
		}

		// Drain this session's widget slot unconditionally so text-only
		// sessions don't leak a map entry; only emit A2UI parts when active.
		e.mu.Lock()
		ws := e.widgets[sessionID]
		delete(e.widgets, sessionID)
		e.mu.Unlock()

		// Assemble the artifact: text first (fallback), then A2UI parts.
		parts := []*a2a.Part{a2a.NewTextPart(strings.TrimSpace(orDefault(finalText, "Готово.")))}
		if a2uiActive {
			for _, w := range ws {
				if msgs, ok := a2ui.FromWidget(w); ok {
					for _, m := range msgs {
						part := a2a.NewDataPart(m)
						part.MediaType = a2ui.MIMEType
						parts = append(parts, part)
					}
				}
			}
		}
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
