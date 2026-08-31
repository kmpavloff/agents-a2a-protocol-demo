package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestLoadOrchestratorAppliesEnvOverride(t *testing.T) {
	p := writeTemp(t, "worker_url: \"http://localhost:8081\"\nllm:\n  base_url: \"http://localhost:1234/v1\"\n  model: \"local-model\"\n  api_key: \"lm-studio\"\n")
	t.Setenv("WORKER_URL", "http://host.docker.internal:8081")
	cfg, err := LoadOrchestrator(p)
	if err != nil {
		t.Fatalf("LoadOrchestrator: %v", err)
	}
	if cfg.WorkerURL != "http://host.docker.internal:8081" {
		t.Errorf("env override not applied: got %q", cfg.WorkerURL)
	}
	if cfg.LLM.BaseURL != "http://localhost:1234/v1" {
		t.Errorf("llm.base_url from YAML not preserved: got %q", cfg.LLM.BaseURL)
	}
}

func TestLoadOrchestratorDefaultsListenAddrAndPublicURL(t *testing.T) {
	p := writeTemp(t, "worker_url: \"http://localhost:8081\"\nllm:\n  base_url: \"http://localhost:1234/v1\"\n  model: \"local-model\"\n  api_key: \"lm-studio\"\n")
	cfg, err := LoadOrchestrator(p)
	if err != nil {
		t.Fatalf("LoadOrchestrator: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("default listen_addr: got %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.PublicURL != "http://localhost:8080" {
		t.Errorf("default public_url: got %q, want %q", cfg.PublicURL, "http://localhost:8080")
	}
}

func TestLoadOrchestratorValidatesRequired(t *testing.T) {
	p := writeTemp(t, "worker_url: \"\"\n")
	if _, err := LoadOrchestrator(p); err == nil {
		t.Fatal("expected validation error for empty worker_url")
	}
}

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
	if o.CardPath != "/.well-known/agent.json" {
		t.Errorf("card_path: got %q", o.CardPath)
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
	if cfg.Agents[0].CardPath != "/.well-known/agent-card.json" {
		t.Errorf("migrated card_path: got %q", cfg.Agents[0].CardPath)
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

// Адрес агента подменяется окружением: в контейнере воркер живёт по другому
// имени, а конфиг тот же. Без этого docker-compose тихо ходил бы на localhost.
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

func TestLoadOrchestratorAgentURLFromEnv(t *testing.T) {
	p := writeTemp(t, `
agents:
  - id: orders
    url: "http://localhost:8081"
llm:
  base_url: "http://localhost:1234/v1"
`)
	t.Setenv("A2A_AGENT_ORDERS_URL", "http://worker:8081")
	cfg, err := LoadOrchestrator(p)
	if err != nil {
		t.Fatalf("LoadOrchestrator: %v", err)
	}
	if cfg.Agents[0].URL != "http://worker:8081" {
		t.Errorf("url from env: got %q", cfg.Agents[0].URL)
	}
}
