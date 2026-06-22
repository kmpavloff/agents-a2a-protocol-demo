package llm

import (
	"context"
	"encoding/json"
	"iter"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"

	config "github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// Model is an adkmodel.LLM adapter that calls an OpenAI-compatible chat
// completions endpoint (e.g., LM Studio).
type Model struct {
	client openai.Client
	model  string
}

// New constructs a Model that sends requests to cfg.BaseURL using cfg.APIKey.
func New(cfg config.LLMConfig) *Model {
	c := openai.NewClient(
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	)
	return &Model{client: c, model: cfg.Model}
}

// Name implements adkmodel.LLM.
func (m *Model) Name() string { return m.model }

// GenerateContent implements adkmodel.LLM. It translates the adk LLMRequest
// into an OpenAI chat completion call and wraps the response in LLMResponse.
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
		// Tool call takes precedence over text content.
		if len(choice.ToolCalls) > 0 {
			tc := choice.ToolCalls[0]
			args := parseJSONObject(tc.Function.Arguments)
			// Preserve the tool-call ID in FunctionCall.ID so that
			// multi-step round-trips can match the tool result to this call.
			callID := tc.ID
			if callID == "" {
				callID = "call_" + tc.Function.Name
			}
			part := &genai.Part{FunctionCall: &genai.FunctionCall{
				ID:   callID,
				Name: tc.Function.Name,
				Args: args,
			}}
			yield(&adkmodel.LLMResponse{
				Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{part}},
				TurnComplete: true,
			}, nil)
			return
		}

		yield(&adkmodel.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: choice.Content}}},
			TurnComplete: true,
		}, nil)
	}
}

// toOpenAIMessages converts an adk LLMRequest into OpenAI message params.
//
// Mapping:
//   - req.Config.SystemInstruction → openai.SystemMessage
//   - genai.RoleUser content → openai.UserMessage
//   - genai.RoleModel content with FunctionCall part(s) → assistant message
//     with structured tool_calls (ChatCompletionAssistantMessageParam.ToolCalls)
//   - genai.RoleModel content with text → openai.AssistantMessage
//   - content with FunctionResponse part → openai.ToolMessage keyed by the
//     matching tool_call_id (from FunctionResponse.ID, or synthesised as
//     "call_"+name to match the id used in the assistant message above)
func toOpenAIMessages(req *adkmodel.LLMRequest) []openai.ChatCompletionMessageParamUnion {
	if req == nil {
		return nil
	}
	var msgs []openai.ChatCompletionMessageParamUnion

	if req.Config != nil && req.Config.SystemInstruction != nil {
		msgs = append(msgs, openai.SystemMessage(contentText(req.Config.SystemInstruction)))
	}

	for _, c := range req.Contents {
		// Check for FunctionResponse parts first (tool results), regardless of role.
		if fr := firstFunctionResponse(c); fr != nil {
			b, _ := json.Marshal(fr.Response)
			// Use FunctionResponse.ID as the tool_call_id; fall back to
			// "call_"+name to match the id synthesised in the assistant message.
			toolCallID := fr.ID
			if toolCallID == "" {
				toolCallID = "call_" + fr.Name
			}
			msgs = append(msgs, openai.ToolMessage(string(b), toolCallID))
			continue
		}

		switch c.Role {
		case genai.RoleUser:
			msgs = append(msgs, openai.UserMessage(contentText(c)))
		case genai.RoleModel:
			if fcs := allFunctionCalls(c); len(fcs) > 0 {
				// Emit a proper assistant message with structured tool_calls so
				// a strict OpenAI-compatible server accepts the subsequent tool
				// messages without rejecting the request.
				msgs = append(msgs, assistantWithToolCalls(fcs))
			} else {
				msgs = append(msgs, openai.AssistantMessage(contentText(c)))
			}
		default:
			// Unknown role: best-effort as user message.
			msgs = append(msgs, openai.UserMessage(contentText(c)))
		}
	}

	return msgs
}

// assistantWithToolCalls builds a ChatCompletionMessageParamUnion that carries
// an assistant message with one or more tool_calls entries.
func assistantWithToolCalls(fcs []*genai.FunctionCall) openai.ChatCompletionMessageParamUnion {
	toolCalls := make([]openai.ChatCompletionMessageToolCallParam, 0, len(fcs))
	for _, fc := range fcs {
		callID := fc.ID
		if callID == "" {
			callID = "call_" + fc.Name
		}
		argsJSON, _ := json.Marshal(fc.Args)
		toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
			ID: callID,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      fc.Name,
				Arguments: string(argsJSON),
			},
		})
	}
	asst := openai.ChatCompletionAssistantMessageParam{
		ToolCalls: toolCalls,
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &asst}
}

// toOpenAITools converts adk tool declarations (in req.Config.Tools) to
// OpenAI ChatCompletionToolParam slice. Returns nil when no declarations are
// present so that the tools field is omitted from the request entirely.
//
// adk surfaces function declarations via req.Config.Tools ([]*genai.Tool),
// each of which may carry FunctionDeclarations ([]*genai.FunctionDeclaration).
// Each FunctionDeclaration has Name, Description, and a Parameters *genai.Schema.
// The Parameters schema is marshalled to JSON and unmarshalled into
// shared.FunctionParameters (map[string]any) — pragmatic and robust since
// genai.Schema has JSON tags that produce valid JSON Schema.
func toOpenAITools(req *adkmodel.LLMRequest) []openai.ChatCompletionToolParam {
	if req == nil || req.Config == nil || len(req.Config.Tools) == 0 {
		return nil
	}

	var tools []openai.ChatCompletionToolParam
	for _, t := range req.Config.Tools {
		if t == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			var params shared.FunctionParameters
			if fd.Parameters != nil {
				b, err := json.Marshal(fd.Parameters)
				if err == nil {
					_ = json.Unmarshal(b, &params)
				}
			}
			fnDef := shared.FunctionDefinitionParam{
				Name:        fd.Name,
				Description: openai.String(fd.Description),
				Parameters:  params,
			}
			tools = append(tools, openai.ChatCompletionToolParam{
				Function: fnDef,
			})
		}
	}
	return tools
}

// parseJSONObject parses a JSON object string into a map. Malformed JSON
// returns an empty map.
func parseJSONObject(s string) map[string]any {
	out := map[string]any{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// contentText extracts concatenated text from all parts of a Content.
func contentText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var s string
	for _, p := range c.Parts {
		s += p.Text
	}
	return s
}

// firstFunctionCall returns the first FunctionCall part found in a Content,
// or nil if none exists.
func firstFunctionCall(c *genai.Content) *genai.FunctionCall {
	for _, p := range c.Parts {
		if p.FunctionCall != nil {
			return p.FunctionCall
		}
	}
	return nil
}

// allFunctionCalls returns all FunctionCall parts found in a Content.
func allFunctionCalls(c *genai.Content) []*genai.FunctionCall {
	var out []*genai.FunctionCall
	for _, p := range c.Parts {
		if p.FunctionCall != nil {
			out = append(out, p.FunctionCall)
		}
	}
	return out
}

// firstFunctionResponse returns the first FunctionResponse part found in a
// Content, or nil if none exists.
func firstFunctionResponse(c *genai.Content) *genai.FunctionResponse {
	for _, p := range c.Parts {
		if p.FunctionResponse != nil {
			return p.FunctionResponse
		}
	}
	return nil
}
