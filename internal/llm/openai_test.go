package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"

	config "github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// ---- Stub tests ----

func TestStubReturnsScriptedText(t *testing.T) {
	s := NewStub(StubTurn{Text: "hello"})
	var got string
	for resp, err := range s.GenerateContent(context.Background(), nil, false) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != nil && len(resp.Content.Parts) > 0 {
			got = resp.Content.Parts[0].Text
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
			fc = resp.Content.Parts[0].FunctionCall
		}
	}
	if fc == nil || fc.Name != "get_order_status" {
		t.Fatalf("want function call get_order_status, got %+v", fc)
	}
}

func TestStubAdvancesPerCall(t *testing.T) {
	s := NewStub(StubTurn{Text: "first"}, StubTurn{Text: "second"})
	// Consume the first turn.
	for resp, err := range s.GenerateContent(context.Background(), nil, false) {
		_ = resp
		_ = err
	}
	// After one call, idx should be 1.
	if s.idx != 1 {
		t.Fatalf("after one call idx should be 1, got %d", s.idx)
	}
}

// ---- Real adapter test ----

func configLLM(base string) config.LLMConfig {
	return config.LLMConfig{BaseURL: base, Model: "local-model", APIKey: "test"}
}

func TestModelCallsEndpointAndReturnsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	m := New(configLLM(srv.URL + "/v1"))
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "ping"}}},
		},
	}
	var got string
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != nil && len(resp.Content.Parts) > 0 {
			got = resp.Content.Parts[0].Text
		}
	}
	if got != "pong" {
		t.Errorf("want 'pong', got %q", got)
	}
}
