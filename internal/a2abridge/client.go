package a2abridge

import (
	"context"
	"fmt"
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
	mu      sync.Mutex
	pending map[string]pending // keyed by orchestrator session id
}

// NewOrdersClient resolves the worker AgentCard at workerURL and creates an
// A2A client from it.
func NewOrdersClient(ctx context.Context, workerURL string) (*OrdersClient, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, workerURL)
	if err != nil {
		return nil, fmt.Errorf("resolve worker card: %w", err)
	}
	cl, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		return nil, fmt.Errorf("a2a client: %w", err)
	}
	return &OrdersClient{
		client:  cl,
		pending: make(map[string]pending),
	}, nil
}

// ask is the testable core behind the adk tool wrapper. It sends text to the
// orders worker agent, resuming a pending input-required task when one exists
// for the given sessionID, and returns the agent's response text.
func (c *OrdersClient) ask(ctx context.Context, sessionID, text string) (string, error) {
	c.mu.Lock()
	p, hasPending := c.pending[sessionID]
	c.mu.Unlock()

	var msg *a2a.Message
	if hasPending {
		// Resume an existing input-required task by referencing its IDs.
		info := a2a.TaskInfo{TaskID: p.taskID, ContextID: p.contextID}
		msg = a2a.NewMessageForTask(a2a.MessageRoleUser, info, a2a.NewTextPart(text))
	} else {
		msg = a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	}

	res, err := c.client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return "", fmt.Errorf("orders agent unreachable: %w", err)
	}

	switch r := res.(type) {
	case *a2a.Message:
		// Synchronous response — no task created.
		c.clearPending(sessionID)
		if len(r.Parts) > 0 {
			return r.Parts[0].Text(), nil
		}
		return "Done.", nil

	case *a2a.Task:
		if r.Status.State == a2a.TaskStateInputRequired {
			// Store the task so the next call resumes it.
			c.mu.Lock()
			c.pending[sessionID] = pending{taskID: r.ID, contextID: r.ContextID}
			c.mu.Unlock()
			return "NEEDS_USER_INPUT: " + statusMessageText(r), nil
		}
		// Completed (or failed/canceled) — clear any pending state.
		c.clearPending(sessionID)
		return taskResultText(r), nil

	default:
		return "", fmt.Errorf("unexpected A2A result type %T", res)
	}
}

// clearPending removes any pending task for sessionID.
func (c *OrdersClient) clearPending(sessionID string) {
	c.mu.Lock()
	delete(c.pending, sessionID)
	c.mu.Unlock()
}

// askArgs is the input schema for the ask_orders_agent adk function tool.
type askArgs struct {
	Message string `json:"message" description:"What to ask or tell the orders agent"`
}

// Tool returns an adk function tool named ask_orders_agent that delegates to
// the orders worker agent via A2A. The orchestrator session id is obtained
// from tool.Context.SessionID() (available via the embedded ReadonlyContext).
func (c *OrdersClient) Tool() tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name:        "ask_orders_agent",
		Description: "Delegate an order-related request to the orders agent. If it returns NEEDS_USER_INPUT, ask the user that question, then call this tool again with their answer.",
	}, func(tc tool.Context, a askArgs) (string, error) {
		// tool.Context embeds context.Context via agent.ReadonlyContext, so tc
		// itself satisfies context.Context. SessionID() is also on ReadonlyContext.
		return c.ask(tc, tc.SessionID(), a.Message)
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create ask_orders_agent tool: %v", err))
	}
	return t
}

// statusMessageText extracts the question text from an input-required task's
// status message.
func statusMessageText(t *a2a.Task) string {
	if t.Status.Message != nil && len(t.Status.Message.Parts) > 0 {
		return t.Status.Message.Parts[0].Text()
	}
	return "The orders agent needs more information."
}

// taskResultText returns the last artifact text of a completed task, falling
// back to the last history message text, then "Done." if neither is present.
func taskResultText(t *a2a.Task) string {
	if len(t.Artifacts) > 0 {
		last := t.Artifacts[len(t.Artifacts)-1]
		if len(last.Parts) > 0 {
			return last.Parts[0].Text()
		}
	}
	if len(t.History) > 0 {
		last := t.History[len(t.History)-1]
		if len(last.Parts) > 0 {
			return last.Parts[0].Text()
		}
	}
	return "Done."
}
