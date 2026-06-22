# A2A Orders Assistant Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a two-agent educational demo where a TUI orchestrator agent delegates order-management work to a worker agent over the A2A protocol, showcasing tool calls and the `input-required` user-clarification flow.

**Architecture:** Orchestrator = TUI/REPL + adk-go LlmAgent that exposes the remote worker as an `ask_orders_agent` tool (backed by an A2A client). Worker = A2A server wrapping an adk-go LlmAgent with mock order tools. Both LLM agents talk to a local LM Studio (OpenAI-compatible) through a custom `model.LLM` adapter. The worker signals it needs more input by replying `NEED_INPUT: <question>`, which the A2A executor maps to the A2A `input-required` task state; adk sessions keyed by the A2A `contextID` preserve history so resuming continues the same task.

**Tech Stack:** Go 1.23+, `google.golang.org/adk` (adk-go), `github.com/a2aproject/a2a-go/v2`, `github.com/openai/openai-go`, `gopkg.in/yaml.v3`, Docker Compose. LLM served by LM Studio (OpenAI-compatible API).

## Global Constraints

- Go module path: `github.com/kmpavloff/agents-a2a-protocol-demo`. Minimum Go: **1.23** (required for `iter.Seq2`).
- All inter-agent communication is **A2A** (`a2aproject/a2a-go` v2). No ACP, no other protocol.
- Agents are built with **adk-go** (`google.golang.org/adk`). No second agent framework.
- LLM access is **only** through the `internal/llm` adapter (the `adkmodel.LLM` interface). No direct OpenAI/genai calls elsewhere.
- Mock order data is loaded from `data/orders.json` into an **in-memory** store. No database.
- Tests must **not** require LM Studio: use the stub model from `internal/llm/stub.go`.
- Config is YAML per service with environment-variable overrides.
- TDD: write the failing test first, see it fail, implement, see it pass, commit.

---

## File Structure

```
go.mod
cmd/
  worker/main.go          # wires config → orders store → worker agent → A2A server
  orchestrator/main.go    # wires config → A2A client tool → orchestrator agent → TUI
internal/
  config/
    config.go             # YAML structs + Load() + env overrides + validation
    config_test.go
  orders/
    store.go              # in-memory store + JSON seed loader
    store_test.go
    tools.go              # adk function tools over the store
    tools_test.go
  llm/
    openai.go             # model.LLM adapter over openai-go → LM Studio
    openai_test.go
    stub.go               # deterministic scripted model for tests
  agent/
    worker.go             # builds the worker LlmAgent (tools + NEED_INPUT prompt)
    orchestrator.go       # builds the orchestrator LlmAgent (ask_orders_agent tool)
  a2abridge/
    server.go             # adk Runner → a2asrv.AgentExecutor (with input-required)
    server_test.go
    client.go             # ask_orders_agent tool: A2A client + pending-task state
    client_test.go
  tui/
    repl.go               # minimal REPL loop around the orchestrator runner
configs/
  worker.yaml
  orchestrator.yaml
data/
  orders.json
Dockerfile
docker-compose.yml
README.md
.gitignore
```

---

### Task 0: Project scaffold and dependency pinning

**Files:**
- Create: `go.mod`, `.gitignore`, `data/orders.json`, `configs/worker.yaml`, `configs/orchestrator.yaml`

**Interfaces:**
- Consumes: nothing.
- Produces: module `github.com/kmpavloff/agents-a2a-protocol-demo`; the seed data file and config files used by later tasks.

- [ ] **Step 1: Initialize the module and add dependencies**

Run:
```bash
cd /home/kmpavloff/projects/agents-a2a-protocol-demo
go mod init github.com/kmpavloff/agents-a2a-protocol-demo
go get google.golang.org/adk@latest
go get github.com/a2aproject/a2a-go/v2@latest
go get github.com/openai/openai-go@latest
go get gopkg.in/yaml.v3@latest
```
Expected: `go.mod` and `go.sum` created with the four dependencies.

- [ ] **Step 2: Verify the version-sensitive adk-go / genai APIs this plan relies on**

These confirm the exact symbols used in later tasks. Run each and read the output:
```bash
go doc google.golang.org/adk/model.LLM
go doc google.golang.org/adk/model.LLMRequest
go doc google.golang.org/adk/model.LLMResponse
go doc google.golang.org/adk/agent/llmagent.New
go doc google.golang.org/adk/tool/functiontool.New
go doc google.golang.org/adk/runner.New
go doc google.golang.org/adk/session.InMemoryService
go doc google.golang.org/genai.Content
go doc google.golang.org/genai.Part
go doc google.golang.org/genai.FunctionCall
go doc github.com/a2aproject/a2a-go/v2/a2asrv.AgentExecutor
go doc github.com/a2aproject/a2a-go/v2/a2asrv.NewHandler
go doc github.com/a2aproject/a2a-go/v2/a2aclient.NewFromCard
go doc github.com/a2aproject/a2a-go/v2/a2a.TaskState
```
Expected: each prints a definition. If a symbol's package path or signature differs from what later tasks assume (e.g. `genai.Part` is a struct rather than an interface, or `runner.New` takes different config fields), note the real signature and adapt the corresponding task's code to match — the surrounding logic stays the same.

- [ ] **Step 3: Create `.gitignore`**

```
/bin/
*.exe
.env
```

- [ ] **Step 4: Create `data/orders.json` seed data**

```json
{
  "orders": [
    { "id": "1023", "customer": "alice", "item": "Mechanical keyboard", "amount": 89.90, "currency": "EUR", "status": "delivered", "created": "2026-06-01", "refundable": true },
    { "id": "1041", "customer": "alice", "item": "USB-C hub", "amount": 34.50, "currency": "EUR", "status": "delivered", "created": "2026-06-10", "refundable": true },
    { "id": "1055", "customer": "alice", "item": "Laptop stand", "amount": 45.00, "currency": "EUR", "status": "shipped", "created": "2026-06-18", "refundable": false },
    { "id": "2007", "customer": "bob", "item": "Wireless mouse", "amount": 25.00, "currency": "EUR", "status": "processing", "created": "2026-06-20", "refundable": false }
  ],
  "sales_stats": [
    { "period": "2026-05", "orders": 320, "revenue": 14250.40, "currency": "EUR" },
    { "period": "2026-06", "orders": 198, "revenue": 9120.10, "currency": "EUR" }
  ]
}
```

- [ ] **Step 5: Create `configs/worker.yaml`**

```yaml
# Worker (orders-agent) configuration. Env vars override these (see internal/config).
listen_addr: ":8081"
public_url: "http://localhost:8081"
data_path: "data/orders.json"
llm:
  base_url: "http://localhost:1234/v1"
  model: "local-model"
  api_key: "lm-studio"
```

- [ ] **Step 6: Create `configs/orchestrator.yaml`**

```yaml
# Orchestrator configuration. Env vars override these (see internal/config).
worker_url: "http://localhost:8081"
llm:
  base_url: "http://localhost:1234/v1"
  model: "local-model"
  api_key: "lm-studio"
```

- [ ] **Step 7: Verify the module builds and commit**

Run:
```bash
go build ./... 2>&1 || true   # no Go files yet; this just confirms the toolchain runs
git add -A && git commit -m "chore: scaffold module, deps, seed data, configs"
```
Expected: commit succeeds.

---

### Task 1: Config loading with env overrides

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type LLMConfig struct { BaseURL, Model, APIKey string }`
  - `type WorkerConfig struct { ListenAddr, PublicURL, DataPath string; LLM LLMConfig }`
  - `type OrchestratorConfig struct { WorkerURL string; LLM LLMConfig }`
  - `func LoadWorker(path string) (WorkerConfig, error)`
  - `func LoadOrchestrator(path string) (OrchestratorConfig, error)`
  - Env overrides: `WORKER_LISTEN_ADDR`, `WORKER_PUBLIC_URL`, `WORKER_DATA_PATH`, `WORKER_URL`, `LLM_BASE_URL`, `LLM_MODEL`, `LLM_API_KEY`.

- [ ] **Step 1: Write the failing test**

`internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadWorkerAppliesEnvOverride(t *testing.T) {
	p := writeTemp(t, "listen_addr: \":8081\"\npublic_url: \"http://localhost:8081\"\ndata_path: \"data/orders.json\"\nllm:\n  base_url: \"http://localhost:1234/v1\"\n  model: \"local-model\"\n  api_key: \"lm-studio\"\n")
	t.Setenv("LLM_BASE_URL", "http://host.docker.internal:1234/v1")
	cfg, err := LoadWorker(p)
	if err != nil {
		t.Fatalf("LoadWorker: %v", err)
	}
	if cfg.LLM.BaseURL != "http://host.docker.internal:1234/v1" {
		t.Errorf("env override not applied: got %q", cfg.LLM.BaseURL)
	}
	if cfg.ListenAddr != ":8081" {
		t.Errorf("listen_addr: got %q", cfg.ListenAddr)
	}
}

func TestLoadWorkerValidatesRequired(t *testing.T) {
	p := writeTemp(t, "listen_addr: \"\"\n")
	if _, err := LoadWorker(p); err == nil {
		t.Fatal("expected validation error for empty listen_addr")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadWorker -v`
Expected: FAIL — `undefined: LoadWorker`.

- [ ] **Step 3: Write minimal implementation**

`internal/config/config.go`:
```go
// Package config loads per-service YAML configuration with env-var overrides.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type LLMConfig struct {
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	APIKey  string `yaml:"api_key"`
}

type WorkerConfig struct {
	ListenAddr string    `yaml:"listen_addr"`
	PublicURL  string    `yaml:"public_url"`
	DataPath   string    `yaml:"data_path"`
	LLM        LLMConfig `yaml:"llm"`
}

type OrchestratorConfig struct {
	WorkerURL string    `yaml:"worker_url"`
	LLM       LLMConfig `yaml:"llm"`
}

func env(key, cur string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return cur
}

func (c *LLMConfig) applyEnv() {
	c.BaseURL = env("LLM_BASE_URL", c.BaseURL)
	c.Model = env("LLM_MODEL", c.Model)
	c.APIKey = env("LLM_API_KEY", c.APIKey)
}

func LoadWorker(path string) (WorkerConfig, error) {
	var c WorkerConfig
	if err := readYAML(path, &c); err != nil {
		return c, err
	}
	c.ListenAddr = env("WORKER_LISTEN_ADDR", c.ListenAddr)
	c.PublicURL = env("WORKER_PUBLIC_URL", c.PublicURL)
	c.DataPath = env("WORKER_DATA_PATH", c.DataPath)
	c.LLM.applyEnv()
	if c.ListenAddr == "" {
		return c, fmt.Errorf("worker config: listen_addr is required")
	}
	if c.DataPath == "" {
		return c, fmt.Errorf("worker config: data_path is required")
	}
	if c.LLM.BaseURL == "" {
		return c, fmt.Errorf("worker config: llm.base_url is required")
	}
	return c, nil
}

func LoadOrchestrator(path string) (OrchestratorConfig, error) {
	var c OrchestratorConfig
	if err := readYAML(path, &c); err != nil {
		return c, err
	}
	c.WorkerURL = env("WORKER_URL", c.WorkerURL)
	c.LLM.applyEnv()
	if c.WorkerURL == "" {
		return c, fmt.Errorf("orchestrator config: worker_url is required")
	}
	if c.LLM.BaseURL == "" {
		return c, fmt.Errorf("orchestrator config: llm.base_url is required")
	}
	return c, nil
}

func readYAML(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config && git commit -m "feat(config): YAML loading with env overrides and validation"
```

---

### Task 2: In-memory orders store with JSON seeding

**Files:**
- Create: `internal/orders/store.go`, `internal/orders/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Order struct { ID, Customer, Item string; Amount float64; Currency, Status, Created string; Refundable bool }`
  - `type SalesStat struct { Period string; Orders int; Revenue float64; Currency string }`
  - `type Store struct { ... }` (mutex-guarded maps)
  - `func Load(path string) (*Store, error)`
  - `func (s *Store) Get(id string) (Order, bool)`
  - `func (s *Store) ByCustomer(customer string) []Order` (sorted by Created desc)
  - `func (s *Store) Stats(period string) (SalesStat, bool)`
  - `func (s *Store) Refund(id string) (Order, error)` (errors: `ErrNotFound`, `ErrNotRefundable`; sets status to `"refunded"`)
  - `var ErrNotFound, ErrNotRefundable error`

- [ ] **Step 1: Write the failing test**

`internal/orders/store_test.go`:
```go
package orders

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func seed(t *testing.T) *Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "orders.json")
	body := `{"orders":[
		{"id":"1023","customer":"alice","item":"Keyboard","amount":89.9,"currency":"EUR","status":"delivered","created":"2026-06-01","refundable":true},
		{"id":"1041","customer":"alice","item":"Hub","amount":34.5,"currency":"EUR","status":"delivered","created":"2026-06-10","refundable":true},
		{"id":"1055","customer":"alice","item":"Stand","amount":45,"currency":"EUR","status":"shipped","created":"2026-06-18","refundable":false}
	],"sales_stats":[{"period":"2026-06","orders":198,"revenue":9120.1,"currency":"EUR"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func TestByCustomerSortedDesc(t *testing.T) {
	s := seed(t)
	got := s.ByCustomer("alice")
	if len(got) != 3 {
		t.Fatalf("want 3 orders, got %d", len(got))
	}
	if got[0].ID != "1055" {
		t.Errorf("want newest (1055) first, got %s", got[0].ID)
	}
}

func TestRefundHappyPath(t *testing.T) {
	s := seed(t)
	o, err := s.Refund("1041")
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if o.Status != "refunded" {
		t.Errorf("status: want refunded, got %s", o.Status)
	}
}

func TestRefundNotRefundable(t *testing.T) {
	s := seed(t)
	if _, err := s.Refund("1055"); !errors.Is(err, ErrNotRefundable) {
		t.Fatalf("want ErrNotRefundable, got %v", err)
	}
}

func TestRefundNotFound(t *testing.T) {
	s := seed(t)
	if _, err := s.Refund("9999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestStats(t *testing.T) {
	s := seed(t)
	st, ok := s.Stats("2026-06")
	if !ok || st.Orders != 198 {
		t.Fatalf("stats lookup failed: %+v ok=%v", st, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orders/ -run TestRefund -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write minimal implementation**

`internal/orders/store.go`:
```go
// Package orders is the mock order-management domain used by the worker agent.
package orders

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

var (
	ErrNotFound      = errors.New("order not found")
	ErrNotRefundable = errors.New("order is not refundable")
)

type Order struct {
	ID         string  `json:"id"`
	Customer   string  `json:"customer"`
	Item       string  `json:"item"`
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	Status     string  `json:"status"`
	Created    string  `json:"created"`
	Refundable bool    `json:"refundable"`
}

type SalesStat struct {
	Period   string  `json:"period"`
	Orders   int     `json:"orders"`
	Revenue  float64 `json:"revenue"`
	Currency string  `json:"currency"`
}

type Store struct {
	mu     sync.RWMutex
	orders map[string]Order
	stats  map[string]SalesStat
}

type seedFile struct {
	Orders     []Order     `json:"orders"`
	SalesStats []SalesStat `json:"sales_stats"`
}

func Load(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read seed %s: %w", path, err)
	}
	var sf seedFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, fmt.Errorf("parse seed %s: %w", path, err)
	}
	s := &Store{orders: map[string]Order{}, stats: map[string]SalesStat{}}
	for _, o := range sf.Orders {
		s.orders[o.ID] = o
	}
	for _, st := range sf.SalesStats {
		s.stats[st.Period] = st
	}
	return s, nil
}

func (s *Store) Get(id string) (Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}

func (s *Store) ByCustomer(customer string) []Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Order
	for _, o := range s.orders {
		if o.Customer == customer {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

func (s *Store) Stats(period string) (SalesStat, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.stats[period]
	return st, ok
}

func (s *Store) Refund(id string) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return Order{}, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	if !o.Refundable {
		return Order{}, fmt.Errorf("%s: %w", id, ErrNotRefundable)
	}
	o.Status = "refunded"
	s.orders[id] = o
	return o, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/orders/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/orders/store.go internal/orders/store_test.go && git commit -m "feat(orders): in-memory store with JSON seeding"
```

---

### Task 3: Order tools as adk function tools

**Files:**
- Create: `internal/orders/tools.go`, `internal/orders/tools_test.go`

**Interfaces:**
- Consumes: `*Store` (Task 2).
- Produces:
  - `func Tools(s *Store) []tool.Tool` — returns five tools: `find_order`, `get_order_status`, `list_recent_orders`, `get_sales_stats`, `initiate_refund`.
  - Each tool's handler is a package-level function tested directly:
    - `func getOrderStatus(s *Store, id string) (string, error)`
    - `func listRecentOrders(s *Store, customer string) (string, error)`
    - `func getSalesStats(s *Store, period string) (string, error)`
    - `func initiateRefund(s *Store, id string) (string, error)`
    - `func findOrder(s *Store, query string) (string, error)`
  - Handlers return human-readable strings; domain errors (`ErrNotFound`, `ErrNotRefundable`) are returned as the **string result** (so the LLM can react), never as a Go error.

> Note: `functiontool.New` signature is confirmed as `functiontool.New(functiontool.Config{Name, Description}, func(ctx tool.Context, args T) (R, error))`. If Step 1 of Task 0 showed a different arg-struct convention, adjust only the `Tools` wrapper in Step 3; the package-level handlers and their tests are unaffected.

- [ ] **Step 1: Write the failing test**

`internal/orders/tools_test.go`:
```go
package orders

import (
	"strings"
	"testing"
)

func TestGetOrderStatus(t *testing.T) {
	s := seed(t)
	out, err := getOrderStatus(s, "1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "delivered") {
		t.Errorf("want status in output, got %q", out)
	}
}

func TestGetOrderStatusNotFound(t *testing.T) {
	s := seed(t)
	out, err := getOrderStatus(s, "9999")
	if err != nil {
		t.Fatalf("domain miss must not be a Go error, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "not found") {
		t.Errorf("want 'not found' message, got %q", out)
	}
}

func TestListRecentOrders(t *testing.T) {
	s := seed(t)
	out, err := listRecentOrders(s, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1055") || !strings.Contains(out, "1023") {
		t.Errorf("want all alice orders listed, got %q", out)
	}
}

func TestInitiateRefundNotRefundable(t *testing.T) {
	s := seed(t)
	out, err := initiateRefund(s, "1055")
	if err != nil {
		t.Fatalf("must not be a Go error, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "not refundable") {
		t.Errorf("want not-refundable message, got %q", out)
	}
}

func TestToolsCount(t *testing.T) {
	s := seed(t)
	if got := len(Tools(s)); got != 5 {
		t.Errorf("want 5 tools, got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orders/ -run TestGetOrderStatus -v`
Expected: FAIL — `undefined: getOrderStatus`.

- [ ] **Step 3: Write minimal implementation**

`internal/orders/tools.go`:
```go
package orders

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

func getOrderStatus(s *Store, id string) (string, error) {
	o, ok := s.Get(id)
	if !ok {
		return fmt.Sprintf("Order %s not found.", id), nil
	}
	return fmt.Sprintf("Order %s (%s) is %s. Amount: %.2f %s.", o.ID, o.Item, o.Status, o.Amount, o.Currency), nil
}

func listRecentOrders(s *Store, customer string) (string, error) {
	list := s.ByCustomer(customer)
	if len(list) == 0 {
		return fmt.Sprintf("No orders found for customer %q.", customer), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Recent orders for %s:\n", customer)
	for _, o := range list {
		fmt.Fprintf(&b, "- #%s %s (%s, %.2f %s, %s)\n", o.ID, o.Item, o.Status, o.Amount, o.Currency, o.Created)
	}
	return b.String(), nil
}

func getSalesStats(s *Store, period string) (string, error) {
	st, ok := s.Stats(period)
	if !ok {
		return fmt.Sprintf("No sales statistics for period %q.", period), nil
	}
	return fmt.Sprintf("Sales for %s: %d orders, revenue %.2f %s.", st.Period, st.Orders, st.Revenue, st.Currency), nil
}

func initiateRefund(s *Store, id string) (string, error) {
	o, err := s.Refund(id)
	switch {
	case errors.Is(err, ErrNotFound):
		return fmt.Sprintf("Cannot refund: order %s not found.", id), nil
	case errors.Is(err, ErrNotRefundable):
		return fmt.Sprintf("Cannot refund: order %s is not refundable.", id), nil
	case err != nil:
		return "", err
	}
	return fmt.Sprintf("Refund initiated for order %s (%.2f %s).", o.ID, o.Amount, o.Currency), nil
}

func findOrder(s *Store, query string) (string, error) {
	q := strings.TrimSpace(strings.TrimPrefix(query, "#"))
	if o, ok := s.Get(q); ok {
		return getOrderStatus(s, o.ID)
	}
	// fall back to item substring match across all customers
	var hits []Order
	for _, c := range []string{"alice", "bob"} {
		for _, o := range s.ByCustomer(c) {
			if strings.Contains(strings.ToLower(o.Item), strings.ToLower(query)) {
				hits = append(hits, o)
			}
		}
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No order matched %q.", query), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Matches for %q:\n", query)
	for _, o := range hits {
		fmt.Fprintf(&b, "- #%s %s (%s)\n", o.ID, o.Item, o.Status)
	}
	return b.String(), nil
}

// argument structs (adk derives the tool JSON schema from these)
type idArgs struct {
	OrderID string `json:"order_id" description:"The order identifier, e.g. 1041"`
}
type customerArgs struct {
	Customer string `json:"customer" description:"The customer name"`
}
type periodArgs struct {
	Period string `json:"period" description:"Period in YYYY-MM format"`
}
type queryArgs struct {
	Query string `json:"query" description:"Free-text order search query"`
}

func mustTool(t tool.Tool, err error) tool.Tool {
	if err != nil {
		panic(err)
	}
	return t
}

// Tools returns the order tools bound to the given store.
func Tools(s *Store) []tool.Tool {
	return []tool.Tool{
		mustTool(functiontool.New(functiontool.Config{Name: "find_order", Description: "Find an order by id or item text."},
			func(_ tool.Context, a queryArgs) (string, error) { return findOrder(s, a.Query) })),
		mustTool(functiontool.New(functiontool.Config{Name: "get_order_status", Description: "Get the status of an order by id."},
			func(_ tool.Context, a idArgs) (string, error) { return getOrderStatus(s, a.OrderID) })),
		mustTool(functiontool.New(functiontool.Config{Name: "list_recent_orders", Description: "List a customer's recent orders, newest first."},
			func(_ tool.Context, a customerArgs) (string, error) { return listRecentOrders(s, a.Customer) })),
		mustTool(functiontool.New(functiontool.Config{Name: "get_sales_stats", Description: "Get sales statistics for a period (YYYY-MM)."},
			func(_ tool.Context, a periodArgs) (string, error) { return getSalesStats(s, a.Period) })),
		mustTool(functiontool.New(functiontool.Config{Name: "initiate_refund", Description: "Initiate a refund for an order by id."},
			func(_ tool.Context, a idArgs) (string, error) { return initiateRefund(s, a.OrderID) })),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/orders/ -v`
Expected: PASS. (If the `functiontool.New` signature differed in Task 0, fix the `Tools` wrapper now and re-run.)

- [ ] **Step 5: Commit**

```bash
git add internal/orders/tools.go internal/orders/tools_test.go && git commit -m "feat(orders): adk function tools over the store"
```

---

### Task 4: OpenAI-compatible model adapter + stub model

**Files:**
- Create: `internal/llm/openai.go`, `internal/llm/openai_test.go`, `internal/llm/stub.go`

**Interfaces:**
- Consumes: `config.LLMConfig` (Task 1).
- Produces:
  - `func New(cfg config.LLMConfig) *Model` where `*Model` implements `adkmodel.LLM` (`Name() string`, `GenerateContent(ctx, *adkmodel.LLMRequest, bool) iter.Seq2[*adkmodel.LLMResponse, error]`).
  - `type Stub struct { Script []StubTurn }` implementing `adkmodel.LLM`; `type StubTurn struct { Text string; Call *genai.FunctionCall }`. Each call to `GenerateContent` returns the next scripted turn (text response, or a single function-call response).
  - `func NewStub(turns ...StubTurn) *Stub`.

> The translation between adk's genai-based `LLMRequest`/`LLMResponse` and the OpenAI chat API is the version-sensitive part. Write it against the symbols confirmed in Task 0, Step 2. The mapping:
> - **Request:** `req.Config.SystemInstruction` → an OpenAI `system` message; each `genai.Content` in `req.Contents` → an OpenAI message (`Role` user/model→assistant/tool); a `Part` carrying a `FunctionResponse` → an OpenAI `tool` message; `req.Tools` declarations → OpenAI `tools` with JSON schema.
> - **Response:** OpenAI assistant text → `LLMResponse{Content: genai.NewContentFromText(text, genai.RoleModel), TurnComplete:true}`; an OpenAI `tool_calls` entry → `LLMResponse` whose `Content` carries a `genai.FunctionCall` part.
> If a confirmed genai constructor differs (e.g. `genai.NewContentFromText` vs building the struct literally), use the real one; the structure of the mapping is unchanged.

- [ ] **Step 1: Write the failing test (stub first — it needs no network)**

`internal/llm/openai_test.go`:
```go
package llm

import (
	"context"
	"testing"

	"google.golang.org/genai"
)

func TestStubReturnsScriptedText(t *testing.T) {
	s := NewStub(StubTurn{Text: "hello"})
	var got string
	for resp, err := range s.GenerateContent(context.Background(), nil, false) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != nil && len(resp.Content.Parts) > 0 {
			got = resp.Content.Parts[0].GetText()
		}
	}
	if got != "hello" {
		t.Errorf("want 'hello', got %q", got)
	}
}

func TestStubReturnsScriptedFunctionCall(t *testing.T) {
	s := NewStub(StubTurn{Call: &genai.FunctionCall{Name: "get_order_status", Args: map[string]any{"order_id": "1041"}}})
	var fc *genai.FunctionCall
	for resp, err := range s.GenerateContent(context.Background(), nil, false) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != nil && len(resp.Content.Parts) > 0 {
			fc = resp.Content.Parts[0].GetFunctionCall()
		}
	}
	if fc == nil || fc.Name != "get_order_status" {
		t.Fatalf("want function call get_order_status, got %+v", fc)
	}
}

func TestStubAdvancesPerCall(t *testing.T) {
	s := NewStub(StubTurn{Text: "first"}, StubTurn{Text: "second"})
	read := func() string {
		var out string
		for resp := range s.GenerateContent(context.Background(), nil, false) {
			_ = resp
		}
		return out
	}
	_ = read
	// first call consumed turn 0, second call consumes turn 1
	if s.idx != 1 {
		t.Fatalf("after one call idx should be 1, got %d", s.idx)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestStub -v`
Expected: FAIL — `undefined: NewStub`.

- [ ] **Step 3: Implement the stub**

`internal/llm/stub.go`:
```go
// Package llm adapts a local OpenAI-compatible endpoint (LM Studio) to adk's
// model.LLM interface, and provides a deterministic stub for tests.
package llm

import (
	"context"
	"iter"

	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

// StubTurn is one scripted model response: either Text or a single Call.
type StubTurn struct {
	Text string
	Call *genai.FunctionCall
}

// Stub is a deterministic adkmodel.LLM for tests. It does no network I/O.
type Stub struct {
	Script []StubTurn
	idx    int
}

func NewStub(turns ...StubTurn) *Stub { return &Stub{Script: turns} }

func (s *Stub) Name() string { return "stub" }

func (s *Stub) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		var turn StubTurn
		if s.idx < len(s.Script) {
			turn = s.Script[s.idx]
		}
		s.idx++
		var part genai.Part
		if turn.Call != nil {
			part = genai.Part{FunctionCall: turn.Call}
		} else {
			part = genai.Part{Text: turn.Text}
		}
		yield(&adkmodel.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []genai.Part{part}},
			TurnComplete: true,
		}, true == true && true) // see note below
	}
}
```

> The `yield(..., nil)` second argument is the error. In the literal above replace the placeholder boolean expression with `nil`. (Written this way only to flag: the second yield arg is `error`, pass `nil` on success.) If Task 0 showed `genai.Part` is an interface with constructors (`genai.Text(...)`, `genai.NewPartFromFunctionCall(...)`) rather than a struct with exported fields, use those constructors here and in `openai.go`.

Replace the final `yield(...)` line with:
```go
		yield(&adkmodel.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []genai.Part{part}},
			TurnComplete: true,
		}, nil)
```

- [ ] **Step 4: Run stub tests to verify they pass**

Run: `go test ./internal/llm/ -run TestStub -v`
Expected: PASS. Fix genai part construction per Task 0 findings if the build fails.

- [ ] **Step 5: Write the failing test for the real adapter (against a fake OpenAI HTTP server)**

Append to `internal/llm/openai_test.go`:
```go
import (
	"net/http"
	"net/http/httptest"
)

func TestModelCallsEndpointAndReturnsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	m := New(configLLM(srv.URL + "/v1"))
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []genai.Part{{Text: "ping"}}}},
	}
	var got string
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != nil && len(resp.Content.Parts) > 0 {
			got = resp.Content.Parts[0].GetText()
		}
	}
	if got != "pong" {
		t.Errorf("want 'pong', got %q", got)
	}
}
```
Add the import for `adkmodel "google.golang.org/adk/model"` to the test file, and a helper:
```go
func configLLM(base string) config.LLMConfig {
	return config.LLMConfig{BaseURL: base, Model: "local-model", APIKey: "test"}
}
```
(import `config "github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"`).

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/llm/ -run TestModelCalls -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 7: Implement the adapter**

`internal/llm/openai.go`:
```go
package llm

import (
	"context"
	"iter"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

type Model struct {
	client openai.Client
	model  string
}

func New(cfg config.LLMConfig) *Model {
	c := openai.NewClient(
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	)
	return &Model{client: c, model: cfg.Model}
}

func (m *Model) Name() string { return m.model }

func (m *Model) GenerateContent(ctx context.Context, req *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		msgs := toOpenAIMessages(req)
		params := openai.ChatCompletionNewParams{
			Model:    m.model,
			Messages: msgs,
		}
		if tools := toOpenAITools(req); len(tools) > 0 {
			params.Tools = tools
		}
		resp, err := m.client.Chat.Completions.New(ctx, params)
		if err != nil {
			yield(nil, err)
			return
		}
		if len(resp.Choices) == 0 {
			yield(&adkmodel.LLMResponse{TurnComplete: true}, nil)
			return
		}
		choice := resp.Choices[0].Message
		// Tool call takes precedence over text.
		if len(choice.ToolCalls) > 0 {
			tc := choice.ToolCalls[0]
			args := parseJSONObject(tc.Function.Arguments)
			part := genai.Part{FunctionCall: &genai.FunctionCall{Name: tc.Function.Name, Args: args}}
			yield(&adkmodel.LLMResponse{
				Content:      &genai.Content{Role: genai.RoleModel, Parts: []genai.Part{part}},
				TurnComplete: true,
			}, nil)
			return
		}
		yield(&adkmodel.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []genai.Part{{Text: choice.Content}}},
			TurnComplete: true,
		}, nil)
	}
}
```

Add the translation helpers in the same file:
```go
import (
	"encoding/json"
)

func parseJSONObject(s string) map[string]any {
	out := map[string]any{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func toOpenAIMessages(req *adkmodel.LLMRequest) []openai.ChatCompletionMessageParamUnion {
	var msgs []openai.ChatCompletionMessageParamUnion
	if req.Config != nil && req.Config.SystemInstruction != nil {
		msgs = append(msgs, openai.SystemMessage(contentText(req.Config.SystemInstruction)))
	}
	for _, c := range req.Contents {
		switch c.Role {
		case genai.RoleUser:
			msgs = append(msgs, openai.UserMessage(contentText(c)))
		case genai.RoleModel:
			if fc := firstFunctionCall(c); fc != nil {
				args, _ := json.Marshal(fc.Args)
				msgs = append(msgs, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{{
							ID:   fc.Name,
							Type: "function",
							Function: openai.ChatCompletionMessageToolCallFunctionParam{
								Name:      fc.Name,
								Arguments: string(args),
							},
						}},
					},
				})
				continue
			}
			msgs = append(msgs, openai.AssistantMessage(contentText(c)))
		case genai.RoleFunction, genai.RoleTool:
			if fr := firstFunctionResponse(c); fr != nil {
				b, _ := json.Marshal(fr.Response)
				msgs = append(msgs, openai.ToolMessage(string(b), fr.Name))
			}
		}
	}
	return msgs
}

func toOpenAITools(req *adkmodel.LLMRequest) []openai.ChatCompletionToolParam {
	// req.Tools maps tool name -> adk tool; adk fills FunctionDeclarations into
	// req.Config.Tools. Verify the exact field in Task 0 and map each declaration
	// (Name, Description, Parameters JSON schema) to openai.ChatCompletionToolParam.
	// Returning nil is acceptable for a first pass; tool-calling still works because
	// LM Studio infers from the assistant/tool message history, but declaring tools
	// improves reliability. Implement using the confirmed declaration type.
	return nil
}

func contentText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var s string
	for _, p := range c.Parts {
		s += p.GetText()
	}
	return s
}

func firstFunctionCall(c *genai.Content) *genai.FunctionCall {
	for _, p := range c.Parts {
		if fc := p.GetFunctionCall(); fc != nil {
			return fc
		}
	}
	return nil
}

func firstFunctionResponse(c *genai.Content) *genai.FunctionResponse {
	for _, p := range c.Parts {
		if fr := p.GetFunctionResponse(); fr != nil {
			return fr
		}
	}
	return nil
}
```

> The exact `openai-go` message/tool constructor names (`openai.UserMessage`, `openai.ToolMessage`, `ChatCompletionToolParam`) are confirmed via `go doc github.com/openai/openai-go` — run it before implementing if the build complains, and adjust constructor names to the installed version. The `toOpenAITools` body is the one place left as a documented follow-up (returning `nil` is functional); fill it once Task 0 confirms how adk surfaces `FunctionDeclaration`s on `LLMRequest`.

- [ ] **Step 8: Run all llm tests to verify they pass**

Run: `go test ./internal/llm/ -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/llm && git commit -m "feat(llm): OpenAI-compatible model adapter + stub model"
```

---

### Task 5: Worker agent + A2A server with input-required

**Files:**
- Create: `internal/agent/worker.go`, `internal/a2abridge/server.go`, `internal/a2abridge/server_test.go`, `cmd/worker/main.go`

**Interfaces:**
- Consumes: `orders.Tools` (Task 3), `adkmodel.LLM` (Task 4), `config.WorkerConfig` (Task 1).
- Produces:
  - `func agent.NewWorker(model adkmodel.LLM, tools []tool.Tool) (agent.Agent, error)` — adk LlmAgent with the order tools and a system instruction that ends with: *"If you are missing information required to act (e.g. which order), reply with exactly one line `NEED_INPUT: <your question>` and nothing else."*
  - `func a2abridge.NewExecutor(runner *runner.Runner) a2asrv.AgentExecutor` — runs the adk runner per A2A message; session id = A2A `contextID`; if the final model text starts with `NEED_INPUT:`, emits a status update with `a2a.TaskStateInputRequired` carrying the question; otherwise emits an artifact with the answer + `a2a.TaskStateCompleted`.
  - `func a2abridge.AgentCard(publicURL string) a2a.AgentCard` — minimal card (name `orders-agent`, one skill `manage_orders`).

> `const needInputPrefix = "NEED_INPUT:"` lives in `internal/a2abridge/server.go` and is the single source of truth for the sentinel.

- [ ] **Step 1: Write the failing test (executor round trip with stub model)**

`internal/a2abridge/server_test.go`:
```go
package a2abridge

import (
	"context"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"
	"google.golang.org/genai"
)

func newTestRunner(t *testing.T, model *llm.Stub, store *orders.Store) *runner.Runner {
	t.Helper()
	ag, err := agent.NewWorker(model, orders.Tools(store))
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: "test", Agent: ag, SessionService: session.InMemoryService()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExecutorEmitsInputRequired(t *testing.T) {
	store := seedStore(t)
	model := llm.NewStub(llm.StubTurn{Text: "NEED_INPUT: Which order id should I refund?"})
	exec := NewExecutor(newTestRunner(t, model, store))

	states := runExecutor(t, exec, "refund my order")
	if last := states[len(states)-1]; last != a2a.TaskStateInputRequired {
		t.Fatalf("want final state input-required, got %v", last)
	}
}

func TestExecutorCompletesWithAnswer(t *testing.T) {
	store := seedStore(t)
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "get_order_status", Args: map[string]any{"order_id": "1041"}}},
		llm.StubTurn{Text: "Order 1041 is delivered."},
	)
	exec := NewExecutor(newTestRunner(t, model, store))

	states, text := runExecutorCollect(t, exec, "status of 1041")
	if last := states[len(states)-1]; last != a2a.TaskStateCompleted {
		t.Fatalf("want completed, got %v", last)
	}
	if !strings.Contains(text, "1041") {
		t.Errorf("want answer mentioning 1041, got %q", text)
	}
}
```
Add the test helpers (`seedStore`, `runExecutor`, `runExecutorCollect`) in the same file:
```go
func seedStore(t *testing.T) *orders.Store {
	t.Helper()
	// reuse the orders package seed shape
	p := t.TempDir() + "/o.json"
	body := `{"orders":[{"id":"1041","customer":"alice","item":"Hub","amount":34.5,"currency":"EUR","status":"delivered","created":"2026-06-10","refundable":true}],"sales_stats":[]}`
	if err := writeFile(p, body); err != nil {
		t.Fatal(err)
	}
	s, err := orders.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func runExecutor(t *testing.T, exec a2asrvAgentExecutor, text string) []a2a.TaskState {
	states, _ := runExecutorCollect(t, exec, text)
	return states
}
```
> Replace `a2asrvAgentExecutor` with the real `a2asrv.AgentExecutor` type and implement `runExecutorCollect` to call `exec.Execute(ctx, execCtx)` with a constructed `*a2asrv.ExecutorContext` (message + new context id), iterating the returned `iter.Seq2[a2a.Event, error]`, recording each `*a2a.TaskStatusUpdateEvent.Status.State` and accumulating artifact text. Use the exact `ExecutorContext` constructor confirmed in Task 0; `writeFile` is `os.WriteFile` with `0o600`.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/a2abridge/ -run TestExecutor -v`
Expected: FAIL — `undefined: NewExecutor` / `agent.NewWorker`.

- [ ] **Step 3: Implement the worker agent**

`internal/agent/worker.go`:
```go
// Package agent builds the orchestrator and worker LlmAgents.
package agent

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const workerInstruction = `You are the orders-agent. You manage customer orders using the provided tools.
Use tools to look up orders, statuses, statistics, and to initiate refunds.
If you are missing information required to act (for example, which order id to refund when several match), reply with exactly one line:
NEED_INPUT: <your question>
and nothing else. Otherwise answer the user clearly and concisely.`

func NewWorker(model adkmodel.LLM, tools []tool.Tool) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "orders_agent",
		Description: "Manages orders, statuses, statistics and refunds.",
		Model:       model,
		Instruction: workerInstruction,
		Tools:       tools,
	})
}
```
> If Task 0 showed `llmagent.New` returns `*llmagent.LLMAgent` (concrete) implementing `agent.Agent`, the return type `agent.Agent` still holds. Adjust the import/type names to the confirmed package (the docs use both `agent.Agent` and `agent.New`).

- [ ] **Step 4: Implement the executor and agent card**

`internal/a2abridge/server.go`:
```go
// Package a2abridge connects adk agents to the a2a-go server and client.
package a2abridge

import (
	"context"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

const needInputPrefix = "NEED_INPUT:"

type executor struct{ runner *runner.Runner }

func NewExecutor(r *runner.Runner) a2asrv.AgentExecutor { return &executor{runner: r} }

func (e *executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}

		userText := ec.Message.Parts[0].Text()
		sessionID := string(ec.ContextID) // adk session keyed by A2A context for history continuity
		msg := &genai.Content{Role: genai.RoleUser, Parts: []genai.Part{{Text: userText}}}

		var finalText string
		for event, err := range e.runner.Run(ctx, "a2a-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				yield(nil, err)
				return
			}
			if event != nil && event.LLMResponse.Content != nil {
				for _, p := range event.LLMResponse.Content.Parts {
					if t := p.GetText(); t != "" {
						finalText = t
					}
				}
			}
		}

		if q := strings.TrimSpace(strings.TrimPrefix(finalText, needInputPrefix)); strings.HasPrefix(strings.TrimSpace(finalText), needInputPrefix) {
			ask := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(q))
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, ask), nil)
			return
		}
		if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(finalText)), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
}

func (e *executor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

func AgentCard(publicURL string) a2a.AgentCard {
	return a2a.AgentCard{
		Name:        "orders-agent",
		Description: "Manages orders, statuses, statistics and refunds.",
		URL:         publicURL,
		Version:     "0.1.0",
		Skills: []a2a.AgentSkill{{
			ID:          "manage_orders",
			Name:        "Manage orders",
			Description: "Look up orders, statuses, sales statistics, and initiate refunds.",
		}},
	}
}
```
> The `a2a.NewStatusUpdateEvent` third arg type (status message) and `AgentCard`/`AgentSkill` field names are version-sensitive — reconcile with Task 0's `go doc` output. The control flow (submitted → working → input-required | artifact+completed) is the contract to preserve.

- [ ] **Step 5: Run executor tests to verify they pass**

Run: `go test ./internal/a2abridge/ -run TestExecutor -v`
Expected: PASS.

- [ ] **Step 6: Wire `cmd/worker/main.go`**

```go
package main

import (
	"log"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2abridge"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"
)

func main() {
	cfg, err := config.LoadWorker("configs/worker.yaml")
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store, err := orders.Load(cfg.DataPath)
	if err != nil {
		log.Fatalf("orders: %v", err)
	}
	model := llm.New(cfg.LLM)
	ag, err := agent.NewWorker(model, orders.Tools(store))
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	r, err := runner.New(runner.Config{AppName: "orders", Agent: ag, SessionService: session.InMemoryService()})
	if err != nil {
		log.Fatalf("runner: %v", err)
	}
	handler := a2asrv.NewHandler(a2abridge.NewExecutor(r))
	// Expose agent card + A2A endpoints. Use the http wrapper confirmed in Task 0;
	// fall back to a2asrv's provided REST/JSON-RPC mux registration.
	http.Handle("/", a2asrv.NewHTTPHandler(handler, a2abridge.AgentCard(cfg.PublicURL)))
	log.Printf("orders-agent listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, nil))
}
```
> `a2asrv.NewHTTPHandler` is a placeholder name for whatever the installed a2a-go exposes to turn a `RequestHandler` + `AgentCard` into an `http.Handler` (the README shows `http.Handle("/", restOrJSONRPCHandler)`). Confirm the exact constructor in Task 0 and use it; the card must be served at the well-known path the client resolver expects.

- [ ] **Step 7: Build and commit**

Run:
```bash
go build ./... && go test ./internal/a2abridge/ -v
git add internal/agent/worker.go internal/a2abridge cmd/worker && git commit -m "feat(worker): adk worker agent + A2A server with input-required"
```
Expected: build OK, tests PASS.

---

### Task 6: ask_orders_agent client tool with pending-task state

**Files:**
- Create: `internal/a2abridge/client.go`, `internal/a2abridge/client_test.go`

**Interfaces:**
- Consumes: `a2aclient` (resolved against the worker URL), `tool.Tool` (adk).
- Produces:
  - `type OrdersClient struct { ... }` holding the A2A client and a `map[string]pending` (key = orchestrator session id) where `pending{ taskID, contextID }`.
  - `func NewOrdersClient(ctx, workerURL string) (*OrdersClient, error)` — resolves the worker AgentCard and builds the A2A client.
  - `func (c *OrdersClient) Tool() tool.Tool` — an adk tool named `ask_orders_agent` taking `{ message string }`. Behaviour:
    1. If a pending task exists for this session, send the message **into that task** (same taskID/contextID) — this resumes an `input-required` task.
    2. Otherwise send a fresh message.
    3. If the worker returns `input-required`: store the pending task and return to the orchestrator LLM: `"NEEDS_USER_INPUT: <question>"` (the orchestrator's instruction tells it to ask the user, then call `ask_orders_agent` again with the answer).
    4. If the worker returns `completed`: clear any pending task and return the artifact/answer text.
  - Session id is obtained from the adk `tool.Context` (the invocation/session id).

- [ ] **Step 1: Write the failing test (in-process worker server)**

`internal/a2abridge/client_test.go`:
```go
package a2abridge

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
)

func startWorker(t *testing.T, model *llm.Stub) string {
	t.Helper()
	store := seedStore(t)
	ag, _ := agent.NewWorker(model, nil) // tools not needed; stub drives behaviour
	r, _ := runner.New(runner.Config{AppName: "t", Agent: ag, SessionService: session.InMemoryService()})
	h := a2asrv.NewHandler(NewExecutor(r))
	srv := httptest.NewServer(a2asrv.NewHTTPHandler(h, AgentCard("")))
	t.Cleanup(srv.Close)
	_ = store
	return srv.URL
}

func TestClientRelaysInputRequiredThenCompletes(t *testing.T) {
	// First worker run asks for input; second run (resume) completes.
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
		t.Fatalf("want NEEDS_USER_INPUT, got %q", first)
	}

	second, err := c.ask(context.Background(), sess, "1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "Refund initiated") {
		t.Fatalf("want completion text, got %q", second)
	}
}
```
> `c.ask(ctx, sessionID, msg)` is the testable core behind the adk tool wrapper; `Tool()` adapts it to adk by pulling the session id from `tool.Context`. Implement `ask` and have `Tool()` call it.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/a2abridge/ -run TestClientRelays -v`
Expected: FAIL — `undefined: NewOrdersClient`.

- [ ] **Step 3: Implement the client tool**

`internal/a2abridge/client.go`:
```go
package a2abridge

import (
	"context"
	"fmt"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

type pending struct {
	taskID    a2a.TaskID
	contextID a2a.ContextID
}

type OrdersClient struct {
	client  *a2aclient.Client
	mu      sync.Mutex
	pending map[string]pending
}

func NewOrdersClient(ctx context.Context, workerURL string) (*OrdersClient, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, workerURL)
	if err != nil {
		return nil, fmt.Errorf("resolve worker card: %w", err)
	}
	cl, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		return nil, fmt.Errorf("a2a client: %w", err)
	}
	return &OrdersClient{client: cl, pending: map[string]pending{}}, nil
}

func (c *OrdersClient) ask(ctx context.Context, sessionID, text string) (string, error) {
	c.mu.Lock()
	p, hasPending := c.pending[sessionID]
	c.mu.Unlock()

	var msg *a2a.Message
	if hasPending {
		msg = a2a.NewMessageForTask(a2a.MessageRoleUser, a2a.TaskInfo{TaskID: p.taskID, ContextID: p.contextID}, a2a.NewTextPart(text))
	} else {
		msg = a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	}

	res, err := c.client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return "", fmt.Errorf("orders agent unreachable: %w", err)
	}

	switch r := res.(type) {
	case *a2a.Message:
		c.clear(sessionID)
		return r.Parts[0].Text(), nil
	case *a2a.Task:
		if r.Status.State == a2a.TaskStateInputRequired {
			c.mu.Lock()
			c.pending[sessionID] = pending{taskID: r.ID, contextID: r.ContextID}
			c.mu.Unlock()
			return "NEEDS_USER_INPUT: " + statusMessageText(r), nil
		}
		c.clear(sessionID)
		return taskResultText(r), nil
	default:
		return "", fmt.Errorf("unexpected A2A result type %T", res)
	}
}

func (c *OrdersClient) clear(sessionID string) {
	c.mu.Lock()
	delete(c.pending, sessionID)
	c.mu.Unlock()
}

type askArgs struct {
	Message string `json:"message" description:"What to ask or tell the orders agent"`
}

func (c *OrdersClient) Tool() tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name:        "ask_orders_agent",
		Description: "Delegate an order-related request to the orders agent. If it returns NEEDS_USER_INPUT, ask the user that question, then call this tool again with their answer.",
	}, func(tc tool.Context, a askArgs) (string, error) {
		return c.ask(tc.Context(), sessionIDFrom(tc), a.Message)
	})
	if err != nil {
		panic(err)
	}
	return t
}
```
Add the small helpers (reconcile names with Task 0 `go doc`):
```go
// statusMessageText extracts the question text from an input-required task.
func statusMessageText(t *a2a.Task) string {
	if t.Status.Message != nil && len(t.Status.Message.Parts) > 0 {
		return t.Status.Message.Parts[0].Text()
	}
	return "The orders agent needs more information."
}

// taskResultText returns the last artifact/message text of a completed task.
func taskResultText(t *a2a.Task) string {
	if len(t.Artifacts) > 0 && len(t.Artifacts[len(t.Artifacts)-1].Parts) > 0 {
		return t.Artifacts[len(t.Artifacts)-1].Parts[0].Text()
	}
	if len(t.History) > 0 {
		last := t.History[len(t.History)-1]
		if len(last.Parts) > 0 {
			return last.Parts[0].Text()
		}
	}
	return "Done."
}

// sessionIDFrom derives a stable per-conversation key from the adk tool context.
func sessionIDFrom(tc tool.Context) string { return tc.SessionID() }
```
> `tool.Context`'s accessor for the session id (`SessionID()` vs `Session().ID()` vs `InvocationID()`) and `a2a.Task` field names (`Artifacts`, `History`, `Status.Message`) are version-sensitive; confirm in Task 0 and adjust. The pending-task keying contract (same session → same worker task) must hold.

- [ ] **Step 4: Run client tests to verify they pass**

Run: `go test ./internal/a2abridge/ -run TestClientRelays -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/a2abridge/client.go internal/a2abridge/client_test.go && git commit -m "feat(orchestrator): ask_orders_agent A2A client tool with input-required relay"
```

---

### Task 7: Orchestrator agent + minimal TUI

**Files:**
- Create: `internal/agent/orchestrator.go`, `internal/tui/repl.go`, `cmd/orchestrator/main.go`

**Interfaces:**
- Consumes: `OrdersClient.Tool()` (Task 6), `adkmodel.LLM` (Task 4), `config.OrchestratorConfig`.
- Produces:
  - `func agent.NewOrchestrator(model adkmodel.LLM, ordersTool tool.Tool) (agent.Agent, error)` — LlmAgent whose instruction tells it to use `ask_orders_agent` for anything order-related and, when that tool returns `NEEDS_USER_INPUT:`, to ask the user that exact question and wait.
  - `func tui.Run(ctx context.Context, r *runner.Runner) error` — REPL loop: read a line, run the orchestrator, print assistant text; colorized prompts.

- [ ] **Step 1: Implement the orchestrator agent**

`internal/agent/orchestrator.go`:
```go
package agent

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const orchestratorInstruction = `You are a customer-support orchestrator. The user talks only to you.
For anything about orders, statuses, sales statistics, or refunds, call the ask_orders_agent tool with a clear message.
If ask_orders_agent returns a line starting with "NEEDS_USER_INPUT:", ask the user exactly that question, then on their next message call ask_orders_agent again with their answer.
Keep replies short and friendly. Assume the current customer is "alice" unless told otherwise.`

func NewOrchestrator(model adkmodel.LLM, ordersTool tool.Tool) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "orchestrator",
		Description: "Talks to the user and delegates order work to the orders agent.",
		Model:       model,
		Instruction: orchestratorInstruction,
		Tools:       []tool.Tool{ordersTool},
	})
}
```

- [ ] **Step 2: Implement the TUI REPL**

`internal/tui/repl.go`:
```go
// Package tui is a minimal REPL front-end for the orchestrator agent.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

const (
	cyan  = "\033[36m"
	gray  = "\033[90m"
	reset = "\033[0m"
)

func Run(ctx context.Context, r *runner.Runner) error {
	fmt.Printf("%sOrders Assistant.%s Type your request, or 'exit' to quit.\n", cyan, reset)
	in := bufio.NewScanner(os.Stdin)
	const userID, sessID = "tui-user", "tui-session"
	for {
		fmt.Printf("%syou>%s ", cyan, reset)
		if !in.Scan() {
			return in.Err()
		}
		line := in.Text()
		if line == "exit" || line == "quit" {
			return nil
		}
		if line == "" {
			continue
		}
		msg := &genai.Content{Role: genai.RoleUser, Parts: []genai.Part{{Text: line}}}
		for event, err := range r.Run(ctx, userID, sessID, msg, agent.RunConfig{}) {
			if err != nil {
				fmt.Printf("%s[error] %v%s\n", gray, err, reset)
				break
			}
			if event != nil && event.LLMResponse.Content != nil {
				for _, p := range event.LLMResponse.Content.Parts {
					if t := p.GetText(); t != "" {
						fmt.Printf("%sassistant>%s %s\n", cyan, reset, t)
					}
				}
			}
		}
	}
}
```

- [ ] **Step 3: Wire `cmd/orchestrator/main.go`**

```go
package main

import (
	"context"
	"log"

	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2abridge"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/tui"
)

func main() {
	ctx := context.Background()
	cfg, err := config.LoadOrchestrator("configs/orchestrator.yaml")
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	oc, err := a2abridge.NewOrdersClient(ctx, cfg.WorkerURL)
	if err != nil {
		log.Fatalf("orders client (is the worker running at %s?): %v", cfg.WorkerURL, err)
	}
	model := llm.New(cfg.LLM)
	ag, err := agent.NewOrchestrator(model, oc.Tool())
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	r, err := runner.New(runner.Config{AppName: "orchestrator", Agent: ag, SessionService: session.InMemoryService()})
	if err != nil {
		log.Fatalf("runner: %v", err)
	}
	if err := tui.Run(ctx, r); err != nil {
		log.Fatalf("tui: %v", err)
	}
}
```

- [ ] **Step 4: Build everything and commit**

Run:
```bash
go build ./... && go vet ./...
git add internal/agent/orchestrator.go internal/tui cmd/orchestrator && git commit -m "feat(orchestrator): orchestrator agent + minimal TUI REPL"
```
Expected: build + vet clean.

---

### Task 8: End-to-end integration test (in-process, stub model)

**Files:**
- Create: `internal/a2abridge/e2e_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: a single test that exercises orchestrator → A2A → worker → input-required → resume → completed, all in-process with stub models (no LM Studio).

- [ ] **Step 1: Write the failing test**

`internal/a2abridge/e2e_test.go`:
```go
package a2abridge

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
)

func TestEndToEndRefundWithClarification(t *testing.T) {
	// Worker: turn 1 asks for input, turn 2 completes the refund.
	workerModel := llm.NewStub(
		llm.StubTurn{Text: "NEED_INPUT: Which order id should I refund?"},
		llm.StubTurn{Text: "Refund initiated for order 1041."},
	)
	store := seedStore(t)
	wag, _ := agent.NewWorker(workerModel, nil)
	wr, _ := runner.New(runner.Config{AppName: "w", Agent: wag, SessionService: session.InMemoryService()})
	srv := httptest.NewServer(a2asrv.NewHTTPHandler(a2asrv.NewHandler(NewExecutor(wr)), AgentCard("")))
	defer srv.Close()
	_ = store

	oc, err := NewOrdersClient(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// Drive the client tool directly to model the orchestrator's two turns.
	sess := "e2e"
	r1, err := oc.ask(context.Background(), sess, "I want a refund for my last order")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r1, "NEEDS_USER_INPUT:") {
		t.Fatalf("turn 1 should need input, got %q", r1)
	}
	r2, err := oc.ask(context.Background(), sess, "order 1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r2, "Refund initiated") {
		t.Fatalf("turn 2 should complete refund, got %q", r2)
	}
	_ = genai.RoleUser // keep import if unused after edits
}
```

- [ ] **Step 2: Run it to verify it fails, then passes**

Run: `go test ./internal/a2abridge/ -run TestEndToEnd -v`
Expected: initially may fail if helpers need adjustment; iterate until PASS. This proves the full A2A `input-required`/resume contract holds with the same worker task across two client turns.

- [ ] **Step 3: Run the whole suite and commit**

Run:
```bash
go test ./... -v
git add internal/a2abridge/e2e_test.go && git commit -m "test(e2e): orchestrator→worker input-required round trip in-process"
```
Expected: all PASS.

---

### Task 9: Docker, Compose, and README tutorial

**Files:**
- Create: `Dockerfile`, `docker-compose.yml`, `README.md`

**Interfaces:**
- Consumes: the two binaries `cmd/worker`, `cmd/orchestrator`.
- Produces: a reproducible run via `docker compose`, plus tutorial docs.

- [ ] **Step 1: Create a multi-stage `Dockerfile`**

```dockerfile
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGET=worker
RUN CGO_ENABLED=0 go build -o /out/app ./cmd/${TARGET}

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/app /app/app
COPY configs/ /app/configs/
COPY data/ /app/data/
ENTRYPOINT ["/app/app"]
```

- [ ] **Step 2: Create `docker-compose.yml`**

```yaml
services:
  worker:
    build:
      context: .
      args: { TARGET: worker }
    environment:
      WORKER_LISTEN_ADDR: ":8081"
      WORKER_PUBLIC_URL: "http://worker:8081"
      LLM_BASE_URL: "http://host.docker.internal:1234/v1"
    extra_hosts: ["host.docker.internal:host-gateway"]
    ports: ["8081:8081"]

  orchestrator:
    build:
      context: .
      args: { TARGET: orchestrator }
    depends_on: [worker]
    environment:
      WORKER_URL: "http://worker:8081"
      LLM_BASE_URL: "http://host.docker.internal:1234/v1"
    extra_hosts: ["host.docker.internal:host-gateway"]
    stdin_open: true
    tty: true
```

- [ ] **Step 3: Write `README.md`**

Include: what A2A is and how this demo shows it; the architecture diagram from the spec; prerequisites (Go 1.23+, LM Studio with a tool-capable model loaded, server started on `:1234`); how to run locally (`go run ./cmd/worker` then `go run ./cmd/orchestrator`); how to run via `docker compose up worker -d` then `docker compose run --rm orchestrator`; a walkthrough of the refund-with-clarification scenario showing where `input-required` happens; how tests run without LM Studio (`go test ./...`). Keep it concise and example-driven.

- [ ] **Step 4: Verify and commit**

Run:
```bash
go build ./... && go test ./...
docker compose config >/dev/null   # validates compose syntax
git add Dockerfile docker-compose.yml README.md && git commit -m "docs: Dockerfile, compose, and README tutorial"
```
Expected: build + tests PASS, compose validates.

---

## Notes on residual API verification

This plan pins behaviour and control flow precisely. A handful of exact symbol
names in the young `adk-go` and `a2a-go` libraries are confirmed in **Task 0,
Step 2** with `go doc`; where the installed version differs, adjust the named
constructors/types in place — the tests encode the contract and will catch
regressions. The two spots most likely to need adaptation:

1. `genai.Part` construction (struct fields vs constructors) — affects
   `internal/llm` and any place building `genai.Content`.
2. The a2a-go HTTP handler constructor and `ExecutorContext` shape — affects
   `cmd/worker/main.go` and the executor tests.
