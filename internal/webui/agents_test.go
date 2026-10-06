package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

func TestAgentsHandlerServesJSON(t *testing.T) {
	h := AgentsHandler(func(context.Context) []testAgent {
		return []testAgent{{ID: "orders", Name: "Агент заказов", Available: true}}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"id":"orders"`) || !strings.Contains(body, `"available":true`) {
		t.Errorf("body: %s", body)
	}
}

// Пустой список должен приезжать как [], а не как null: фронтенд итерирует
// результат без дополнительных проверок.
func TestAgentsHandlerServesEmptyArray(t *testing.T) {
	h := AgentsHandler(func(context.Context) []testAgent { return nil })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("body: %q, want []", got)
	}
}

func TestAgentCallsHandler(t *testing.T) {
	var gotCtx string
	var gotAfter int64
	h := AgentCallsHandler(func(ctxID string, after int64) []string {
		gotCtx, gotAfter = ctxID, after
		return nil
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agent-calls?contextId=c1&after=7", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body)
	}
	if gotCtx != "c1" || gotAfter != 7 {
		t.Errorf("параметры: %q %d", gotCtx, gotAfter)
	}
	for _, q := range []string{"", "?contextId=c1&after=x"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agent-calls"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q: status %d", q, rec.Code)
		}
	}
}
