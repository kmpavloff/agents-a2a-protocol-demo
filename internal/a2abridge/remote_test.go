package a2abridge

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
)

// ouroborosStub отвечает ровно как внешний агент: карточка по своему пути,
// Basic-auth, JSON-RPC на "/", заведомо нерабочий адрес внутри карточки
// (0.0.0.0) и состояния в форме TASK_STATE_*.
type ouroborosStub struct {
	URL string

	// reply — тело успешного ответа; пустое означает stubCompleted.
	// Ставится до первого запроса, поэтому под мьютексом не ходит.
	reply string

	mu       sync.Mutex
	requests []map[string]any
	headers  []http.Header
	workOnce bool // первый SendMessage вернёт WORKING
}

const stubCard = `{"name":"Who I Am","description":"d","version":"1.4.0","protocolVersion":"1.0",
 "url":"http://0.0.0.0:18800/",
 "supportedInterfaces":[{"url":"http://0.0.0.0:18800/","protocolBinding":"JSONRPC","protocolVersion":"1.0"}],
 "capabilities":{},"defaultInputModes":["text/plain"],
 "defaultOutputModes":["text/plain","application/a2ui+json"],"skills":[]}`

const stubCompleted = `{"jsonrpc":"2.0","id":"1","result":{"id":"t1","contextId":"c1",
 "status":{"state":"TASK_STATE_COMPLETED","message":{"messageId":"m1","role":"ROLE_AGENT",
   "parts":[{"text":"Заказ доставлен","mediaType":"text/plain"}]}},
 "artifacts":[{"artifactId":"a1","parts":[{"data":[
   {"version":"v0.9.1","createSurface":{"surfaceId":"s1","catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},
   {"version":"v0.9.1","updateComponents":{"surfaceId":"s1","components":[{"Text":{"id":"t","text":"Order"}}]}}
 ],"mediaType":"application/a2ui+json"}]}]}}`

func startOuroborosStub(t *testing.T, workOnce bool) *ouroborosStub {
	t.Helper()
	s := &ouroborosStub{workOnce: workOnce}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.URL = "http://" + ln.Addr().String()

	authed := func(w http.ResponseWriter, r *http.Request) bool {
		u, p, ok := r.BasicAuth()
		if !ok || u != "ouroboros" || p != "testpass" {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent.json", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, stubCard)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.headers = append(s.headers, r.Header.Clone())
		working := s.workOnce && req["method"] == "SendMessage"
		if working {
			s.workOnce = false
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if working {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_WORKING"}}}`)
			return
		}
		body := s.reply
		if body == "" {
			body = stubCompleted
		}
		_, _ = io.WriteString(w, body)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return s
}

// request возвращает i-й принятый JSON-RPC запрос.
func (s *ouroborosStub) request(t *testing.T, i int) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.requests) {
		t.Fatalf("request #%d not received (got %d)", i, len(s.requests))
	}
	return s.requests[i]
}

// header возвращает заголовки i-го принятого запроса.
func (s *ouroborosStub) header(t *testing.T, i int) http.Header {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.headers) {
		t.Fatalf("request #%d not received (got %d)", i, len(s.headers))
	}
	return s.headers[i]
}

// message достаёт params.message из i-го запроса.
func (s *ouroborosStub) message(t *testing.T, i int) map[string]any {
	t.Helper()
	params, _ := s.request(t, i)["params"].(map[string]any)
	msg, _ := params["message"].(map[string]any)
	if msg == nil {
		t.Fatalf("request #%d has no params.message: %v", i, s.request(t, i))
	}
	return msg
}

func ouroborosCfg(url string) config.AgentConfig {
	return config.AgentConfig{
		ID: "ouroboros", Name: "Ouroboros", URL: url,
		CardPath: "/.well-known/agent.json", Skill: "shop", Verbatim: true,
		Description: "Заказы магазина.", Timeout: "10s",
		Auth: config.AuthConfig{Type: "basic", Username: "ouroboros", Password: "testpass"},
	}
}

func TestRemoteAskSendsSkillAndParsesA2UI(t *testing.T) {
	s := startOuroborosStub(t, false)
	r := NewRemote(ouroborosCfg(s.URL), nil)

	reply, err := r.Ask(context.Background(), "sess-1", "статус заказа ORD-001")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if reply.Text != "Заказ доставлен" {
		t.Errorf("text: got %q", reply.Text)
	}
	if len(reply.A2UI) != 2 {
		t.Fatalf("a2ui: got %d messages, want 2", len(reply.A2UI))
	}
	// metadata.skill обязан уехать на провод: без него внешний агент не
	// включит инструменты навыка.
	meta, _ := s.message(t, 0)["metadata"].(map[string]any)
	if meta["skill"] != "shop" {
		t.Errorf("metadata.skill: got %v", meta)
	}
}

func TestRemoteReusesContextIDPerSession(t *testing.T) {
	s := startOuroborosStub(t, false)
	r := NewRemote(ouroborosCfg(s.URL), nil)
	ctx := context.Background()

	if _, err := r.Ask(ctx, "sess-1", "первый"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ask(ctx, "sess-1", "второй"); err != nil {
		t.Fatal(err)
	}
	if got := s.message(t, 1)["contextId"]; got != "c1" {
		t.Errorf("contextId not reused: %v", got)
	}

	// Другая сессия начинает разговор с нуля.
	if _, err := r.Ask(ctx, "sess-2", "чужая сессия"); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.message(t, 2)["contextId"]; ok && got != "" {
		t.Errorf("contextId leaked across sessions: %v", got)
	}
}

func TestRemotePollsWhileWorking(t *testing.T) {
	s := startOuroborosStub(t, true) // первый ответ — WORKING
	r := NewRemote(ouroborosCfg(s.URL), nil)

	reply, err := r.Ask(context.Background(), "sess-1", "долгий запрос")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if reply.Text != "Заказ доставлен" {
		t.Errorf("text after polling: got %q", reply.Text)
	}
	if got := s.request(t, 1)["method"]; got != "GetTask" {
		t.Errorf("expected a GetTask poll, got %v", got)
	}
}

func TestRemoteFailsWithoutAuth(t *testing.T) {
	s := startOuroborosStub(t, false)
	cfg := ouroborosCfg(s.URL)
	cfg.Auth = config.AuthConfig{}
	r := NewRemote(cfg, nil)

	if _, err := r.Ask(context.Background(), "sess-1", "привет"); err == nil {
		t.Fatal("expected an error without basic auth")
	}
	if r.Available() {
		t.Error("agent must be marked unavailable after a failed connect")
	}
}

// Карточка объявляет нерабочий адрес 0.0.0.0 — клиент обязан ходить по адресу
// из конфига, иначе ни один запрос не дойдёт.
func TestRemoteOverridesCardURL(t *testing.T) {
	s := startOuroborosStub(t, false)
	r := NewRemote(ouroborosCfg(s.URL), nil)
	if err := r.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	r.mu.Lock()
	got := r.card.SupportedInterfaces[0].URL
	r.mu.Unlock()
	if got != s.URL {
		t.Errorf("interface URL: got %q, want %q", got, s.URL)
	}
}

func TestRemoteProfileUsesConfigDescription(t *testing.T) {
	s := startOuroborosStub(t, false)
	r := NewRemote(ouroborosCfg(s.URL), nil)
	if err := r.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := r.Profile()
	if p.ToolName != "ask_ouroboros" {
		t.Errorf("tool name: got %q", p.ToolName)
	}
	if !strings.Contains(p.ToolDesc, "Заказы магазина.") {
		t.Errorf("tool desc: got %q", p.ToolDesc)
	}
	if !strings.Contains(p.Summary, "Ouroboros") {
		t.Errorf("summary: got %q", p.Summary)
	}
}

// Без description профиль по-прежнему выводится из AgentCard — это заявленное
// свойство демо, и ломать его нельзя.
func TestRemoteProfileFallsBackToCard(t *testing.T) {
	s := startOuroborosStub(t, false)
	cfg := ouroborosCfg(s.URL)
	cfg.Description = ""
	r := NewRemote(cfg, nil)
	if err := r.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.Profile().ToolName; got != "ask_Who_I_Am" {
		t.Errorf("card-derived tool name: got %q", got)
	}
}

func TestWrapBareResult(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string
		changed bool
	}{
		{
			name:    "голая задача оборачивается",
			body:    `{"jsonrpc":"2.0","id":"1","result":{"id":"t1","status":{"state":"TASK_STATE_COMPLETED"}}}`,
			want:    `"task"`,
			changed: true,
		},
		{
			name:    "голое сообщение оборачивается",
			body:    `{"jsonrpc":"2.0","id":"1","result":{"messageId":"m1","role":"ROLE_AGENT","parts":[]}}`,
			want:    `"message"`,
			changed: true,
		},
		{
			name:    "корректный ответ не трогается",
			body:    `{"jsonrpc":"2.0","id":"1","result":{"task":{"id":"t1"}}}`,
			changed: false,
		},
		{
			name:    "ошибка JSON-RPC не трогается",
			body:    `{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"Method not found"}}`,
			changed: false,
		},
		{
			name:    "мусор не трогается",
			body:    `не json`,
			changed: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := wrapBareResult([]byte(c.body))
			if changed != c.changed {
				t.Fatalf("changed: got %v, want %v (body %s)", changed, c.changed, got)
			}
			if !changed {
				if string(got) != c.body {
					t.Errorf("body altered: %s", got)
				}
				return
			}
			if !strings.Contains(string(got), c.want) {
				t.Errorf("wrapper %s missing: %s", c.want, got)
			}
		})
	}
}

func TestMergeEndpoint(t *testing.T) {
	cases := []struct{ base, declared, want string }{
		// Чужая карточка объявляет нерабочий хост — берём хост из конфига.
		{"http://192.168.1.68:18800", "http://0.0.0.0:18800/", "http://192.168.1.68:18800"},
		// Наш воркер объявляет путь /invoke — терять его нельзя.
		{"http://127.0.0.1:8081", "http://127.0.0.1:8081/invoke", "http://127.0.0.1:8081/invoke"},
		{"http://127.0.0.1:8081", "http://0.0.0.0:8081/invoke", "http://127.0.0.1:8081/invoke"},
	}
	for _, c := range cases {
		if got := mergeEndpoint(c.base, c.declared); got != c.want {
			t.Errorf("mergeEndpoint(%q, %q) = %q, want %q", c.base, c.declared, got, c.want)
		}
	}
}

// Агенту, объявившему A2UI, надо явно сказать, что клиент его отрендерит:
// иначе он на своё усмотрение отвечает то виджетом, то голым текстом.
func TestRemoteRequestsA2UIWhenCardAdvertisesIt(t *testing.T) {
	s := startOuroborosStub(t, false)
	r := NewRemote(ouroborosCfg(s.URL), nil)
	if _, err := r.Ask(context.Background(), "sess-1", "статус заказа"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	params, _ := s.request(t, 0)["params"].(map[string]any)
	cfg, _ := params["configuration"].(map[string]any)
	modes, _ := cfg["acceptedOutputModes"].([]any)
	found := false
	for _, m := range modes {
		if m == "application/a2ui+json" {
			found = true
		}
	}
	if !found {
		t.Errorf("acceptedOutputModes must offer A2UI, got %v", modes)
	}
}

// Штатный признак «клиент умеет A2UI» по спеке расширения — не набор режимов
// вывода, а capabilities в метаданных сообщения плюс заголовок. Заголовок
// уходит двумя именами: A2A 1.0 знает A2A-Extensions, спека A2UI v0.9 —
// X-A2A-Extensions, и агент, собранный по ней, второго не увидит.
func TestRemoteAnnouncesA2UICapabilities(t *testing.T) {
	s := startOuroborosStub(t, false)
	r := NewRemote(ouroborosCfg(s.URL), nil)
	if _, err := r.Ask(context.Background(), "sess-1", "статус заказа"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	meta, _ := s.message(t, 0)["metadata"].(map[string]any)
	caps, ok := meta["a2uiClientCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("нет metadata.a2uiClientCapabilities: %v", meta)
	}
	v09, _ := caps["v0.9"].(map[string]any)
	ids, _ := v09["supportedCatalogIds"].([]any)
	if len(ids) == 0 || ids[0] != a2ui.CatalogID {
		t.Errorf("supportedCatalogIds = %v, ожидался basic-каталог", ids)
	}
	// skill остаётся на месте: capabilities его не вытесняют.
	if meta["skill"] != "shop" {
		t.Errorf("metadata.skill = %v, ожидалось shop", meta["skill"])
	}

	h := s.header(t, 0)
	for _, name := range []string{"A2A-Extensions", "X-A2A-Extensions"} {
		if got := h.Get(name); got != a2ui.ExtensionURI {
			t.Errorf("заголовок %s = %q, ожидался %q", name, got, a2ui.ExtensionURI)
		}
	}
}

// Разметку спека кладёт в части сообщения — оттуда её и надо читать. Артефакт
// остаётся запасным путём (см. TestRemoteAskSendsSkillAndParsesA2UI), но когда
// разметка есть в обоих местах, поверхность не должна приехать дважды.
func TestRemotePrefersA2UIFromStatusMessage(t *testing.T) {
	s := startOuroborosStub(t, false)
	s.reply = `{"jsonrpc":"2.0","id":"1","result":{"id":"t1","contextId":"c1",
	 "status":{"state":"TASK_STATE_COMPLETED","message":{"messageId":"m1","role":"ROLE_AGENT","parts":[
	   {"text":"Заказ доставлен","mediaType":"text/plain"},
	   {"metadata":{"mimeType":"application/a2ui+json"},"data":[
	     {"version":"v0.9.1","createSurface":{"surfaceId":"from-message"}},
	     {"version":"v0.9.1","updateComponents":{"surfaceId":"from-message","components":[{"Text":{"id":"t","text":"Order"}}]}}
	   ]}]}},
	 "artifacts":[{"artifactId":"a1","parts":[{"mediaType":"application/a2ui+json","data":[
	   {"version":"v0.9.1","createSurface":{"surfaceId":"from-artifact"}},
	   {"version":"v0.9.1","updateComponents":{"surfaceId":"from-artifact","components":[{"Text":{"id":"t","text":"Order"}}]}}
	 ]}]}]}}`

	r := NewRemote(ouroborosCfg(s.URL), nil)
	reply, err := r.Ask(context.Background(), "sess-1", "статус заказа")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if reply.Text != "Заказ доставлен" {
		t.Errorf("текст ответа = %q", reply.Text)
	}
	var ids []string
	for _, m := range reply.A2UI {
		if cs, ok := m["createSurface"].(map[string]any); ok {
			id, _ := cs["surfaceId"].(string)
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 || ids[0] != "from-message" {
		t.Errorf("поверхности = %v, ожидалась одна из сообщения", ids)
	}
}

// Провалившаяся задача — не ответ. Агент кладёт в статусное сообщение причину
// отказа, и без разделения она попадала бы в ленту как обычная реплика:
// пользователь видел бы «timed out» как ответ на свой вопрос.
func TestRemoteFailedTaskIsError(t *testing.T) {
	for _, state := range []string{"TASK_STATE_FAILED", "TASK_STATE_REJECTED", "TASK_STATE_CANCELED"} {
		s := startOuroborosStub(t, false)
		s.reply = `{"jsonrpc":"2.0","id":"1","result":{"id":"t1","contextId":"c1",
		 "status":{"state":"` + state + `","message":{"messageId":"m1","role":"ROLE_AGENT",
		   "parts":[{"text":"timed out","mediaType":"text/plain"}]}}}}`

		r := NewRemote(ouroborosCfg(s.URL), nil)
		reply, err := r.Ask(context.Background(), "sess-1", "статус заказа")
		if err == nil {
			t.Errorf("%s: ожидалась ошибка, получен ответ %q", state, reply.Text)
			continue
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Errorf("%s: причина отказа потеряна: %v", state, err)
		}
	}
}

// Агент, не объявивший A2UI, не должен получать запрос на него.
func TestRemoteSkipsA2UIRequestForPlainAgent(t *testing.T) {
	workerURL := startWorker(t, llm.NewStub(llm.StubTurn{Text: "ок"}))
	r := NewRemote(config.AgentConfig{ID: "orders", URL: workerURL,
		CardPath: "/.well-known/agent-card.json"}, nil)
	if err := r.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.acceptsA2UI() {
		t.Error("worker card does not advertise A2UI, must not be asked for it")
	}
}

// A2UI приезжает и текстовой частью с mediaType application/a2ui+json. Такая
// часть не должна попасть в текст ответа: пользователь увидел бы JSON, а в
// режиме «Авто» его же получила бы модель как результат инструмента.
func TestFirstProseTextSkipsA2UI(t *testing.T) {
	a2uiPart := a2a.NewTextPart(`[{"createSurface":{}}]`)
	a2uiPart.MediaType = a2ui.MIMEType
	prose := a2a.NewTextPart("Заказ доставлен")

	if got := firstProseText([]*a2a.Part{a2uiPart, prose}); got != "Заказ доставлен" {
		t.Errorf("A2UI-часть просочилась в текст: %q", got)
	}
	if got := firstProseText([]*a2a.Part{a2uiPart}); got != "" {
		t.Errorf("без человекочитаемой части текста быть не должно: %q", got)
	}
}
