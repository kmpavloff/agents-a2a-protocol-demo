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
