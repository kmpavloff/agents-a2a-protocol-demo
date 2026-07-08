package a2abridge

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// pending holds the A2A task state needed to resume an input-required task.
type pending struct {
	taskID    a2a.TaskID
	contextID string
}

// OrdersClient is an A2A client that delegates to the orders worker agent.
// It tracks pending input-required tasks per orchestrator session so that
// a follow-up user message resumes the exact same worker task.
type OrdersClient struct {
	client  *a2aclient.Client
	trace   *Tracer
	profile WorkerProfile
	mu      sync.Mutex
	pending map[string]pending // keyed by orchestrator session id
	// onWidget, if set, receives structured widgets the worker returns in
	// DataParts so they reach the UI directly instead of being flattened into
	// the orchestrator LLM's text answer.
	onWidget func(map[string]any)
}

// SetWidgetHandler registers a callback for widgets (DataParts) the worker
// emits. The handler runs on the goroutine that invokes the delegating tool.
func (c *OrdersClient) SetWidgetHandler(fn func(map[string]any)) { c.onWidget = fn }

// NewOrdersClient resolves the worker AgentCard at workerURL and creates an
// A2A client from it. trace may be nil to disable protocol tracing.
func NewOrdersClient(ctx context.Context, workerURL string, trace *Tracer) (*OrdersClient, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, workerURL)
	if err != nil {
		return nil, fmt.Errorf("resolve worker card: %w", err)
	}
	trace.Logf("resolved worker AgentCard %q at %s", card.Name, workerURL)
	cl, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		return nil, fmt.Errorf("a2a client: %w", err)
	}
	profile := ProfileFromCard(card)
	trace.Logf("derived delegating tool %q from card", profile.ToolName)
	return &OrdersClient{
		client:  cl,
		trace:   trace,
		profile: profile,
		pending: make(map[string]pending),
	}, nil
}

// ask is the testable core behind the adk tool wrapper. It sends text to the
// orders worker agent, resuming a pending input-required task when one exists
// for the given sessionID, and returns the agent's response text.
func (c *OrdersClient) ask(ctx context.Context, sessionID, text string) (string, error) {
	// Guard against empty/whitespace messages: some models emit a probing tool
	// call with no message. Instead of wasting an A2A round-trip, tell the model
	// to supply a concrete request. Returned as a normal tool result (not an
	// error) so the model reads it and retries with real content.
	if strings.TrimSpace(text) == "" {
		c.trace.Logf("✖ empty message — skipping A2A call, asking model to provide a concrete request | session=%s", sessionID)
		return "Пустой запрос: сформулируйте конкретный вопрос или действие по заказам в поле message и вызовите инструмент снова.", nil
	}

	c.mu.Lock()
	p, hasPending := c.pending[sessionID]
	c.mu.Unlock()

	c.trace.Logf("──▶ orchestrator delegating to orders worker | session=%s", sessionID)

	var msg *a2a.Message
	if hasPending {
		// Resume an existing input-required task by referencing its IDs.
		c.trace.Logf("    resuming input-required task | taskID=%s contextID=%s", p.taskID, p.contextID)
		info := a2a.TaskInfo{TaskID: p.taskID, ContextID: p.contextID}
		msg = a2a.NewMessageForTask(a2a.MessageRoleUser, info, a2a.NewTextPart(text))
	} else {
		c.trace.Logf("    starting new task (no pending task for session)")
		msg = a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	}
	c.trace.Logf("    SendMessage role=user text=%q", text)

	res, err := c.client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		c.trace.Logf("    ✖ SendMessage failed: %v", err)
		return "", fmt.Errorf("orders agent unreachable: %w", err)
	}

	switch r := res.(type) {
	case *a2a.Message:
		// Synchronous response — no task created.
		c.trace.Logf("◀── response: Message (synchronous, no task) | parts=%d", len(r.Parts))
		c.clearPending(sessionID)
		if len(r.Parts) > 0 {
			result := r.Parts[0].Text()
			c.trace.Logf("    ✔ result=%q", result)
			return result, nil
		}
		c.trace.Logf("    ✔ empty message, returning fallback")
		return "Готово.", nil

	case *a2a.Task:
		c.trace.Logf("◀── response: Task | id=%s contextID=%s state=%s", r.ID, r.ContextID, r.Status.State)
		if r.Status.State == a2a.TaskStateInputRequired {
			// Store the task so the next call resumes it.
			c.mu.Lock()
			c.pending[sessionID] = pending{taskID: r.ID, contextID: r.ContextID}
			c.mu.Unlock()
			c.forwardWidget(statusParts(r)) // e.g. confirmation widget
			question := statusMessageText(r)
			c.trace.Logf("    ⏸ input-required — stored pending task, asking user: %q", question)
			return "NEEDS_USER_INPUT: " + question, nil
		}
		// Completed (or failed/canceled) — clear any pending state.
		c.clearPending(sessionID)
		c.forwardWidget(artifactParts(r)) // e.g. order / order-list widget
		result := taskResultText(r)
		c.trace.Logf("    ✔ terminal state, cleared pending | result=%q", result)
		return result, nil

	default:
		c.trace.Logf("    ✖ unexpected A2A result type %T", res)
		return "", fmt.Errorf("unexpected A2A result type %T", res)
	}
}

// clearPending removes any pending task for sessionID.
func (c *OrdersClient) clearPending(sessionID string) {
	c.mu.Lock()
	delete(c.pending, sessionID)
	c.mu.Unlock()
}

// Profile returns the worker profile derived from the resolved AgentCard.
func (c *OrdersClient) Profile() WorkerProfile { return c.profile }

// pendingTaskID returns the A2A task id that is pending for the given session,
// or the zero value if there is none. Intended for tests.
func (c *OrdersClient) pendingTaskID(sessionID string) a2a.TaskID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending[sessionID].taskID
}

// askArgs is the input schema for the ask_orders_agent adk function tool.
type askArgs struct {
	Message string `json:"message" description:"Что спросить или сообщить агенту по заказам"`
}

// Tool returns an adk function tool, named and described per the derived
// WorkerProfile, that delegates to the worker agent via A2A. The orchestrator
// session id is obtained from tool.Context.SessionID() (available via the
// embedded ReadonlyContext).
func (c *OrdersClient) Tool() tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name:        c.profile.ToolName,
		Description: c.profile.ToolDesc,
	}, func(tc tool.Context, a askArgs) (string, error) {
		// tool.Context embeds context.Context via agent.ReadonlyContext, so tc
		// itself satisfies context.Context. SessionID() is also on ReadonlyContext.
		return c.ask(tc, tc.SessionID(), a.Message)
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create %s tool: %v", c.profile.ToolName, err))
	}
	return t
}

// forwardWidget sends the first widget among parts (if any) to the registered
// handler, so it renders in the UI without passing through the LLM.
func (c *OrdersClient) forwardWidget(parts []*a2a.Part) {
	if c.onWidget == nil {
		return
	}
	if w := firstWidget(parts); w != nil {
		c.trace.Logf("    ⟐ widget DataPart (%v) → UI, bypassing LLM", w["_kind"])
		c.onWidget(w)
	}
}

// statusParts returns the parts of an input-required task's status message.
func statusParts(t *a2a.Task) []*a2a.Part {
	if t.Status.Message == nil {
		return nil
	}
	return t.Status.Message.Parts
}

// artifactParts returns the parts of a task's last artifact.
func artifactParts(t *a2a.Task) []*a2a.Part {
	if len(t.Artifacts) == 0 {
		return nil
	}
	return t.Artifacts[len(t.Artifacts)-1].Parts
}

// firstWidget returns the payload of the first DataPart whose metadata.kind
// marks it as a widget ("widget/..."), with the kind injected under "_kind" so
// a renderer can dispatch on it. Returns nil when there is no widget part.
func firstWidget(parts []*a2a.Part) map[string]any {
	for _, p := range parts {
		if p == nil {
			continue
		}
		kind, _ := p.Metadata["kind"].(string)
		if !strings.HasPrefix(kind, "widget/") {
			continue
		}
		data, ok := p.Data().(map[string]any)
		if !ok {
			continue
		}
		out := map[string]any{"_kind": kind}
		for k, v := range data {
			out[k] = v
		}
		return out
	}
	return nil
}

// statusMessageText extracts the question text from an input-required task's
// status message.
func statusMessageText(t *a2a.Task) string {
	if t.Status.Message != nil && len(t.Status.Message.Parts) > 0 {
		return t.Status.Message.Parts[0].Text()
	}
	return "Агенту по заказам нужны дополнительные данные."
}

// taskResultText returns the last artifact text of a completed task, falling
// back to the last history message text, then "Done." if neither is present.
// Empty-text parts are skipped so that a blank artifact falls through to the
// "Done." fallback rather than returning an empty string.
func taskResultText(t *a2a.Task) string {
	if len(t.Artifacts) > 0 {
		last := t.Artifacts[len(t.Artifacts)-1]
		for _, p := range last.Parts {
			if txt := p.Text(); txt != "" {
				return txt
			}
		}
	}
	if len(t.History) > 0 {
		last := t.History[len(t.History)-1]
		for _, p := range last.Parts {
			if txt := p.Text(); txt != "" {
				return txt
			}
		}
	}
	return "Готово."
}
