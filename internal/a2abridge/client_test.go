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
	"google.golang.org/adk/tool"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"
)

// startWorkerServer binds a real listener, wires an adk worker (with the given
// tools) behind an A2A JSON-RPC handler + AgentCard, and returns its base URL.
// Binding first lets the AgentCard embed the same URL in SupportedInterfaces.
func startWorkerServer(t *testing.T, model *llm.Stub, tools []tool.Tool) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()

	ag, agErr := agent.NewWorker(model, tools)
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
	handler := a2asrv.NewHandler(NewExecutor(r, nil))

	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })

	return url
}

// startWorker starts a worker with no tools; the stub drives behaviour.
func startWorker(t *testing.T, model *llm.Stub) string {
	t.Helper()
	return startWorkerServer(t, model, nil)
}

// startWorkerWithTools starts a worker with the real order tools bound to store,
// so tool side effects (e.g. refunds) are observable in tests.
func startWorkerWithTools(t *testing.T, model *llm.Stub, store *orders.Store) string {
	t.Helper()
	return startWorkerServer(t, model, orders.Tools(store))
}

func TestClientRejectsEmptyMessageWithoutA2ACall(t *testing.T) {
	// A client with a nil a2a client would panic if ask actually made a call;
	// the empty-message guard must return before any A2A round-trip.
	c := &OrdersClient{pending: make(map[string]pending)}
	for _, text := range []string{"", "   ", "\t\n"} {
		out, err := c.ask(context.Background(), "sess", text)
		if err != nil {
			t.Fatalf("text %q: unexpected error: %v", text, err)
		}
		if !strings.Contains(out, "Пустой запрос") {
			t.Fatalf("text %q: want empty-request hint, got %q", text, out)
		}
	}
}

func TestClientRelaysInputRequiredThenCompletes(t *testing.T) {
	// First worker invocation asks for an order id; second resumes the same task.
	model := llm.NewStub(
		llm.StubTurn{Text: "NEED_INPUT: Какой номер заказа?"},
		llm.StubTurn{Text: "Возврат по заказу 1041 оформлен."},
	)
	url := startWorker(t, model)

	c, err := NewOrdersClient(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := "orch-session-1"

	first, err := c.ask(context.Background(), sess, "оформить возврат")
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
	if !strings.Contains(second, "оформлен") {
		t.Fatalf("want completion text containing 'оформлен', got %q", second)
	}

	// After completion the pending entry must be cleared (task was resumed, not
	// a new task), confirming resume identity via the A2A protocol.
	if afterID := c.pendingTaskID(sess); afterID != "" {
		t.Errorf("expected pending task cleared after completion, still have id %q", afterID)
	}
}

func TestToolNameFromCard(t *testing.T) {
	url := startWorker(t, llm.NewStub())
	c, err := NewOrdersClient(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	tl := c.Tool()
	if tl.Name() != "ask_orders_agent" {
		t.Errorf("tool name = %q, want ask_orders_agent (derived from card)", tl.Name())
	}
	if !strings.Contains(tl.Description(), "NEEDS_USER_INPUT") {
		t.Errorf("tool description should carry the NEEDS_USER_INPUT tail; got %q", tl.Description())
	}
	if c.Profile().ToolName != tl.Name() {
		t.Errorf("Profile().ToolName %q != tool.Name() %q", c.Profile().ToolName, tl.Name())
	}
}
