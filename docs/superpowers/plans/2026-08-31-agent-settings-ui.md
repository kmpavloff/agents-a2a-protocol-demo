# Настройка интеграции с агентами из UI — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Завести, изменить и удалить удалённого A2A-агента прямо из веб-интерфейса, чтобы правка действовала немедленно, без перезапуска оркестратора.

**Architecture:** Базовый список агентов остаётся в рукописном `configs/orchestrator.yaml`. Правки из UI ложатся отдельным overlay-файлом `configs/agents.local.yaml`, который перекрывает записи целиком по `id`. Новый пакет `internal/agentstore` владеет слиянием и записью, а `a2abridge.Registry` получает `Apply`, пересобирающий только изменившихся агентов, — соединения и зависшие HITL-задачи остальных не страдают.

**Tech Stack:** Go 1.26 (`net/http` с шаблонами `метод + {id}`, `gopkg.in/yaml.v3`), Lit 3 + TypeScript + Vite во фронтенде.

**Spec:** `docs/superpowers/specs/2026-08-31-agent-settings-ui-design.md`

## Global Constraints

- Пароль агента **никогда** не уходит в ответе HTTP. Наружу — только `auth.hasPassword: true|false`.
- Env-перекрытия (`A2A_AGENT_<ID>_URL`, `A2A_AGENT_<ID>_PASSWORD`) применяются **после** слияния overlay и остаются последним словом.
- Overlay-запись перекрывает агента **целиком**, а не по полям; позиция агента в списке при этом сохраняется.
- `config.AgentConfig` состоит только из строк и bool — сравнивается через `==`, без рефлексии.
- Комментарии в коде — по-русски, как во всём проекте; объясняют «почему», а не «что».
- Каждая задача заканчивается зелёным `go build ./... && go test ./...` и коммитом.

---

### Task 1: Слияние overlay с базовым списком

**Files:**
- Create: `internal/agentstore/store.go`
- Test: `internal/agentstore/store_test.go`

**Interfaces:**
- Consumes: `config.AgentConfig` из `internal/config`.
- Produces: `agentstore.Override` (структура: встроенный `config.AgentConfig` + `Hidden bool`), `merge(base []config.AgentConfig, over []Override) []config.AgentConfig` (не экспортируется, используется задачами 4 и далее внутри пакета).

- [ ] **Step 1: Написать падающий тест**

`internal/agentstore/store_test.go`:

```go
package agentstore

import (
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

func base() []config.AgentConfig {
	return []config.AgentConfig{
		{ID: "orders", Name: "Агент заказов", URL: "http://localhost:8081"},
		{ID: "ouroboros", Name: "Ouroboros", URL: "http://192.168.1.68:18800"},
	}
}

// Перекрытие идёт целой записью и не двигает агента по списку: порядок задаёт
// пункты селектора, и агент, которому поменяли адрес, не должен прыгать в конец.
func TestMergeOverridesInPlace(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "orders", Name: "Мой воркер", URL: "http://127.0.0.1:9000"}},
	})
	if len(got) != 2 {
		t.Fatalf("agents: got %d, want 2", len(got))
	}
	if got[0].ID != "orders" || got[0].URL != "http://127.0.0.1:9000" || got[0].Name != "Мой воркер" {
		t.Errorf("перекрытая запись: %+v", got[0])
	}
	if got[1].ID != "ouroboros" || got[1].URL != "http://192.168.1.68:18800" {
		t.Errorf("нетронутая запись изменилась: %+v", got[1])
	}
}

// Поля, которых нет в overlay-записи, не подтягиваются из базовой: запись
// перекрывает агента целиком.
func TestMergeOverrideReplacesWholeRecord(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "ouroboros", URL: "http://192.168.1.68:18800"}},
	})
	if got[1].Name != "" {
		t.Errorf("имя должно исчезнуть вместе с перекрытой записью, got %q", got[1].Name)
	}
}

func TestMergeHiddenDropsAgent(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "orders"}, Hidden: true},
	})
	if len(got) != 1 || got[0].ID != "ouroboros" {
		t.Fatalf("скрытый агент не убран: %+v", got)
	}
}

func TestMergeAppendsUnknownIDs(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}},
		{AgentConfig: config.AgentConfig{ID: "billing", URL: "http://localhost:9200"}},
	})
	if len(got) != 4 {
		t.Fatalf("agents: got %d, want 4", len(got))
	}
	if got[2].ID != "shop" || got[3].ID != "billing" {
		t.Errorf("новые агенты должны идти в конец в порядке overlay: %+v", got)
	}
}

// Пустой overlay ничего не меняет: без файла демо работает ровно как раньше.
func TestMergeWithoutOverrides(t *testing.T) {
	got := merge(base(), nil)
	if len(got) != 2 || got[0].ID != "orders" || got[1].ID != "ouroboros" {
		t.Errorf("базовый список изменился: %+v", got)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/agentstore/ -run TestMerge -v`
Expected: FAIL — `undefined: merge`, `undefined: Override`.

- [ ] **Step 3: Реализовать слияние**

`internal/agentstore/store.go`:

```go
// Package agentstore хранит правки списка агентов, сделанные из веб-интерфейса.
//
// Базовый список приезжает из configs/orchestrator.yaml и остаётся рукописным:
// в нём живут комментарии, объясняющие каждого агента, и переписывать его
// машинно значило бы их потерять. Правки UI ложатся отдельным overlay-файлом
// поверх базового списка.
package agentstore

import (
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// Override — одна overlay-запись: агент целиком плюс признак «скрыт».
//
// Hidden живёт здесь, а не в config.AgentConfig: он описывает не агента, а
// решение UI убрать его из списка. Агент, заведённый в YAML, иначе не
// удаляется — базовый файл мы не трогаем.
type Override struct {
	config.AgentConfig `yaml:",inline"`
	Hidden             bool `yaml:"hidden,omitempty"`
}

// merge накладывает overlay на базовый список.
//
// Запись с совпавшим id заменяет базовую целиком и остаётся на её месте:
// порядок задаёт пункты селектора в браузере и выбор агента для терминального
// REPL, и менять его из-за правки адреса нельзя. Записи с незнакомыми id — это
// заведённые через UI агенты, они уходят в конец в порядке overlay.
func merge(base []config.AgentConfig, over []Override) []config.AgentConfig {
	byID := make(map[string]Override, len(over))
	for _, o := range over {
		byID[o.ID] = o
	}
	used := make(map[string]bool, len(over))
	out := make([]config.AgentConfig, 0, len(base)+len(over))
	for _, b := range base {
		o, ok := byID[b.ID]
		if !ok {
			out = append(out, b)
			continue
		}
		used[o.ID] = true
		if o.Hidden {
			continue
		}
		out = append(out, o.AgentConfig)
	}
	for _, o := range over {
		if used[o.ID] || o.Hidden {
			continue
		}
		out = append(out, o.AgentConfig)
	}
	return out
}
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `go test ./internal/agentstore/ -v`
Expected: PASS, пять тестов.

- [ ] **Step 5: Коммит**

```bash
git add internal/agentstore/
git commit -m "feat(agentstore): слияние overlay с базовым списком агентов"
```

---

### Task 2: Чтение и атомарная запись overlay-файла

**Files:**
- Create: `internal/agentstore/file.go`
- Test: `internal/agentstore/file_test.go`

**Interfaces:**
- Consumes: `Override` из задачи 1.
- Produces: `loadOverlay(path string) ([]Override, error)` — отсутствующий файл даёт `nil, nil`; `saveOverlay(path string, over []Override) error` — атомарная запись через временный файл и `os.Rename`.

- [ ] **Step 1: Написать падающий тест**

`internal/agentstore/file_test.go`:

```go
package agentstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// Отсутствие overlay — обычное состояние свежего клона, а не ошибка.
func TestLoadOverlayMissingFile(t *testing.T) {
	over, err := loadOverlay(filepath.Join(t.TempDir(), "нет-такого.yaml"))
	if err != nil {
		t.Fatalf("loadOverlay: %v", err)
	}
	if over != nil {
		t.Errorf("overlay: got %+v, want nil", over)
	}
}

func TestSaveLoadOverlayRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	want := []Override{
		{AgentConfig: config.AgentConfig{
			ID: "ouroboros", Name: "Ouroboros", URL: "http://192.168.1.68:18800",
			Verbatim: true, Timeout: "240s",
			Auth: config.AuthConfig{Type: "basic", Username: "ouroboros", Password: "test"},
		}},
		{AgentConfig: config.AgentConfig{ID: "orders"}, Hidden: true},
	}
	if err := saveOverlay(p, want); err != nil {
		t.Fatalf("saveOverlay: %v", err)
	}
	got, err := loadOverlay(p)
	if err != nil {
		t.Fatalf("loadOverlay: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("overlay: got %d записей, want 2", len(got))
	}
	if got[0] != want[0] {
		t.Errorf("первая запись:\n got %+v\nwant %+v", got[0], want[0])
	}
	if !got[1].Hidden || got[1].ID != "orders" {
		t.Errorf("вторая запись: %+v", got[1])
	}
}

// Шапка объясняет тому, кто откроет файл руками, что его перезапишут.
func TestSaveOverlayWritesHeader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	if err := saveOverlay(p, nil); err != nil {
		t.Fatalf("saveOverlay: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "#") {
		t.Errorf("файл должен начинаться с комментария-предупреждения:\n%s", b)
	}
}

// Запись идёт через временный файл: оборванная запись не должна оставить
// половину конфига — и не должна оставить мусор рядом.
func TestSaveOverlayLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agents.local.yaml")
	if err := saveOverlay(p, []Override{{AgentConfig: config.AgentConfig{ID: "a", URL: "http://x"}}}); err != nil {
		t.Fatalf("saveOverlay: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "agents.local.yaml" {
		t.Errorf("в каталоге остался мусор: %v", entries)
	}
}

// Пароль лежит в файле открытым текстом — права должны это учитывать.
func TestSaveOverlayFilePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	if err := saveOverlay(p, nil); err != nil {
		t.Fatalf("saveOverlay: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("права: got %o, want 600", fi.Mode().Perm())
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/agentstore/ -run Overlay -v`
Expected: FAIL — `undefined: loadOverlay`, `undefined: saveOverlay`.

- [ ] **Step 3: Реализовать чтение и запись**

`internal/agentstore/file.go`:

```go
package agentstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// overlayHeader объясняет тому, кто откроет файл руками, почему его правки
// проживут до первого сохранения из UI.
const overlayHeader = "# Управляется из веб-интерфейса (экран «Настройки»).\n" +
	"# Правки руками будут перезаписаны при следующем сохранении.\n"

// overlayDoc — форма файла. Отдельный тип, а не голый список: так overlay
// выглядит как orchestrator.yaml, и в него позже можно добавить поля, не ломая
// уже написанные файлы.
type overlayDoc struct {
	Agents []Override `yaml:"agents"`
}

// loadOverlay читает overlay. Отсутствие файла — обычное состояние свежего
// клона, а не ошибка: правок из UI просто ещё не было.
func loadOverlay(path string) ([]Override, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agents overlay %s: %w", path, err)
	}
	var doc overlayDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse agents overlay %s: %w", path, err)
	}
	return doc.Agents, nil
}

// saveOverlay пишет overlay целиком, атомарно: сначала временный файл рядом,
// потом переименование. Оборванная запись иначе оставила бы половину списка
// агентов, и оркестратор не поднялся бы вовсе.
func saveOverlay(path string, over []Override) error {
	body, err := yaml.Marshal(overlayDoc{Agents: over})
	if err != nil {
		return fmt.Errorf("marshal agents overlay: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Временный файл — в том же каталоге: переименование атомарно только в
	// пределах одной файловой системы.
	tmp, err := os.CreateTemp(dir, ".agents-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp overlay: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op после удачного Rename
	// Пароли лежат открытым текстом, как и в orchestrator.yaml.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp overlay: %w", err)
	}
	if _, err := tmp.WriteString(overlayHeader); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp overlay: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp overlay: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp overlay: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp overlay to %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `go test ./internal/agentstore/ -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/agentstore/
git commit -m "feat(agentstore): чтение и атомарная запись overlay-файла"
```

---

### Task 3: Экспорт валидации агента и путь overlay в конфиге

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `config.ValidateAgent(a AgentConfig) error` — проверка одной записи (id, url, timeout, auth.type), без env и без проверки на дубликаты;
  - `config.NormalizeAgents(agents []AgentConfig) error` — прежнее тело `applyAgentDefaults`: умолчания, env-перекрытия, валидация, отказ на дубликатах; правит слайс на месте;
  - `config.AgentEnvVar(id, field string) string` — имя переменной окружения (`field` — `"URL"` или `"PASSWORD"`);
  - `OrchestratorConfig.AgentsOverlayPath string` (`yaml:"agents_overlay_path"`), по умолчанию `configs/agents.local.yaml`, перекрывается `A2A_AGENTS_OVERLAY_PATH`.

- [ ] **Step 1: Написать падающий тест**

Дописать в `internal/config/config_test.go`:

```go
func TestValidateAgentRejectsBadRecords(t *testing.T) {
	cases := map[string]AgentConfig{
		"пустой id":       {ID: "", URL: "http://x"},
		"id с заглавными": {ID: "Orders", URL: "http://x"},
		"без url":         {ID: "orders"},
		"кривой timeout":  {ID: "orders", URL: "http://x", Timeout: "полчаса"},
		"чужой auth":      {ID: "orders", URL: "http://x", Auth: AuthConfig{Type: "oauth"}},
	}
	for name, a := range cases {
		if err := ValidateAgent(a); err == nil {
			t.Errorf("%s: ожидалась ошибка", name)
		}
	}
	ok := AgentConfig{ID: "orders", URL: "http://x", Timeout: "30s", Auth: AuthConfig{Type: "basic"}}
	if err := ValidateAgent(ok); err != nil {
		t.Errorf("корректная запись отвергнута: %v", err)
	}
}

// NormalizeAgents — то же, что делает загрузка конфига: умолчания, env, отказ
// на дубликатах. Нужна отдельно, чтобы применить её к слитому списку.
func TestNormalizeAgentsAppliesDefaultsAndEnv(t *testing.T) {
	t.Setenv("A2A_AGENT_ORDERS_URL", "http://from-env:8081")
	agents := []AgentConfig{{ID: "orders", URL: "http://localhost:8081"}}
	if err := NormalizeAgents(agents); err != nil {
		t.Fatalf("NormalizeAgents: %v", err)
	}
	if agents[0].URL != "http://from-env:8081" {
		t.Errorf("env-перекрытие не применено: %q", agents[0].URL)
	}
	if agents[0].CardPath != "/.well-known/agent-card.json" {
		t.Errorf("card_path по умолчанию не подставлен: %q", agents[0].CardPath)
	}
}

func TestNormalizeAgentsRejectsDuplicates(t *testing.T) {
	agents := []AgentConfig{{ID: "orders", URL: "http://a"}, {ID: "orders", URL: "http://b"}}
	if err := NormalizeAgents(agents); err == nil {
		t.Fatal("дубликат id должен быть ошибкой")
	}
}

func TestAgentEnvVarName(t *testing.T) {
	if got := AgentEnvVar("my-agent", "URL"); got != "A2A_AGENT_MY_AGENT_URL" {
		t.Errorf("got %q", got)
	}
}

func TestLoadOrchestratorDefaultsOverlayPath(t *testing.T) {
	p := writeTemp(t, "worker_url: \"http://localhost:8081\"\nllm:\n  base_url: \"http://localhost:1234/v1\"\n")
	cfg, err := LoadOrchestrator(p)
	if err != nil {
		t.Fatalf("LoadOrchestrator: %v", err)
	}
	if cfg.AgentsOverlayPath != "configs/agents.local.yaml" {
		t.Errorf("agents_overlay_path: got %q", cfg.AgentsOverlayPath)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: ValidateAgent`, `undefined: NormalizeAgents`, `undefined: AgentEnvVar`, нет поля `AgentsOverlayPath`.

- [ ] **Step 3: Реализовать**

В `internal/config/config.go` добавить поле в `OrchestratorConfig` (рядом с `A2ALogPath`):

```go
	// AgentsOverlayPath — файл с правками списка агентов, сделанными из UI.
	// Лежит отдельно от этого конфига: тот рукописный, и машинная перезапись
	// стёрла бы объясняющие комментарии.
	AgentsOverlayPath string `yaml:"agents_overlay_path"`
```

Заменить `envAgent` на экспортируемую обёртку и разрезать `applyAgentDefaults`:

```go
// AgentEnvVar возвращает имя переменной окружения для поля агента: пароль
// незачем держать в файле, а адрес приходится подменять при запуске в
// контейнере. field — "URL" или "PASSWORD".
func AgentEnvVar(id, field string) string {
	return "A2A_AGENT_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_" + field
}

// ValidateAgent проверяет одну запись агента — то же, что делает загрузка
// конфига, но без env-перекрытий и без проверки на дубликаты. Отдельно нужна
// затем, что запись из UI проверяется до того, как попадёт в список.
func ValidateAgent(a AgentConfig) error {
	if !agentIDRe.MatchString(a.ID) {
		return fmt.Errorf("agent id %q must match %s", a.ID, agentIDRe)
	}
	if a.URL == "" {
		return fmt.Errorf("agent %q: url is required", a.ID)
	}
	if a.Timeout != "" {
		if d, err := time.ParseDuration(a.Timeout); err != nil || d <= 0 {
			return fmt.Errorf("agent %q: bad timeout %q", a.ID, a.Timeout)
		}
	}
	switch a.Auth.Type {
	case "", "basic":
	default:
		return fmt.Errorf("agent %q: unsupported auth type %q", a.ID, a.Auth.Type)
	}
	return nil
}

// NormalizeAgents подставляет умолчания и env-перекрытия, затем валидирует
// список. Применяется и к списку из YAML, и к слитому с overlay — поэтому
// env остаётся последним словом в обоих случаях.
func NormalizeAgents(agents []AgentConfig) error {
	seen := make(map[string]bool, len(agents))
	for i := range agents {
		a := &agents[i]
		// Адрес перекрывается окружением: в контейнере агент живёт по другому
		// имени, чем на машине разработчика, а конфиг один и тот же.
		if u := os.Getenv(AgentEnvVar(a.ID, "URL")); u != "" {
			a.URL = u
		}
		if err := ValidateAgent(*a); err != nil {
			return fmt.Errorf("orchestrator config: %w", err)
		}
		if seen[a.ID] {
			return fmt.Errorf("orchestrator config: duplicate agent id %q", a.ID)
		}
		seen[a.ID] = true
		if a.CardPath == "" {
			a.CardPath = defaultCardPath
		}
		if p := os.Getenv(AgentEnvVar(a.ID, "PASSWORD")); p != "" {
			a.Auth.Password = p
		}
	}
	return nil
}
```

Удалить прежние `envAgent` и `applyAgentDefaults`; в `LoadOrchestrator` заменить вызов `applyAgentDefaults(c.Agents)` на `NormalizeAgents(c.Agents)` и добавить умолчание пути рядом с `A2ALogPath`:

```go
	c.AgentsOverlayPath = env("A2A_AGENTS_OVERLAY_PATH", c.AgentsOverlayPath)
	if c.AgentsOverlayPath == "" {
		c.AgentsOverlayPath = "configs/agents.local.yaml"
	}
```

> Порядок внутри `NormalizeAgents` изменился намеренно: env-адрес подставляется **до** валидации, иначе запись с пустым `url` в файле и заданным `A2A_AGENT_<ID>_URL` отвергалась бы, хотя адрес есть.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `go test ./internal/config/ ./... -count=1`
Expected: PASS — включая старые тесты загрузки конфига.

- [ ] **Step 5: Коммит**

```bash
git add internal/config/
git commit -m "refactor(config): экспорт валидации агента и путь overlay-файла"
```

---

### Task 4: Store — записи для UI и мутации

**Files:**
- Modify: `internal/agentstore/store.go`
- Test: `internal/agentstore/store_test.go`

**Interfaces:**
- Consumes: `merge`, `Override` (задача 1), `loadOverlay`/`saveOverlay` (задача 2), `config.NormalizeAgents`/`ValidateAgent`/`AgentEnvVar` (задача 3).
- Produces:
  - `type Record struct { config.AgentConfig; Hidden bool; Source string; HasPassword bool; EnvLocked []string }`
  - `func New(base []config.AgentConfig, overlayPath string) (*Store, error)`
  - `func (s *Store) OnChange(fn func([]config.AgentConfig))`
  - `func (s *Store) Agents() []config.AgentConfig` — слитые, нормализованные, без скрытых
  - `func (s *Store) Records() []Record` — включая скрытых
  - `func (s *Store) Create(a config.AgentConfig) error`
  - `func (s *Store) Update(id string, a config.AgentConfig) error` — пустой `Auth.Password` сохраняет прежний
  - `func (s *Store) Delete(id string) error`
  - `func (s *Store) Reset(id string) error`
  - `var ErrNotFound`, `var ErrExists`

- [ ] **Step 1: Написать падающий тест**

Дописать в `internal/agentstore/store_test.go`:

```go
import (
	"errors"
	"path/filepath"
	// ...остальные уже есть
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	s, err := New(base(), p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, p
}

func TestStoreCreateAppendsAndPersists(t *testing.T) {
	s, p := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	agents := s.Agents()
	if len(agents) != 3 || agents[2].ID != "shop" {
		t.Fatalf("agents: %+v", agents)
	}
	// Пережило перезапуск: новый Store с тем же файлом видит агента.
	again, err := New(base(), p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(again.Agents()) != 3 {
		t.Errorf("после перечитывания: %+v", again.Agents())
	}
}

func TestStoreCreateRejectsDuplicateAndInvalid(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "orders", URL: "http://x"}); !errors.Is(err, ErrExists) {
		t.Errorf("дубликат id: got %v, want ErrExists", err)
	}
	if err := s.Create(config.AgentConfig{ID: "БОЛЬШИЕ", URL: "http://x"}); err == nil {
		t.Error("невалидный id должен быть отвергнут")
	}
	// Отвергнутая запись не должна осесть в состоянии.
	if len(s.Agents()) != 2 {
		t.Errorf("список изменился после неудачных Create: %+v", s.Agents())
	}
}

func TestStoreUpdateOverridesFileAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("orders", config.AgentConfig{ID: "orders", Name: "Свой", URL: "http://127.0.0.1:9000"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	agents := s.Agents()
	if agents[0].URL != "http://127.0.0.1:9000" || agents[0].Name != "Свой" {
		t.Errorf("правка не применилась: %+v", agents[0])
	}
	recs := s.Records()
	if recs[0].Source != "ui" {
		t.Errorf("источник: got %q, want ui", recs[0].Source)
	}
}

// Пустой пароль в запросе означает «не менять»: наружу мы его не отдаём, и
// форма присылает пустое поле каждый раз, когда пароль не трогали.
func TestStoreUpdateKeepsPasswordWhenEmpty(t *testing.T) {
	s, _ := newStore(t)
	withPass := config.AgentConfig{
		ID: "ouroboros", URL: "http://192.168.1.68:18800",
		Auth: config.AuthConfig{Type: "basic", Username: "ouroboros", Password: "секрет"},
	}
	if err := s.Update("ouroboros", withPass); err != nil {
		t.Fatalf("Update: %v", err)
	}
	noPass := withPass
	noPass.Auth.Password = ""
	noPass.Name = "Новое имя"
	if err := s.Update("ouroboros", noPass); err != nil {
		t.Fatalf("Update: %v", err)
	}
	agents := s.Agents()
	if agents[1].Auth.Password != "секрет" {
		t.Errorf("пароль потерян: %q", agents[1].Auth.Password)
	}
	if agents[1].Name != "Новое имя" {
		t.Errorf("остальные поля не применились: %+v", agents[1])
	}
}

func TestStoreUpdateUnknownAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("нет-такого", config.AgentConfig{ID: "нет-такого", URL: "http://x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// Файлового агента удалить из YAML мы не можем — помечаем скрытым. В списке
// настроек он остаётся, иначе вернуть его можно было бы только правкой файла.
func TestStoreDeleteHidesFileAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Delete("orders"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if agents := s.Agents(); len(agents) != 1 || agents[0].ID != "ouroboros" {
		t.Fatalf("agents: %+v", agents)
	}
	recs := s.Records()
	if len(recs) != 2 || recs[0].ID != "orders" || !recs[0].Hidden {
		t.Fatalf("скрытый агент должен остаться в Records: %+v", recs)
	}
}

// Агент, заведённый через UI, удаляется насовсем: скрывать нечего.
func TestStoreDeleteRemovesUIAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("shop"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, r := range s.Records() {
		if r.ID == "shop" {
			t.Errorf("UI-агент должен исчезнуть целиком: %+v", r)
		}
	}
}

func TestStoreResetRestoresFileVersion(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("orders", config.AgentConfig{ID: "orders", URL: "http://127.0.0.1:9000"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("ouroboros"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset("orders"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := s.Reset("ouroboros"); err != nil {
		t.Fatalf("Reset скрытого: %v", err)
	}
	agents := s.Agents()
	if len(agents) != 2 || agents[0].URL != "http://localhost:8081" {
		t.Errorf("сброс не вернул версию из файла: %+v", agents)
	}
	if s.Records()[0].Source != "file" {
		t.Errorf("источник после сброса: %q", s.Records()[0].Source)
	}
	if err := s.Reset("orders"); !errors.Is(err, ErrNotFound) {
		t.Errorf("сброс без overlay-записи: got %v, want ErrNotFound", err)
	}
}

// Правка адреса, заданного переменной окружения, ничего не изменит — UI обязан
// об этом сказать, иначе пользователь чинит несуществующую поломку.
func TestStoreRecordsMarksEnvLocked(t *testing.T) {
	t.Setenv("A2A_AGENT_ORDERS_URL", "http://from-env:8081")
	s, _ := newStore(t)
	recs := s.Records()
	if recs[0].URL != "http://from-env:8081" {
		t.Errorf("показан адрес из файла, а не действующий: %q", recs[0].URL)
	}
	if len(recs[0].EnvLocked) != 1 || recs[0].EnvLocked[0] != "url" {
		t.Errorf("envLocked: %+v", recs[0].EnvLocked)
	}
}

func TestStoreRecordsReportPasswordWithoutRevealing(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("ouroboros", config.AgentConfig{
		ID: "ouroboros", URL: "http://192.168.1.68:18800",
		Auth: config.AuthConfig{Type: "basic", Username: "u", Password: "секрет"},
	}); err != nil {
		t.Fatal(err)
	}
	r := s.Records()[1]
	if !r.HasPassword {
		t.Error("HasPassword должен быть true")
	}
	if r.Auth.Password != "" {
		t.Errorf("Records не должны нести пароль: %q", r.Auth.Password)
	}
}

func TestStoreNotifiesOnChange(t *testing.T) {
	s, _ := newStore(t)
	var got [][]config.AgentConfig
	s.OnChange(func(agents []config.AgentConfig) { got = append(got, agents) })
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(config.AgentConfig{ID: "orders", URL: "http://x"}); err == nil {
		t.Fatal("ожидался отказ на дубликате")
	}
	if len(got) != 1 {
		t.Fatalf("подписчик вызван %d раз(а), want 1 — только на удачных правках", len(got))
	}
	if len(got[0]) != 3 {
		t.Errorf("подписчику приехал не тот список: %+v", got[0])
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/agentstore/ -v`
Expected: FAIL — `undefined: New`, `undefined: Record`, `undefined: ErrExists`…

- [ ] **Step 3: Реализовать Store**

Дописать в `internal/agentstore/store.go` (импорты: `errors`, `fmt`, `os`, `sync`, `slices`, `config`):

```go
// ErrNotFound — агента с таким id нет; ErrExists — уже есть. Отдельные ошибки
// нужны обработчику: 404 и 409 против 400 у невалидной записи.
var (
	ErrNotFound = errors.New("agent not found")
	ErrExists   = errors.New("agent already exists")
)

// Record — запись для экрана настроек: действующий конфиг агента плюс
// происхождение. Пароль сюда не попадает никогда — только признак, что он
// задан.
type Record struct {
	config.AgentConfig
	Hidden      bool
	Source      string   // "file" — только из YAML, "ui" — есть overlay-запись
	HasPassword bool
	EnvLocked   []string // поля, перекрытые окружением: "url", "password"
}

// Store владеет списком агентов: рукописной базой из orchestrator.yaml и
// overlay-файлом с правками из UI.
type Store struct {
	overlayPath string

	mu       sync.Mutex
	base     []config.AgentConfig
	over     []Override
	merged   []config.AgentConfig // слитый и нормализованный, без скрытых
	onChange func([]config.AgentConfig)
}

// New читает overlay и складывает его с базовым списком. Битый overlay — это
// отказ на старте, а не молчаливое игнорирование: агенты, заведённые через UI,
// не должны исчезать незаметно.
func New(base []config.AgentConfig, overlayPath string) (*Store, error) {
	over, err := loadOverlay(overlayPath)
	if err != nil {
		return nil, err
	}
	s := &Store{overlayPath: overlayPath, base: base, over: over}
	merged, err := s.mergeNormalized(over)
	if err != nil {
		return nil, err
	}
	s.merged = merged
	return s, nil
}

// OnChange задаёт подписчика, которому уезжает новый список после каждой
// удачной правки. Через него живой Registry узнаёт об изменениях.
func (s *Store) OnChange(fn func([]config.AgentConfig)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

// mergeNormalized складывает базу с overlay и прогоняет через ту же
// нормализацию, что и загрузка конфига, — так env-перекрытия остаются
// последним словом и после правок из UI.
func (s *Store) mergeNormalized(over []Override) ([]config.AgentConfig, error) {
	merged := merge(s.base, over)
	if err := config.NormalizeAgents(merged); err != nil {
		return nil, err
	}
	return merged, nil
}

// Agents возвращает действующий список — тот, по которому живёт Registry.
func (s *Store) Agents() []config.AgentConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.merged)
}

// Records описывает агентов для экрана настроек, включая скрытых: вернуть
// удалённого агента иначе можно было бы только правкой файла.
func (s *Store) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	byID := make(map[string]Override, len(s.over))
	for _, o := range s.over {
		byID[o.ID] = o
	}
	out := make([]Record, 0, len(s.base)+len(s.over))
	seen := make(map[string]bool, len(s.over))
	add := func(a config.AgentConfig, hidden, fromOverlay bool) {
		source := "file"
		if fromOverlay {
			source = "ui"
		}
		r := Record{AgentConfig: a, Hidden: hidden, Source: source}
		// Показываем действующее значение, а не то, что лежит в файле: адрес
		// мог быть перекрыт окружением, и правка такого поля ничего не даст.
		if u := os.Getenv(config.AgentEnvVar(a.ID, "URL")); u != "" {
			r.URL = u
			r.EnvLocked = append(r.EnvLocked, "url")
		}
		r.HasPassword = a.Auth.Password != ""
		if os.Getenv(config.AgentEnvVar(a.ID, "PASSWORD")) != "" {
			r.HasPassword = true
			r.EnvLocked = append(r.EnvLocked, "password")
		}
		r.Auth.Password = "" // наружу пароль не уходит никогда
		out = append(out, r)
	}
	for _, b := range s.base {
		if o, ok := byID[b.ID]; ok {
			seen[o.ID] = true
			add(o.AgentConfig, o.Hidden, true)
			continue
		}
		add(b, false, false)
	}
	for _, o := range s.over {
		if seen[o.ID] {
			continue
		}
		add(o.AgentConfig, o.Hidden, true)
	}
	return out
}

// apply — общий хвост всех мутаций: пересчитать, записать файл, зафиксировать
// состояние и позвать подписчика. Пока запись не удалась, состояние прежнее.
func (s *Store) apply(over []Override) error {
	merged, err := s.mergeNormalized(over)
	if err != nil {
		return err
	}
	if err := saveOverlay(s.overlayPath, over); err != nil {
		return err
	}
	s.over, s.merged = over, merged
	// Подписчик зовётся под мьютексом хранилища: так правки доезжают до реестра
	// ровно в том порядке, в каком применялись. Отсюда ограничение — подписчику
	// нельзя ходить обратно в Store, это самоблокировка. Единственный подписчик,
	// Registry.Apply, туда и не ходит: он берёт только свою блокировку.
	if s.onChange != nil {
		s.onChange(slices.Clone(merged))
	}
	return nil
}

// indexOver находит overlay-запись по id.
func indexOver(over []Override, id string) int {
	return slices.IndexFunc(over, func(o Override) bool { return o.ID == id })
}

// known отвечает, знает ли Store такого агента — в базе или в overlay.
func (s *Store) known(id string) bool {
	if slices.ContainsFunc(s.base, func(a config.AgentConfig) bool { return a.ID == id }) {
		return true
	}
	return indexOver(s.over, id) >= 0
}

// Create заводит нового агента. Занятый id — ошибка: правка существующего идёт
// через Update, и молча перетереть его нельзя.
func (s *Store) Create(a config.AgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.ValidateAgent(a); err != nil {
		return err
	}
	if s.known(a.ID) {
		return fmt.Errorf("%w: %q", ErrExists, a.ID)
	}
	return s.apply(append(slices.Clone(s.over), Override{AgentConfig: a}))
}

// Update заменяет запись агента целиком. Пустой пароль означает «не менять»:
// наружу мы его не отдаём, и форма присылает пустое поле каждый раз, когда
// пароль не трогали. Убрать пароль можно, убрав auth целиком.
func (s *Store) Update(id string, a config.AgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = id
	if err := config.ValidateAgent(a); err != nil {
		return err
	}
	if !s.known(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if a.Auth.Password == "" {
		a.Auth.Password = s.currentPassword(id)
	}
	over := slices.Clone(s.over)
	if i := indexOver(over, id); i >= 0 {
		over[i] = Override{AgentConfig: a}
	} else {
		over = append(over, Override{AgentConfig: a})
	}
	return s.apply(over)
}

// currentPassword достаёт действующий пароль агента — из overlay, иначе из базы.
func (s *Store) currentPassword(id string) string {
	if i := indexOver(s.over, id); i >= 0 {
		return s.over[i].Auth.Password
	}
	for _, b := range s.base {
		if b.ID == id {
			return b.Auth.Password
		}
	}
	return ""
}

// Delete убирает агента. Заведённый через UI исчезает совсем, пришедший из
// YAML помечается скрытым: базовый файл рукописный, и мы его не трогаем.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	inBase := slices.ContainsFunc(s.base, func(a config.AgentConfig) bool { return a.ID == id })
	over := slices.Clone(s.over)
	i := indexOver(over, id)
	switch {
	case inBase && i >= 0:
		over[i] = Override{AgentConfig: config.AgentConfig{ID: id}, Hidden: true}
	case inBase:
		over = append(over, Override{AgentConfig: config.AgentConfig{ID: id}, Hidden: true})
	default:
		over = slices.Delete(over, i, i+1)
	}
	return s.apply(over)
}

// Reset забывает overlay-запись: агент возвращается к версии из YAML, а
// скрытый — в список. Без overlay-записи сбрасывать нечего.
func (s *Store) Reset(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := indexOver(s.over, id)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	over := slices.Clone(s.over)
	return s.apply(slices.Delete(over, i, i+1))
}
```

> `Hidden`-запись намеренно хранит только `id`: скрытому агенту остальные поля не нужны, а `Reset` вернёт версию из YAML.
> В `Create`/`Update` валидация идёт до проверки существования — сообщение «кривой id» полезнее, чем «такого агента нет».

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `go test ./internal/agentstore/ -v -count=1`
Expected: PASS, все тесты задач 1, 2 и 4.

- [ ] **Step 5: Коммит**

```bash
git add internal/agentstore/
git commit -m "feat(agentstore): CRUD над списком агентов поверх overlay"
```

---

### Task 5: Registry.Apply, поколения и SetClientInit

**Files:**
- Modify: `internal/a2abridge/registry.go`
- Test: `internal/a2abridge/registry_test.go`

**Interfaces:**
- Produces:
  - `func (g *Registry) Apply(agents []config.AgentConfig)` — дифф по id;
  - `func (g *Registry) Generation() uint64` — растёт при каждом изменении состава;
  - `func (g *Registry) SetClientInit(fn func(*OrdersClient))` — вызывается в `ClientFor` при создании клиента.

- [ ] **Step 1: Написать падающий тест**

Дописать в `internal/a2abridge/registry_test.go`:

```go
// Нетронутый агент должен пережить правку соседа: в его *Remote лежат живое
// соединение и зависшая input-required задача, и терять их из-за чужой правки
// нельзя.
func TestApplyKeepsUnchangedRemotes(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	b := ouroborosCfg(s.URL)
	b.ID = "two"
	g := NewRegistry([]config.AgentConfig{a, b}, nil)

	before, _ := g.Get("one")
	changed := b
	changed.Name = "Другое имя"
	g.Apply([]config.AgentConfig{a, changed})

	after, ok := g.Get("one")
	if !ok || before != after {
		t.Error("нетронутый агент пересоздан")
	}
	two, ok := g.Get("two")
	if !ok || two.Name() != "Другое имя" {
		t.Errorf("изменённый агент не обновлён: %v", ok)
	}
}

func TestApplyAddsAndRemoves(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	g := NewRegistry([]config.AgentConfig{a}, nil)

	b := ouroborosCfg(s.URL)
	b.ID = "two"
	g.Apply([]config.AgentConfig{b})

	if _, ok := g.Get("one"); ok {
		t.Error("удалённый агент остался в реестре")
	}
	if _, ok := g.Get("two"); !ok {
		t.Error("новый агент не появился")
	}
	if ids := g.IDs(); len(ids) != 1 || ids[0] != "two" {
		t.Errorf("порядок: %v", ids)
	}
}

// Изменённый агент теряет и кэшированного клиента: тот держит ссылку на старый
// *Remote и продолжал бы ходить по прежнему адресу.
func TestApplyDropsClientOfChangedAgent(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	g := NewRegistry([]config.AgentConfig{a}, nil)
	before, _ := g.ClientFor("one")

	changed := a
	changed.Name = "Другое"
	g.Apply([]config.AgentConfig{changed})

	after, _ := g.ClientFor("one")
	if before == after {
		t.Error("клиент изменённого агента должен быть пересоздан")
	}
}

func TestApplyBumpsGeneration(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	g := NewRegistry([]config.AgentConfig{a}, nil)
	gen := g.Generation()

	same := a
	g.Apply([]config.AgentConfig{same})
	if g.Generation() != gen {
		t.Error("поколение не должно расти, когда ничего не изменилось")
	}

	changed := a
	changed.URL = "http://127.0.0.1:1"
	g.Apply([]config.AgentConfig{changed})
	if g.Generation() == gen {
		t.Error("поколение должно вырасти после правки")
	}
}

// Клиент, созданный уже после старта, обязан получить те же обработчики, что и
// созданные в конструкторе исполнителя, — иначе виджеты нового агента молча
// пропадут.
func TestClientInitRunsForLaterClients(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	g := NewRegistry([]config.AgentConfig{a}, nil)

	var inited []string
	g.SetClientInit(func(c *OrdersClient) { inited = append(inited, c.Profile().ToolName) })

	b := ouroborosCfg(s.URL)
	b.ID = "two"
	g.Apply([]config.AgentConfig{a, b})
	if _, ok := g.ClientFor("two"); !ok {
		t.Fatal("клиент нового агента не создан")
	}
	if len(inited) != 1 {
		t.Fatalf("SetClientInit вызван %d раз(а), want 1", len(inited))
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/a2abridge/ -run 'TestApply|TestClientInit' -v`
Expected: FAIL — `g.Apply undefined`, `g.Generation undefined`, `g.SetClientInit undefined`.

- [ ] **Step 3: Реализовать**

В `internal/a2abridge/registry.go` добавить поля в `Registry`:

```go
	clientInit func(*OrdersClient) // навешивание обработчиков на нового клиента
	gen        uint64             // поколение состава; растёт при каждом изменении
	cfgs       map[string]config.AgentConfig // конфиг, по которому создан Remote
```

`NewRegistry` заполняет `cfgs` рядом с `remotes`. В `ClientFor` после создания клиента вызвать `clientInit`:

```go
	c := NewOrdersClientFromRemote(r)
	g.clients[id] = c
	if g.clientInit != nil {
		g.clientInit(c)
	}
	return c, true
```

И новые методы:

```go
// SetClientInit задаёт подготовку клиента — навешивание обработчиков виджетов,
// A2UI и файлов. Разовым циклом по IDs это делать нельзя: клиент, созданный
// после правки конфига, остался бы без обработчиков, и виджеты нового агента
// молча пропадали бы.
func (g *Registry) SetClientInit(fn func(*OrdersClient)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.clientInit = fn
	// Клиенты, созданные до подписки, тоже должны быть подготовлены.
	for _, c := range g.clients {
		fn(c)
	}
}

// Generation — номер состава агентов. Растёт при любом изменении списка;
// исполнитель по нему понимает, что кэш runner'ов пора выбросить.
func (g *Registry) Generation() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gen
}

// Apply приводит реестр к новому списку агентов.
//
// Сверка идёт по id: config.AgentConfig состоит только из строк и bool, так что
// сравнение обычное. Нетронутый агент остаётся тем же *Remote — в нём живое
// соединение, разобранная карточка и зависшие input-required задачи, и терять
// их из-за правки соседа нельзя. Изменённый пересоздаётся целиком: cfg читается
// без мьютекса (ID, Name, Verbatim), и править его на месте — гонка.
func (g *Registry) Apply(agents []config.AgentConfig) {
	g.mu.Lock()
	defer g.mu.Unlock()

	order := make([]string, 0, len(agents))
	remotes := make(map[string]*Remote, len(agents))
	clients := make(map[string]*OrdersClient, len(agents))
	cfgs := make(map[string]config.AgentConfig, len(agents))
	changed := len(agents) != len(g.order)

	for _, cfg := range agents {
		order = append(order, cfg.ID)
		cfgs[cfg.ID] = cfg
		if old, ok := g.remotes[cfg.ID]; ok && g.cfgs[cfg.ID] == cfg {
			remotes[cfg.ID] = old
			if c, ok := g.clients[cfg.ID]; ok {
				clients[cfg.ID] = c
			}
			continue
		}
		changed = true
		g.trace.Logf("agent %q: конфиг изменился — пересоздаём соединение", cfg.ID)
		remotes[cfg.ID] = NewRemote(cfg, g.trace)
	}
	if !changed && !slices.Equal(order, g.order) {
		changed = true
	}
	g.order, g.remotes, g.clients, g.cfgs = order, remotes, clients, cfgs
	if changed {
		g.gen++
	}
}
```

Добавить в импорты `slices`.

> Клиент изменённого агента не переносится намеренно: он держит ссылку на старый `*Remote` и продолжал бы ходить по прежнему адресу. Новый создастся лениво в `ClientFor` — уже подготовленным через `clientInit`.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `go test ./internal/a2abridge/ -count=1`
Expected: PASS, включая прежние тесты реестра.

- [ ] **Step 5: Коммит**

```bash
git add internal/a2abridge/registry.go internal/a2abridge/registry_test.go
git commit -m "feat(a2abridge): живое применение нового списка агентов"
```

---

### Task 6: Сброс кэша runner'ов по смене поколения

**Files:**
- Modify: `internal/a2abridge/orchserver.go:60-95` (конструктор), `internal/a2abridge/orchserver.go:110-160` (`runnerFor`)
- Test: `internal/a2abridge/orchserver_test.go`

**Interfaces:**
- Consumes: `Registry.Generation()`, `Registry.SetClientInit` (задача 5).
- Produces: поведение — после `reg.Apply` с изменением состава `runnerFor` собирает runner заново.

- [ ] **Step 1: Написать падающий тест**

Дописать в `internal/a2abridge/orchserver_test.go`:

```go
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
	if ws, _, _ := e.drain("сессия"); len(ws) != 1 {
		t.Errorf("виджет нового агента потерян: %+v", ws)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/a2abridge/ -run 'TestRunnerCache|TestExecutorWires' -v`
Expected: FAIL — второй тест падает на потерянном виджете, первый на `built == 1` после правки.

- [ ] **Step 3: Реализовать**

В `orchExecutor` добавить поле рядом с `runners`:

```go
	gen uint64 // поколение реестра, под которое собран кэш runner'ов
```

В `NewOrchestratorExecutor` заменить цикл `for _, id := range reg.IDs()` на одну подписку — она же покроет и клиентов, созданных позже:

```go
	// Обработчики вешаются на КАЖДОГО клиента, включая созданных после правки
	// конфига: разовый цикл по нынешним агентам оставил бы новых без них, и
	// их виджеты молча пропадали бы.
	reg.SetClientInit(func(c *OrdersClient) {
		c.SetWidgetHandler(func(sessionID string, w map[string]any) {
			e.mu.Lock()
			e.widgets[sessionID] = append(e.widgets[sessionID], w)
			e.mu.Unlock()
		})
		c.SetA2UIHandler(func(sessionID string, msgs []map[string]any) {
			e.mu.Lock()
			e.a2uis[sessionID] = append(e.a2uis[sessionID], msgs...)
			e.mu.Unlock()
		})
		c.SetFileHandler(func(sessionID, filename, mediaType string, data []byte) {
			e.mu.Lock()
			e.files[sessionID] = append(e.files[sessionID], attachedFile{name: filename, mediaType: mediaType, data: data})
			e.mu.Unlock()
		})
	})
	e.gen = reg.Generation()
```

В начало `runnerFor` — сверку поколения:

```go
	// Состав агентов мог измениться из UI. Кэш собран под прежний: в нём и
	// старое описание в промпте, и инструмент удалённого агента.
	if gen := e.reg.Generation(); gen != e.gen {
		e.mu.Lock()
		clear(e.runners)
		e.gen = gen
		e.mu.Unlock()
	}
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `go test ./internal/a2abridge/ -count=1`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/a2abridge/orchserver.go internal/a2abridge/orchserver_test.go
git commit -m "feat(a2abridge): сброс кэша runner'ов при смене состава агентов"
```

---

### Task 7: HTTP-эндпоинты CRUD

**Files:**
- Create: `internal/webui/agentconfig.go`
- Test: `internal/webui/agentconfig_test.go`

**Interfaces:**
- Consumes: `*agentstore.Store` (задача 4).
- Produces: `func RegisterAgentConfig(mux *http.ServeMux, s *agentstore.Store)` — регистрирует `GET|POST /api/agents/config`, `PUT|DELETE /api/agents/config/{id}`, `POST /api/agents/config/{id}/reset`.

- [ ] **Step 1: Написать падающий тест**

`internal/webui/agentconfig_test.go`:

```go
package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agentstore"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

func newMux(t *testing.T) (*http.ServeMux, *agentstore.Store) {
	t.Helper()
	base := []config.AgentConfig{{
		ID: "orders", Name: "Агент заказов", URL: "http://localhost:8081",
		Auth: config.AuthConfig{Type: "basic", Username: "u", Password: "секрет"},
	}}
	s, err := agentstore.New(base, filepath.Join(t.TempDir(), "agents.local.yaml"))
	if err != nil {
		t.Fatalf("agentstore.New: %v", err)
	}
	mux := http.NewServeMux()
	RegisterAgentConfig(mux, s)
	return mux, s
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

// Пароль не уходит наружу ни при каких условиях — только признак, что он задан.
func TestGetAgentConfigNeverRevealsPassword(t *testing.T) {
	mux, _ := newMux(t)
	rec := do(t, mux, http.MethodGet, "/api/agents/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "секрет") || strings.Contains(body, `"password"`) {
		t.Fatalf("в ответе пароль: %s", body)
	}
	if !strings.Contains(body, `"hasPassword":true`) {
		t.Errorf("нет признака заданного пароля: %s", body)
	}
	if !strings.Contains(body, `"source":"file"`) || !strings.Contains(body, `"cardPath"`) {
		t.Errorf("ответ не в ожидаемой форме: %s", body)
	}
}

func TestCreateAgent(t *testing.T) {
	mux, s := newMux(t)
	rec := do(t, mux, http.MethodPost, "/api/agents/config",
		`{"id":"shop","name":"Магазин","url":"http://localhost:9100","verbatim":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: %d, body: %s", rec.Code, rec.Body)
	}
	agents := s.Agents()
	if len(agents) != 2 || agents[1].ID != "shop" || !agents[1].Verbatim {
		t.Errorf("агент не заведён: %+v", agents)
	}
}

func TestCreateAgentRejectsInvalid(t *testing.T) {
	mux, _ := newMux(t)
	rec := do(t, mux, http.MethodPost, "/api/agents/config", `{"id":"shop"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %s", rec.Body)
	}
	if body["error"] == "" {
		t.Errorf("нет описания ошибки: %s", rec.Body)
	}
}

func TestCreateAgentRejectsDuplicate(t *testing.T) {
	mux, _ := newMux(t)
	rec := do(t, mux, http.MethodPost, "/api/agents/config", `{"id":"orders","url":"http://x"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("status: %d, want 409", rec.Code)
	}
}

func TestUpdateAgentKeepsPasswordWhenOmitted(t *testing.T) {
	mux, s := newMux(t)
	rec := do(t, mux, http.MethodPut, "/api/agents/config/orders",
		`{"id":"orders","name":"Свой","url":"http://127.0.0.1:9000","auth":{"type":"basic","username":"u"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body: %s", rec.Code, rec.Body)
	}
	a := s.Agents()[0]
	if a.URL != "http://127.0.0.1:9000" || a.Name != "Свой" {
		t.Errorf("правка не применилась: %+v", a)
	}
	if a.Auth.Password != "секрет" {
		t.Errorf("пароль потерян: %q", a.Auth.Password)
	}
}

func TestUpdateUnknownAgentIs404(t *testing.T) {
	mux, _ := newMux(t)
	rec := do(t, mux, http.MethodPut, "/api/agents/config/нетакого", `{"url":"http://x"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: %d, want 404", rec.Code)
	}
}

func TestDeleteAndResetAgent(t *testing.T) {
	mux, s := newMux(t)
	if rec := do(t, mux, http.MethodDelete, "/api/agents/config/orders", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status: %d", rec.Code)
	}
	if len(s.Agents()) != 0 {
		t.Fatalf("агент не скрыт: %+v", s.Agents())
	}
	if rec := do(t, mux, http.MethodPost, "/api/agents/config/orders/reset", ""); rec.Code != http.StatusOK {
		t.Fatalf("reset status: %d", rec.Code)
	}
	if len(s.Agents()) != 1 {
		t.Errorf("сброс не вернул агента: %+v", s.Agents())
	}
}

// Скрытый агент остаётся в списке настроек — иначе вернуть его можно было бы
// только правкой файла.
func TestHiddenAgentStaysInConfigList(t *testing.T) {
	mux, _ := newMux(t)
	do(t, mux, http.MethodDelete, "/api/agents/config/orders", "")
	rec := do(t, mux, http.MethodGet, "/api/agents/config", "")
	if !strings.Contains(rec.Body.String(), `"hidden":true`) {
		t.Errorf("скрытый агент пропал из списка: %s", rec.Body)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/webui/ -v`
Expected: FAIL — `undefined: RegisterAgentConfig`.

- [ ] **Step 3: Реализовать обработчики**

`internal/webui/agentconfig.go`:

```go
package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agentstore"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// agentOut — форма записи в ответе. Отдельный от входного тип, и это не
// дублирование ради красоты: у него просто нет поля пароля, поэтому вернуть
// пароль наружу нельзя даже по невнимательности.
type agentOut struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	CardPath    string   `json:"cardPath"`
	Skill       string   `json:"skill"`
	Verbatim    bool     `json:"verbatim"`
	Timeout     string   `json:"timeout"`
	Description string   `json:"description"`
	Auth        authOut  `json:"auth"`
	Hidden      bool     `json:"hidden"`
	Source      string   `json:"source"`
	EnvLocked   []string `json:"envLocked"`
}

type authOut struct {
	Type        string `json:"type"`
	Username    string `json:"username"`
	HasPassword bool   `json:"hasPassword"`
}

// agentIn — то, что присылает форма. Пустой password означает «не менять»:
// прочитать текущий браузер не может, и форма шлёт пустое поле каждый раз,
// когда пароль не трогали.
type agentIn struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	CardPath    string `json:"cardPath"`
	Skill       string `json:"skill"`
	Verbatim    bool   `json:"verbatim"`
	Timeout     string `json:"timeout"`
	Description string `json:"description"`
	Auth        struct {
		Type     string `json:"type"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"auth"`
}

func (in agentIn) toConfig() config.AgentConfig {
	return config.AgentConfig{
		ID: in.ID, Name: in.Name, URL: in.URL, CardPath: in.CardPath,
		Skill: in.Skill, Verbatim: in.Verbatim, Timeout: in.Timeout,
		Description: in.Description,
		Auth: config.AuthConfig{
			Type: in.Auth.Type, Username: in.Auth.Username, Password: in.Auth.Password,
		},
	}
}

func toOut(r agentstore.Record) agentOut {
	locked := r.EnvLocked
	if locked == nil {
		locked = []string{} // фронтенд итерирует без проверок на null
	}
	return agentOut{
		ID: r.ID, Name: r.Name, URL: r.URL, CardPath: r.CardPath,
		Skill: r.Skill, Verbatim: r.Verbatim, Timeout: r.Timeout,
		Description: r.Description,
		Auth: authOut{
			Type: r.Auth.Type, Username: r.Auth.Username, HasPassword: r.HasPassword,
		},
		Hidden: r.Hidden, Source: r.Source, EnvLocked: locked,
	}
}

// RegisterAgentConfig вешает на mux редактирование списка агентов.
//
// Эндпоинты ничем не защищены — как и /invoke: это демо-стенд. Значит, режим
// --web нельзя выставлять за пределы доверенной сети: через этот API можно
// подменить адрес агента и увести туда весь разговор.
func RegisterAgentConfig(mux *http.ServeMux, s *agentstore.Store) {
	mux.HandleFunc("GET /api/agents/config", func(w http.ResponseWriter, r *http.Request) {
		recs := s.Records()
		out := make([]agentOut, 0, len(recs))
		for _, rec := range recs {
			out = append(out, toOut(rec))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("POST /api/agents/config", func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeAgent(w, r)
		if !ok {
			return
		}
		writeResult(w, http.StatusCreated, s.Create(in.toConfig()))
	})

	mux.HandleFunc("PUT /api/agents/config/{id}", func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeAgent(w, r)
		if !ok {
			return
		}
		writeResult(w, http.StatusOK, s.Update(r.PathValue("id"), in.toConfig()))
	})

	mux.HandleFunc("DELETE /api/agents/config/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, http.StatusNoContent, s.Delete(r.PathValue("id")))
	})

	mux.HandleFunc("POST /api/agents/config/{id}/reset", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, http.StatusOK, s.Reset(r.PathValue("id")))
	})
}

func decodeAgent(w http.ResponseWriter, r *http.Request) (agentIn, bool) {
	var in agentIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неразбираемый JSON: " + err.Error()})
		return in, false
	}
	return in, true
}

// writeResult переводит ошибку хранилища в код ответа: «нет такого» и «уже
// есть» — не то же самое, что кривая запись, и форма показывает их по-разному.
func writeResult(w http.ResponseWriter, okStatus int, err error) {
	switch {
	case err == nil:
		if okStatus == http.StatusNoContent {
			w.WriteHeader(okStatus)
			return
		}
		writeJSON(w, okStatus, map[string]string{"status": "ok"})
	case errors.Is(err, agentstore.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, agentstore.ErrExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Заголовок уже ушёл — остаётся только не молчать в логе.
		return
	}
}
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `go test ./internal/webui/ -v -count=1`
Expected: PASS, включая прежние тесты `AgentsHandler`.

- [ ] **Step 5: Коммит**

```bash
git add internal/webui/
git commit -m "feat(webui): HTTP-эндпоинты правки списка агентов"
```

---

### Task 8: Проводка в оркестраторе

**Files:**
- Modify: `cmd/orchestrator/main.go:50-95`
- Modify: `configs/orchestrator.example.yaml`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: `agentstore.New`, `Store.Agents`, `Store.OnChange` (задача 4), `Registry.Apply` (задача 5), `webui.RegisterAgentConfig` (задача 7).

- [ ] **Step 1: Собрать overlay в main и подписать реестр**

В `cmd/orchestrator/main.go` после загрузки конфига заменить создание реестра:

```go
	// Правки из UI лежат отдельным файлом поверх рукописного конфига.
	store, err := agentstore.New(cfg.Agents, cfg.AgentsOverlayPath)
	if err != nil {
		log.Fatalf("agents overlay: %v", err)
	}
	agents := store.Agents()
	reg := a2abridge.NewRegistry(agents, trace)
	// Правка из UI доезжает до живого реестра: новый агент появляется в
	// селекторе и в режиме «Авто» без перезапуска.
	store.OnChange(reg.Apply)
```

Заменить `for _, a := range cfg.Agents` на `for _, a := range agents` в стартовом логе и добавить строку про overlay:

```go
	log.Printf("agents (%d), overlay %s:", len(agents), cfg.AgentsOverlayPath)
```

В блоке `if *web` зарегистрировать эндпоинты рядом с `/api/agents`:

```go
		// Правка списка агентов из браузера.
		webui.RegisterAgentConfig(mux, store)
```

Добавить импорт `"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agentstore"`.

- [ ] **Step 2: Дописать пример конфига и .gitignore**

В `configs/orchestrator.example.yaml` перед блоком `a2a_log_path` добавить:

```yaml
# Файл с правками списка агентов, сделанными на экране «Настройки» в браузере.
# Он машинный: перекрывает записи `agents:` по id и переписывается при каждом
# сохранении, поэтому комментарии держите здесь, а не в нём. Значение ниже —
# умолчание; можно не указывать вовсе. Переменная: A2A_AGENTS_OVERLAY_PATH.
agents_overlay_path: "configs/agents.local.yaml"
```

В `.gitignore` рядом с `configs/orchestrator.yaml`:

```
# Правки списка агентов из UI: содержат адреса и пароли конкретного стенда.
configs/agents.local.yaml
```

- [ ] **Step 3: Проверить сборку и весь набор тестов**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 4: Проверить, что оркестратор поднимается и отдаёт список**

```bash
go run ./cmd/orchestrator --web &
sleep 3
curl -s localhost:8080/api/agents/config | head -c 400
curl -s -X POST localhost:8080/api/agents/config \
  -H 'Content-Type: application/json' \
  -d '{"id":"probe","name":"Проба","url":"http://127.0.0.1:9999"}' -w '\n%{http_code}\n'
curl -s localhost:8080/api/agents | head -c 300
curl -s -X DELETE localhost:8080/api/agents/config/probe -w '\n%{http_code}\n'
kill %1
```

Expected: список приезжает без паролей; создание даёт `201`, и агент `probe` появляется в `/api/agents` **без перезапуска**; удаление даёт `204`. Файл `configs/agents.local.yaml` после этого содержит только шапку и пустой список — уберите его: `rm -f configs/agents.local.yaml`.

- [ ] **Step 5: Коммит**

```bash
git add cmd/orchestrator/main.go configs/orchestrator.example.yaml .gitignore
git commit -m "feat(orchestrator): подключение overlay-хранилища агентов"
```

---

### Task 9: Вкладки «Чат» и «Настройки» во фронтенде

**Files:**
- Modify: `web/src/app.ts`

**Interfaces:**
- Produces: элемент `<agent-settings>` монтируется при `_tab === 'settings'`; событие `agents-changed` от него перечитывает `/api/agents`.

- [ ] **Step 1: Добавить состояние вкладки**

В `web/src/app.ts` рядом с прочими `@state()`:

```ts
  // Вкладка: чат или настройки агентов. Хэш нужен, чтобы перезагрузка не
  // выкидывала обратно в чат посреди правки конфига.
  @state() private _tab: 'chat' | 'settings' =
    location.hash === '#settings' ? 'settings' : 'chat';
```

В `connectedCallback` подписаться на смену хэша (кнопкой «назад» в браузере тоже пользуются):

```ts
    this.#onHashChange = () => {
      this._tab = location.hash === '#settings' ? 'settings' : 'chat';
    };
    window.addEventListener('hashchange', this.#onHashChange);
```

Поле и отписка:

```ts
  #onHashChange: (() => void) | undefined;
```

```ts
    if (this.#onHashChange) window.removeEventListener('hashchange', this.#onHashChange);
```

И переключатель:

```ts
  #selectTab(tab: 'chat' | 'settings') {
    this._tab = tab;
    location.hash = tab === 'settings' ? '#settings' : '';
  }
```

- [ ] **Step 2: Добавить разметку вкладок**

Импортировать компонент рядом с прочими импортами:

```ts
import './agent-settings.js';
```

В `render()` над `<h2>`:

```ts
      <nav class="tabs">
        <button
          type="button"
          class=${this._tab === 'chat' ? 'tab active' : 'tab'}
          @click=${() => this.#selectTab('chat')}
        >
          Чат
        </button>
        <button
          type="button"
          class=${this._tab === 'settings' ? 'tab active' : 'tab'}
          @click=${() => this.#selectTab('settings')}
        >
          Настройки
        </button>
      </nav>
      ${this._tab === 'settings'
        ? html`<agent-settings
            @agents-changed=${() => void this.#loadAgents()}
          ></agent-settings>`
        : nothing}
```

Всё, что сейчас возвращает `render()` после `<h2>`, обернуть в один контейнер,
который **прячется стилем, а не размонтируется**. Это четыре существующих блока
подряд, в прежнем порядке и без изменений: `div.agent-bar`, `div.feed`, `form` и
`details.proto`. Открывающий и закрывающий теги ставятся так:

```ts
      <div class="chat" ?hidden=${this._tab !== 'chat'}>
        ${html`<div class="agent-bar">
              <!-- существующая панель агентов целиком, без правок -->
            </div>`}
        <div class="feed"><!-- существующая лента --></div>
        <form><!-- существующая форма ввода --></form>
        <!-- существующий details.proto -->
      </div>
```

Размонтировать чат нельзя: в `_items` лежат живые A2UI-поверхности, и
пересоздание `<a2ui-surface>` при каждом переключении вкладки роняет рендерер
на «Surface … already exists».

- [ ] **Step 3: Добавить стили**

В блок `static styles` дописать:

```css
    /* Чат прячется, но остаётся в DOM: в _items лежат живые A2UI-поверхности,
       и пересоздавать <a2ui-surface> на каждом переключении вкладки —
       напрашиваться на «Surface … already exists». */
    [hidden] {
      display: none !important;
    }
    .tabs {
      display: flex;
      gap: 4px;
      margin-bottom: 12px;
      border-bottom: 1px solid #e1e4e8;
    }
    .tab {
      padding: 8px 16px;
      border: none;
      border-radius: 8px 8px 0 0;
      background: transparent;
      color: #57606a;
      font-size: 14px;
      font-weight: 600;
      cursor: pointer;
    }
    .tab.active {
      background: #f0f1f3;
      color: #0b57d0;
    }
```

- [ ] **Step 4: Проверить сборку**

Run: `cd web && yarn build`
Expected: `tsc` без ошибок (компонент `agent-settings` появится в задаче 10 — до неё сборка упадёт на отсутствующем модуле, поэтому задачи 9 и 10 коммитятся вместе; выполняйте их подряд).

- [ ] **Step 5: Коммит вместе с задачей 10**

Отложить: `web/src/app.ts` коммитится одним коммитом с `agent-settings.ts`.

---

### Task 10: Компонент экрана настроек

**Files:**
- Create: `web/src/agent-settings.ts`

**Interfaces:**
- Consumes: `GET|POST /api/agents/config`, `PUT|DELETE /api/agents/config/{id}`, `POST /api/agents/config/{id}/reset` (задача 7); `GET /api/agents` для живого статуса.
- Produces: `<agent-settings>`, событие `agents-changed` (bubbles, composed) после каждой удачной правки.

- [ ] **Step 1: Написать компонент**

`web/src/agent-settings.ts`:

```ts
import {LitElement, html, css, nothing} from 'lit';
import {customElement, state} from 'lit/decorators.js';

/** Запись агента, как её отдаёт GET /api/agents/config. */
interface AgentConfig {
  id: string;
  name: string;
  url: string;
  cardPath: string;
  skill: string;
  verbatim: boolean;
  timeout: string;
  description: string;
  auth: {type: string; username: string; hasPassword: boolean};
  hidden: boolean;
  source: 'file' | 'ui';
  envLocked: string[];
}

/** Живой статус из GET /api/agents — им же питается селектор в чате. */
interface AgentStatus {
  id: string;
  available: boolean;
  probed: boolean;
}

/** Форма правки: пароль отдельным полем, пустое значит «не менять». */
interface Draft extends AgentConfig {
  password: string;
  isNew: boolean;
}

const EMPTY: Draft = {
  id: '', name: '', url: '', cardPath: '', skill: '', verbatim: false,
  timeout: '', description: '',
  auth: {type: '', username: '', hasPassword: false},
  hidden: false, source: 'ui', envLocked: [], password: '', isNew: true,
};

@customElement('agent-settings')
export class AgentSettings extends LitElement {
  @state() private _agents: AgentConfig[] = [];
  @state() private _status: Record<string, AgentStatus> = {};
  @state() private _draft: Draft | null = null;
  @state() private _error = '';
  @state() private _busy = false;

  connectedCallback() {
    super.connectedCallback();
    void this.#load();
  }

  async #load() {
    try {
      const [cfgRes, statusRes] = await Promise.all([
        fetch('/api/agents/config'),
        fetch('/api/agents'),
      ]);
      if (!cfgRes.ok) throw new Error(`HTTP ${cfgRes.status}`);
      this._agents = (await cfgRes.json()) as AgentConfig[];
      this._error = '';
      if (statusRes.ok) {
        const list = (await statusRes.json()) as AgentStatus[];
        this._status = Object.fromEntries(list.map((s) => [s.id, s]));
      }
    } catch (err) {
      this._error = `Не удалось загрузить список агентов: ${err}`;
    }
  }

  /** Правка применилась — чат должен обновить свой селектор. */
  #announce() {
    this.dispatchEvent(new CustomEvent('agents-changed', {bubbles: true, composed: true}));
  }

  #edit(a: AgentConfig) {
    this._draft = {...a, auth: {...a.auth}, password: '', isNew: false};
    this._error = '';
  }

  #add() {
    this._draft = {...EMPTY, auth: {...EMPTY.auth}};
    this._error = '';
  }

  /** Одно место для всех мутаций: коды ответов у них разные, разбор — общий. */
  async #send(method: string, path: string, body?: unknown): Promise<boolean> {
    this._busy = true;
    try {
      const res = await fetch(path, {
        method,
        headers: body ? {'Content-Type': 'application/json'} : undefined,
        body: body ? JSON.stringify(body) : undefined,
      });
      if (!res.ok) {
        // Сервер объясняет отказ словами — показываем их, а не голый код.
        let msg = `HTTP ${res.status}`;
        try {
          const j = await res.json();
          if (j?.error) msg = j.error;
        } catch {
          /* тело не JSON — остаётся код */
        }
        this._error = msg;
        return false;
      }
      this._error = '';
      await this.#load();
      this.#announce();
      return true;
    } catch (err) {
      this._error = `Запрос не ушёл: ${err}`;
      return false;
    } finally {
      this._busy = false;
    }
  }

  async #save() {
    const d = this._draft;
    if (!d) return;
    const body = {
      id: d.id, name: d.name, url: d.url, cardPath: d.cardPath, skill: d.skill,
      verbatim: d.verbatim, timeout: d.timeout, description: d.description,
      auth: {type: d.auth.type, username: d.auth.username, password: d.password},
    };
    const ok = d.isNew
      ? await this.#send('POST', '/api/agents/config', body)
      : await this.#send('PUT', `/api/agents/config/${encodeURIComponent(d.id)}`, body);
    if (ok) this._draft = null;
  }

  async #remove(a: AgentConfig) {
    if (!confirm(`Удалить агента «${a.name || a.id}»?`)) return;
    if (await this.#send('DELETE', `/api/agents/config/${encodeURIComponent(a.id)}`)) {
      if (this._draft?.id === a.id) this._draft = null;
    }
  }

  async #reset(a: AgentConfig) {
    if (await this.#send('POST', `/api/agents/config/${encodeURIComponent(a.id)}/reset`)) {
      if (this._draft?.id === a.id) this._draft = null;
    }
  }

  #patch(part: Partial<Draft>) {
    if (this._draft) this._draft = {...this._draft, ...part};
  }

  static styles = css`
    :host {
      display: block;
      font-family: system-ui, sans-serif;
      color: #222;
    }
    .cols {
      display: flex;
      gap: 16px;
      align-items: flex-start;
    }
    .list {
      flex: 0 0 260px;
      border: 1px solid #d0d7de;
      border-radius: 10px;
      overflow: hidden;
    }
    .row {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 10px 12px;
      border-bottom: 1px solid #eef1f4;
      cursor: pointer;
    }
    .row:last-child {
      border-bottom: none;
    }
    .row:hover {
      background: #f6f8fa;
    }
    .row.hidden-agent {
      opacity: 0.55;
    }
    .row-main {
      flex: 1;
      min-width: 0;
    }
    .row-name {
      font-weight: 600;
      font-size: 14px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .row-id {
      font-size: 12px;
      color: #8b949e;
    }
    .badge {
      font-size: 11px;
      padding: 1px 7px;
      border-radius: 999px;
      border: 1px solid #d0d7de;
      color: #57606a;
      white-space: nowrap;
    }
    .badge.ok {
      color: #1a7f37;
      border-color: #b7e0c1;
    }
    .badge.down {
      color: #b35900;
      border-color: #f0cba8;
    }
    .add {
      width: 100%;
      padding: 10px;
      border: none;
      border-top: 1px solid #eef1f4;
      background: #f6f8fa;
      color: #0b57d0;
      font-weight: 600;
      cursor: pointer;
    }
    .form {
      flex: 1;
      border: 1px solid #d0d7de;
      border-radius: 10px;
      padding: 16px;
    }
    label {
      display: block;
      margin: 10px 0 4px;
      font-size: 13px;
      font-weight: 600;
      color: #57606a;
    }
    input[type='text'],
    input[type='password'],
    select,
    textarea {
      width: 100%;
      box-sizing: border-box;
      padding: 8px 10px;
      border: 1px solid #ccc;
      border-radius: 8px;
      font-size: 14px;
      font-family: inherit;
    }
    input[disabled] {
      background: #f0f1f3;
      color: #8b949e;
    }
    .hint {
      font-size: 12px;
      color: #8b949e;
      margin-top: 3px;
    }
    .check {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-top: 12px;
      font-size: 14px;
    }
    .actions {
      display: flex;
      gap: 8px;
      margin-top: 18px;
    }
    button.primary {
      padding: 10px 18px;
      border: none;
      border-radius: 8px;
      background: #1177ee;
      color: #fff;
      font-weight: 600;
      cursor: pointer;
    }
    button.ghost {
      padding: 10px 18px;
      border: 1px solid #d0d7de;
      border-radius: 8px;
      background: #fff;
      color: #57606a;
      cursor: pointer;
    }
    button[disabled] {
      opacity: 0.5;
      cursor: default;
    }
    .error {
      margin: 12px 0 0;
      padding: 10px 12px;
      border-radius: 8px;
      background: #fff1f0;
      border: 1px solid #f5c2c0;
      color: #b32020;
      font-size: 13px;
    }
    .empty {
      color: #8b949e;
      font-size: 14px;
    }
  `;

  #renderRow(a: AgentConfig) {
    const st = this._status[a.id];
    return html`<div
      class=${a.hidden ? 'row hidden-agent' : 'row'}
      @click=${() => this.#edit(a)}
    >
      <div class="row-main">
        <div class="row-name">${a.name || a.id}</div>
        <div class="row-id">${a.id} · ${a.source === 'ui' ? 'изменён в UI' : 'из файла'}</div>
      </div>
      ${a.hidden
        ? html`<span class="badge">скрыт</span>`
        : st?.probed
          ? html`<span class=${st.available ? 'badge ok' : 'badge down'}>
              ${st.available ? 'отвечает' : 'не отвечает'}
            </span>`
          : nothing}
    </div>`;
  }

  #renderForm(d: Draft) {
    const lockedURL = d.envLocked.includes('url');
    const lockedPass = d.envLocked.includes('password');
    return html`<div class="form">
      <label for="f-id">Идентификатор</label>
      <input
        id="f-id"
        type="text"
        .value=${d.id}
        ?disabled=${!d.isNew}
        placeholder="например, shop"
        @input=${(e: Event) => this.#patch({id: (e.target as HTMLInputElement).value})}
      />
      <div class="hint">
        ${d.isNew
          ? 'Латиница в нижнем регистре, цифры, дефис. Из него получается имя делегирующего инструмента: ask_<id>.'
          : 'Идентификатор не меняется: чтобы сменить его, заведите агента заново.'}
      </div>

      <label for="f-name">Название</label>
      <input id="f-name" type="text" .value=${d.name}
        placeholder="подпись в селекторе; пусто — имя из AgentCard"
        @input=${(e: Event) => this.#patch({name: (e.target as HTMLInputElement).value})} />

      <label for="f-url">Адрес</label>
      <input id="f-url" type="text" .value=${d.url} ?disabled=${lockedURL}
        placeholder="http://192.168.1.68:18800"
        @input=${(e: Event) => this.#patch({url: (e.target as HTMLInputElement).value})} />
      ${lockedURL
        ? html`<div class="hint">Перекрыто переменной окружения — правка здесь ни на что не повлияет.</div>`
        : nothing}

      <label for="f-card">Путь к AgentCard</label>
      <input id="f-card" type="text" .value=${d.cardPath}
        placeholder="/.well-known/agent-card.json"
        @input=${(e: Event) => this.#patch({cardPath: (e.target as HTMLInputElement).value})} />

      <label for="f-skill">Навык (metadata.skill)</label>
      <input id="f-skill" type="text" .value=${d.skill}
        placeholder="пусто — не отправлять"
        @input=${(e: Event) => this.#patch({skill: (e.target as HTMLInputElement).value})} />

      <label for="f-timeout">Таймаут хода</label>
      <input id="f-timeout" type="text" .value=${d.timeout} placeholder="120s"
        @input=${(e: Event) => this.#patch({timeout: (e.target as HTMLInputElement).value})} />

      <label for="f-descr">Описание для модели</label>
      <textarea id="f-descr" rows="3" .value=${d.description}
        placeholder="чем этот агент отличается от прочих — по нему модель выбирает его в режиме «Авто»"
        @input=${(e: Event) => this.#patch({description: (e.target as HTMLTextAreaElement).value})}></textarea>

      <div class="check">
        <input id="f-verbatim" type="checkbox" .checked=${d.verbatim}
          @change=${(e: Event) => this.#patch({verbatim: (e.target as HTMLInputElement).checked})} />
        <label for="f-verbatim" style="margin:0">Отвечать напрямую, без локальной модели</label>
      </div>

      <label for="f-auth">Аутентификация</label>
      <select id="f-auth"
        @change=${(e: Event) =>
          this.#patch({auth: {...d.auth, type: (e.target as HTMLSelectElement).value}})}>
        <option value="" ?selected=${d.auth.type === ''}>нет</option>
        <option value="basic" ?selected=${d.auth.type === 'basic'}>HTTP Basic</option>
      </select>

      ${d.auth.type === 'basic'
        ? html`
            <label for="f-user">Логин</label>
            <input id="f-user" type="text" .value=${d.auth.username}
              @input=${(e: Event) =>
                this.#patch({auth: {...d.auth, username: (e.target as HTMLInputElement).value}})} />

            <label for="f-pass">Пароль</label>
            <input id="f-pass" type="password" .value=${d.password} ?disabled=${lockedPass}
              placeholder=${d.auth.hasPassword ? 'задан — оставьте пустым, чтобы не менять' : ''}
              @input=${(e: Event) => this.#patch({password: (e.target as HTMLInputElement).value})} />
            <div class="hint">
              ${lockedPass
                ? 'Перекрыт переменной окружения.'
                : 'Пароль не показывается: сервер его не отдаёт.'}
            </div>
          `
        : nothing}

      ${this._error ? html`<p class="error">${this._error}</p>` : nothing}

      <div class="actions">
        <button class="primary" ?disabled=${this._busy} @click=${() => void this.#save()}>
          Сохранить
        </button>
        <button class="ghost" ?disabled=${this._busy} @click=${() => (this._draft = null)}>
          Отмена
        </button>
        ${!d.isNew && d.source === 'ui'
          ? html`<button class="ghost" ?disabled=${this._busy}
              title="Забыть правки из UI и вернуть версию из orchestrator.yaml"
              @click=${() => void this.#reset(d)}>
              Сбросить к конфигу
            </button>`
          : nothing}
        ${!d.isNew && !d.hidden
          ? html`<button class="ghost" ?disabled=${this._busy} @click=${() => void this.#remove(d)}>
              Удалить
            </button>`
          : nothing}
        ${d.hidden
          ? html`<button class="ghost" ?disabled=${this._busy} @click=${() => void this.#reset(d)}>
              Вернуть
            </button>`
          : nothing}
      </div>
    </div>`;
  }

  render() {
    return html`
      <div class="cols">
        <div class="list">
          ${this._agents.length
            ? this._agents.map((a) => this.#renderRow(a))
            : html`<div class="row"><span class="empty">Агентов нет</span></div>`}
          <button class="add" @click=${() => this.#add()}>+ Добавить агента</button>
        </div>
        ${this._draft
          ? this.#renderForm(this._draft)
          : html`<div class="form">
              <p class="empty">Выберите агента слева или добавьте нового.</p>
              ${this._error ? html`<p class="error">${this._error}</p>` : nothing}
            </div>`}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'agent-settings': AgentSettings;
  }
}
```

- [ ] **Step 2: Собрать фронтенд**

Run: `cd web && yarn build`
Expected: `tsc` без ошибок, `internal/webui/dist` заполнен.

- [ ] **Step 3: Проверить в браузере вручную**

```bash
go run ./cmd/orchestrator --web
```

Открыть `http://localhost:8080`, вкладка «Настройки»: список из двух агентов, у обоих бейдж «из файла». Завести агента (`id: probe`, `url: http://127.0.0.1:9999`), убедиться, что он появился в списке и в селекторе на вкладке «Чат», а вкладка «Чат» не потеряла ленту. Удалить `probe`. Затем `rm -f configs/agents.local.yaml`.

- [ ] **Step 4: Проверить полный набор тестов**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS (собранный фронтенд встраивается через `//go:embed`).

- [ ] **Step 5: Коммит**

```bash
git add web/src/app.ts web/src/agent-settings.ts
git commit -m "feat(web): экран настройки агентов"
```

---

### Task 11: Документация и сквозная проверка

**Files:**
- Modify: `README.md`

**Interfaces:** ничего нового не производит; закрывает раздел «Проверка» спеки.

- [ ] **Step 1: Описать возможность в README**

В раздел про `--web` (рядом с описанием селектора агентов) добавить:

```markdown
### Настройка агентов из браузера

В `--web` режиме рядом с чатом есть вкладка **Настройки**: агента можно завести,
отредактировать и удалить прямо в браузере, без перезапуска — новый агент сразу
появляется в селекторе и в режиме «Авто».

Правки не трогают `configs/orchestrator.yaml`: они ложатся отдельным файлом
`configs/agents.local.yaml` (путь настраивается полем `agents_overlay_path`),
который перекрывает записи `agents:` по `id`. Агента, пришедшего из YAML, UI
удалить не может — он помечается скрытым, и кнопка «Вернуть» возвращает его.
Кнопка «Сбросить к конфигу» забывает overlay-запись целиком.

Пароль наружу не отдаётся никогда: список показывает лишь признак «задан», а
пустое поле пароля при сохранении означает «не менять». Адрес и пароль,
заданные переменными `A2A_AGENT_<ID>_URL` и `A2A_AGENT_<ID>_PASSWORD`, остаются
последним словом — такие поля форма показывает заблокированными.

> **Внимание.** Эти эндпоинты, как и `/invoke`, ничем не защищены: любой, кто
> дотянется до порта, может подменить адрес агента и увести туда разговор.
> Оркестратор в режиме `--web` рассчитан на localhost или доверенную сеть.
```

- [ ] **Step 2: Прогнать сквозной сценарий по скиллу `verify`**

Поднять воркер и оркестратор:

```bash
go run ./cmd/worker &
cd web && yarn build && cd ..
go run ./cmd/orchestrator --web &
```

Через `/api/agents/config` завести второго агента, указывающего на того же воркера (`id: probe`, `url: http://localhost:8081`), и убедиться, что разговор с ним идёт **без перезапуска**: `POST /invoke` с `metadata.agentId = "probe"` и заголовком `A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9` на запрос «статус заказа 1041» должен вернуть `application/a2ui+json` DataPart с карточкой заказа. Это и есть проверка того, что обработчики виджетов доехали до клиента, созданного после старта.

- [ ] **Step 3: Снять скриншот экрана настроек**

По рецепту из скилла `verify` (playwright-core во временном каталоге вне репозитория). Драйвер переключает вкладку и снимает форму:

```js
await page.goto('http://localhost:8080#settings', {waitUntil: 'networkidle'});
await page.waitForFunction(
  () => document.querySelector('orders-app')?.shadowRoot
    ?.querySelector('agent-settings')?.shadowRoot?.querySelector('.list'),
  null, {timeout: 15000},
);
await page.evaluate(() => {
  const s = document.querySelector('orders-app').shadowRoot.querySelector('agent-settings');
  s.shadowRoot.querySelectorAll('.row')[1].dispatchEvent(new Event('click', {bubbles: true}));
});
await page.waitForTimeout(500);
await page.screenshot({path: 'settings.png', fullPage: true});
console.log('ERRORS:', errors.length ? errors.join('\n') : 'none');
```

**Посмотреть на картинку.** Форма должна показывать поля выбранного агента, бейдж источника и — если задан `A2A_AGENT_<ID>_URL` — заблокированный адрес с подписью. `ERRORS: none`.

- [ ] **Step 4: Прибраться и проверить всё разом**

```bash
rm -f configs/agents.local.yaml
lsof -ti:8080,8081 | xargs -r kill
go build ./... && go vet ./... && go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add README.md
git commit -m "docs: настройка агентов из браузера"
```
