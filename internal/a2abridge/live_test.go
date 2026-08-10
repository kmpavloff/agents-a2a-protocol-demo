package a2abridge

import (
	"context"
	"os"
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// TestLiveRemoteAgent — необязательная проверка против НАСТОЯЩЕГО удалённого
// агента: только она ловит расхождения, которые стаб воспроизвести не может
// (форма securitySchemes, отсутствие oneof-обёртки, живой диалект A2UI).
// По умолчанию пропускается; включается адресом агента в окружении:
//
//	LIVE_AGENT_URL=http://192.168.1.68:18800 \
//	LIVE_AGENT_CARD_PATH=/.well-known/agent.json \
//	LIVE_AGENT_SKILL=shop \
//	LIVE_AGENT_USER=ouroboros LIVE_AGENT_PASSWORD=... \
//	go test ./internal/a2abridge/ -run TestLiveRemoteAgent -v -timeout 300s
func TestLiveRemoteAgent(t *testing.T) {
	url := os.Getenv("LIVE_AGENT_URL")
	if url == "" {
		t.Skip("LIVE_AGENT_URL not set")
	}
	cardPath := os.Getenv("LIVE_AGENT_CARD_PATH")
	if cardPath == "" {
		cardPath = "/.well-known/agent-card.json"
	}
	prompt := os.Getenv("LIVE_AGENT_PROMPT")
	if prompt == "" {
		prompt = "покажи статус заказа ORD-001"
	}
	cfg := config.AgentConfig{
		ID: "live", Name: "Live agent", URL: url, CardPath: cardPath,
		Skill: os.Getenv("LIVE_AGENT_SKILL"), Timeout: "240s",
		Description: "Проверочный удалённый агент.",
		Auth: config.AuthConfig{
			Type:     "basic",
			Username: os.Getenv("LIVE_AGENT_USER"),
			Password: os.Getenv("LIVE_AGENT_PASSWORD"),
		},
	}
	r := NewRemote(cfg, NewTracer(os.Stdout, "[live] "))
	reply, err := r.Ask(context.Background(), "live-session", prompt)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if reply.Text == "" {
		t.Error("agent returned no text")
	}
	t.Logf("текст: %s", reply.Text)
	t.Logf("A2UI-сообщений: %d", len(reply.A2UI))
	// Если агент прислал A2UI, он обязан доехать до рендерабельной формы.
	for _, m := range reply.A2UI {
		uc, ok := m["updateComponents"].(map[string]any)
		if !ok {
			continue
		}
		comps, _ := uc["components"].([]map[string]any)
		for _, c := range comps {
			if id, _ := c["id"].(string); id == "" {
				t.Errorf("component without id: %v", c)
			}
			if typ, _ := c["component"].(string); typ == "" {
				t.Errorf("component without type: %v", c)
			}
		}
	}
}
