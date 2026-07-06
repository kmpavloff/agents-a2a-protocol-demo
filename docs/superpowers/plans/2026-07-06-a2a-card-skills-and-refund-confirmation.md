# Навыки из AgentCard + подтверждение возвратов — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Оркестратор узнаёт возможности воркера из его AgentCard (имя инструмента, описание, доменный блок промпта), а возврат средств требует подтверждения человека через нативный HITL adk, переданный по каналу A2A `input-required`.

**Architecture:** Воркер обогащает единственный навык `manage_orders` полями `Tags`/`Examples`. Чистая функция `ProfileFromCard` превращает резолвнутую карточку в `WorkerProfile{ToolName, ToolDesc, Summary}`, которым питаются `OrdersClient.Tool()` и `NewOrchestrator`. Инструмент `initiate_refund` получает `RequireConfirmationProvider`; executor воркера ловит спец-вызов `adk_request_confirmation`, эмитит `input-required` с вопросом, а на ответ «да» подаёт в runner `FunctionResponse{confirmed:true}` (adk сам до-выполняет возврат), на «нет» — коротко замыкает с сообщением об отмене.

**Tech Stack:** Go, `google.golang.org/adk` v1.4.0 (`tool/functiontool`, `tool/toolconfirmation`), `github.com/a2aproject/a2a-go/v2` (`a2a`, `a2asrv`, `a2aclient`), `google.golang.org/genai`.

## Global Constraints

- Язык всех пользовательских строк и промптов — русский (как в существующем коде).
- Поведенческие правила протокола в промпте оркестратора **сохраняются**; из хардкода уходит только доменная часть (что умеет воркер).
- Имя инструмента должно матчить `^[a-zA-Z_][a-zA-Z0-9_]*$` (требование к именам функций-инструментов adk/genai).
- Подтверждение требуется только для `initiate_refund`; read-only инструменты не трогаем.
- `Tracer.Logf` вызывается на возможном `nil` (тесты передают `nil` трейсер) — новые вызовы трейса тоже должны идти через `c.trace`/`e.trace`, которые nil-безопасны.
- TDD: сначала падающий тест, затем минимальная реализация. Частые коммиты. Запуск тестов: `go test ./...`.

---

### Task 1: Деривация профиля из AgentCard (чистые функции)

**Files:**
- Create: `internal/a2abridge/profile.go`
- Test: `internal/a2abridge/profile_test.go`

**Interfaces:**
- Consumes: `github.com/a2aproject/a2a-go/v2/a2a` (`*a2a.AgentCard`, `a2a.AgentSkill` с полями `Name`, `Description`, `Examples`, `Tags`).
- Produces:
  - `type WorkerProfile struct { ToolName string; ToolDesc string; Summary string }`
  - `func ProfileFromCard(card *a2a.AgentCard) WorkerProfile`
  - `func sanitizeToolName(name string) string`

- [ ] **Step 1: Write the failing test**

```go
// internal/a2abridge/profile_test.go
package a2abridge

import (
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func sampleCard() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "orders-agent",
		Description: "Управляет заказами, статусами, статистикой и возвратами.",
		Skills: []a2a.AgentSkill{{
			ID:          "manage_orders",
			Name:        "Управление заказами",
			Description: "Поиск заказов, статусы, статистика продаж и оформление возвратов.",
			Tags:        []string{"заказы", "возвраты"},
			Examples:    []string{"верни деньги за заказ 1041", "статус заказа 1041"},
		}},
	}
}

func TestSanitizeToolName(t *testing.T) {
	cases := map[string]string{
		"orders-agent": "ask_orders_agent",
		"Orders Agent": "ask_Orders_Agent",
		"":             "ask_agent",
		"---":          "ask_agent",
	}
	for in, want := range cases {
		if got := sanitizeToolName(in); got != want {
			t.Errorf("sanitizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProfileFromCard(t *testing.T) {
	p := ProfileFromCard(sampleCard())
	if p.ToolName != "ask_orders_agent" {
		t.Errorf("ToolName = %q, want ask_orders_agent", p.ToolName)
	}
	for _, want := range []string{"Управление заказами", "верни деньги за заказ 1041", "NEEDS_USER_INPUT"} {
		if !strings.Contains(p.ToolDesc, want) {
			t.Errorf("ToolDesc missing %q; got %q", want, p.ToolDesc)
		}
	}
	for _, want := range []string{"orders-agent", "Управление заказами"} {
		if !strings.Contains(p.Summary, want) {
			t.Errorf("Summary missing %q; got %q", want, p.Summary)
		}
	}
}

func TestProfileFromCardNilSafe(t *testing.T) {
	p := ProfileFromCard(nil)
	if p.ToolName != "ask_agent" {
		t.Errorf("nil card ToolName = %q, want ask_agent", p.ToolName)
	}
	if !strings.Contains(p.ToolDesc, "NEEDS_USER_INPUT") {
		t.Errorf("nil card ToolDesc should still carry the NEEDS_USER_INPUT tail; got %q", p.ToolDesc)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/a2abridge/ -run 'Profile|SanitizeToolName' -v`
Expected: FAIL — `undefined: ProfileFromCard` / `undefined: sanitizeToolName`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/a2abridge/profile.go
package a2abridge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// WorkerProfile is everything the orchestrator learns about the worker from its
// AgentCard: the delegating tool's name and description, plus a human-readable
// capabilities block injected into the orchestrator's system prompt.
type WorkerProfile struct {
	ToolName string
	ToolDesc string
	Summary  string
}

// needsInputTail is appended to every derived tool description so the orchestrator
// LLM knows how to handle a clarification/confirmation bounce.
const needsInputTail = "Если он вернёт NEEDS_USER_INPUT, задайте пользователю этот вопрос, затем снова вызовите инструмент с его ответом."

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// sanitizeToolName turns a card name into a valid function-tool name of the form
// ask_<slug>. Non-alphanumeric runs collapse to "_"; an empty result falls back
// to "ask_agent".
func sanitizeToolName(name string) string {
	slug := strings.Trim(nonAlnum.ReplaceAllString(name, "_"), "_")
	if slug == "" {
		return "ask_agent"
	}
	return "ask_" + slug
}

// quoteExamples wraps each example in guillemets and joins them.
func quoteExamples(ex []string, sep string) string {
	q := make([]string, len(ex))
	for i, e := range ex {
		q[i] = "«" + e + "»"
	}
	return strings.Join(q, sep)
}

// ProfileFromCard derives the delegating tool + prompt block from a worker card.
// A nil card yields safe fallbacks and never panics.
func ProfileFromCard(card *a2a.AgentCard) WorkerProfile {
	var name, desc string
	var skills []a2a.AgentSkill
	if card != nil {
		name, desc, skills = card.Name, card.Description, card.Skills
	}

	// Tool description: agent description + each skill + examples + fixed tail.
	var td strings.Builder
	td.WriteString("Делегировать запрос удалённому агенту.")
	if desc != "" {
		td.WriteString(" " + desc)
	}
	for _, s := range skills {
		fmt.Fprintf(&td, " Навык «%s»: %s", s.Name, s.Description)
		if len(s.Examples) > 0 {
			fmt.Fprintf(&td, " Примеры: %s.", quoteExamples(s.Examples, ", "))
		}
	}
	td.WriteString(" " + needsInputTail)

	// Summary block for the orchestrator prompt.
	agentName := name
	if agentName == "" {
		agentName = "агент"
	}
	var sm strings.Builder
	fmt.Fprintf(&sm, "Агент по имени «%s» умеет:", agentName)
	for _, s := range skills {
		fmt.Fprintf(&sm, "\n- %s — %s", s.Name, s.Description)
		if len(s.Examples) > 0 {
			fmt.Fprintf(&sm, " (примеры: %s)", quoteExamples(s.Examples, "; "))
		}
	}

	return WorkerProfile{
		ToolName: sanitizeToolName(name),
		ToolDesc: td.String(),
		Summary:  sm.String(),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/a2abridge/ -run 'Profile|SanitizeToolName' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/a2abridge/profile.go internal/a2abridge/profile_test.go
git commit -m "feat(a2a): derive worker tool profile from AgentCard"
```

---

### Task 2: Обогатить навык воркера в AgentCard

**Files:**
- Modify: `internal/a2abridge/server.go:147-163` (function `AgentCard`)
- Test: `internal/a2abridge/profile_test.go` (add one test using the real card)

**Interfaces:**
- Consumes: `sampleCard`/`ProfileFromCard` semantics from Task 1; real `AgentCard(publicURL string) *a2a.AgentCard`.
- Produces: enriched `manage_orders` skill with `Tags` and `Examples`.

- [ ] **Step 1: Write the failing test**

```go
// append to internal/a2abridge/profile_test.go
func TestRealCardProfileMentionsExamples(t *testing.T) {
	p := ProfileFromCard(AgentCard("http://localhost:8081"))
	if !strings.Contains(p.ToolDesc, "1041") {
		t.Errorf("real-card ToolDesc should include an example mentioning an order id; got %q", p.ToolDesc)
	}
	if p.ToolName != "ask_orders_agent" {
		t.Errorf("real-card ToolName = %q, want ask_orders_agent", p.ToolName)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/a2abridge/ -run TestRealCardProfileMentionsExamples -v`
Expected: FAIL — current card skill has no `Examples`, so `ToolDesc` lacks "1041".

- [ ] **Step 3: Write minimal implementation**

Replace the `Skills` literal in `AgentCard` (`internal/a2abridge/server.go`):

```go
		Skills: []a2a.AgentSkill{{
			ID:          "manage_orders",
			Name:        "Управление заказами",
			Description: "Поиск заказов по номеру или товару, статусы, статистика продаж за период и оформление возвратов.",
			Tags:        []string{"заказы", "статусы", "статистика продаж", "возвраты", "поиск"},
			Examples: []string{
				"верни деньги за заказ 1041",
				"статус заказа 1041",
				"последние заказы alice",
				"статистика продаж за 2026-06",
			},
		}},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/a2abridge/ -run 'Profile|Card' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/a2abridge/server.go internal/a2abridge/profile_test.go
git commit -m "feat(a2a): enrich orders-agent skill with tags and examples"
```

---

### Task 3: Инструмент `ask_*` строится из профиля

**Files:**
- Modify: `internal/a2abridge/client.go` (struct `OrdersClient`, `NewOrdersClient`, `Tool`; add `Profile`)
- Test: `internal/a2abridge/client_test.go` (add `TestToolNameFromCard`)

**Interfaces:**
- Consumes: `ProfileFromCard` (Task 1); `startWorker` helper (existing, `client_test.go`).
- Produces:
  - field `profile WorkerProfile` on `OrdersClient`
  - `func (c *OrdersClient) Profile() WorkerProfile`
  - `Tool()` uses `c.profile.ToolName` / `c.profile.ToolDesc`.

- [ ] **Step 1: Write the failing test**

```go
// append to internal/a2abridge/client_test.go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/a2abridge/ -run TestToolNameFromCard -v`
Expected: FAIL — `c.Profile` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/a2abridge/client.go`:

Add a field to `OrdersClient`:

```go
type OrdersClient struct {
	client  *a2aclient.Client
	trace   *Tracer
	profile WorkerProfile
	mu      sync.Mutex
	pending map[string]pending // keyed by orchestrator session id
}
```

In `NewOrdersClient`, after the card is resolved and before returning, build the profile:

```go
	profile := ProfileFromCard(card)
	trace.Logf("derived delegating tool %q from card", profile.ToolName)
	return &OrdersClient{
		client:  cl,
		trace:   trace,
		profile: profile,
		pending: make(map[string]pending),
	}, nil
```

Add the getter (near `pendingTaskID`):

```go
// Profile returns the worker profile derived from the resolved AgentCard.
func (c *OrdersClient) Profile() WorkerProfile { return c.profile }
```

Change `Tool()` to use the profile (replace the hardcoded `Name`/`Description`):

```go
func (c *OrdersClient) Tool() tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name:        c.profile.ToolName,
		Description: c.profile.ToolDesc,
	}, func(tc tool.Context, a askArgs) (string, error) {
		return c.ask(tc, tc.SessionID(), a.Message)
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create %s tool: %v", c.profile.ToolName, err))
	}
	return t
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/a2abridge/ -v`
Expected: PASS (all existing a2abridge tests still green — they call `ask` directly and are unaffected).

- [ ] **Step 5: Commit**

```bash
git add internal/a2abridge/client.go internal/a2abridge/client_test.go
git commit -m "feat(a2a): build delegating tool from derived worker profile"
```

---

### Task 4: Инструкция оркестратора из карточки

**Files:**
- Modify: `internal/agent/orchestrator.go` (const → template, `NewOrchestrator` signature)
- Modify: `cmd/orchestrator/main.go:42` (pass `oc.Profile().Summary`)
- Test: `internal/agent/orchestrator_test.go` (new)

**Interfaces:**
- Consumes: `oc.Profile().Summary` (Task 3); `ordersTool.Name()`.
- Produces: `func NewOrchestrator(model adkmodel.LLM, ordersTool tool.Tool, workerSummary string) (agent.Agent, error)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agent/orchestrator_test.go
package agent

import (
	"strings"
	"testing"
)

func TestBuildOrchestratorInstruction(t *testing.T) {
	got := buildOrchestratorInstruction("ask_orders_agent", "Агент по имени «orders-agent» умеет: возвраты")
	if !strings.Contains(got, "ask_orders_agent") {
		t.Errorf("instruction must reference the derived tool name; got %q", got)
	}
	if !strings.Contains(got, "Агент по имени «orders-agent» умеет") {
		t.Errorf("instruction must embed the worker summary; got %q", got)
	}
	if strings.Contains(got, "статистику продаж или возвраты вызывайте") {
		t.Errorf("instruction must not hardcode the orders domain routing anymore")
	}
	if !strings.Contains(got, "NEEDS_USER_INPUT") {
		t.Errorf("instruction must keep the NEEDS_USER_INPUT behavioral rule; got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestBuildOrchestratorInstruction -v`
Expected: FAIL — `undefined: buildOrchestratorInstruction`.

- [ ] **Step 3: Write minimal implementation**

Replace the const + `NewOrchestrator` in `internal/agent/orchestrator.go` with:

```go
import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// orchestratorInstructionTmpl is a domain-neutral prompt: %[1]s is the delegating
// tool name, %[2]s is the worker capabilities block derived from the AgentCard.
const orchestratorInstructionTmpl = `Вы — оркестратор клиентской поддержки. Пользователь общается только с вами. Всю предметную работу вы выполняете, делегируя её инструменту %[1]s.

%[2]s

Правила вызова %[1]s:
- По любому запросу, относящемуся к тому, что умеет агент (см. выше), вызывайте %[1]s.
- В поле "message" передавайте полный, самодостаточный запрос. ВСЕГДА дословно копируйте все конкретные детали пользователя — номера, названия, периоды и точное действие. Пример: «верни деньги за заказ 1041» → message «Оформить возврат по заказу 1041», а не расплывчатое «оформить возврат».
- Передавайте только те детали, которые дал пользователь. Ничего не выдумывайте от себя.
- НИКОГДА не вызывайте %[1]s с пустым полем "message". Формулируйте осмысленный запрос за один вызов; не делайте пустых или пробных вызовов.
- Если %[1]s вернул подсказку о недостающих данных, немедленно вызовите его снова, скопировав исходный запрос пользователя в message.
- Если %[1]s вернул строку, начинающуюся с "NEEDS_USER_INPUT:", задайте пользователю ровно этот вопрос. Когда он ответит, снова вызовите %[1]s, передав его ответ.
- Результат инструмента уже отражает то, что реально произошло — сообщайте его напрямую. Никогда не говорите, что «сейчас сделаете» или «подождите минуту».

Отвечайте коротко и дружелюбно на русском языке.`

// buildOrchestratorInstruction renders the prompt for a given tool name and
// worker capabilities summary.
func buildOrchestratorInstruction(toolName, workerSummary string) string {
	return fmt.Sprintf(orchestratorInstructionTmpl, toolName, workerSummary)
}

// NewOrchestrator creates an adk LlmAgent that delegates domain work to the
// worker agent via the derived delegating tool. workerSummary is the capability
// block derived from the worker's AgentCard.
func NewOrchestrator(model adkmodel.LLM, ordersTool tool.Tool, workerSummary string) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "orchestrator",
		Description: "Общается с пользователем и делегирует предметную работу удалённому агенту.",
		Model:       model,
		Instruction: buildOrchestratorInstruction(ordersTool.Name(), workerSummary),
		Tools:       []tool.Tool{ordersTool},
	})
}
```

In `cmd/orchestrator/main.go`, update the call (line 42):

```go
	ag, err := agent.NewOrchestrator(model, ordersTool, oc.Profile().Summary)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/ -v && go build ./...`
Expected: PASS and clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/orchestrator.go internal/agent/orchestrator_test.go cmd/orchestrator/main.go
git commit -m "feat(orchestrator): derive instruction from worker AgentCard"
```

---

### Task 5: Возврат помечается требующим подтверждения

**Files:**
- Modify: `internal/orders/tools.go` (function `Tools`, add `refundNeedsConfirmation`)
- Test: `internal/orders/tools_test.go` (add `TestRefundNeedsConfirmation`)

**Interfaces:**
- Consumes: existing `idArgs.orderID()`.
- Produces: `func refundNeedsConfirmation(a idArgs) bool`; `initiate_refund` tool config carries `RequireConfirmationProvider: refundNeedsConfirmation`.

- [ ] **Step 1: Write the failing test**

```go
// append to internal/orders/tools_test.go
func TestRefundNeedsConfirmation(t *testing.T) {
	if !refundNeedsConfirmation(idArgs{OrderID: "1041"}) {
		t.Error("refund with a concrete order id must require confirmation")
	}
	if refundNeedsConfirmation(idArgs{}) {
		t.Error("empty/probing refund call must NOT require confirmation")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orders/ -run TestRefundNeedsConfirmation -v`
Expected: FAIL — `undefined: refundNeedsConfirmation`.

- [ ] **Step 3: Write minimal implementation**

In `internal/orders/tools.go`, add the predicate (near `Tools`):

```go
// refundNeedsConfirmation gates the HITL confirmation for initiate_refund: only
// a call that carries a concrete order id triggers a confirmation request, so a
// model's empty probing call does not spam the user with confirmations.
func refundNeedsConfirmation(a idArgs) bool { return a.orderID() != "" }
```

Update the `initiate_refund` entry inside `Tools`:

```go
		mustTool(functiontool.New(functiontool.Config{
			Name:                        "initiate_refund",
			Description:                 "Оформить возврат по заказу (по его номеру).",
			RequireConfirmationProvider: refundNeedsConfirmation,
		},
			func(_ tool.Context, a idArgs) (string, error) { return initiateRefund(s, a.orderID()) })),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/orders/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orders/tools.go internal/orders/tools_test.go
git commit -m "feat(orders): require human confirmation for refunds"
```

---

### Task 6: Executor воркера мостит подтверждение в A2A input-required

**Files:**
- Modify: `internal/a2abridge/server.go` (struct `executor`, `NewExecutor`, `Execute`; add helpers `parseAffirmative`, `refundConfirmQuestion`)
- Test: `internal/a2abridge/server_test.go` (add `TestParseAffirmative`, `TestExecutorRequestsRefundConfirmation`, and a status-message capture helper)

**Interfaces:**
- Consumes: `toolconfirmation.FunctionCallName`, `toolconfirmation.OriginalCallFrom` (`google.golang.org/adk/tool/toolconfirmation`); `genai.FunctionResponse`, `genai.Content`; `orders.Tools` (via `newTestRunner`).
- Produces: `func parseAffirmative(text string) bool`; `func refundConfirmQuestion(orig *genai.FunctionCall) string`; executor field `pendingConfirm map[string]string` keyed by A2A contextID.

- [ ] **Step 1: Write the failing tests**

```go
// append to internal/a2abridge/server_test.go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/a2abridge/ -run 'ParseAffirmative|RequestsRefundConfirmation' -v`
Expected: FAIL — `undefined: parseAffirmative`; executor emits `completed`/error instead of `input-required`.

- [ ] **Step 3: Write minimal implementation**

In `internal/a2abridge/server.go`:

Add imports `sync` and `toolconfirmation`:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"
)
```

Extend the `executor` struct and constructor:

```go
type executor struct {
	runner *runner.Runner
	trace  *Tracer

	mu             sync.Mutex
	pendingConfirm map[string]string // A2A contextID → adk_request_confirmation call ID
}

// NewExecutor wraps an adk Runner as an a2asrv.AgentExecutor.
// trace may be nil to disable protocol tracing.
func NewExecutor(r *runner.Runner, trace *Tracer) a2asrv.AgentExecutor {
	return &executor{runner: r, trace: trace, pendingConfirm: make(map[string]string)}
}
```

Add helpers at the bottom of the file:

```go
// parseAffirmative interprets a free-text user reply to a yes/no confirmation.
// Anything not clearly affirmative is treated as a refusal (safe default: no
// money moves without an explicit "yes").
func parseAffirmative(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	switch {
	case t == "да" || t == "yes" || t == "y" || t == "ок" || t == "ok" || t == "ага" || t == "угу":
		return true
	case strings.HasPrefix(t, "да,") || strings.HasPrefix(t, "да ") || strings.HasPrefix(t, "yes"):
		return true
	case strings.Contains(t, "подтверж") || strings.Contains(t, "оформляй") || strings.Contains(t, "давай, оформ"):
		return true
	}
	return false
}

// refundConfirmQuestion builds the Russian confirmation prompt from the original
// initiate_refund call captured inside the adk_request_confirmation event.
func refundConfirmQuestion(orig *genai.FunctionCall) string {
	id := ""
	if orig != nil && orig.Args != nil {
		for _, k := range []string{"order_id", "order_number", "number", "id"} {
			if v, ok := orig.Args[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					id = strings.TrimSpace(s)
					break
				}
			}
		}
	}
	if id != "" {
		return fmt.Sprintf("Подтвердите оформление возврата по заказу %s? (да/нет)", id)
	}
	return "Подтвердите оформление возврата? (да/нет)"
}
```

Replace the whole `Execute` method with the confirmation-aware version:

```go
// Execute implements a2asrv.AgentExecutor.
func (e *executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		newTask := ec.StoredTask == nil
		sessionID := ec.ContextID // = A2A contextID for history continuity across turns
		e.trace.Logf("▶ incoming A2A request | contextID=%s newTask=%v", ec.ContextID, newTask)

		// 1. Emit a submitted task if this is a new task.
		if newTask {
			e.trace.Logf("  → emit: submitted task")
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		} else {
			e.trace.Logf("  resuming stored task | id=%s", ec.StoredTask.ID)
		}

		// 2. Emit working status.
		e.trace.Logf("  → emit: working")
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// 3. Build the runner input. If a confirmation is pending for this session,
		//    this turn carries the user's yes/no answer, not a fresh request.
		var userText string
		if ec.Message != nil && len(ec.Message.Parts) > 0 {
			userText = ec.Message.Parts[0].Text()
		}

		e.mu.Lock()
		confirmCallID, awaitingConfirm := e.pendingConfirm[sessionID]
		if awaitingConfirm {
			delete(e.pendingConfirm, sessionID)
		}
		e.mu.Unlock()

		var msg *genai.Content
		if awaitingConfirm {
			approved := parseAffirmative(userText)
			e.trace.Logf("  confirmation answer=%q → approved=%v", userText, approved)
			if !approved {
				// Short-circuit: never feed a rejection to adk (avoids the
				// ErrConfirmationRejected tool-error path); tell the user directly.
				e.trace.Logf("  → emit: artifact + completed | refund declined by user")
				if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart("Возврат отменён по вашему решению.")), nil) {
					return
				}
				yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
				return
			}
			fr := &genai.FunctionResponse{
				Name:     toolconfirmation.FunctionCallName,
				ID:       confirmCallID,
				Response: map[string]any{"confirmed": true},
			}
			msg = &genai.Content{Role: string(genai.RoleUser), Parts: []*genai.Part{{FunctionResponse: fr}}}
		} else {
			e.trace.Logf("  user text=%q — running orders agent (LLM + tools)", userText)
			msg = genai.NewContentFromText(userText, genai.RoleUser)
		}

		// 4. Run the adk runner, watching for a HITL confirmation request.
		var finalText, confirmQuestion, capturedCallID string
		e.trace.Logf("%s  · агент → LLM: запрос%s", gray, reset)
		for event, err := range e.runner.Run(ctx, "a2a-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				e.trace.Logf("  ✖ runner error: %v", err)
				yield(nil, err)
				return
			}
			if event == nil || event.Content == nil {
				continue
			}
			for _, p := range event.Content.Parts {
				switch {
				case p.FunctionCall != nil && p.FunctionCall.Name == toolconfirmation.FunctionCallName:
					// adk asks for human approval before running a guarded tool.
					capturedCallID = p.FunctionCall.ID
					orig, oerr := toolconfirmation.OriginalCallFrom(p.FunctionCall)
					if oerr != nil {
						e.trace.Logf("  ✖ OriginalCallFrom: %v", oerr)
						confirmQuestion = "Подтвердите выполнение действия? (да/нет)"
					} else {
						confirmQuestion = refundConfirmQuestion(orig)
					}
					e.trace.Logf("  ⏸ tool confirmation requested | callID=%s question=%q", capturedCallID, confirmQuestion)
				case p.FunctionCall != nil:
					e.trace.Logf("%s  · LLM → агент: вызвать %s(%s)%s",
						gray, p.FunctionCall.Name, compactArgs(p.FunctionCall.Args), reset)
				case p.FunctionResponse != nil:
					e.trace.Logf("%s  · инструмент %s → LLM: результат, снова спрашиваю LLM%s",
						gray, p.FunctionResponse.Name, reset)
				case p.Text != "":
					finalText = p.Text
				}
			}
		}

		// 5. Confirmation requested → pause the task as input-required.
		if capturedCallID != "" {
			e.mu.Lock()
			e.pendingConfirm[sessionID] = capturedCallID
			e.mu.Unlock()
			e.trace.Logf("  → emit: input-required (confirmation) | question=%q", confirmQuestion)
			ask := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(confirmQuestion))
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, ask), nil)
			return
		}

		// 6. Check for the NEED_INPUT clarification sentinel.
		trimmed := strings.TrimSpace(finalText)
		e.trace.Logf("  agent produced finalText=%q", trimmed)
		if strings.HasPrefix(trimmed, needInputPrefix) {
			question := strings.TrimSpace(strings.TrimPrefix(trimmed, needInputPrefix))
			e.trace.Logf("  → emit: input-required | question=%q", question)
			ask := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(question))
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, ask), nil)
			return
		}

		// 7. Emit artifact + completed. Guard against an empty finalText.
		artifactText := trimmed
		if artifactText == "" {
			artifactText = "Готово."
		}
		e.trace.Logf("  → emit: artifact + completed | artifact=%q", artifactText)
		if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(artifactText)), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
}
```

Note: `encoding/json` remains used by `compactArgs`; keep the import.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/a2abridge/ -v`
Expected: PASS (new confirmation tests + all existing executor/client/e2e tests).

- [ ] **Step 5: Commit**

```bash
git add internal/a2abridge/server.go internal/a2abridge/server_test.go
git commit -m "feat(a2a): bridge adk tool confirmation to A2A input-required"
```

---

### Task 7: E2E подтверждение возврата + документация

**Files:**
- Modify: `internal/a2abridge/client_test.go` (add `startWorkerWithTools` helper)
- Test: `internal/a2abridge/e2e_test.go` (add `TestEndToEndRefundConfirmed`, `TestEndToEndRefundDeclined`)
- Modify: `README.md` (document card-driven discovery + refund confirmation)

**Interfaces:**
- Consumes: `NewOrdersClient`, `oc.ask`, `orders.Load`/`orders.Tools`, `startWorkerWithTools`.
- Produces: `func startWorkerWithTools(t *testing.T, model *llm.Stub, store *orders.Store) string`.

- [ ] **Step 1: Write the failing tests**

Add the helper to `internal/a2abridge/client_test.go` (import `"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"`):

```go
// startWorkerWithTools is like startWorker but registers the real order tools
// bound to store, so tool side effects (e.g. refunds) are observable in tests.
func startWorkerWithTools(t *testing.T, model *llm.Stub, store *orders.Store) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()

	ag, agErr := agent.NewWorker(model, orders.Tools(store))
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
```

Add the e2e tests to `internal/a2abridge/e2e_test.go` (imports: `context`, `os`, `strings`, `testing`, `google.golang.org/genai`, `internal/llm`, `internal/orders`):

```go
// e2eStore seeds a refundable order 1041 for the confirmation flow.
func e2eStore(t *testing.T) *orders.Store {
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

func TestEndToEndRefundConfirmed(t *testing.T) {
	store := e2eStore(t)
	// Turn 1: LLM calls initiate_refund → adk requests confirmation.
	// Turn 2 (after "да"): adk executes the refund → LLM summarizes.
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
		llm.StubTurn{Text: "Возврат по заказу 1041 оформлен."},
	)
	url := startWorkerWithTools(t, model, store)

	oc, err := NewOrdersClient(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := "conf-yes"

	r1, err := oc.ask(context.Background(), sess, "оформи возврат по заказу 1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r1, "NEEDS_USER_INPUT:") || !strings.Contains(r1, "1041") {
		t.Fatalf("turn 1 should ask to confirm order 1041, got %q", r1)
	}

	r2, err := oc.ask(context.Background(), sess, "да")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r2, "оформлен") {
		t.Fatalf("turn 2 should complete the refund, got %q", r2)
	}
	if o, _ := store.Get("1041"); o.Status != "refunded" {
		t.Errorf("store should show order 1041 refunded, got status %q", o.Status)
	}
}

func TestEndToEndRefundDeclined(t *testing.T) {
	store := e2eStore(t)
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
	)
	url := startWorkerWithTools(t, model, store)

	oc, err := NewOrdersClient(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := "conf-no"

	if _, err := oc.ask(context.Background(), sess, "оформи возврат по заказу 1041"); err != nil {
		t.Fatal(err)
	}
	r2, err := oc.ask(context.Background(), sess, "нет, не надо")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(r2), "отмен") {
		t.Fatalf("declining should report the refund was cancelled, got %q", r2)
	}
	if o, _ := store.Get("1041"); o.Status == "refunded" {
		t.Error("refund must NOT have executed after the user declined")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/a2abridge/ -run 'EndToEndRefund' -v`
Expected: FAIL first with a compile error (`undefined: startWorkerWithTools`) until Step 1's helper is added; then the two new tests should already pass **if** Tasks 5–6 are complete. If Tasks 5–6 are not yet merged, they FAIL on the assertions. (Run this task after Tasks 5–6.)

- [ ] **Step 3: Update the README**

In `README.md`, adjust the two areas:

- Where it says the orchestrator has "one custom tool `ask_orders_agent`", note the tool name and its description are **derived from the worker's AgentCard** at startup (A2A capability discovery), not hardcoded.
- Add a short bullet to the worker/flow description: refunds are guarded by a Human-in-the-Loop confirmation — `initiate_refund` pauses the A2A task in `input-required`, the orchestrator relays the yes/no question to the user, and the refund executes only on an explicit "да".

Keep edits minimal and factual; do not rename existing sections.

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS, no vet complaints.

- [ ] **Step 5: Commit**

```bash
git add internal/a2abridge/client_test.go internal/a2abridge/e2e_test.go README.md
git commit -m "test(a2a): e2e refund confirmation; docs: card discovery + HITL"
```

---

## Self-Review

**Spec coverage:**
- Компонент 1 §1.1 (обогащение карточки) → Task 2. §1.2 (profile.go) → Task 1. §1.3 (проводка: OrdersClient.Profile/Tool, NewOrchestrator, main) → Tasks 3–4. ✓
- Компонент 2 §2.2 (провайдер подтверждения на refund) → Task 5. §2.3 (executor: обнаружение, pending, возобновление, парсинг да/нет) → Task 6. §2.4 (клиент без изменений) → соблюдено (Tasks 6–7 не трогают `ask`/pending клиента). ✓
- План тестирования спеки: profile_test → Tasks 1–2; tools_test → Task 5; server_test (обнаружение + input-required) → Task 6; e2e (да/нет) → Task 7; обновление тестов под derived tool name → Task 3 (существующие a2abridge-тесты используют `ask` напрямую, поэтому имя инструмента их не ломает; добавлен `TestToolNameFromCard`). ✓
- Обработка ошибок из спеки: пустой refund-вызов (провайдер=false) → Task 5; `OriginalCallFrom` ошибка → Task 6 (fallback-вопрос); неоднозначный ответ = отказ → Task 6 (`parseAffirmative`); пустой `card.Name`/нет навыков → Task 1 (`TestProfileFromCardNilSafe`). ✓
- README (discovery + confirmation) → Task 7. ✓

**Placeholder scan:** нет TBD/«обработать ошибки»/«аналогично Task N» — весь код приведён дословно.

**Type consistency:** `WorkerProfile{ToolName,ToolDesc,Summary}` одинаково в Tasks 1/3/4. `ProfileFromCard`, `sanitizeToolName`, `refundNeedsConfirmation`, `parseAffirmative`, `refundConfirmQuestion`, `buildOrchestratorInstruction`, `NewOrchestrator(model, tool, summary)` — сигнатуры совпадают между определением и использованием. Ключ `pendingConfirm` — A2A contextID и там, и там. `toolconfirmation.FunctionCallName`/`OriginalCallFrom` используются согласно API adk v1.4.0.

**Порядок исполнения:** Task 7 запускать после Tasks 5–6 (его e2e-ассерты зависят от них). Остальные — по порядку.
