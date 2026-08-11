package a2abridge

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
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

// startOrchestrator wires an orchestrator over a registry holding a single
// agent — the real in-process worker — and returns an A2A test server URL for
// the orchestrator itself.
func startOrchestrator(t *testing.T, orchModel *llm.Stub, workerURL string) (string, *OrdersClient) {
	t.Helper()
	return startOrchestratorWith(t, orchModel, []config.AgentConfig{
		{ID: "orders", Name: "Агент заказов", URL: workerURL, CardPath: "/.well-known/agent-card.json"},
	})
}

// startOrchestratorWith wires an orchestrator over an arbitrary set of agents.
func startOrchestratorWith(t *testing.T, orchModel *llm.Stub, agents []config.AgentConfig) (string, *OrdersClient) {
	t.Helper()
	reg := NewRegistry(agents, nil)
	build := func(tools []tool.Tool, summary string) (*runner.Runner, error) {
		ag, err := agent.NewOrchestrator(orchModel, tools, summary)
		if err != nil {
			return nil, err
		}
		return runner.New(runner.Config{
			AppName: "orch", Agent: ag,
			SessionService: session.InMemoryService(), AutoCreateSession: true,
		})
	}
	url := serveExecutor(t, NewOrchestratorExecutor(reg, build, nil), OrchestratorCard)
	oc, ok := reg.ClientFor(agents[0].ID)
	if !ok {
		t.Fatalf("registry has no client for %q", agents[0].ID)
	}
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
	body := string(receipt.Raw())
	if receipt.MediaType != "text/html" || receipt.Filename != "receipt-1041.html" {
		t.Errorf("receipt must be an HTML file, got %q %q", receipt.MediaType, receipt.Filename)
	}
	if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, "•••• 1111") ||
		!strings.Contains(body, "Квитанция о возврате") {
		t.Errorf("receipt HTML must carry the masked card and title, got: %q", body)
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

// failingBuilder проваливает тест при любой попытке собрать LLM-runner.
func failingBuilder(t *testing.T) RunnerBuilder {
	t.Helper()
	return func([]tool.Tool, string) (*runner.Runner, error) {
		t.Error("verbatim turn must not build an LLM runner")
		return nil, fmt.Errorf("runner must not be built")
	}
}

// Явно выбранный verbatim-агент отвечает мимо локальной модели: её пересказ
// только испортил бы уже готовый ответ и добавил бы задержку поверх удалённой.
func TestExecutorVerbatimBypassesLLM(t *testing.T) {
	s := startOuroborosStub(t, false)
	reg := NewRegistry([]config.AgentConfig{ouroborosCfg(s.URL)}, nil)
	url := serveExecutor(t, NewOrchestratorExecutor(reg, failingBuilder(t), nil), OrchestratorCard)

	probe := newA2UIClient(t, url)
	probe.c.AgentID = "ouroboros"
	parts := probe.sendText(t, "статус заказа ORD-001")

	text, a2uiParts := "", 0
	for _, p := range parts {
		if p == nil {
			continue
		}
		if p.MediaType == a2ui.MIMEType {
			a2uiParts++
			continue
		}
		if txt := p.Text(); txt != "" {
			text = txt
		}
	}
	if text != "Заказ доставлен" {
		t.Errorf("verbatim text: got %q", text)
	}
	if a2uiParts == 0 {
		t.Errorf("expected the agent's A2UI to pass through, got parts=%#v", parts)
	}
}

// Браузер с устаревшим списком агентов не должен ломать диалог.
func TestExecutorUnknownAgentFallsBackToAuto(t *testing.T) {
	workerURL := startWorker(t, llm.NewStub(llm.StubTurn{Text: "неважно"}))
	orchModel := llm.NewStub(llm.StubTurn{Text: "Здравствуйте!"})
	url, _ := startOrchestrator(t, orchModel, workerURL)

	probe := newA2UIClient(t, url)
	probe.c.AgentID = "несуществующий"
	parts := probe.sendText(t, "привет")

	if len(parts) == 0 || parts[0].Text() != "Здравствуйте!" {
		t.Fatalf("expected the auto path to answer, got %#v", parts)
	}
}

// В режиме «Авто» модель видит инструменты всех доступных агентов.
func TestExecutorAutoExposesEveryTool(t *testing.T) {
	workerURL := startWorker(t, llm.NewStub(llm.StubTurn{Text: "неважно"}))
	s := startOuroborosStub(t, false)
	external := ouroborosCfg(s.URL)
	external.Verbatim = false // в «Авто» агент участвует как обычный инструмент

	reg := NewRegistry([]config.AgentConfig{
		{ID: "orders", Name: "Агент заказов", URL: workerURL, CardPath: "/.well-known/agent-card.json"},
		external,
	}, nil)

	var gotTools []string
	build := func(tools []tool.Tool, summary string) (*runner.Runner, error) {
		for _, tl := range tools {
			gotTools = append(gotTools, tl.Name())
		}
		ag, err := agent.NewOrchestrator(llm.NewStub(llm.StubTurn{Text: "ок"}), tools, summary)
		if err != nil {
			return nil, err
		}
		return runner.New(runner.Config{
			AppName: "orch", Agent: ag,
			SessionService: session.InMemoryService(), AutoCreateSession: true,
		})
	}
	url := serveExecutor(t, NewOrchestratorExecutor(reg, build, nil), OrchestratorCard)

	probe := newA2UIClient(t, url)
	probe.sendText(t, "привет")

	if len(gotTools) != 2 {
		t.Fatalf("auto mode tools: got %v, want two", gotTools)
	}
	if gotTools[0] != "ask_orders_agent" || gotTools[1] != "ask_ouroboros" {
		t.Errorf("tool names: %v", gotTools)
	}
}

// При явном выборе агента модель получает ровно один инструмент.
func TestExecutorExplicitSelectionNarrowsTools(t *testing.T) {
	workerURL := startWorker(t, llm.NewStub(llm.StubTurn{Text: "неважно"}))
	s := startOuroborosStub(t, false)
	external := ouroborosCfg(s.URL)
	external.Verbatim = false

	reg := NewRegistry([]config.AgentConfig{
		{ID: "orders", Name: "Агент заказов", URL: workerURL, CardPath: "/.well-known/agent-card.json"},
		external,
	}, nil)

	var gotTools []string
	build := func(tools []tool.Tool, summary string) (*runner.Runner, error) {
		for _, tl := range tools {
			gotTools = append(gotTools, tl.Name())
		}
		ag, err := agent.NewOrchestrator(llm.NewStub(llm.StubTurn{Text: "ок"}), tools, summary)
		if err != nil {
			return nil, err
		}
		return runner.New(runner.Config{
			AppName: "orch", Agent: ag,
			SessionService: session.InMemoryService(), AutoCreateSession: true,
		})
	}
	url := serveExecutor(t, NewOrchestratorExecutor(reg, build, nil), OrchestratorCard)

	probe := newA2UIClient(t, url)
	probe.c.AgentID = "ouroboros"
	probe.sendText(t, "привет")

	if len(gotTools) != 1 || gotTools[0] != "ask_ouroboros" {
		t.Fatalf("explicit selection tools: got %v, want [ask_ouroboros]", gotTools)
	}
}

func TestActionToPromptDescribesUnknownButton(t *testing.T) {
	got := actionToPrompt("return_order", map[string]any{"label": "Оформить возврат", "order_id": "ORD-001"})
	if !strings.Contains(got, "Оформить возврат") || !strings.Contains(got, "order_id: ORD-001") {
		t.Errorf("prompt: %q", got)
	}
	// Наши HITL-кнопки сохраняют канонические ответы.
	if got := actionToPrompt("approve_refund", map[string]any{}); got != "да" {
		t.Errorf("approve_refund → %q, want «да»", got)
	}
}

// Агент выводит surfaceId из контекста, который живёт всю сессию. Если не
// разводить поверхности по ходам, второй ход роняет рендерер в браузере
// ошибкой «Surface … already exists» — а на сервере всё выглядит исправным.
func TestExecutorGivesEachTurnItsOwnSurface(t *testing.T) {
	s := startOuroborosStub(t, false)
	reg := NewRegistry([]config.AgentConfig{ouroborosCfg(s.URL)}, nil)
	url := serveExecutor(t, NewOrchestratorExecutor(reg, failingBuilder(t), nil), OrchestratorCard)

	probe := newA2UIClient(t, url)
	probe.c.AgentID = "ouroboros"

	surfaceIDs := func(parts []*a2a.Part) []string {
		var ids []string
		for _, p := range parts {
			if p == nil || p.MediaType != a2ui.MIMEType {
				continue
			}
			m, ok := p.Data().(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"createSurface", "updateComponents"} {
				if payload, ok := m[key].(map[string]any); ok {
					if id, ok := payload["surfaceId"].(string); ok {
						ids = append(ids, id)
					}
				}
			}
		}
		return ids
	}

	first := surfaceIDs(probe.sendText(t, "первый заказ"))
	second := surfaceIDs(probe.sendText(t, "второй заказ"))
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("ожидались A2UI-части в обоих ходах: %v / %v", first, second)
	}
	for _, a := range first {
		for _, b := range second {
			if a == b {
				t.Fatalf("поверхность %q переиспользована на втором ходу", a)
			}
		}
	}
	// Внутри одного хода ссылка обязана остаться единой.
	for _, id := range first[1:] {
		if id != first[0] {
			t.Errorf("в пределах хода surfaceId разъехались: %v", first)
		}
	}
}
