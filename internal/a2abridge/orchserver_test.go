package a2abridge

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
)

// serveExecutor binds a listener and serves the given executor behind an A2A
// JSON-RPC handler + AgentCard built from cardFor(url). Returns the base URL.
func serveExecutor(t *testing.T, exec a2asrv.AgentExecutor, cardFor func(string) *a2a.AgentCard) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	handler := a2asrv.NewHandler(exec)
	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(cardFor(url)))
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return url
}

// a2uiTestClient is a thin A2A client that activates the A2UI extension.
type a2uiTestClient struct{ c *A2UIProbe }

func newA2UIClient(t *testing.T, url string) *a2uiTestClient {
	t.Helper()
	p, err := NewA2UIProbe(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	return &a2uiTestClient{c: p}
}

func (a *a2uiTestClient) sendText(t *testing.T, text string) []*a2a.Part {
	t.Helper()
	parts, err := a.c.SendText(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	return parts
}

// startOrchestrator wires an orchestrator runner whose ask_orders_agent tool
// delegates to a real in-process worker, then returns an A2A test server URL for
// the orchestrator itself.
func startOrchestrator(t *testing.T, orchModel *llm.Stub, workerURL string) (string, *OrdersClient) {
	t.Helper()
	oc, err := NewOrdersClient(context.Background(), workerURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agent.NewOrchestrator(orchModel, oc.Tool(), oc.Profile().Summary)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{
		AppName: "orch", Agent: ag,
		SessionService: session.InMemoryService(), AutoCreateSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	url := serveExecutor(t, NewOrchestratorExecutor(r, oc, nil), OrchestratorCard)
	return url, oc
}

func TestOrchestratorEmitsA2UIWidget(t *testing.T) {
	store := e2eStore(t)
	workerModel := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
	)
	workerURL := startWorkerWithTools(t, workerModel, store)

	orchModel := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "ask_orders_agent", Args: map[string]any{"message": "верни деньги за 1041"}}},
		llm.StubTurn{Text: "Подтвердите оформление возврата по заказу 1041?"},
	)
	orchURL, _ := startOrchestrator(t, orchModel, workerURL)

	client := newA2UIClient(t, orchURL)
	parts := client.sendText(t, "верни деньги за 1041")

	var a2uiParts int
	for _, p := range parts {
		if p != nil && p.MediaType == a2ui.MIMEType {
			a2uiParts++
		}
	}
	if a2uiParts == 0 {
		t.Fatalf("expected an application/a2ui+json part, got parts=%#v", parts)
	}
}

func TestOrchestratorActionResumesRefund(t *testing.T) {
	store := e2eStore(t)
	// Worker: turn 1 → confirmation; turn 2 (after "да") → refund done.
	workerModel := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
		llm.StubTurn{Text: "Возврат по заказу 1041 оформлен."},
	)
	workerURL := startWorkerWithTools(t, workerModel, store)
	// Orchestrator: turn 1 delegates the request; turn 2 delegates the "да".
	orchModel := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "ask_orders_agent", Args: map[string]any{"message": "верни деньги за 1041"}}},
		llm.StubTurn{Text: "Подтвердите оформление возврата по заказу 1041?"},
		llm.StubTurn{Call: &genai.FunctionCall{Name: "ask_orders_agent", Args: map[string]any{"message": "да"}}},
		llm.StubTurn{Text: "Возврат по заказу 1041 оформлен."},
	)
	orchURL, _ := startOrchestrator(t, orchModel, workerURL)
	client := newA2UIClient(t, orchURL)

	// Turn 1: request refund → confirmation widget.
	client.sendText(t, "верни деньги за 1041")
	if o, _ := store.Get("1041"); o.Status == "refunded" {
		t.Fatal("refund must not execute before the button is clicked")
	}
	t.Logf("turn 1 contextID=%s", client.c.contextID)

	// Turn 2: click "Оформить возврат" → approve_refund action.
	_, err := client.c.SendAction(context.Background(), "approve_refund", map[string]any{"order_id": "1041"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turn 2 contextID=%s", client.c.contextID)
	if o, _ := store.Get("1041"); o.Status != "refunded" {
		t.Errorf("approve_refund action must resume the task and refund; status=%q", o.Status)
	}
}
