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
