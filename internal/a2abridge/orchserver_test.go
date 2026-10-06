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
		if isA2UIPart(p) {
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

// a2uiMessages собирает A2UI-сообщения из частей ответа. Часть по спеке одна на
// набор, а её data — массив сообщений, поэтому наборы склеиваются.
func a2uiMessages(parts []*a2a.Part) []map[string]any {
	var out []map[string]any
	for _, p := range parts {
		if !isA2UIPart(p) {
			continue
		}
		switch v := p.Data().(type) {
		case []any:
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					out = append(out, m)
				}
			}
		case map[string]any:
			out = append(out, v)
		}
	}
	return out
}

// hasA2UIComponent reports whether any A2UI part among parts contains a
// component of the given type.
func hasA2UIComponent(parts []*a2a.Part, component string) bool {
	for _, data := range a2uiMessages(parts) {
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
		if isA2UIPart(p) {
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

// Навык из второго селектора браузера доходит до агента в metadata.skill, а
// «Авто» и «без навыка» его не передают — даже если раньше в этом разговоре
// навык был выбран.
func TestExecutorPassesSelectedSkill(t *testing.T) {
	s := startOuroborosStub(t, false)
	reg := NewRegistry([]config.AgentConfig{ouroborosCfg(s.URL)}, nil)
	url := serveExecutor(t, NewOrchestratorExecutor(reg, failingBuilder(t), nil), OrchestratorCard)
	skillOf := func(i int) any {
		meta, _ := s.message(t, i)["metadata"].(map[string]any)
		return meta["skill"]
	}

	probe := newA2UIClient(t, url)
	probe.c.AgentID, probe.c.Skill = "ouroboros", "support"
	probe.sendText(t, "статус заказа ORD-001")
	if skillOf(0) != "support" {
		t.Errorf("выбранный навык: %v", skillOf(0))
	}
	probe.c.Skill = ""
	probe.sendText(t, "ещё раз")
	if skillOf(1) != nil {
		t.Errorf("«без навыка» не должен передавать навык: %v", skillOf(1))
	}
	probe.c.Skill = "чужой"
	probe.sendText(t, "и ещё")
	if skillOf(2) != nil {
		t.Errorf("навык не из списка агента не должен уходить: %v", skillOf(2))
	}
}

// Нажатие кнопки уезжает внешнему агенту штатным событием A2UI, а не
// пересказом на человеческом языке: пересказ понимает только модель, а событие
// — любой агент, собранный на референсном SDK.
func TestExecutorForwardsActionAsA2UIEvent(t *testing.T) {
	s := startOuroborosStub(t, false)
	reg := NewRegistry([]config.AgentConfig{ouroborosCfg(s.URL)}, nil)
	url := serveExecutor(t, NewOrchestratorExecutor(reg, failingBuilder(t), nil), OrchestratorCard)

	probe := newA2UIClient(t, url)
	probe.c.AgentID = "ouroboros"
	if _, err := probe.c.SendAction(context.Background(), "return_order",
		map[string]any{"order_id": "ORD-002", "label": "Оформить возврат"}); err != nil {
		t.Fatal(err)
	}

	parts, _ := s.message(t, 0)["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("ожидалась одна часть, получено %d: %v", len(parts), parts)
	}
	part, _ := parts[0].(map[string]any)
	if txt, _ := part["text"].(string); txt != "" {
		t.Errorf("действие ушло текстом %q — ожидалось событие", txt)
	}
	meta, _ := part["metadata"].(map[string]any)
	if meta["mimeType"] != a2ui.MIMEType {
		t.Errorf("metadata.mimeType = %v, ожидалось %q", meta["mimeType"], a2ui.MIMEType)
	}
	name, ctx, ok := a2ui.ParseAction(part["data"])
	if !ok {
		t.Fatalf("действие не разобралось: %v", part["data"])
	}
	if name != "return_order" || ctx["order_id"] != "ORD-002" {
		t.Errorf("получено name=%q ctx=%v", name, ctx)
	}
}

// Провалившийся ход и недоступный агент — разные беды, и в ленте они обязаны
// читаться по-разному: «недоступен» отправит пользователя чинить сеть там, где
// агент на связи и просто не справился с запросом.
func TestAgentErrorTextDistinguishesFailureFromOutage(t *testing.T) {
	failed := &TurnFailedError{
		AgentID: "ouroboros",
		State:   a2a.TaskStateFailed,
		Reason:  "Client error '403 Forbidden' for url 'http://127.0.0.1:8767/chat/allocate-internal'\nFor more information check: https://developer.mozilla.org/…",
	}
	got := agentErrorText("Ouroboros · магазин", failed)
	if strings.Contains(got, "недоступен") {
		t.Errorf("провал хода назван недоступностью: %q", got)
	}
	if !strings.Contains(got, "403 Forbidden") {
		t.Errorf("причина отказа потеряна: %q", got)
	}
	// Вторая строка причины — ссылка на документацию, в ленте она лишняя.
	if strings.Contains(got, "developer.mozilla.org") || strings.Contains(got, "\n") {
		t.Errorf("в ленту утекла многострочная простыня: %q", got)
	}

	down := fmt.Errorf("agent %q unreachable: %w", "ouroboros", context.DeadlineExceeded)
	if got := agentErrorText("Ouroboros · магазин", down); !strings.Contains(got, "недоступен") {
		t.Errorf("недоступность должна называться недоступностью: %q", got)
	}
}

// Текстовый режим сквозной: клиент не объявил A2UI — значит и у внешнего
// агента разметку просить незачем, он потратит на неё время впустую.
//
// Заодно это проверка того, что намерение клиента доезжает до Remote по пути
// «Авто»: там между исполнителем и агентом стоит runner ADK с вызовом
// инструмента, и значение контекста обязано пережить эту прослойку.
func TestTextModeStopsAskingAgentForA2UI(t *testing.T) {
	for _, tc := range []struct {
		name     string
		a2ui     bool
		wantCaps bool
	}{
		{"режим виджетов", true, true},
		{"текстовый режим", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startOuroborosStub(t, false)
			orchModel := llm.NewStub(
				llm.StubTurn{Call: &genai.FunctionCall{Name: "ask_ouroboros", Args: map[string]any{"message": "статус заказа"}}},
				llm.StubTurn{Text: "готово"},
			)
			url, _ := startOrchestratorWith(t, orchModel, []config.AgentConfig{ouroborosCfg(s.URL)})

			probe := newA2UIClient(t, url)
			probe.c.A2UI = tc.a2ui
			if _, err := probe.c.SendText(context.Background(), "статус заказа"); err != nil {
				t.Fatal(err)
			}

			meta, _ := s.message(t, 0)["metadata"].(map[string]any)
			_, gotCaps := meta["a2uiClientCapabilities"]
			if gotCaps != tc.wantCaps {
				t.Errorf("a2uiClientCapabilities у агента: %v, ожидалось %v", gotCaps, tc.wantCaps)
			}
			gotHeader := s.header(t, 0).Get("A2A-Extensions") != ""
			if gotHeader != tc.wantCaps {
				t.Errorf("заголовок расширения у агента: %v, ожидалось %v", gotHeader, tc.wantCaps)
			}
		})
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
		for _, m := range a2uiMessages(parts) {
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

// После правки конфига кэш runner'ов обязан обнулиться: иначе модель осталась
// бы со старым промптом, а после удаления агента звала бы несуществующий
// инструмент.
func TestRunnerCacheDropsOnRegistryChange(t *testing.T) {
	s := startOuroborosStub(t, false)
	cfg := ouroborosCfg(s.URL)
	cfg.ID = "one"
	reg := NewRegistry([]config.AgentConfig{cfg}, nil)

	built := 0
	build := func(tools []tool.Tool, summary string) (*runner.Runner, error) {
		built++
		return &runner.Runner{}, nil
	}
	e := NewOrchestratorExecutor(reg, build, nil).(*orchExecutor)

	ctx := context.Background()
	if _, err := e.runnerFor(ctx, "one"); err != nil {
		t.Fatalf("runnerFor: %v", err)
	}
	if _, err := e.runnerFor(ctx, "one"); err != nil {
		t.Fatalf("runnerFor: %v", err)
	}
	if built != 1 {
		t.Fatalf("runner собран %d раз(а) — кэш не работает", built)
	}

	changed := cfg
	changed.Description = "Другое описание."
	reg.Apply([]config.AgentConfig{changed})

	if _, err := e.runnerFor(ctx, "one"); err != nil {
		t.Fatalf("runnerFor после правки: %v", err)
	}
	if built != 2 {
		t.Errorf("после правки runner собран %d раз(а), want 2", built)
	}
}

// Клиент агента, добавленного на ходу, должен получить обработчики виджетов.
func TestExecutorWiresHandlersForNewAgents(t *testing.T) {
	s := startOuroborosStub(t, false)
	cfg := ouroborosCfg(s.URL)
	cfg.ID = "one"
	reg := NewRegistry([]config.AgentConfig{cfg}, nil)
	build := func([]tool.Tool, string) (*runner.Runner, error) { return &runner.Runner{}, nil }
	e := NewOrchestratorExecutor(reg, build, nil).(*orchExecutor)

	added := ouroborosCfg(s.URL)
	added.ID = "two"
	reg.Apply([]config.AgentConfig{cfg, added})
	c, ok := reg.ClientFor("two")
	if !ok {
		t.Fatal("клиент нового агента не создан")
	}
	c.onWidget("сессия", map[string]any{"_kind": "widget/order"})
	if ws, _, _, _ := e.drain("сессия"); len(ws) != 1 {
		t.Errorf("виджет нового агента потерян: %+v", ws)
	}
}

// textOnlyReply — ответ агента без A2UI: одна текстовая часть. Так внешний
// агент отвечает, когда решил обойтись без разметки, хотя объявляет её.
const textOnlyReply = `{"jsonrpc":"2.0","id":"1","result":{"id":"t1","contextId":"c1",
 "status":{"state":"TASK_STATE_COMPLETED","message":{"messageId":"m1","role":"ROLE_AGENT",
   "parts":[{"text":"**ORD-001 — доставлен**\n\nКлиент: Иван Петров\nСумма: 3 499 ₽\nТрек-номер: RU123456789","mediaType":"text/plain"}]}}}}`

// stubRunner собирает runner на сценарном стабе модели: сначала вызов
// делегирующего инструмента, затем заданный текст.
func stubRunner(t *testing.T, toolName, finalText string) RunnerBuilder {
	t.Helper()
	return func(tools []tool.Tool, summary string) (*runner.Runner, error) {
		model := llm.NewStub(
			llm.StubTurn{Call: &genai.FunctionCall{
				Name: toolName, Args: map[string]any{"message": "статус заказа ORD-001"},
			}},
			llm.StubTurn{Text: finalText},
		)
		ag, err := agent.NewOrchestrator(model, tools, summary)
		if err != nil {
			return nil, err
		}
		return runner.New(runner.Config{
			AppName: "orch", Agent: ag,
			SessionService: session.InMemoryService(), AutoCreateSession: true,
		})
	}
}

// Промпт запрещает модели называть значения: их покажет карточка. Когда
// карточки нет — внешний агент прислал один текст, — подводка «Вот детали
// вашего заказа:» остаётся единственным, что видит пользователь, и данные
// пропадают. В ленту должен уйти ответ самого агента.
func TestLLMTurnFallsBackToAgentTextWithoutWidget(t *testing.T) {
	s := startOuroborosStub(t, false)
	s.reply = textOnlyReply
	cfg := ouroborosCfg(s.URL)
	cfg.Verbatim = false // ход идёт через локальную модель, а не напрямую
	reg := NewRegistry([]config.AgentConfig{cfg}, nil)
	url := serveExecutor(t,
		NewOrchestratorExecutor(reg, stubRunner(t, "ask_ouroboros", "Вот детали вашего заказа:"), nil),
		OrchestratorCard)

	probe := newA2UIClient(t, url)
	parts := probe.sendText(t, "статус заказа ORD-001")

	text := ""
	for _, p := range parts {
		if p != nil && !isA2UIPart(p) && p.Text() != "" {
			text = p.Text()
		}
	}
	if !strings.Contains(text, "RU123456789") {
		t.Errorf("данные агента не доехали до пользователя, got %q", text)
	}
	if text == "Вот детали вашего заказа:" {
		t.Error("пользователь получил подводку без данных")
	}
}

// Обратная сторона: если модель ответила содержательнее агента, подменять
// нечего — её ответ и остаётся.
func TestLLMTurnKeepsItsOwnFullerAnswer(t *testing.T) {
	s := startOuroborosStub(t, false)
	s.reply = textOnlyReply
	cfg := ouroborosCfg(s.URL)
	cfg.Verbatim = false
	reg := NewRegistry([]config.AgentConfig{cfg}, nil)
	long := strings.Repeat("Развёрнутый ответ модели. ", 20)
	url := serveExecutor(t,
		NewOrchestratorExecutor(reg, stubRunner(t, "ask_ouroboros", long), nil),
		OrchestratorCard)

	probe := newA2UIClient(t, url)
	parts := probe.sendText(t, "статус заказа ORD-001")

	text := ""
	for _, p := range parts {
		if p != nil && !isA2UIPart(p) && p.Text() != "" {
			text = p.Text()
		}
	}
	if !strings.Contains(text, "Развёрнутый ответ модели") {
		t.Errorf("ответ модели подменён напрасно, got %q", text)
	}
}

// Виджет есть — подводка модели уместна, и подменять её нельзя: иначе рядом с
// карточкой встанет её же текстовый пересказ.
func TestPickAnswerKeepsLeadInWhenWidgetIsShown(t *testing.T) {
	agentText := []string{"Очень длинный ответ агента, который никто не должен показать рядом с карточкой."}
	got, swapped := pickAnswer("Вот детали:", agentText, true)
	if swapped || got != "Вот детали:" {
		t.Errorf("got %q swapped=%v", got, swapped)
	}
}
