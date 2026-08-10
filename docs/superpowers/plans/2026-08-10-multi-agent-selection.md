# Multi-Agent Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Оркестратор держит список удалённых A2A-агентов из конфига, а пользователь выбирает агента в браузере («Авто» / конкретный агент), включая внешний агент Ouroboros в локальной сети.

**Architecture:** Конфиг получает список `agents`. Каждый агент — объект `a2abridge.Remote` (ленивый резолв AgentCard по своему пути, Basic-auth, подмена адреса из карточки, `metadata.skill`, поллинг `GetTask`). `Registry` собирает их и отдаёт adk-инструменты. `orchExecutor` маршрутизирует по `metadata.agentId` из браузера: «Авто» (LLM со всеми инструментами), явный выбор (LLM с одним) или `verbatim` (без LLM). Чужой A2UI приводится к форме нашего рендерера функцией `a2ui.Ingest`.

**Tech Stack:** Go 1.26, `github.com/a2aproject/a2a-go/v2 v2.3.1`, `google.golang.org/adk`, TypeScript + Lit + `@a2ui/web_core` 0.10.x, Vite.

Спека: `docs/superpowers/specs/2026-08-10-multi-agent-selection-design.md`.

## Global Constraints

- Go-тесты не ходят в сеть: удалённый агент поднимается `httptest`/`net.Listen`, LLM — `llm.Stub`.
- `go test ./...` должен проходить целиком после каждой задачи.
- Существующее поведение воркера (HITL-возврат, карта, квитанция, виджеты, TUI) не меняется.
- Имя делегирующего инструмента выводится из AgentCard, если у агента нет `description` в конфиге; при наличии `description` — `ask_<id>`.
- Пароль никогда не попадает в трейс и в логи.
- Комментарии и пользовательские строки — на русском, как в остальном коде.
- Java-порт (`java/`) в этой итерации не трогаем.
- A2UI-версия наших сообщений — `a2ui.Version` (`v0.9`), каталог — `a2ui.CatalogID`.

## File Structure

| Файл | Ответственность |
|---|---|
| `internal/config/config.go` (M) | `AgentConfig`, `AuthConfig`, `Agents`, миграция `worker_url`, env-пароль, валидация |
| `internal/a2ui/ingest.go` (C) | Извлечение и нормализация чужого A2UI в форму basic-каталога v0.9 |
| `internal/a2abridge/remote.go` (C) | Одно соединение с одним удалённым агентом: карточка, auth, сессии, `Ask` |
| `internal/a2abridge/registry.go` (C) | `id → *Remote`, `List`/`Get`/`Tools`, разрешение коллизий имён |
| `internal/a2abridge/client.go` (M) | `OrdersClient` переезжает на `Remote`, добавляет проброс A2UI |
| `internal/a2abridge/orchserver.go` (M) | Маршрутизация по `metadata.agentId`, verbatim-ветка, кэш runner'ов |
| `internal/webui/agents.go` (C) | `GET /api/agents` |
| `cmd/orchestrator/main.go` (M) | Сборка `Registry`, проводка в web/TUI |
| `web/src/app.ts`, `web/src/client.ts` (M) | Селектор агента, `metadata.agentId`, имя агента в спиннере |
| `configs/orchestrator.example.yaml`, `README.md`, `java/README.md` (M) | Документация |

---

### Task 1: Конфиг — список агентов

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.AgentConfig{ID, Name, URL, CardPath, Skill, Verbatim, Timeout, Description, Auth}`, `config.AuthConfig{Type, Username, Password}`, `OrchestratorConfig.Agents []AgentConfig`, `(AgentConfig).TimeoutDuration() time.Duration`.

- [ ] **Step 1: Написать падающие тесты**

```go
func TestLoadOrchestratorParsesAgents(t *testing.T) {
	p := writeTemp(t, `
agents:
  - id: orders
    name: "Агент заказов"
    url: "http://localhost:8081"
  - id: ouroboros
    name: "Ouroboros"
    url: "http://192.168.1.68:18800"
    card_path: "/.well-known/agent.json"
    skill: "shop"
    verbatim: true
    timeout: "180s"
    description: "Заказы магазина."
    auth: {type: basic, username: ouroboros, password: testpass}
llm:
  base_url: "http://localhost:1234/v1"
`)
	cfg, err := LoadOrchestrator(p)
	if err != nil {
		t.Fatalf("LoadOrchestrator: %v", err)
	}
	if len(cfg.Agents) != 2 {
		t.Fatalf("agents: got %d, want 2", len(cfg.Agents))
	}
	if cfg.Agents[0].CardPath != "/.well-known/agent-card.json" {
		t.Errorf("default card_path: got %q", cfg.Agents[0].CardPath)
	}
	if cfg.Agents[0].TimeoutDuration() != 120*time.Second {
		t.Errorf("default timeout: got %v", cfg.Agents[0].TimeoutDuration())
	}
	o := cfg.Agents[1]
	if o.Skill != "shop" || !o.Verbatim || o.TimeoutDuration() != 180*time.Second {
		t.Errorf("ouroboros fields: %+v", o)
	}
	if o.Auth.Type != "basic" || o.Auth.Username != "ouroboros" || o.Auth.Password != "testpass" {
		t.Errorf("auth: %+v", o.Auth)
	}
}

func TestLoadOrchestratorMigratesWorkerURL(t *testing.T) {
	p := writeTemp(t, "worker_url: \"http://localhost:8081\"\nllm:\n  base_url: \"http://localhost:1234/v1\"\n")
	cfg, err := LoadOrchestrator(p)
	if err != nil {
		t.Fatalf("LoadOrchestrator: %v", err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].ID != "orders" || cfg.Agents[0].URL != "http://localhost:8081" {
		t.Fatalf("migration: %+v", cfg.Agents)
	}
}

func TestLoadOrchestratorAgentPasswordFromEnv(t *testing.T) {
	p := writeTemp(t, `
agents:
  - id: ouroboros
    url: "http://192.168.1.68:18800"
    description: "d"
    auth: {type: basic, username: ouroboros}
llm:
  base_url: "http://localhost:1234/v1"
`)
	t.Setenv("A2A_AGENT_OUROBOROS_PASSWORD", "from-env")
	cfg, err := LoadOrchestrator(p)
	if err != nil {
		t.Fatalf("LoadOrchestrator: %v", err)
	}
	if cfg.Agents[0].Auth.Password != "from-env" {
		t.Errorf("password from env: got %q", cfg.Agents[0].Auth.Password)
	}
}

func TestLoadOrchestratorRejectsBadAgents(t *testing.T) {
	cases := map[string]string{
		"duplicate id": "agents:\n  - {id: a, url: \"http://x\"}\n  - {id: a, url: \"http://y\"}\nllm:\n  base_url: \"http://l\"\n",
		"bad id":       "agents:\n  - {id: \"Ouro Boros\", url: \"http://x\"}\nllm:\n  base_url: \"http://l\"\n",
		"empty url":    "agents:\n  - {id: a, url: \"\"}\nllm:\n  base_url: \"http://l\"\n",
		"bad timeout":  "agents:\n  - {id: a, url: \"http://x\", timeout: \"soon\"}\nllm:\n  base_url: \"http://l\"\n",
		"bad auth":     "agents:\n  - {id: a, url: \"http://x\", auth: {type: oauth}}\nllm:\n  base_url: \"http://l\"\n",
		"no agents":    "llm:\n  base_url: \"http://l\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadOrchestrator(writeTemp(t, body)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test ./internal/config/ -run TestLoadOrchestrator -v`
Expected: FAIL — `cfg.Agents undefined`.

- [ ] **Step 3: Реализовать**

```go
// AuthConfig описывает, как аутентифицироваться у удалённого агента.
// Поддерживается только HTTP Basic: встроенный a2aclient.AuthInterceptor умеет
// лишь Bearer, поэтому Basic мы ставим своим RoundTripper'ом.
type AuthConfig struct {
	Type     string `yaml:"type"` // "" или "basic"
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// AgentConfig — один удалённый A2A-агент, с которым умеет говорить оркестратор.
type AgentConfig struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`
	CardPath string `yaml:"card_path"`
	// Skill уезжает в metadata.skill каждого сообщения — так внешний агент
	// понимает, какой набор инструментов включать.
	Skill    string `yaml:"skill"`
	Verbatim bool   `yaml:"verbatim"`
	Timeout  string `yaml:"timeout"`
	// Description вытесняет вывод из AgentCard: у агента может быть сотня
	// навыков, и промпт локальной модели такого не переживёт.
	Description string     `yaml:"description"`
	Auth        AuthConfig `yaml:"auth"`
}

const (
	defaultCardPath     = "/.well-known/agent-card.json"
	defaultAgentTimeout = 120 * time.Second
)

// TimeoutDuration возвращает таймаут SendMessage, подставляя значение по
// умолчанию. Валидность строки проверена при загрузке конфига.
func (a AgentConfig) TimeoutDuration() time.Duration {
	if a.Timeout == "" {
		return defaultAgentTimeout
	}
	d, err := time.ParseDuration(a.Timeout)
	if err != nil || d <= 0 {
		return defaultAgentTimeout
	}
	return d
}

var agentIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// envAgentPassword — имя переменной окружения с паролем агента.
func envAgentPassword(id string) string {
	return "A2A_AGENT_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_PASSWORD"
}

// applyAgentDefaults подставляет умолчания и env-перекрытия, затем валидирует.
func applyAgentDefaults(agents []AgentConfig) error {
	seen := make(map[string]bool, len(agents))
	for i := range agents {
		a := &agents[i]
		if !agentIDRe.MatchString(a.ID) {
			return fmt.Errorf("orchestrator config: agent id %q must match %s", a.ID, agentIDRe)
		}
		if seen[a.ID] {
			return fmt.Errorf("orchestrator config: duplicate agent id %q", a.ID)
		}
		seen[a.ID] = true
		if a.URL == "" {
			return fmt.Errorf("orchestrator config: agent %q: url is required", a.ID)
		}
		if a.CardPath == "" {
			a.CardPath = defaultCardPath
		}
		if a.Timeout != "" {
			if d, err := time.ParseDuration(a.Timeout); err != nil || d <= 0 {
				return fmt.Errorf("orchestrator config: agent %q: bad timeout %q", a.ID, a.Timeout)
			}
		}
		switch a.Auth.Type {
		case "", "basic":
		default:
			return fmt.Errorf("orchestrator config: agent %q: unsupported auth type %q", a.ID, a.Auth.Type)
		}
		if p := os.Getenv(envAgentPassword(a.ID)); p != "" {
			a.Auth.Password = p
		}
	}
	return nil
}
```

В `LoadOrchestrator` — после `applyEnv`, до проверки LLM:

```go
	// Совместимость: одиночный worker_url становится единственным агентом.
	if len(c.Agents) == 0 && c.WorkerURL != "" {
		c.Agents = []AgentConfig{{ID: "orders", Name: "Агент заказов", URL: c.WorkerURL}}
	}
	if len(c.Agents) == 0 {
		return c, fmt.Errorf("orchestrator config: at least one agent (agents: or worker_url:) is required")
	}
	if err := applyAgentDefaults(c.Agents); err != nil {
		return c, err
	}
```

Старую проверку `if c.WorkerURL == ""` — удалить (её заменяет проверка выше).

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/config/
git commit -m "feat(config): список удалённых агентов вместо одного worker_url"
```

---

### Task 2: `a2ui.Ingest` — приём чужого A2UI

**Files:**
- Create: `internal/a2ui/ingest.go`
- Test: `internal/a2ui/ingest_test.go`
- Использует фикстуру: `internal/a2ui/testdata/ouroboros_order_card.json`

**Interfaces:**
- Produces: `a2ui.Part{MediaType, Text string, Data any}`, `a2ui.Ingest(parts []Part) []map[string]any`.
- Пакет `a2ui` остаётся транспортно-независимым: он НЕ импортирует `a2a`. Конвертацию `a2a.Part → a2ui.Part` делает `a2abridge`.

- [ ] **Step 1: Написать падающие тесты**

```go
// basicCatalog — 18 компонентов basic-каталога v0.9. Ingest обязан выдавать
// только их: всё остальное наш рендерер (@a2ui/web_core) отвергает.
var basicCatalog = map[string]bool{
	"Text": true, "Image": true, "Icon": true, "Video": true, "AudioPlayer": true,
	"Row": true, "Column": true, "List": true, "Card": true, "Tabs": true,
	"Modal": true, "Divider": true, "Button": true, "TextField": true,
	"CheckBox": true, "ChoicePicker": true, "Slider": true, "DateTimeInput": true,
}

// assertRenderable проверяет инвариант, ради которого Ingest существует:
// каждый компонент плоский, с id и типом из basic-каталога.
func assertRenderable(t *testing.T, msgs []map[string]any) {
	t.Helper()
	if len(msgs) == 0 {
		t.Fatal("no A2UI messages")
	}
	seenUpdate := false
	for _, m := range msgs {
		uc, ok := m["updateComponents"].(map[string]any)
		if !ok {
			continue
		}
		seenUpdate = true
		comps, _ := uc["components"].([]map[string]any)
		if len(comps) == 0 {
			t.Fatal("updateComponents without components")
		}
		for _, c := range comps {
			id, _ := c["id"].(string)
			typ, _ := c["component"].(string)
			if id == "" {
				t.Errorf("component without id: %v", c)
			}
			if !basicCatalog[typ] {
				t.Errorf("component %q is not in the basic catalog: %v", typ, c)
			}
		}
	}
	if !seenUpdate {
		t.Fatal("no updateComponents message")
	}
}

// TestIngestRealOuroborosResponse гоняет Ingest на живом ответе агента,
// снятом 2026-08-10: текстовая часть с mediaType a2ui+json, обёрнутые
// компоненты и четыре типа вне каталога.
func TestIngestRealOuroborosResponse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "ouroboros_order_card.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Artifacts []struct {
				Parts []struct {
					Text      string `json:"text"`
					Data      any    `json:"data"`
					MediaType string `json:"mediaType"`
				} `json:"parts"`
			} `json:"artifacts"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	var parts []Part
	for _, p := range resp.Result.Artifacts[0].Parts {
		parts = append(parts, Part{MediaType: p.MediaType, Text: p.Text, Data: p.Data})
	}
	msgs := Ingest(parts)
	assertRenderable(t, msgs)
}

// TestIngestContractShape — форма из контракта: DataPart с массивом сообщений
// и уже плоскими компонентами. Должна проходить насквозь.
func TestIngestContractShape(t *testing.T) {
	data := []any{
		map[string]any{"version": "v0.9.1", "createSurface": map[string]any{"surfaceId": "s1", "catalogId": CatalogID}},
		map[string]any{"version": "v0.9.1", "updateComponents": map[string]any{
			"surfaceId": "s1",
			"components": []any{
				map[string]any{"id": "title", "component": "Text", "text": "Order", "variant": "h2"},
			},
		}},
	}
	msgs := Ingest([]Part{{MediaType: MIMEType, Data: data}})
	assertRenderable(t, msgs)
	if msgs[0]["version"] != Version {
		t.Errorf("version not normalised: %v", msgs[0]["version"])
	}
}

func TestIngestDegradesUnknownComponents(t *testing.T) {
	comps := []any{
		map[string]any{"Heading": map[string]any{"id": "h", "text": "Заказ", "level": 2}},
		map[string]any{"Metric": map[string]any{"id": "m", "label": "Итого", "value": "3 499 ₽"}},
		map[string]any{"Callout": map[string]any{"id": "c", "variant": "success", "title": "Доставлен", "text": "Иван"}},
		map[string]any{"Table": map[string]any{"id": "t",
			"columns": []any{map[string]any{"key": "sku", "label": "Артикул"}},
			"rows":    []any{map[string]any{"sku": "WIDGET-RED"}}}},
		map[string]any{"Button": map[string]any{"id": "b", "label": "Отследить", "variant": "secondary"}},
		map[string]any{"Sparkline": map[string]any{"id": "x", "points": []any{1, 2}}},
	}
	data := []any{
		map[string]any{"createSurface": map[string]any{"surfaceId": "s1", "catalogId": CatalogID}},
		map[string]any{"updateComponents": map[string]any{"surfaceId": "s1", "components": comps}},
	}
	msgs := Ingest([]Part{{MediaType: MIMEType, Data: data}})
	assertRenderable(t, msgs)

	byID := map[string]map[string]any{}
	for _, m := range msgs {
		if uc, ok := m["updateComponents"].(map[string]any); ok {
			for _, c := range uc["components"].([]map[string]any) {
				byID[c["id"].(string)] = c
			}
		}
	}
	if byID["h"]["component"] != "Text" || byID["h"]["variant"] != "h3" {
		t.Errorf("Heading → %v", byID["h"])
	}
	if txt, _ := byID["m"]["text"].(string); !strings.Contains(txt, "Итого") || !strings.Contains(txt, "3 499") {
		t.Errorf("Metric → %v", byID["m"])
	}
	if byID["c"]["component"] != "Column" {
		t.Errorf("Callout → %v", byID["c"])
	}
	if txt, _ := byID["t"]["text"].(string); !strings.Contains(txt, "| Артикул |") || !strings.Contains(txt, "WIDGET-RED") {
		t.Errorf("Table → %v", byID["t"])
	}
	if byID["b"]["component"] != "Button" || byID["b"]["child"] == nil {
		t.Errorf("Button → %v", byID["b"])
	}
	if byID["x"]["component"] != "Text" {
		t.Errorf("unknown component → %v", byID["x"])
	}
}

func TestIngestIgnoresGarbage(t *testing.T) {
	cases := [][]Part{
		nil,
		{{MediaType: "text/plain", Text: "просто текст"}},
		{{MediaType: MIMEType, Text: "не json"}},
		{{MediaType: MIMEType, Data: 42}},
		{{MediaType: MIMEType, Data: []any{"строка вместо объекта"}}},
		{{MediaType: MIMEType, Data: map[string]any{"updateComponents": "не объект"}}},
	}
	for i, parts := range cases {
		if got := Ingest(parts); len(got) != 0 {
			t.Errorf("case %d: expected no messages, got %v", i, got)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test ./internal/a2ui/ -run TestIngest -v`
Expected: FAIL — `undefined: Ingest`.

- [ ] **Step 3: Реализовать `internal/a2ui/ingest.go`**

Ключевые правила (полный код пишется по ним):

```go
// Part — минимальный вид A2A-части, нужный Ingest. Пакет a2ui намеренно не
// импортирует a2a: конвертацию делает транспортный слой.
type Part struct {
	MediaType string
	Text      string
	Data      any
}

// Ingest достаёт A2UI-сообщения из частей ответа удалённого агента и приводит
// их к форме, которую понимает наш рендерер (плоские компоненты basic-каталога
// v0.9).
//
// Толерантен по входу, потому что живой агент отдаёт не то, что обещает его
// контракт: A2UI приезжает и как DataPart с массивом сообщений, и как одиночный
// объект, и как ТЕКСТОВАЯ часть с mediaType application/a2ui+json, внутри
// которой JSON-строка. Компоненты приезжают и плоскими, и обёрнутыми
// ({"Text": {...}}), и типов, которых нет в объявленном самим агентом каталоге.
//
// Инвариант: Ingest никогда не паникует и не возвращает ошибку. Худший исход —
// блёклый, но валидный набор компонентов. Чужой агент не должен ронять наш UI.
func Ingest(parts []Part) []map[string]any
```

Правила нормализации компонента:

| Вход | Выход |
|---|---|
| `{"id", "component": "<из каталога>", …}` | как есть |
| `{"id", "component": "Button", "label": L}` | `Button{child: id+"__lbl"}` + `Text(id+"__lbl", L)` |
| `{"<Тип>": {…}}` | разворачивается, дальше по типу |
| `Heading{text}` | `Text{variant: "h3"}` |
| `Metric{label, value}` | `Text{text: "**label:** value"}` |
| `Callout{title, text}` | `Column{children: [id+"__t", id+"__b"]}` + `Text(h3)` + `Text(body)` |
| `Table{columns, rows}` | `Text` с markdown-таблицей |
| прочее | `Text` с компактным JSON-дампом свойств |

Инварианты реализации:
- **id контейнера сохраняется**: при разворачивании `Callout` в `Column` id остаётся прежним, иначе родительский `children` перестанет ссылаться на существующий компонент. Новые дочерние id — `<id>__t`, `<id>__b`, `<id>__lbl`.
- Компонент без `id` получает `gen<N>` (счётчик в пределах одного вызова).
- `version` любого сообщения переписывается в `Version`.
- Сообщения не из белого списка (`createSurface`, `updateComponents`, `updateDataModel`, `deleteSurface`) отбрасываются.
- `components` на выходе — всегда `[]map[string]any`.

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/a2ui/ -v`
Expected: PASS (все, включая существующие).

- [ ] **Step 5: Коммит**

```bash
git add internal/a2ui/
git commit -m "feat(a2ui): толерантный приём чужого A2UI с нормализацией в basic-каталог"
```

---

### Task 3: `Remote` — соединение с одним удалённым агентом

**Files:**
- Create: `internal/a2abridge/remote.go`
- Test: `internal/a2abridge/remote_test.go`

**Interfaces:**
- Consumes: `config.AgentConfig`, `a2ui.Ingest`, `a2ui.Part`, существующие `Tracer`, `WorkerProfile`, `ProfileFromCard`, `firstWidget`, `attachedFile`.
- Produces:

```go
type Reply struct {
	Text       string
	NeedsInput bool                // задача встала в input-required
	A2UI       []map[string]any    // готовые к рендеру A2UI-сообщения
	Widgets    []map[string]any    // доменные виджеты воркера (widget/...)
	Files      []attachedFile
}

func NewRemote(cfg config.AgentConfig, trace *Tracer) *Remote
func (r *Remote) ID() string
func (r *Remote) Name() string
func (r *Remote) Verbatim() bool
func (r *Remote) Available() bool
func (r *Remote) Profile() WorkerProfile
func (r *Remote) Connect(ctx context.Context) error
func (r *Remote) Ask(ctx context.Context, sessionID, text string) (Reply, error)
func (r *Remote) PendingTaskID(sessionID string) a2a.TaskID
```

- [ ] **Step 1: Написать падающие тесты**

```go
// ouroborosStub — HTTP-сервер, отвечающий ровно как внешний агент: карточка по
// своему пути, Basic-auth, JSON-RPC на "/", адрес внутри карточки заведомо
// нерабочий (0.0.0.0), состояния в форме TASK_STATE_*.
type ouroborosStub struct {
	URL      string
	Requests []map[string]any // тела принятых JSON-RPC запросов
	workOnce bool             // первый SendMessage вернёт WORKING
	mu       sync.Mutex
}

func startOuroborosStub(t *testing.T, workOnce bool) *ouroborosStub {
	t.Helper()
	s := &ouroborosStub{workOnce: workOnce}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.URL = "http://" + ln.Addr().String()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		u, p, ok := r.BasicAuth()
		if !ok || u != "ouroboros" || p != "testpass" {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/.well-known/agent.json", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = io.WriteString(w, `{"name":"Who I Am","description":"d","version":"1.4.0","protocolVersion":"1.0",
		 "url":"http://0.0.0.0:18800/",
		 "supportedInterfaces":[{"url":"http://0.0.0.0:18800/","protocolBinding":"JSONRPC","protocolVersion":"1.0"}],
		 "capabilities":{},"defaultInputModes":["text/plain"],
		 "defaultOutputModes":["text/plain","application/a2ui+json"],"skills":[]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.Requests = append(s.Requests, req)
		first := s.workOnce && req["method"] == "SendMessage"
		if first {
			s.workOnce = false
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if first {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_WORKING"}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"id":"t1","contextId":"c1",
		 "status":{"state":"TASK_STATE_COMPLETED","message":{"messageId":"m1","role":"ROLE_AGENT",
		   "parts":[{"text":"Заказ доставлен","mediaType":"text/plain"}]}},
		 "artifacts":[{"artifactId":"a1","parts":[{"data":[
		   {"version":"v0.9.1","createSurface":{"surfaceId":"s1","catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},
		   {"version":"v0.9.1","updateComponents":{"surfaceId":"s1","components":[{"id":"t","component":"Text","text":"Order"}]}}
		 ],"mediaType":"application/a2ui+json"}]}]}}`)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return s
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
	// metadata.skill обязан уехать на провод — без него внешний агент не
	// включит инструменты навыка.
	msg := s.Requests[0]["params"].(map[string]any)["message"].(map[string]any)
	meta, _ := msg["metadata"].(map[string]any)
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
	second := s.Requests[1]["params"].(map[string]any)["message"].(map[string]any)
	if second["contextId"] != "c1" {
		t.Errorf("contextId not reused: %v", second["contextId"])
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
	if s.Requests[1]["method"] != "GetTask" {
		t.Errorf("expected GetTask poll, got %v", s.Requests[1]["method"])
	}
}

func TestRemoteFailsWithoutAuth(t *testing.T) {
	s := startOuroborosStub(t, false)
	cfg := ouroborosCfg(s.URL)
	cfg.Auth = config.AuthConfig{}
	r := NewRemote(cfg, nil)
	if _, err := r.Ask(context.Background(), "sess-1", "привет"); err == nil {
		t.Fatal("expected error without basic auth")
	}
	if r.Available() {
		t.Error("agent must be marked unavailable after a failed connect")
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
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test ./internal/a2abridge/ -run TestRemote -v`
Expected: FAIL — `undefined: NewRemote`.

- [ ] **Step 3: Реализовать `remote.go`**

Опорные точки:

```go
// basicAuthTransport подставляет заголовок Basic на каждый запрос. Встроенный
// a2aclient.AuthInterceptor не годится: он умеет только Bearer.
type basicAuthTransport struct {
	base       http.RoundTripper
	user, pass string
}

func (t *basicAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.SetBasicAuth(t.user, t.pass)
	return t.base.RoundTrip(clone)
}
```

`Connect` (под мьютексом, идемпотентный):
1. `agentcard.Resolver{Client: httpClient}.Resolve(ctx, cfg.URL, agentcard.WithPath(cfg.CardPath))`.
2. **Патч адреса**: `for _, iface := range card.SupportedInterfaces { iface.URL = cfg.URL }`; если `SupportedInterfaces` пуст — добавить `a2a.NewAgentInterface(cfg.URL, a2a.TransportProtocolJSONRPC)`. Причина — в карточке живого агента стоит `http://0.0.0.0:18800/`, по которому клиент никуда не попадёт.
3. `a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(httpClient))`.
4. Профиль: если `cfg.Description != ""` → `WorkerProfile{ToolName: "ask_" + toolIdent(cfg.ID), ToolDesc: cfg.Description + " " + needsInputTail, Summary: "Агент по имени «" + r.Name() + "» умеет: " + cfg.Description}`; иначе `ProfileFromCard(card)`.
5. При ошибке — `r.available = false`, вернуть ошибку; следующий вызов пробует снова.

`Ask`:
1. `Connect` (ленивый).
2. `ctx, cancel := context.WithTimeout(ctx, cfg.TimeoutDuration())`.
3. Сообщение: `pending` есть → `a2a.NewMessageForTask`, иначе `a2a.NewMessage`; затем `msg.ContextID = r.contexts[sessionID]` (если есть) и `msg.Metadata = map[string]any{"skill": cfg.Skill}` (если скилл задан).
4. `SendMessage`; на `*a2a.Message` — синхронный ответ; на `*a2a.Task` — при `WORKING`/`SUBMITTED` опрашивать `GetTask` каждые 2 с до истечения ctx.
5. Запомнить `task.ContextID`; при `INPUT_REQUIRED` — сохранить `pending` и `NeedsInput=true`.
6. Собрать `Reply`: текст — `status.Message` при наличии, иначе существующий `taskResultText`; `A2UI` — `a2ui.Ingest` по частям артефакта **и** по частям статусного сообщения; `Widgets` — существующий `firstWidget`; `Files` — части с `Filename`.

`toolIdent` — уже существующий подход: `nonAlnum.ReplaceAllString(id, "_")`.

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/a2abridge/ -v`
Expected: PASS (включая все существующие тесты пакета).

- [ ] **Step 5: Коммит**

```bash
git add internal/a2abridge/remote.go internal/a2abridge/remote_test.go
git commit -m "feat(a2abridge): Remote — соединение с произвольным удалённым A2A-агентом"
```

---

### Task 4: `OrdersClient` переезжает на `Remote`

**Files:**
- Modify: `internal/a2abridge/client.go`
- Test: существующие `internal/a2abridge/client_test.go`, `e2e_test.go`, `widget_e2e_test.go` должны проходить без правок логики

**Interfaces:**
- Consumes: `Remote`, `Reply`.
- Produces: `NewOrdersClientFromRemote(r *Remote) *OrdersClient`; `(*OrdersClient).SetA2UIHandler(fn func(sessionID string, msgs []map[string]any))`. Публичные `Tool()`, `Profile()`, `SetWidgetHandler`, `SetFileHandler`, `ask`, `pendingTaskID` сохраняются.

- [ ] **Step 1: Написать падающий тест на новый проброс A2UI**

```go
func TestOrdersClientForwardsA2UIFromRemote(t *testing.T) {
	s := startOuroborosStub(t, false)
	oc := NewOrdersClientFromRemote(NewRemote(ouroborosCfg(s.URL), nil))
	var got []map[string]any
	oc.SetA2UIHandler(func(_ string, msgs []map[string]any) { got = append(got, msgs...) })
	if _, err := oc.ask(context.Background(), "sess-1", "статус заказа"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("a2ui forwarded: got %d, want 2", len(got))
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/a2abridge/ -run TestOrdersClientForwardsA2UI -v`
Expected: FAIL — `undefined: NewOrdersClientFromRemote`.

- [ ] **Step 3: Переписать `client.go` поверх `Remote`**

- `OrdersClient` хранит `remote *Remote` вместо `client`/`profile`/`pending`; `emptyCalls`, `onWidget`, `onFile` остаются, добавляется `onA2UI`.
- `NewOrdersClient(ctx, workerURL, trace)` сохраняется как обёртка: `NewOrdersClientFromRemote(NewRemote(config.AgentConfig{ID: "orders", URL: workerURL}, trace))` плюс немедленный `Connect` (нынешний контракт — ошибка при недоступном воркере на старте).
- `ask` сжимается до: пустой текст → прежняя подсказка; иначе `remote.Ask`, затем `forwardWidget`/`forwardFiles`/`forwardA2UI` и текст (`"NEEDS_USER_INPUT: " + reply.Text` при `reply.NeedsInput`).
- `Profile()` → `remote.Profile()`; `pendingTaskID` → `remote.PendingTaskID`.

- [ ] **Step 4: Прогнать весь пакет**

Run: `go test ./... `
Expected: PASS — существующие e2e-тесты воркера ловят регрессии рефакторинга.

- [ ] **Step 5: Коммит**

```bash
git add internal/a2abridge/
git commit -m "refactor(a2abridge): OrdersClient поверх Remote + проброс A2UI"
```

---

### Task 5: `Registry`

**Files:**
- Create: `internal/a2abridge/registry.go`
- Test: `internal/a2abridge/registry_test.go`

**Interfaces:**
- Produces:

```go
type AgentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Verbatim    bool   `json:"verbatim"`
	Available   bool   `json:"available"`
}

func NewRegistry(agents []config.AgentConfig, trace *Tracer) *Registry
func (g *Registry) Get(id string) (*Remote, bool)
func (g *Registry) First() *Remote
func (g *Registry) List(ctx context.Context) []AgentInfo
func (g *Registry) Clients() []*OrdersClient      // по одному на агента, порядок конфига
func (g *Registry) ClientFor(id string) (*OrdersClient, bool)
```

- [ ] **Step 1: Написать падающие тесты**

```go
func TestRegistryResolvesToolNameCollisions(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID, a.Description = "one", "Общий агент."
	b := ouroborosCfg(s.URL)
	b.ID, b.Description = "two", "Общий агент."
	g := NewRegistry([]config.AgentConfig{a, b}, nil)
	names := map[string]bool{}
	for _, c := range g.Clients() {
		names[c.Tool().Name()] = true
	}
	if len(names) != 2 {
		t.Fatalf("tool names must be unique: %v", names)
	}
}

func TestRegistryListMarksUnavailable(t *testing.T) {
	cfg := ouroborosCfg("http://127.0.0.1:1") // заведомо закрытый порт
	g := NewRegistry([]config.AgentConfig{cfg}, nil)
	infos := g.List(context.Background())
	if len(infos) != 1 || infos[0].Available {
		t.Fatalf("expected one unavailable agent, got %+v", infos)
	}
	if infos[0].ID != "ouroboros" || infos[0].Name != "Ouroboros" {
		t.Errorf("info: %+v", infos[0])
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test ./internal/a2abridge/ -run TestRegistry -v`
Expected: FAIL — `undefined: NewRegistry`.

- [ ] **Step 3: Реализовать `registry.go`**

- Хранит порядок конфига (для `First()` и стабильного списка в UI).
- `Clients()` создаёт `OrdersClient` лениво, по одному на агента, и кэширует.
- Коллизия имён инструментов разрешается при первом построении: второму и далее назначается `ask_<id>`; в трейс — предупреждение.
- `List` вызывает `Connect` на каждом агенте (быстрый таймаут не нужен: соединение кэшируется), проставляет `Available`.

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/a2abridge/ -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/a2abridge/registry.go internal/a2abridge/registry_test.go
git commit -m "feat(a2abridge): Registry — набор удалённых агентов и их инструментов"
```

---

### Task 6: Маршрутизация в `orchExecutor`

**Files:**
- Modify: `internal/a2abridge/orchserver.go`
- Test: `internal/a2abridge/orchserver_test.go`

**Interfaces:**
- Consumes: `Registry`, `Reply`.
- Produces: `NewMultiAgentExecutor(reg *Registry, build func(oc *OrdersClient) (*runner.Runner, error), trace *Tracer) a2asrv.AgentExecutor`. Существующий `NewOrchestratorExecutor(r, oc, trace)` сохраняется как обёртка над одним агентом.

- [ ] **Step 1: Написать падающие тесты**

```go
// verbatim-агент не должен трогать локальную LLM: стаб настроен так, что любой
// его вызов провалит тест.
func TestExecutorVerbatimBypassesLLM(t *testing.T) {
	s := startOuroborosStub(t, false)
	reg := NewRegistry([]config.AgentConfig{ouroborosCfg(s.URL)}, nil)
	url := startMultiAgentServer(t, reg, failingRunnerBuilder(t))
	probe := newA2UIClient(t, url)
	parts := probe.sendTextWithAgent(t, "статус заказа", "ouroboros")
	if !hasA2UIPart(parts) {
		t.Fatal("expected an A2UI part in the artifact")
	}
	if textOf(parts) != "Заказ доставлен" {
		t.Errorf("verbatim text: got %q", textOf(parts))
	}
}

// Неизвестный agentId не должен ломать диалог — тихий откат в «Авто».
func TestExecutorUnknownAgentFallsBackToAuto(t *testing.T) { /* стаб-LLM отвечает текстом */ }

// В «Авто» модель видит инструменты всех агентов.
func TestExecutorAutoExposesAllTools(t *testing.T) { /* два агента → два инструмента у runner'а */ }
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test ./internal/a2abridge/ -run TestExecutor -v`
Expected: FAIL — `undefined: NewMultiAgentExecutor`.

- [ ] **Step 3: Реализовать маршрутизацию**

- Читать `ec.Message.Metadata["agentId"]` (строка).
- `""`/`"auto"`/неизвестный → runner со всеми инструментами (кэш по ключу `"auto"`; для неизвестного — запись в трейс).
- Агент с `Verbatim()` → `remote.Ask` напрямую; артефакт = текст + `a2uiParts` из `Reply.A2UI` + файлы; LLM не создаётся.
- Иначе → runner с одним инструментом (кэш по `agentId`).
- Существующая ветка прямого resume HITL-кнопок остаётся и работает для агента, который держит pending-задачу.
- Ключ сессии для `Remote` — `ec.ContextID` (как сейчас); поскольку у каждого `Remote` своя карта сессий, разделение по агентам получается само.

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./... `
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/a2abridge/
git commit -m "feat(a2abridge): маршрутизация по metadata.agentId + verbatim-проброс"
```

---

### Task 7: `/api/agents` и проводка в `main.go`

**Files:**
- Create: `internal/webui/agents.go`, `internal/webui/agents_test.go`
- Modify: `cmd/orchestrator/main.go`

**Interfaces:**
- Produces: `webui.AgentsHandler(list func(context.Context) []a2abridge.AgentInfo) http.Handler`.

> `webui` импортирует `a2abridge` только ради типа; чтобы не заводить цикл, `AgentsHandler` принимает `any`-совместимый список через дженерик-функцию `func AgentsHandler[T any](list func(context.Context) []T) http.Handler`.

- [ ] **Step 1: Написать падающий тест**

```go
func TestAgentsHandlerServesJSON(t *testing.T) {
	type info struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	h := AgentsHandler(func(context.Context) []info { return []info{{ID: "orders", Name: "Агент заказов"}} })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `"id":"orders"`) {
		t.Errorf("body: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/webui/ -run TestAgentsHandler -v`
Expected: FAIL — `undefined: AgentsHandler`.

- [ ] **Step 3: Реализовать хендлер и проводку**

`main.go`:
- `reg := a2abridge.NewRegistry(cfg.Agents, trace)`.
- Лог: по одной строке на агента (`id`, `url`, `skill`, `verbatim`) — без пароля.
- `--web`: `mux.Handle("/api/agents", webui.AgentsHandler(reg.List))`, исполнитель — `a2abridge.NewMultiAgentExecutor(reg, buildRunner, trace)`, где `buildRunner` собирает `agent.NewOrchestrator` + `runner.New` для переданного набора инструментов.
- TUI: `reg.ClientFor(cfg.Agents[0].ID)` — поведение как сегодня.
- Недоступный агент больше не валит старт: `log.Printf` с предупреждением вместо `log.Fatalf`.

- [ ] **Step 4: Прогнать тесты и сборку**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/webui/ cmd/orchestrator/
git commit -m "feat(webui): /api/agents и проводка реестра агентов в оркестратор"
```

---

### Task 8: Селектор агента в браузере

**Files:**
- Modify: `web/src/client.ts`, `web/src/app.ts`

- [ ] **Step 1: `client.ts` — передавать выбор агента**

```ts
export class A2UIClient {
  #agentId = 'auto';

  setAgent(id: string) {
    this.#agentId = id || 'auto';
  }
```
В `#send` добавить в message: `...(this.#agentId && this.#agentId !== 'auto' ? {metadata: {agentId: this.#agentId}} : {})`.

- [ ] **Step 2: `app.ts` — загрузка списка и `<select>`**

```ts
interface AgentInfo {
  id: string;
  name: string;
  description: string;
  verbatim: boolean;
  available: boolean;
}

@state() private _agents: AgentInfo[] = [];
@state() private _agentId = localStorage.getItem('a2a.agentId') ?? 'auto';

async #loadAgents() {
  try {
    const res = await fetch('/api/agents');
    this._agents = await res.json();
  } catch (err) {
    console.error('agents list failed:', err);
  }
}
```
`connectedCallback` вызывает `void this.#loadAgents()` и `this.#client.setAgent(this._agentId)`; смена в `<select>` пишет `localStorage` и вызывает `setAgent`.

- [ ] **Step 3: Спиннер с именем агента**

`агент печатает…` → имя выбранного агента (для `auto` — «агент»). При 52-секундном ответе важно понимать, кого ждём.

- [ ] **Step 4: Сборка**

Run: `cd web && yarn build`
Expected: сборка проходит, `internal/webui/dist/index.html` появляется.

- [ ] **Step 5: Коммит**

```bash
git add web/src/
git commit -m "feat(webui): селектор агента в браузере"
```

---

### Task 9: Документация

**Files:**
- Modify: `configs/orchestrator.example.yaml`, `README.md`, `java/README.md`

- [ ] **Step 1: `configs/orchestrator.example.yaml`** — блок `agents:` с обоими агентами и комментариями (включая `A2A_AGENT_<ID>_PASSWORD` и то, что `worker_url` оставлен ради совместимости).

- [ ] **Step 2: `README.md`** — раздел «Несколько агентов»: конфиг, выбор в UI, режимы «Авто»/явный/`verbatim`, таблица env-переменных пополняется `A2A_AGENT_<ID>_PASSWORD`.

- [ ] **Step 3: `java/README.md`** — строка о том, что мультиагентный режим реализован только в Go-порте.

- [ ] **Step 4: Проверка**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add README.md configs/orchestrator.example.yaml java/README.md
git commit -m "docs: мультиагентный конфиг и выбор агента в UI"
```

---

## Self-Review

**Покрытие спеки:** конфиг → Task 1; `Ingest` → Task 2; `Remote` (карточка, auth, подмена URL, skill, поллинг, ленивый резолв) → Task 3; переезд `OrdersClient` → Task 4; `Registry` → Task 5; маршрутизация и verbatim → Task 6; `/api/agents`, проводка, TUI-поведение → Task 7; селектор → Task 8; документация → Task 9.

**Согласованность имён:** `Reply` (Task 3) используется в Task 4 и 6; `AgentInfo` (Task 5) — в Task 7 и 8; `NewOrdersClientFromRemote` (Task 4) — в Task 5; `NewMultiAgentExecutor` (Task 6) — в Task 7.

**Известное расхождение со спекой:** спека изначально требовала `ask_<id>` всегда; правило уточнено (карточка по умолчанию, `ask_<id>` при `description`) и спека обновлена — иначе ломается заявленное свойство демо «имя инструмента выводится из AgentCard».
