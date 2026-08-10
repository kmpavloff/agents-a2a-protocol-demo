package a2abridge

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// pending holds the A2A task state needed to resume an input-required task.
type pending struct {
	taskID    a2a.TaskID
	contextID string
}

// OrdersClient wraps one [Remote] as an adk delegating tool. The transport,
// session bookkeeping and A2UI ingestion live in Remote; what stays here is the
// tool-facing behaviour: guarding against empty tool calls and routing widgets,
// files and A2UI messages to the UI without passing them through the LLM.
type OrdersClient struct {
	remote *Remote
	trace  *Tracer

	mu         sync.Mutex
	emptyCalls map[string]int // consecutive empty-message tool calls per session
	// onWidget, if set, receives structured widgets the worker returns in
	// DataParts, tagged with the orchestrator session id (= A2A contextID) so a
	// multi-session server can route each widget to the right request.
	onWidget func(sessionID string, w map[string]any)
	// onFile, if set, receives downloadable files (raw parts with a filename)
	// the worker attaches to artifacts — e.g. the refund receipt.
	onFile func(sessionID, filename, mediaType string, data []byte)
	// onA2UI, if set, receives A2UI messages a remote agent produced itself
	// (as opposed to widgets our gateway maps). Same routing by session id.
	onA2UI func(sessionID string, msgs []map[string]any)
}

// SetWidgetHandler registers a callback for widgets (DataParts) the worker
// emits. The handler runs on the goroutine that invokes the delegating tool.
func (c *OrdersClient) SetWidgetHandler(fn func(sessionID string, w map[string]any)) { c.onWidget = fn }

// SetFileHandler registers a callback for downloadable files (raw parts) the
// worker attaches to artifacts. Runs on the delegating tool's goroutine.
func (c *OrdersClient) SetFileHandler(fn func(sessionID, filename, mediaType string, data []byte)) {
	c.onFile = fn
}

// SetA2UIHandler registers a callback for A2UI messages the remote agent
// produced on its own. Runs on the delegating tool's goroutine.
func (c *OrdersClient) SetA2UIHandler(fn func(sessionID string, msgs []map[string]any)) {
	c.onA2UI = fn
}

// NewOrdersClientFromRemote wraps an already-configured remote agent.
func NewOrdersClientFromRemote(r *Remote) *OrdersClient {
	return &OrdersClient{remote: r, trace: r.trace, emptyCalls: make(map[string]int)}
}

// NewOrdersClient connects to the orders worker at workerURL and wraps it as a
// delegating tool. Unlike a remote agent added through the registry, the worker
// must be up front: the terminal REPL has nothing to offer without it.
// trace may be nil to disable protocol tracing.
func NewOrdersClient(ctx context.Context, workerURL string, trace *Tracer) (*OrdersClient, error) {
	r := NewRemote(config.AgentConfig{ID: "orders", Name: "Агент заказов", URL: workerURL,
		CardPath: "/.well-known/agent-card.json"}, trace)
	if err := r.Connect(ctx); err != nil {
		return nil, err
	}
	return NewOrdersClientFromRemote(r), nil
}

// Remote returns the underlying remote agent connection.
func (c *OrdersClient) Remote() *Remote { return c.remote }

// ask is the testable core behind the adk tool wrapper. It sends text to the
// remote agent, resuming a pending input-required task when one exists for the
// given sessionID, and returns the agent's response text. Widgets, files and
// A2UI messages are routed to the UI here instead of being returned to the LLM.
func (c *OrdersClient) ask(ctx context.Context, sessionID, text string) (string, error) {
	// Guard against empty/whitespace messages: some models emit a probing tool
	// call with no message. Instead of wasting an A2A round-trip, tell the model
	// to supply a concrete request. Returned as a normal tool result (not an
	// error) so the model reads it and retries with real content.
	if strings.TrimSpace(text) == "" {
		c.trace.Logf("✖ empty message — skipping A2A call, asking model to provide a concrete request | session=%s", sessionID)
		return "Пустой запрос: сформулируйте конкретный вопрос или действие по заказам в поле message и вызовите инструмент снова.", nil
	}

	reply, err := c.remote.Ask(ctx, sessionID, text)
	if err != nil {
		return "", err
	}
	c.forward(sessionID, reply)
	if reply.NeedsInput {
		return "NEEDS_USER_INPUT: " + reply.Text, nil
	}
	return reply.Text, nil
}

// forward routes everything that should reach the UI directly, bypassing the
// LLM: domain widgets, agent-authored A2UI and attached files.
func (c *OrdersClient) forward(sessionID string, reply Reply) {
	if c.onWidget != nil {
		for _, w := range reply.Widgets {
			c.trace.Logf("    ⟐ widget DataPart (%v) → UI, bypassing LLM", w["_kind"])
			c.onWidget(sessionID, w)
		}
	}
	if c.onA2UI != nil && len(reply.A2UI) > 0 {
		c.trace.Logf("    ⟐ %d A2UI message(s) from the agent → UI, bypassing LLM", len(reply.A2UI))
		c.onA2UI(sessionID, reply.A2UI)
	}
	if c.onFile != nil {
		for _, f := range reply.Files {
			c.trace.Logf("    ⟐ file part %q (%s, %d bytes) → UI", f.name, f.mediaType, len(f.data))
			c.onFile(sessionID, f.name, f.mediaType, f.data)
		}
	}
}

// Profile returns the profile derived for the remote agent.
func (c *OrdersClient) Profile() WorkerProfile { return c.remote.Profile() }

// pendingTaskID returns the A2A task id that is pending for the given session,
// or the zero value if there is none. Intended for tests.
func (c *OrdersClient) pendingTaskID(sessionID string) a2a.TaskID {
	return c.remote.PendingTaskID(sessionID)
}

// askArgs is the input schema for the ask_orders_agent adk function tool.
type askArgs struct {
	Message string `json:"message" description:"Что спросить или сообщить агенту по заказам"`
}

// emptyCallLimit is the number of consecutive empty-message tool calls after
// which we force-stop the agent loop. adk (v1.4.0) has NO built-in iteration
// cap — its agent loop runs until an event is "final" — so a model that keeps
// calling the delegating tool with an empty message (observed with some GLM
// builds) would loop forever. See Tool().
const emptyCallLimit = 2

// emptyMessageReply records one consecutive empty-message call for sessionID and
// returns the tool result plus whether the agent loop must be force-stopped
// (true once the limit is reached).
func (c *OrdersClient) emptyMessageReply(sessionID string) (reply string, stop bool) {
	c.mu.Lock()
	c.emptyCalls[sessionID]++
	n := c.emptyCalls[sessionID]
	c.mu.Unlock()
	if n >= emptyCallLimit {
		c.trace.Logf("✖ %d empty tool calls in a row — force-stopping the agent loop | session=%s", n, sessionID)
		return "Запрос не выполнен: поле message пустое. Ответьте пользователю обычным текстом (например, уточните, что он хочет узнать о заказах) — НЕ вызывайте инструмент снова.", true
	}
	c.trace.Logf("✖ empty message (#%d of %d) | session=%s", n, emptyCallLimit, sessionID)
	return "Пустой запрос: укажите конкретный вопрос или действие по заказам в поле message. Если запрос пользователя неясен, задайте ему уточняющий вопрос обычным текстом, не вызывая инструмент с пустым message.", false
}

// clearEmpty resets the consecutive empty-call counter for a session (called
// whenever a real, non-empty message is delegated).
func (c *OrdersClient) clearEmpty(sessionID string) {
	c.mu.Lock()
	delete(c.emptyCalls, sessionID)
	c.mu.Unlock()
}

// Tool returns an adk function tool, named and described per the derived
// WorkerProfile, that delegates to the worker agent via A2A. The orchestrator
// session id is obtained from tool.Context.SessionID() (available via the
// embedded ReadonlyContext).
func (c *OrdersClient) Tool() tool.Tool {
	profile := c.remote.Profile()
	t, err := functiontool.New(functiontool.Config{
		Name:        profile.ToolName,
		Description: profile.ToolDesc,
	}, func(tc tool.Context, a askArgs) (string, error) {
		// tool.Context embeds context.Context via agent.ReadonlyContext, so tc
		// itself satisfies context.Context. SessionID() is also on ReadonlyContext.
		sessionID := tc.SessionID()
		if strings.TrimSpace(a.Message) == "" {
			reply, stop := c.emptyMessageReply(sessionID)
			if stop {
				// Mark this the final response so adk halts its (otherwise
				// unbounded) agent loop instead of inviting yet another call.
				if act := tc.Actions(); act != nil {
					act.SkipSummarization = true
				}
			}
			return reply, nil
		}
		c.clearEmpty(sessionID)
		return c.ask(tc, sessionID, a.Message)
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create %s tool: %v", profile.ToolName, err))
	}
	return t
}
