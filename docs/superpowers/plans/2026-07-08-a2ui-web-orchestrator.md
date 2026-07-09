# A2UI Web UI for Orchestrator — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render the worker's widgets in a browser via Google's A2UI generative-UI protocol, with the orchestrator acting as an A2A server the browser connects to, and interactive buttons that resume A2A tasks.

**Architecture:** browser (Vite+Lit, official `@a2ui/lit` renderer + `@a2a-js/sdk`) ─A2A→ orchestrator (Go a2asrv server + LLM + A2A client) ─A2A→ worker (unchanged). The orchestrator is an A2UI gateway: it maps the worker's domain widgets (existing `DataPart` format) into A2UI v0.9 JSON and translates incoming button `action`s into task resumes. Go serves both the static frontend and `/invoke` on one origin (no CORS).

**Tech Stack:** Go (adk-go v1.4.0, a2a-go v2.3.1), TypeScript + Vite + Lit, `@a2ui/lit`/`@a2ui/web_core`/`@a2a-js/sdk`, LM Studio (OpenAI-compat LLM).

## Global Constraints

- Go module: `github.com/kmpavloff/agents-a2a-protocol-demo`; deps `google.golang.org/adk@v1.4.0`, `github.com/a2aproject/a2a-go/v2@v2.3.1`.
- A2UI protocol version string: `v0.9`. Extension URI: `https://a2ui.org/a2a-extension/a2ui/v0.9`. A2UI DataPart MIME: `application/a2ui+json`. Basic catalog id: `https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json`.
- Frontend packages: `@a2ui/lit` (^0.10), `@a2ui/web_core`, `@a2a-js/sdk`, `lit`. Import versioned paths `@a2ui/lit/v0_9`, `@a2ui/web_core/v0_9`.
- Vite `build.outDir` = `../internal/webui/dist` (go:embed cannot reference `..`, so the build outputs into the embed dir).
- The worker (`internal/orders`, `internal/a2abridge/server.go` widget emission) is NOT changed by this plan.
- All Russian user-facing copy stays Russian.
- Commit after every task. Run `go build ./... && go test ./...` green before each Go commit.

---

### Task 1: `internal/a2ui` — message types, constants, component builders, `FromWidget`

**Files:**
- Create: `internal/a2ui/a2ui.go`
- Test: `internal/a2ui/a2ui_test.go`

**Interfaces:**
- Produces:
  - `const ExtensionURI = "https://a2ui.org/a2a-extension/a2ui/v0.9"`
  - `const MIMEType = "application/a2ui+json"`
  - `const Version = "v0.9"`
  - `const CatalogID = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"`
  - `func FromWidget(w map[string]any) ([]map[string]any, bool)` — returns the ordered A2UI messages (`createSurface` then `updateComponents`) for a widget map whose kind is under `w["_kind"]`; `false` if the kind is unknown.

- [ ] **Step 1: Write the failing test**

```go
package a2ui

import "testing"

func TestFromWidgetConfirmation(t *testing.T) {
	msgs, ok := FromWidget(map[string]any{
		"_kind":    "widget/confirmation",
		"title":    "Подтверждение возврата",
		"message":  "Оформить возврат по заказу 1055?",
		"order_id": "1055",
		"actions": []any{
			map[string]any{"id": "approve", "label": "Оформить возврат"},
			map[string]any{"id": "decline", "label": "Отмена"},
		},
	})
	if !ok {
		t.Fatal("confirmation widget should map")
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages (createSurface, updateComponents), got %d", len(msgs))
	}
	if msgs[0]["version"] != "v0.9" || msgs[0]["createSurface"] == nil {
		t.Errorf("msg0 must be a v0.9 createSurface, got %#v", msgs[0])
	}
	uc, _ := msgs[1]["updateComponents"].(map[string]any)
	if uc == nil {
		t.Fatalf("msg1 must be updateComponents, got %#v", msgs[1])
	}
	comps, _ := uc["components"].([]map[string]any)
	// Expect at least: root, message text, two buttons, two button labels.
	var buttons, actions int
	for _, c := range comps {
		if c["component"] == "Button" {
			buttons++
			if a, ok := c["action"].(map[string]any); ok {
				if _, ok := a["event"].(map[string]any); ok {
					actions++
				}
			}
		}
	}
	if buttons != 2 || actions != 2 {
		t.Errorf("want 2 buttons each with an action.event, got buttons=%d actions=%d", buttons, actions)
	}
}

func TestFromWidgetUnknownKind(t *testing.T) {
	if _, ok := FromWidget(map[string]any{"_kind": "widget/nope"}); ok {
		t.Error("unknown kind must return ok=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/a2ui/ -run TestFromWidget -v`
Expected: FAIL — package/function does not exist.

- [ ] **Step 3: Write the implementation**

```go
// Package a2ui maps the demo's domain widgets to Google's A2UI v0.9 generative-UI
// JSON and parses A2UI action events. It is the only place that knows the A2UI
// wire format; the domain (orders) and transport (a2abridge) packages stay
// A2UI-agnostic.
package a2ui

import "fmt"

const (
	ExtensionURI = "https://a2ui.org/a2a-extension/a2ui/v0.9"
	MIMEType     = "application/a2ui+json"
	Version      = "v0.9"
	CatalogID    = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
)

// surfaceCounter makes surface ids unique within a process without needing a
// random source (unavailable in some sandboxes). It is not concurrency-critical:
// ids only need to be distinct per emitted widget, and the executor emits from a
// single goroutine per request.
var surfaceCounter int

func nextSurfaceID(kind string) string {
	surfaceCounter++
	return fmt.Sprintf("%s-%d", kind, surfaceCounter)
}

// text builds a Text component.
func text(id, s, variant string) map[string]any {
	return map[string]any{"id": id, "component": "Text", "text": s, "variant": variant}
}

// button builds a Button whose child is a Text label and whose click emits an
// A2UI action event {name, context}.
func button(id, labelID, label, variant, actionName string, ctx map[string]any) []map[string]any {
	return []map[string]any{
		{
			"id": id, "component": "Button", "child": labelID, "variant": variant,
			"action": map[string]any{"event": map[string]any{"name": actionName, "context": ctx}},
		},
		text(labelID, label, "body"),
	}
}

// surface wraps components into the standard createSurface + updateComponents pair.
func surface(surfaceID string, components []map[string]any) []map[string]any {
	return []map[string]any{
		{"version": Version, "createSurface": map[string]any{"surfaceId": surfaceID, "catalogId": CatalogID}},
		{"version": Version, "updateComponents": map[string]any{"surfaceId": surfaceID, "components": components}},
	}
}

// FromWidget converts a widget map (keyed by "_kind" plus payload) into the
// ordered A2UI messages to emit. Returns ok=false for an unknown kind.
func FromWidget(w map[string]any) ([]map[string]any, bool) {
	kind, _ := w["_kind"].(string)
	title, _ := w["title"].(string)
	switch kind {
	case "widget/confirmation":
		sid := nextSurfaceID("confirmation")
		msg, _ := w["message"].(string)
		orderID, _ := w["order_id"].(string)
		ctx := map[string]any{"order_id": orderID}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": []any{"title", "msg", "actions"}},
			text("title", title, "h3"),
			text("msg", msg, "body"),
			{"id": "actions", "component": "Row", "children": []any{"approve", "decline"}},
		}
		comps = append(comps, button("approve", "approve_lbl", "Оформить возврат", "primary", "approve_refund", ctx)...)
		comps = append(comps, button("decline", "decline_lbl", "Отмена", "secondary", "decline_refund", ctx)...)
		return surface(sid, comps), true

	case "widget/order":
		sid := nextSurfaceID("order")
		o, _ := w["order"].(map[string]any)
		children := []any{"title"}
		comps := []map[string]any{
			{"id": "root", "component": "Card", "child": "col"},
			{"id": "col", "component": "Column", "children": children},
			text("title", title, "h3"),
		}
		add := func(id, label string, key string) {
			if v, ok := o[key]; ok && v != nil && v != "" {
				children = append(children, id)
				comps = append(comps, text(id, fmt.Sprintf("%s %v", label, v), "body"))
			}
		}
		add("item", "Товар:", "item")
		add("status", "Статус:", "status_label")
		if amt, ok := o["amount"]; ok {
			children = append(children, "amount")
			comps = append(comps, text("amount", fmt.Sprintf("Сумма: %v %v", amt, o["currency"]), "body"))
		}
		add("customer", "Клиент:", "customer")
		add("created", "Дата:", "created")
		comps[1]["children"] = children // refresh Column children after appends
		return surface(sid, comps), true

	case "widget/order_list":
		sid := nextSurfaceID("order_list")
		rows, _ := w["orders"].([]any)
		children := []any{"title"}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": children},
			text("title", title, "h3"),
		}
		for i, r := range rows {
			o, ok := r.(map[string]any)
			if !ok {
				continue
			}
			id := fmt.Sprintf("row%d", i)
			children = append(children, id)
			line := fmt.Sprintf("#%v  %v — %v (%v %v, %v)",
				o["id"], o["item"], o["status_label"], o["amount"], o["currency"], o["created"])
			comps = append(comps, text(id, line, "body"))
		}
		comps[0]["children"] = children
		return surface(sid, comps), true
	}
	return nil, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/a2ui/ -run TestFromWidget -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/a2ui/a2ui.go internal/a2ui/a2ui_test.go
git commit -m "feat(a2ui): map domain widgets to A2UI v0.9 surface JSON

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: `internal/a2ui` — `ParseAction`

**Files:**
- Modify: `internal/a2ui/a2ui.go`
- Test: `internal/a2ui/action_test.go`

**Interfaces:**
- Produces: `func ParseAction(data map[string]any) (name string, ctx map[string]any, ok bool)` — extracts an incoming A2UI action payload `{version:"v0.9", action:{name, context}}` from a DataPart's data map.

- [ ] **Step 1: Write the failing test**

```go
package a2ui

import "testing"

func TestParseAction(t *testing.T) {
	name, ctx, ok := ParseAction(map[string]any{
		"version": "v0.9",
		"action":  map[string]any{"name": "approve_refund", "context": map[string]any{"order_id": "1055"}},
	})
	if !ok || name != "approve_refund" || ctx["order_id"] != "1055" {
		t.Fatalf("got name=%q ctx=%v ok=%v", name, ctx, ok)
	}
}

func TestParseActionRejectsNonAction(t *testing.T) {
	if _, _, ok := ParseAction(map[string]any{"useStreaming": false}); ok {
		t.Error("non-action data must return ok=false")
	}
	if _, _, ok := ParseAction(nil); ok {
		t.Error("nil data must return ok=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/a2ui/ -run TestParseAction -v`
Expected: FAIL — `ParseAction` undefined.

- [ ] **Step 3: Add the implementation to `internal/a2ui/a2ui.go`**

```go
// ParseAction extracts an incoming A2UI action event from a DataPart's data map.
// Shape: {"version":"v0.9","action":{"name":"...","context":{...}}}. Returns
// ok=false when the map is not an action payload.
func ParseAction(data map[string]any) (string, map[string]any, bool) {
	if data == nil {
		return "", nil, false
	}
	action, ok := data["action"].(map[string]any)
	if !ok {
		return "", nil, false
	}
	name, ok := action["name"].(string)
	if !ok || name == "" {
		return "", nil, false
	}
	ctx, _ := action["context"].(map[string]any)
	if ctx == nil {
		ctx = map[string]any{}
	}
	return name, ctx, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/a2ui/ -v`
Expected: PASS (all a2ui tests).

- [ ] **Step 5: Commit**

```bash
git add internal/a2ui/a2ui.go internal/a2ui/action_test.go
git commit -m "feat(a2ui): parse incoming A2UI action events

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Make the widget handler session-aware

**Files:**
- Modify: `internal/a2abridge/client.go` (`SetWidgetHandler`, `forwardWidget`, `onWidget` field)
- Modify: `internal/tui/repl.go` (handler registration)
- Modify: `internal/a2abridge/widget_e2e_test.go` (handler signature in test)
- Test: existing `internal/a2abridge/widget_e2e_test.go`

**Interfaces:**
- Produces: `func (c *OrdersClient) SetWidgetHandler(fn func(sessionID string, w map[string]any))` — the handler now receives the orchestrator session id (= A2A contextID) so a multi-session server can route the widget to the right request.
- Consumes: `a2abridge.OrdersClient.ask(ctx, sessionID, text)` (existing) already has `sessionID` in scope.

- [ ] **Step 1: Update the test to the new signature (failing)**

In `internal/a2abridge/widget_e2e_test.go`, change `clientCapturingWidgets` handler:

```go
	oc.SetWidgetHandler(func(_ string, w map[string]any) { got = append(got, w) })
```

- [ ] **Step 2: Run to verify it fails to compile**

Run: `go test ./internal/a2abridge/ -run TestWorkerEmits 2>&1 | head`
Expected: build error — signature mismatch (`onWidget` still `func(map[string]any)`).

- [ ] **Step 3: Change the field, setter, and call site in `internal/a2abridge/client.go`**

```go
	// onWidget, if set, receives structured widgets the worker returns in
	// DataParts, tagged with the orchestrator session id (= A2A contextID) so a
	// multi-session server can route each widget to the right request.
	onWidget func(sessionID string, w map[string]any)
```

```go
// SetWidgetHandler registers a callback for widgets (DataParts) the worker
// emits. The handler runs on the goroutine that invokes the delegating tool.
func (c *OrdersClient) SetWidgetHandler(fn func(sessionID string, w map[string]any)) { c.onWidget = fn }
```

Update `forwardWidget` to accept and pass the session id:

```go
// forwardWidget sends the first widget among parts (if any) to the registered
// handler, tagged with sessionID, so it renders in the UI without passing
// through the LLM.
func (c *OrdersClient) forwardWidget(sessionID string, parts []*a2a.Part) {
	if c.onWidget == nil {
		return
	}
	if w := firstWidget(parts); w != nil {
		c.trace.Logf("    ⟐ widget DataPart (%v) → UI, bypassing LLM", w["_kind"])
		c.onWidget(sessionID, w)
	}
}
```

Update the two call sites in `ask` (they are inside `ask`, where `sessionID` is a parameter):

```go
			c.forwardWidget(sessionID, statusParts(r)) // e.g. confirmation widget
```
```go
		c.forwardWidget(sessionID, artifactParts(r)) // e.g. order / order-list widget
```

- [ ] **Step 4: Update `internal/tui/repl.go` registration to ignore the session id**

```go
		ws.SetWidgetHandler(func(_ string, w map[string]any) {
			renderWidget(w)
			widgetShown = true
		})
```

- [ ] **Step 5: Run tests**

Run: `go build ./... && go test ./internal/a2abridge/ ./internal/tui/... -v 2>&1 | tail -20`
Expected: PASS (widget e2e tests still green; TUI compiles).

- [ ] **Step 6: Commit**

```bash
git add internal/a2abridge/client.go internal/tui/repl.go internal/a2abridge/widget_e2e_test.go
git commit -m "refactor(a2abridge): session-aware widget handler for multi-session server

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Orchestrator AgentCard with the A2UI extension

**Files:**
- Create: `internal/a2abridge/orchcard.go`
- Test: `internal/a2abridge/orchcard_test.go`

**Interfaces:**
- Consumes: `a2ui.ExtensionURI` (Task 1).
- Produces: `func OrchestratorCard(publicURL string) *a2a.AgentCard` — advertises the A2UI extension in `Capabilities.Extensions` and a JSONRPC interface at `publicURL+"/invoke"`.

- [ ] **Step 1: Write the failing test**

```go
package a2abridge

import (
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

func TestOrchestratorCardAdvertisesA2UI(t *testing.T) {
	card := OrchestratorCard("http://localhost:8080")
	var found bool
	for _, e := range card.Capabilities.Extensions {
		if e.URI == a2ui.ExtensionURI {
			found = true
		}
	}
	if !found {
		t.Errorf("card must advertise the A2UI extension %q; got %#v", a2ui.ExtensionURI, card.Capabilities.Extensions)
	}
	if len(card.SupportedInterfaces) == 0 {
		t.Fatal("card must expose a JSONRPC interface")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/a2abridge/ -run TestOrchestratorCard -v`
Expected: FAIL — `OrchestratorCard` undefined.

- [ ] **Step 3: Implement `internal/a2abridge/orchcard.go`**

```go
package a2abridge

import (
	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

// OrchestratorCard is the AgentCard the orchestrator serves to browser A2UI
// clients. It advertises the A2UI A2A-extension so clients activate generative
// UI, and a JSONRPC interface at publicURL/invoke (matching the mux in main.go).
func OrchestratorCard(publicURL string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "orders-orchestrator",
		Description:        "Оркестратор поддержки: делегирует работу с заказами и отдаёт A2UI-виджеты.",
		Version:            "0.1.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:         a2ui.ExtensionURI,
				Description: "Отдаёт интерфейс через A2UI (generative UI).",
			}},
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(publicURL+"/invoke", a2a.TransportProtocolJSONRPC),
		},
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/a2abridge/ -run TestOrchestratorCard -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/a2abridge/orchcard.go internal/a2abridge/orchcard_test.go
git commit -m "feat(a2abridge): orchestrator AgentCard advertising A2UI extension

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Orchestrator A2A server executor

**Files:**
- Create: `internal/a2abridge/orchserver.go`
- Test: `internal/a2abridge/orchserver_test.go`

**Interfaces:**
- Consumes: `runner.Runner` (orchestrator runner), `*OrdersClient` (to register the session-aware widget handler), `a2ui.FromWidget`, `a2ui.ParseAction`, `a2ui.ExtensionURI`, `a2ui.MIMEType`.
- Produces:
  - `func NewOrchestratorExecutor(r *runner.Runner, oc *OrdersClient, trace *Tracer) a2asrv.AgentExecutor`
  - Behavior: activates the A2UI extension when requested; parses incoming text or A2UI action; runs the runner; emits the assistant text plus (when A2UI active) any widget as an `application/a2ui+json` DataPart artifact.

**Design notes (read before coding):**
- Widget routing: the executor holds `pendingWidgets map[string][]map[string]any` keyed by session id (= `ec.ContextID`). Before running the runner it clears the slice for that session; it registers `oc.SetWidgetHandler` **once** in `NewOrchestratorExecutor` with a closure that appends into `pendingWidgets[sessionID]` under a mutex. After the run, it drains that session's widgets, converts each via `a2ui.FromWidget`, and emits them.
- Action → resume text mapping: `approve_refund`→`"да"`, `decline_refund`→`"нет"`, otherwise a descriptive line so the LLM can react.
- The runner session id = `ec.ContextID` (same convention as the worker executor in `server.go`).
- Extension activation: `ext := &a2a.AgentExtension{URI: a2ui.ExtensionURI}`; `if exts, ok := a2asrv.ExtensionsFrom(ctx); ok && exts.Requested(ext) { exts.Activate(ext); a2uiActive = true }`.

- [ ] **Step 1: Write the failing test (text turn emits A2UI widget)**

```go
package a2abridge

import (
	"context"
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
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"
)

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
```

> This test uses three helpers you will add in Step 3 alongside the implementation: `serveExecutor` (generic version of the existing `startWorkerServer`), `newA2UIClient`, and its `sendText`. Keep them in `orchserver_test.go`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/a2abridge/ -run TestOrchestratorEmitsA2UIWidget -v 2>&1 | head`
Expected: FAIL — `NewOrchestratorExecutor`, `serveExecutor`, `newA2UIClient` undefined.

- [ ] **Step 3: Implement `internal/a2abridge/orchserver.go`**

```go
package a2abridge

import (
	"context"
	"iter"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

type orchExecutor struct {
	runner *runner.Runner
	trace  *Tracer

	mu      sync.Mutex
	widgets map[string][]map[string]any // sessionID → widgets produced this turn
}

// NewOrchestratorExecutor wraps the orchestrator runner as an A2A server that
// speaks the A2UI extension: it maps worker widgets to A2UI JSON and translates
// incoming A2UI button actions into task resumes. trace may be nil.
func NewOrchestratorExecutor(r *runner.Runner, oc *OrdersClient, trace *Tracer) a2asrv.AgentExecutor {
	e := &orchExecutor{runner: r, trace: trace, widgets: make(map[string][]map[string]any)}
	// One global handler routes each widget to its session's slot.
	oc.SetWidgetHandler(func(sessionID string, w map[string]any) {
		e.mu.Lock()
		e.widgets[sessionID] = append(e.widgets[sessionID], w)
		e.mu.Unlock()
	})
	return e
}

// actionToText maps an A2UI button action name to the user text the orchestrator
// LLM should process. Unknown actions fall back to a descriptive line.
func actionToText(name string, ctx map[string]any) string {
	switch name {
	case "approve_refund":
		return "да"
	case "decline_refund":
		return "нет"
	default:
		return "Пользователь нажал действие: " + name
	}
}

func (e *orchExecutor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		sessionID := ec.ContextID

		// Activate the A2UI extension if the client requested it.
		a2uiActive := false
		ext := &a2a.AgentExtension{URI: a2ui.ExtensionURI}
		if exts, ok := a2asrv.ExtensionsFrom(ctx); ok && exts.Requested(ext) {
			exts.Activate(ext)
			a2uiActive = true
		}
		e.trace.Logf("▶ orchestrator A2A request | contextID=%s a2ui=%v", sessionID, a2uiActive)

		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// Parse input: an A2UI action DataPart, or plain text.
		userText := ""
		if ec.Message != nil {
			for _, p := range ec.Message.Parts {
				if data, ok := p.Data().(map[string]any); ok {
					if name, actx, ok := a2ui.ParseAction(data); ok {
						userText = actionToText(name, actx)
						e.trace.Logf("  A2UI action %q → text %q", name, userText)
						break
					}
				}
				if t := p.Text(); t != "" {
					userText = t
				}
			}
		}

		// Reset this session's widget slot before the run.
		e.mu.Lock()
		delete(e.widgets, sessionID)
		e.mu.Unlock()

		// Run the orchestrator LLM. The ask_orders_agent tool delegates to the
		// worker and forwards any widget through the session handler above.
		msg := genai.NewContentFromText(userText, genai.RoleUser)
		var finalText string
		for event, err := range e.runner.Run(ctx, "a2ui-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				yield(nil, err)
				return
			}
			if event == nil || event.Content == nil {
				continue
			}
			for _, p := range event.Content.Parts {
				if p.Text != "" {
					finalText = p.Text
				}
			}
		}

		// Assemble the artifact: text first (fallback), then A2UI parts.
		parts := []*a2a.Part{a2a.NewTextPart(strings.TrimSpace(orDefault(finalText, "Готово.")))}
		if a2uiActive {
			e.mu.Lock()
			ws := e.widgets[sessionID]
			delete(e.widgets, sessionID)
			e.mu.Unlock()
			for _, w := range ws {
				if msgs, ok := a2ui.FromWidget(w); ok {
					for _, m := range msgs {
						part := a2a.NewDataPart(m)
						part.MediaType = a2ui.MIMEType
						parts = append(parts, part)
					}
				}
			}
		}
		if !yield(a2a.NewArtifactEvent(ec, parts...), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
}

func (e *orchExecutor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
```

- [ ] **Step 4: Add test helpers to `internal/a2abridge/orchserver_test.go`**

Add a generic server helper (mirrors `startWorkerServer` but takes any executor + card builder) and a minimal A2UI test client:

```go
import (
	"net"
	"net/http"
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
```

Because the a2a-go client is needed to talk to the orchestrator with the extension header, add a tiny reusable probe in `internal/a2abridge/orchserver.go`:

```go
// A2UIProbe is a minimal A2A client that activates the A2UI extension and
// returns the parts of the resulting task artifact. Used by the browser bridge
// tests and available for local diagnostics.
type A2UIProbe struct{ client *a2aclient.Client }

func NewA2UIProbe(ctx context.Context, url string) (*A2UIProbe, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, url)
	if err != nil {
		return nil, err
	}
	cl, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return &A2UIProbe{client: cl}, nil
}

func (p *A2UIProbe) SendText(ctx context.Context, text string) ([]*a2a.Part, error) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	res, err := p.client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: msg,
		// Activate the A2UI extension for this call.
		Extensions: []string{a2ui.ExtensionURI},
	})
	if err != nil {
		return nil, err
	}
	if task, ok := res.(*a2a.Task); ok && len(task.Artifacts) > 0 {
		return task.Artifacts[len(task.Artifacts)-1].Parts, nil
	}
	return nil, nil
}
```

> Add imports `"github.com/a2aproject/a2a-go/v2/a2aclient"` and `"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"` to `orchserver.go`. **Pin at implementation:** confirm the exact field on `a2a.SendMessageRequest` that carries requested extensions against a2a-go v2.3.1 (grep `SendMessageRequest` / `Extensions` in the SDK). If the request struct does not carry extensions directly, set them via the client's per-call option or a `fetchImpl`/header equivalent — the browser sets the `X-A2A-Extensions` header, so the Go probe must produce the same `A2A-Extensions` service param. Adjust `SendText` accordingly; the test asserts behavior, not the wiring.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/a2abridge/ -run TestOrchestratorEmitsA2UIWidget -v 2>&1 | tail -20`
Expected: PASS — an `application/a2ui+json` part is present.

- [ ] **Step 6: Add the interactive-action test**

```go
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

	// Turn 2: click "Оформить возврат" → approve_refund action.
	_, err := client.c.SendAction(context.Background(), "approve_refund", map[string]any{"order_id": "1041"})
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := store.Get("1041"); o.Status != "refunded" {
		t.Errorf("approve_refund action must resume the task and refund; status=%q", o.Status)
	}
}
```

Add `SendAction` to `A2UIProbe` in `orchserver.go`:

```go
// SendAction sends an A2UI button action back to the agent over A2A.
func (p *A2UIProbe) SendAction(ctx context.Context, name string, actx map[string]any) ([]*a2a.Part, error) {
	part := a2a.NewDataPart(map[string]any{
		"version": a2ui.Version,
		"action":  map[string]any{"name": name, "context": actx},
	})
	part.MediaType = a2ui.MIMEType
	msg := a2a.NewMessage(a2a.MessageRoleUser, part)
	res, err := p.client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg, Extensions: []string{a2ui.ExtensionURI}})
	if err != nil {
		return nil, err
	}
	if task, ok := res.(*a2a.Task); ok && len(task.Artifacts) > 0 {
		return task.Artifacts[len(task.Artifacts)-1].Parts, nil
	}
	return nil, nil
}
```

> **Session continuity pin:** the confirmation resume relies on the orchestrator runner + `OrdersClient.pending` seeing the *same* session across turns. The A2A client must reuse the same `contextID` for turn 2 so `ec.ContextID` is stable. Confirm the a2a-go client reuses the context/task from the first response (as `OrdersClient.ask` does with `NewMessageForTask`); if not, thread the returned `contextID` into `SendAction`'s message. Adjust `A2UIProbe` to remember the last `contextID`/`taskID` like `OrdersClient` does.

- [ ] **Step 7: Run both orchestrator tests**

Run: `go test ./internal/a2abridge/ -run TestOrchestrator -v 2>&1 | tail -30`
Expected: PASS (both).

- [ ] **Step 8: Commit**

```bash
git add internal/a2abridge/orchserver.go internal/a2abridge/orchserver_test.go
git commit -m "feat(a2abridge): orchestrator A2A server executor with A2UI + actions

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: `internal/webui` — embed and serve the built frontend

**Files:**
- Create: `internal/webui/embed.go`
- Create: `internal/webui/dist/index.html` (placeholder so `go:embed` compiles before the first frontend build)
- Test: `internal/webui/embed_test.go`

**Interfaces:**
- Produces: `func Handler() http.Handler` — serves the embedded `dist/` directory (SPA: unknown paths fall back to `index.html`).

- [ ] **Step 1: Create the placeholder so embed compiles**

`internal/webui/dist/index.html`:

```html
<!doctype html>
<title>A2UI demo (placeholder — run the web build)</title>
<p>Frontend not built yet. Run <code>cd web && yarn install && yarn build</code>.</p>
```

- [ ] **Step 2: Write the failing test**

```go
package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerServesIndex(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / = %d, want 200", resp.StatusCode)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/webui/ -v`
Expected: FAIL — `Handler` undefined.

- [ ] **Step 4: Implement `internal/webui/embed.go`**

```go
// Package webui embeds and serves the built A2UI browser frontend.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded frontend. Unknown paths fall back to index.html so
// the single-page app can boot from any URL.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(sub, trimLeadingSlash(r.URL.Path)); err != nil && r.URL.Path != "/" {
			r.URL.Path = "/" // SPA fallback
		}
		fileServer.ServeHTTP(w, r)
	})
}

func trimLeadingSlash(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/webui/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/webui/embed.go internal/webui/embed_test.go internal/webui/dist/index.html
git commit -m "feat(webui): embed and serve the built A2UI frontend (placeholder dist)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: `cmd/orchestrator/main.go` — `--web` mode

**Files:**
- Modify: `cmd/orchestrator/main.go`

**Interfaces:**
- Consumes: `a2abridge.NewOrchestratorExecutor`, `a2abridge.OrchestratorCard`, `webui.Handler`, existing runner/OrdersClient wiring.

- [ ] **Step 1: Add a `--web` flag and server branch**

Add `"flag"`, `"net/http"`, `a2asrv`, and `internal/webui` imports. After the runner `r` is built, branch:

```go
	web := flag.Bool("web", false, "serve the A2UI web UI + A2A server instead of the terminal REPL")
	flag.Parse()

	// ... existing: oc, model, ordersTool, ag, r built as today ...

	if *web {
		publicURL := cfg.PublicURL // add PublicURL to orchestrator config (default http://localhost:8080)
		exec := a2abridge.NewOrchestratorExecutor(r, oc, trace)
		handler := a2asrv.NewHandler(exec)
		mux := http.NewServeMux()
		mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
		mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(a2abridge.OrchestratorCard(publicURL)))
		mux.Handle("/", webui.Handler())
		log.Printf("orchestrator web UI on %s", cfg.ListenAddr)
		log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
		return
	}

	// existing REPL path:
	if err := tui.Run(ctx, r, oc); err != nil {
		log.Fatalf("tui: %v", err)
	}
```

> Add `ListenAddr` (e.g. `:8080`) and `PublicURL` (e.g. `http://localhost:8080`) to the orchestrator config struct + `configs/orchestrator.yaml`, mirroring the worker config. If the orchestrator config lacks these, add them in this task (`internal/config`), with defaults, and a test asserting the defaults load.

- [ ] **Step 2: Build**

Run: `go build ./... && go vet ./...`
Expected: no errors.

- [ ] **Step 3: Smoke-run the server (manual, no LLM needed for card/static)**

Run: `go run ./cmd/orchestrator --web &` then `curl -s localhost:8080/.well-known/agent-card.json | grep a2ui` and `curl -s localhost:8080/ | head`.
Expected: card JSON contains the A2UI extension URI; `/` returns the (placeholder) HTML. Stop the server afterward.

- [ ] **Step 4: Commit**

```bash
git add cmd/orchestrator/main.go internal/config/*.go configs/orchestrator.yaml
git commit -m "feat(orchestrator): --web mode serving A2A + embedded A2UI frontend

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 8: Frontend scaffold (`web/`)

**Files:**
- Create: `web/package.json`, `web/tsconfig.json`, `web/vite.config.ts`, `web/index.html`, `web/.gitignore`

- [ ] **Step 1: Create `web/package.json`**

```json
{
  "name": "a2ui-orders-web",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc && vite build"
  },
  "dependencies": {
    "@a2a-js/sdk": "^0.3.0",
    "@a2ui/lit": "^0.10.0",
    "@a2ui/web_core": "^0.10.0",
    "lit": "^3.0.0"
  },
  "devDependencies": {
    "typescript": "^5.9.0",
    "vite": "^7.0.0"
  }
}
```

> **Pin at implementation:** run `yarn install` (or `npm install`) and let the lockfile resolve exact versions. If `@a2a-js/sdk` / `@a2ui/*` majors differ, update the ranges. Confirm the packages resolve from the public npm registry; if `@a2ui/*` is not published, fall back to vendoring from `google/a2ui` `renderers/lit` + `web_core` builds (note the fallback in the README, do not silently switch).

- [ ] **Step 2: Create `web/tsconfig.json`**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "experimentalDecorators": true,
    "useDefineForClassFields": false,
    "strict": true,
    "skipLibCheck": true,
    "noEmit": true
  },
  "include": ["src"]
}
```

- [ ] **Step 3: Create `web/vite.config.ts`** (build into the Go embed dir; dev-proxy A2A endpoints to the Go server)

```ts
import {defineConfig} from 'vite';

export default defineConfig({
  build: {outDir: '../internal/webui/dist', emptyOutDir: true},
  server: {
    proxy: {
      '/invoke': 'http://localhost:8080',
      '/.well-known': 'http://localhost:8080',
    },
  },
});
```

- [ ] **Step 4: Create `web/index.html`**

```html
<!doctype html>
<html lang="ru">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>A2UI · Ассистент заказов</title>
  </head>
  <body>
    <orders-app></orders-app>
    <script type="module" src="/src/app.ts"></script>
  </body>
</html>
```

- [ ] **Step 5: Create `web/.gitignore`**

```
node_modules
```

- [ ] **Step 6: Install deps**

Run: `cd web && yarn install`
Expected: `node_modules` populated, lockfile written.

- [ ] **Step 7: Commit**

```bash
git add web/package.json web/tsconfig.json web/vite.config.ts web/index.html web/.gitignore web/yarn.lock
git commit -m "chore(web): scaffold Vite+Lit frontend for A2UI

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 9: Frontend A2A client (`web/src/client.ts`)

**Files:**
- Create: `web/src/client.ts`

**Interfaces:**
- Produces: `class A2UIClient { constructor(baseUrl?: string); sendText(text: string): Promise<any[]>; sendAction(name: string, context: Record<string, any>): Promise<any[]> }` — returns the A2UI messages (parsed `application/a2ui+json` DataPart `data`) from the agent's response.

- [ ] **Step 1: Implement `web/src/client.ts`** (modeled on `google/a2ui` `samples/client/lit/shell/client.ts`)

```ts
import {Part, Task} from '@a2a-js/sdk';
import {A2AClient} from '@a2a-js/sdk/client';

const A2UI_MIME = 'application/a2ui+json';
const A2UI_EXT = 'https://a2ui.org/a2a-extension/a2ui/v0.9';

export class A2UIClient {
  #baseUrl: string;
  #client: A2AClient | null = null;
  #contextId: string | undefined;
  #taskId: string | undefined;

  constructor(baseUrl = '') {
    this.#baseUrl = baseUrl;
  }

  async #getClient(): Promise<A2AClient> {
    if (!this.#client) {
      const base = this.#baseUrl || location.origin;
      this.#client = await A2AClient.fromCardUrl(`${base}/.well-known/agent-card.json`, {
        fetchImpl: async (url, init) => {
          const headers = new Headers(init?.headers);
          headers.set('X-A2A-Extensions', A2UI_EXT);
          return fetch(url, {...init, headers});
        },
      });
    }
    return this.#client;
  }

  async #send(parts: Part[]): Promise<any[]> {
    const client = await this.#getClient();
    const message: any = {
      messageId: crypto.randomUUID(),
      role: 'user',
      parts,
      kind: 'message',
    };
    if (this.#taskId) message.taskId = this.#taskId;
    if (this.#contextId) message.contextId = this.#contextId;

    const res = await client.sendMessage({message});
    const result: any = (res as any).result ?? res;

    // Track context/task for follow-up turns (resume the same A2A task).
    if (result?.contextId) this.#contextId = result.contextId;
    if (result?.id && result?.kind === 'task') this.#taskId = result.id;

    // Extract A2UI messages from the latest artifact's data parts.
    const task = result as Task;
    const artifact = task?.artifacts?.[task.artifacts.length - 1];
    const msgs: any[] = [];
    for (const p of artifact?.parts ?? []) {
      const anyP = p as any;
      if ((anyP.mimeType === A2UI_MIME || anyP.kind === 'data') && anyP.data) {
        msgs.push(anyP.data);
      }
    }
    return msgs;
  }

  sendText(text: string): Promise<any[]> {
    return this.#send([{kind: 'text', text} as Part]);
  }

  sendAction(name: string, context: Record<string, any>): Promise<any[]> {
    return this.#send([
      {kind: 'data', mimeType: A2UI_MIME, data: {version: 'v0.9', action: {name, context}}} as Part,
    ]);
  }
}
```

> **Pin at implementation:** the exact `@a2a-js/sdk` message/response field names (`contextId`/`taskId`/`artifacts`/part `mimeType`) — verify against the installed SDK types (`tsc` will flag mismatches). The header-based extension activation follows the reference shell; keep it.

- [ ] **Step 2: Type-check**

Run: `cd web && yarn tsc --noEmit`
Expected: no type errors (fix field-name mismatches flagged by the SDK types).

- [ ] **Step 3: Commit**

```bash
git add web/src/client.ts
git commit -m "feat(web): A2A client with A2UI extension + action send-back

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 10: Frontend Lit app (`web/src/app.ts`)

**Files:**
- Create: `web/src/app.ts`

**Interfaces:**
- Consumes: `A2UIClient` (Task 9), `MessageProcessor` + `basicCatalog` + `A2uiSurface` from `@a2ui/*` (per the renderer README quickstart).

- [ ] **Step 1: Implement `web/src/app.ts`**

```ts
import {LitElement, html, css} from 'lit';
import {customElement, state} from 'lit/decorators.js';
import {MessageProcessor} from '@a2ui/web_core/v0_9';
import {basicCatalog} from '@a2ui/lit/v0_9';
import '@a2ui/lit/v0_9'; // registers <a2ui-surface>
import {A2UIClient} from './client.js';

@customElement('orders-app')
export class OrdersApp extends LitElement {
  #client = new A2UIClient();

  // The processor renders surfaces and routes button actions back to the agent.
  #processor = new MessageProcessor(
    [basicCatalog],
    async (action: any) => {
      const msgs = await this.#client.sendAction(action.name, action.context ?? {});
      this.#ingest(msgs);
    },
  );

  @state() private _surfaces: any[] = [];
  @state() private _log: string[] = [];
  @state() private _busy = false;

  connectedCallback() {
    super.connectedCallback();
    this.#processor.onSurfaceCreated((s: any) => {
      this._surfaces = [...this._surfaces, s];
    });
  }

  #ingest(msgs: any[]) {
    const a2ui = msgs.filter(m => m && m.version === 'v0.9');
    if (a2ui.length) this.#processor.processMessages(a2ui);
  }

  async #send(text: string) {
    if (!text.trim()) return;
    this._log = [...this._log, `вы: ${text}`];
    this._busy = true;
    try {
      const msgs = await this.#client.sendText(text);
      this.#ingest(msgs);
    } finally {
      this._busy = false;
    }
  }

  static styles = css`
    :host { display: block; max-width: 640px; margin: 0 auto; padding: 24px; font-family: system-ui; }
    .log { margin: 12px 0; color: #555; }
    .surfaces { display: flex; flex-direction: column; gap: 16px; }
    form { display: flex; gap: 8px; margin-top: 16px; }
    input { flex: 1; padding: 12px; border-radius: 8px; border: 1px solid #ccc; }
    button { padding: 12px 20px; border-radius: 8px; border: none; background: #3367d6; color: #fff; }
  `;

  render() {
    return html`
      <h2>Ассистент заказов · A2UI</h2>
      <div class="log">${this._log.map(l => html`<div>${l}</div>`)}</div>
      <div class="surfaces">
        ${this._surfaces.map(s => html`<a2ui-surface .surface=${s}></a2ui-surface>`)}
      </div>
      <form @submit=${(e: Event) => {
        e.preventDefault();
        const input = (e.target as HTMLFormElement).querySelector('input')!;
        this.#send(input.value);
        input.value = '';
      }}>
        <input placeholder="Напишите запрос…" ?disabled=${this._busy} />
        <button type="submit" ?disabled=${this._busy}>Отправить</button>
      </form>
    `;
  }
}
```

> **Pin at implementation:** the exact `MessageProcessor` constructor signature (action callback arg) and the `<a2ui-surface>` registration import path — follow the installed `@a2ui/lit` README/types (`renderers/lit/README.md` quickstart is the reference). `tsc` + a browser run will surface any mismatch.

- [ ] **Step 2: Type-check + build into the Go embed dir**

Run: `cd web && yarn build`
Expected: `../internal/webui/dist` now contains the built `index.html` + assets.

- [ ] **Step 3: Verify the Go binary embeds the real build**

Run: `go build ./... && go test ./internal/webui/ -v`
Expected: PASS (Handler serves the real index.html).

- [ ] **Step 4: Commit** (do not commit `internal/webui/dist` build output beyond the placeholder — keep it gitignored except the placeholder)

Add `internal/webui/dist/assets/` and hashed files to `.gitignore`, keep only `index.html` placeholder tracked... instead, simpler: gitignore the whole built dist and keep a tracked placeholder via a separate file. Do:

```bash
echo "internal/webui/dist/*" >> .gitignore
echo "!internal/webui/dist/index.html" >> .gitignore
git checkout -- internal/webui/dist/index.html  # restore placeholder for the committed tree
git add web/src/app.ts .gitignore
git commit -m "feat(web): Lit app rendering A2UI surfaces with interactive actions

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

> Rationale: the built frontend is a reproducible artifact (`yarn build`); committing hashed bundles churns the repo. CI/build docs regenerate `dist` before `go build`. The tracked placeholder keeps `go:embed` compiling on a fresh checkout.

---

### Task 11: End-to-end manual verification (browser)

**Files:** none (verification task).

- [ ] **Step 1: Build the frontend**

Run: `cd web && yarn build`

- [ ] **Step 2: Start LM Studio** (OpenAI-compat at `http://localhost:1234/v1`) and the worker

Run: `go run ./cmd/worker` (separate terminal).

- [ ] **Step 3: Start the orchestrator in web mode**

Run: `go run ./cmd/orchestrator --web` (separate terminal).

- [ ] **Step 4: Open the UI and exercise all three widgets**

Open `http://localhost:8080`. Verify:
- «статус заказа 1041» → an order Card renders.
- «последние заказы alice» → an order-list renders.
- «верни деньги за 1041» → a confirmation Card with two buttons renders; clicking **Оформить возврат** completes the refund (worker store updates; a result message appears); clicking **Отмена** cancels.

- [ ] **Step 5: Record the result** in the PR description / a short note (no code change). If any widget or the action round-trip fails, file the mismatch against the pinned frontend API and fix in the relevant task.

---

### Task 12: Docs

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update README** — add the web architecture (browser ─A2A→ orchestrator ─A2A→ worker, A2UI as an A2A extension), a `## Web UI (A2UI)` section with build + run steps (`cd web && yarn install && yarn build`, then `go run ./cmd/orchestrator --web`), the same-origin/CSP note from the spec's error-handling section, and the Node-only-for-build caveat.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs(readme): A2UI web UI architecture and run instructions

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Оркестратор `--web` A2A-сервер + AgentCard c расширением → Tasks 4, 7. ✓
- Браузер (`@a2ui/lit` + `@a2a-js/sdk`) по A2A → Tasks 8–10. ✓
- Оркестратор как A2UI-шлюз (widget→A2UI) → Task 1 (`FromWidget`) + Task 5 (emit). ✓
- Интерактивные кнопки (action→resume) → Task 2 (`ParseAction`) + Task 5 (`actionToText`, resume) + Task 10 (send-back). ✓
- REPL сохраняется → Task 7 (default path) + Task 3 (TUI still compiles). ✓
- Сессионная маршрутизация виджетов → Task 3 + Task 5 (`pendingWidgets` keyed by sessionID). ✓
- Фолбэк без расширения → Task 5 (`a2uiActive` gate). ✓
- Тесты Go авто, фронт вручную → Tasks 1,2,4,5,6 (unit/e2e) + Task 11 (manual). ✓
- `go:embed web/dist` → Task 6 (embed into `internal/webui/dist`, Vite outDir set in Task 8). ✓

**Placeholder scan:** No "TODO/TBD". Remaining `> Pin at implementation` notes are explicit external-fact verifications (npm versions, exact SDK field names, `SendMessageRequest.Extensions` wiring), each with a concrete verification command (`tsc`, grep the SDK) — not vague requirements. Acceptable and necessary given external deps.

**Type consistency:** `FromWidget([]map[string]any, bool)` used identically in Tasks 1 and 5. `ParseAction(map) (string, map, bool)` used in Tasks 2 and 5. `SetWidgetHandler(func(string, map))` consistent across Tasks 3 and 5. `OrchestratorCard(string) *a2a.AgentCard` consistent across Tasks 4, 5, 7. `A2UIClient.sendText/sendAction` consistent across Tasks 9, 10. `webui.Handler() http.Handler` consistent across Tasks 6, 7.

**Known risk (flagged, not hidden):** the frontend tasks depend on external npm package APIs (`@a2ui/*`, `@a2a-js/sdk`) whose exact signatures are pinned by `tsc` + browser run, not by Go tests. This is inherent to using the official renderer and is verified manually in Task 11.
