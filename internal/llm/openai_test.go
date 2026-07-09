package llm

import (
	"context"
	"encoding/json"
	"io"
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

// TestToolsAreSentToEndpoint verifies that tool declarations present in
// req.Config.Tools are forwarded as an OpenAI "tools" array in the request
// body. The httptest server captures the body and returns a normal text
// completion so the call completes without error.
func TestToolParametersFromJSONSchema(t *testing.T) {
	// Regression: adk's functiontool publishes the generated schema on
	// FunctionDeclaration.ParametersJsonSchema and leaves the legacy Parameters
	// field nil. If the client reads only Parameters, the tool goes out WITHOUT
	// a parameter schema and strict models (e.g. GLM) call it with empty args.
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	m := New(configLLM(srv.URL + "/v1"))
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "hi"}}}},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{{
					Name:        "ask_orders_agent",
					Description: "Delegate to the orders agent",
					// Mirror adk: schema on ParametersJsonSchema, Parameters nil.
					ParametersJsonSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"message": map[string]any{"type": "string", "description": "what to ask"},
						},
						"required": []any{"message"},
					},
				}},
			}},
		},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatal(err)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("expected tools in request body")
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("tool sent WITHOUT a parameters schema (regression): %v", fn)
	}
	props, ok := params["properties"].(map[string]any)
	if !ok || props["message"] == nil {
		t.Fatalf("expected 'message' property in parameters, got %v", params)
	}
}

func TestToolsAreSentToEndpoint(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	m := New(configLLM(srv.URL + "/v1"))
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "what is the status?"}}},
		},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "get_order_status",
							Description: "Returns the status of an order",
							Parameters: &genai.Schema{
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"order_id": {Type: genai.TypeString, Description: "The order identifier"},
								},
								Required: []string{"order_id"},
							},
						},
					},
				},
			},
		},
	}

	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("could not parse captured request body: %v", err)
	}

	toolsRaw, ok := body["tools"]
	if !ok {
		t.Fatal("expected 'tools' key in request body, not found")
	}
	tools, ok := toolsRaw.([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected non-empty tools array, got %T %v", toolsRaw, toolsRaw)
	}

	// Verify the function name is present in the first tool.
	first := tools[0].(map[string]any)
	fn := first["function"].(map[string]any)
	if fn["name"] != "get_order_status" {
		t.Errorf("expected function name 'get_order_status', got %v", fn["name"])
	}

	// Verify that the parameters object has lowercase JSON-Schema types.
	paramsRaw, ok := fn["parameters"]
	if !ok {
		t.Fatal("expected 'parameters' key in function object, not found")
	}
	params, ok := paramsRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected parameters to be a JSON object, got %T", paramsRaw)
	}
	if params["type"] != "object" {
		t.Errorf("expected top-level parameters type to be \"object\" (lowercase), got %v", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected 'properties' in parameters")
	}
	orderIDProp, ok := props["order_id"].(map[string]any)
	if !ok {
		t.Fatal("expected 'order_id' property in parameters")
	}
	if orderIDProp["type"] != "string" {
		t.Errorf("expected order_id type to be \"string\" (lowercase), got %v", orderIDProp["type"])
	}
}

// TestToolCallHistoryTranslatesToAssistantThenTool verifies that a Contents
// slice carrying a model FunctionCall followed by a FunctionResponse produces
// an OpenAI message sequence where the assistant message's tool_call id equals
// the subsequent tool message's tool_call_id. The check is done by capturing
// the outgoing request body via httptest and inspecting the JSON structure.
func TestToolCallHistoryTranslatesToAssistantThenTool(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	m := New(configLLM(srv.URL + "/v1"))
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{
			// Turn 1: user asks.
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "status of order 1041?"}}},
			// Turn 2: model emits a function call with an explicit ID.
			{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{
						ID:   "call_x",
						Name: "get_order_status",
						Args: map[string]any{"order_id": "1041"},
					}},
				},
			},
			// Turn 3: tool result referencing the same call ID.
			{
				Parts: []*genai.Part{
					{FunctionResponse: &genai.FunctionResponse{
						ID:       "call_x",
						Name:     "get_order_status",
						Response: map[string]any{"status": "shipped"},
					}},
				},
			},
		},
	}

	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("could not parse captured request body: %v", err)
	}

	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatal("expected 'messages' array in request body")
	}

	// Find the assistant message with tool_calls and the tool message.
	var assistantToolCallID string
	var toolMsgCallID string
	for _, raw := range msgs {
		msg := raw.(map[string]any)
		role, _ := msg["role"].(string)
		switch role {
		case "assistant":
			if tcs, ok := msg["tool_calls"].([]any); ok && len(tcs) > 0 {
				tc := tcs[0].(map[string]any)
				assistantToolCallID, _ = tc["id"].(string)
			}
		case "tool":
			toolMsgCallID, _ = msg["tool_call_id"].(string)
		}
	}

	if assistantToolCallID == "" {
		t.Fatal("no assistant message with tool_calls found in request")
	}
	if toolMsgCallID == "" {
		t.Fatal("no tool message found in request")
	}
	if assistantToolCallID != toolMsgCallID {
		t.Errorf("assistant tool_call id %q != tool message tool_call_id %q", assistantToolCallID, toolMsgCallID)
	}
}
