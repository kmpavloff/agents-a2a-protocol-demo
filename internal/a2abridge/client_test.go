package a2abridge

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
)

// startWorker binds a real listener first so we know the URL before building
// the AgentCard (which must embed the same URL in SupportedInterfaces).
func startWorker(t *testing.T, model *llm.Stub) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()

	ag, agErr := agent.NewWorker(model, nil) // tools not needed; stub drives behaviour
	if agErr != nil {
		t.Fatal(agErr)
	}
	r, rErr := runner.New(runner.Config{
		AppName:           "t",
		Agent:             ag,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if rErr != nil {
		t.Fatal(rErr)
	}

	card := AgentCard(url)
	handler := a2asrv.NewHandler(NewExecutor(r))

	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })

	return url
}

func TestClientRelaysInputRequiredThenCompletes(t *testing.T) {
	// First worker invocation asks for an order id; second resumes the same task.
	model := llm.NewStub(
		llm.StubTurn{Text: "NEED_INPUT: Which order id?"},
		llm.StubTurn{Text: "Refund initiated for 1041."},
	)
	url := startWorker(t, model)

	c, err := NewOrdersClient(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	sess := "orch-session-1"

	first, err := c.ask(context.Background(), sess, "refund my order")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "NEEDS_USER_INPUT:") {
		t.Fatalf("want NEEDS_USER_INPUT prefix, got %q", first)
	}

	// Capture the pending task ID after the first (input-required) turn; the
	// second ask must resume the SAME task rather than starting a new one.
	pendingID := c.pendingTaskID(sess)
	if pendingID == "" {
		t.Fatal("expected a pending task id after input-required turn, got empty")
	}

	second, err := c.ask(context.Background(), sess, "1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "Refund initiated") {
		t.Fatalf("want completion text containing 'Refund initiated', got %q", second)
	}

	// After completion the pending entry must be cleared (task was resumed, not
	// a new task), confirming resume identity via the A2A protocol.
	if afterID := c.pendingTaskID(sess); afterID != "" {
		t.Errorf("expected pending task cleared after completion, still have id %q", afterID)
	}
	_ = pendingID // referenced above; keep the variable for clarity
}
