package a2abridge

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	internalagent "github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"
)

func newTestRunner(t *testing.T, model *llm.Stub, store *orders.Store) *runner.Runner {
	t.Helper()
	ag, err := internalagent.NewWorker(model, orders.Tools(store))
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: "test", Agent: ag, SessionService: session.InMemoryService(), AutoCreateSession: true})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExecutorEmitsInputRequired(t *testing.T) {
	store := seedStore(t)
	model := llm.NewStub(llm.StubTurn{Text: "NEED_INPUT: Какой заказ вернуть?"})
	exec := NewExecutor(newTestRunner(t, model, store), nil)

	states := runExecutor(t, exec, "оформить возврат")
	if len(states) == 0 {
		t.Fatal("no states emitted")
	}
	if last := states[len(states)-1]; last != a2a.TaskStateInputRequired {
		t.Fatalf("want final state input-required, got %v", last)
	}
}

func TestExecutorCompletesWithAnswer(t *testing.T) {
	store := seedStore(t)
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "get_order_status", Args: map[string]any{"order_id": "1041"}}},
		llm.StubTurn{Text: "Заказ 1041 доставлен."},
	)
	exec := NewExecutor(newTestRunner(t, model, store), nil)

	states, text := runExecutorCollect(t, exec, "статус 1041")
	if len(states) == 0 {
		t.Fatal("no states emitted")
	}
	if last := states[len(states)-1]; last != a2a.TaskStateCompleted {
		t.Fatalf("want completed, got %v", last)
	}
	if !strings.Contains(text, "1041") {
		t.Errorf("want answer mentioning 1041, got %q", text)
	}
}

// seedStore creates a minimal orders store with one order for use in tests.
func seedStore(t *testing.T) *orders.Store {
	t.Helper()
	p := t.TempDir() + "/o.json"
	body := `{"orders":[{"id":"1041","customer":"alice","item":"Хаб","amount":34.5,"currency":"EUR","status":"delivered","created":"2026-06-10","refundable":true}],"sales_stats":[]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := orders.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// runExecutor is a convenience wrapper that returns only the state slice.
func runExecutor(t *testing.T, exec a2asrv.AgentExecutor, text string) []a2a.TaskState {
	t.Helper()
	states, _ := runExecutorCollect(t, exec, text)
	return states
}

// runExecutorCollect builds a minimal ExecutorContext, calls exec.Execute, and
// collects all TaskStatusUpdateEvent states and all artifact text parts.
func runExecutorCollect(t *testing.T, exec a2asrv.AgentExecutor, text string) ([]a2a.TaskState, string) {
	t.Helper()

	ctx := context.Background()

	// Build the incoming user message.
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))

	// Construct ExecutorContext. StoredTask is nil → server will yield a submitted task.
	ec := &a2asrv.ExecutorContext{
		Message:   msg,
		TaskID:    a2a.TaskID("test-task-1"),
		ContextID: "test-context-1",
	}

	var states []a2a.TaskState
	var artifactText strings.Builder

	for event, err := range exec.Execute(ctx, ec) {
		if err != nil {
			t.Fatalf("executor returned error: %v", err)
		}
		switch e := event.(type) {
		case *a2a.TaskStatusUpdateEvent:
			states = append(states, e.Status.State)
		case *a2a.TaskArtifactUpdateEvent:
			if e.Artifact != nil {
				for _, p := range e.Artifact.Parts {
					artifactText.WriteString(p.Text())
				}
			}
		}
	}

	return states, artifactText.String()
}

func TestParseAffirmative(t *testing.T) {
	for _, yes := range []string{"да", "Да", " да, оформляй ", "yes", "подтверждаю", "ок"} {
		if !parseAffirmative(yes) {
			t.Errorf("parseAffirmative(%q) = false, want true", yes)
		}
	}
	for _, no := range []string{"нет", "не надо", "отмена", ""} {
		if parseAffirmative(no) {
			t.Errorf("parseAffirmative(%q) = true, want false", no)
		}
	}
}

// runExecutorInputRequired runs one turn and returns the final state plus the
// input-required status-message text (the question shown to the user).
func runExecutorInputRequired(t *testing.T, exec a2asrv.AgentExecutor, text string) (a2a.TaskState, string) {
	t.Helper()
	ctx := context.Background()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	ec := &a2asrv.ExecutorContext{Message: msg, TaskID: a2a.TaskID("t1"), ContextID: "c1"}

	var last a2a.TaskState
	var question string
	for event, err := range exec.Execute(ctx, ec) {
		if err != nil {
			t.Fatalf("executor error: %v", err)
		}
		if e, ok := event.(*a2a.TaskStatusUpdateEvent); ok {
			last = e.Status.State
			if e.Status.State == a2a.TaskStateInputRequired && e.Status.Message != nil && len(e.Status.Message.Parts) > 0 {
				question = e.Status.Message.Parts[0].Text()
			}
		}
	}
	return last, question
}

func TestExecutorRequestsRefundConfirmation(t *testing.T) {
	store := seedStore(t)
	// The stub drives the worker LLM to call initiate_refund for order 1041.
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
	)
	exec := NewExecutor(newTestRunner(t, model, store), nil)

	state, question := runExecutorInputRequired(t, exec, "оформи возврат по заказу 1041")
	if state != a2a.TaskStateInputRequired {
		t.Fatalf("want input-required, got %v", state)
	}
	if !strings.Contains(question, "1041") || !strings.Contains(strings.ToLower(question), "подтверд") {
		t.Errorf("confirmation question should ask to confirm order 1041; got %q", question)
	}
	// The refund must NOT have happened yet (no confirmation given).
	if o, _ := store.Get("1041"); o.Status == "refunded" {
		t.Error("refund executed before confirmation")
	}
}

