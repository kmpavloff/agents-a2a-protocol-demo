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

	// Второй ход — нажатие кнопки, если агент её прислал. Единственная
	// автоматическая проверка того, что он разбирает штатное событие A2UI, а не
	// только пересказ действия словами.
	name, surfaceID, componentID, actx, ok := firstLiveButton(reply.A2UI)
	if !ok {
		t.Log("кнопок в ответе нет — событие A2UI не проверялось")
		return
	}
	t.Logf("нажимаю кнопку %q поверхности %q", name, surfaceID)
	actionReply, err := r.AskAction(context.Background(), "live-session", name, surfaceID, componentID, actx)
	if err != nil {
		t.Fatalf("AskAction: %v", err)
	}
	if actionReply.Text == "" {
		t.Error("на событие A2UI агент не ответил текстом")
	}
	t.Logf("ответ на действие: %s", actionReply.Text)
}

// firstLiveButton находит в наборе A2UI первую кнопку с действием и возвращает
// всё, что нужно, чтобы это действие отправить.
func firstLiveButton(msgs []map[string]any) (name, surfaceID, componentID string, ctx map[string]any, ok bool) {
	for _, m := range msgs {
		uc, isUpdate := m["updateComponents"].(map[string]any)
		if !isUpdate {
			continue
		}
		sid, _ := uc["surfaceId"].(string)
		comps, _ := uc["components"].([]map[string]any)
		for _, c := range comps {
			if typ, _ := c["component"].(string); typ != "Button" {
				continue
			}
			action, _ := c["action"].(map[string]any)
			event, _ := action["event"].(map[string]any)
			n, _ := event["name"].(string)
			if n == "" {
				continue
			}
			cctx, _ := event["context"].(map[string]any)
			if cctx == nil {
				cctx = map[string]any{}
			}
			id, _ := c["id"].(string)
			return n, sid, id, cctx, true
		}
	}
	return "", "", "", nil, false
}
