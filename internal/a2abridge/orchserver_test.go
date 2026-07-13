package a2abridge

import (
	"context"
	"net"
	"net/http"
	"strings"
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
	// Worker: turn 1 → confirmation; "да" → card form; valid card → refund.
	workerModel := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
		llm.StubTurn{Text: "Возврат по заказу 1041 оформлен."},
	)
	workerURL := startWorkerWithTools(t, workerModel, store)
	// Orchestrator LLM only phrases turn 1; the approve and card-submit buttons
	// resume the worker directly, bypassing the LLM entirely.
	orchModel := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "ask_orders_agent", Args: map[string]any{"message": "верни деньги за 1041"}}},
		llm.StubTurn{Text: "Подтвердите оформление возврата по заказу 1041?"},
	)
	orchURL, _ := startOrchestrator(t, orchModel, workerURL)
	client := newA2UIClient(t, orchURL)

	// Turn 1: request refund → confirmation widget.
	client.sendText(t, "верни деньги за 1041")
	if o, _ := store.Get("1041"); o.Status == "refunded" {
		t.Fatal("refund must not execute before the button is clicked")
	}
	t.Logf("turn 1 contextID=%s", client.c.contextID)

	// Turn 2: click "Оформить возврат" → approve_refund → card form, not refunded yet.
	parts2, err := client.c.SendAction(context.Background(), "approve_refund", map[string]any{"order_id": "1041"})
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := store.Get("1041"); o.Status == "refunded" {
		t.Fatal("refund must not execute before card details are submitted")
	}
	if !hasA2UIComponent(parts2, "TextField") {
		t.Errorf("approve should produce the card form (TextField) as A2UI, got %v", partsSummary(parts2))
	}

	// Turn 3: submit the card form → refund executes; receipt file attached.
	parts3, err := client.c.SendAction(context.Background(), "submit_refund_details",
		map[string]any{"order_id": "1041", "card_number": "4111 1111 1111 1111"})
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := store.Get("1041"); o.Status != "refunded" {
		t.Errorf("submit_refund_details must resume the task and refund; status=%q", o.Status)
	}
	var receipt *a2a.Part
	for _, p := range parts3 {
		if p != nil && p.Filename != "" {
			receipt = p
		}
	}
	if receipt == nil {
		t.Fatalf("completed refund must attach a receipt file, got %v", partsSummary(parts3))
	}
	if receipt.MediaType != "text/plain" || !strings.Contains(string(receipt.Raw()), "•••• 1111") {
		t.Errorf("receipt file must be text/plain with the masked card, got %q: %q",
			receipt.MediaType, string(receipt.Raw()))
	}
}

// hasA2UIComponent reports whether any A2UI DataPart among parts contains a
// component of the given type.
func hasA2UIComponent(parts []*a2a.Part, component string) bool {
	for _, p := range parts {
		if p == nil {
			continue
		}
		data, ok := p.Data().(map[string]any)
		if !ok {
			continue
		}
		uc, ok := data["updateComponents"].(map[string]any)
		if !ok {
			continue
		}
		comps, _ := uc["components"].([]any)
		for _, c := range comps {
			if m, ok := c.(map[string]any); ok && m["component"] == component {
				return true
			}
		}
	}
	return false
}

// partsSummary renders parts compactly for failure messages.
func partsSummary(parts []*a2a.Part) []string {
	var out []string
	for _, p := range parts {
		switch {
		case p == nil:
			out = append(out, "nil")
		case p.Filename != "":
			out = append(out, "file:"+p.Filename)
		case p.Text() != "":
			out = append(out, "text:"+p.Text())
		default:
			out = append(out, "data")
		}
	}
	return out
}
