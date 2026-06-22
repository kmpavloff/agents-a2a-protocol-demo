// Package llm adapts a local OpenAI-compatible endpoint (LM Studio) to adk's
// model.LLM interface, and provides a deterministic stub for tests.
package llm

import (
	"context"
	"iter"

	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

// StubTurn is one scripted model response: either Text or a single Call.
type StubTurn struct {
	Text string
	Call *genai.FunctionCall
}

// Stub is a deterministic adkmodel.LLM for tests. It does no network I/O.
type Stub struct {
	Script []StubTurn
	idx    int
}

// NewStub creates a Stub that replays the given turns in order.
func NewStub(turns ...StubTurn) *Stub { return &Stub{Script: turns} }

// Name implements adkmodel.LLM.
func (s *Stub) Name() string { return "stub" }

// GenerateContent implements adkmodel.LLM. Each call consumes the next scripted
// turn (wrapping around if the script is exhausted).
func (s *Stub) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		var turn StubTurn
		if s.idx < len(s.Script) {
			turn = s.Script[s.idx]
		}
		s.idx++

		var part *genai.Part
		if turn.Call != nil {
			part = &genai.Part{FunctionCall: turn.Call}
		} else {
			part = &genai.Part{Text: turn.Text}
		}

		yield(&adkmodel.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{part}},
			TurnComplete: true,
		}, nil)
	}
}
