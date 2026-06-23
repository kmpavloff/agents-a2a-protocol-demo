// Package a2abridge connects adk agents to the a2a-go server and client.
package a2abridge

import (
	"context"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

// needInputPrefix is the sentinel the worker agent emits when it needs
// clarification from the caller.
const needInputPrefix = "NEED_INPUT:"

type executor struct{ runner *runner.Runner }

// NewExecutor wraps an adk Runner as an a2asrv.AgentExecutor.
func NewExecutor(r *runner.Runner) a2asrv.AgentExecutor { return &executor{runner: r} }

// Execute implements a2asrv.AgentExecutor.
func (e *executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		// 1. Emit a submitted task if this is a new task.
		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}

		// 2. Emit working status.
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// 3. Extract user text and run the adk runner.
		var userText string
		if ec.Message != nil && len(ec.Message.Parts) > 0 {
			userText = ec.Message.Parts[0].Text()
		}

		// sessionID = A2A contextID for history continuity across turns.
		sessionID := ec.ContextID
		msg := genai.NewContentFromText(userText, genai.RoleUser)

		var finalText string
		for event, err := range e.runner.Run(ctx, "a2a-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				yield(nil, err)
				return
			}
			if event != nil && event.Content != nil {
				for _, p := range event.Content.Parts {
					if p.Text != "" {
						finalText = p.Text
					}
				}
			}
		}

		// 4. Check for NEED_INPUT sentinel.
		trimmed := strings.TrimSpace(finalText)
		if strings.HasPrefix(trimmed, needInputPrefix) {
			question := strings.TrimSpace(strings.TrimPrefix(trimmed, needInputPrefix))
			ask := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(question))
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, ask), nil)
			return
		}

		// 5. Emit artifact + completed.
		// Guard against an empty finalText so we never emit a blank artifact.
		artifactText := trimmed
		if artifactText == "" {
			artifactText = "Готово."
		}
		if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(artifactText)), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
}

// Cancel implements a2asrv.AgentExecutor.
func (e *executor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

// AgentCard returns a minimal A2A AgentCard for the orders agent.
// publicURL is the base URL where the JSON-RPC handler is mounted (e.g. "http://localhost:8080").
// The card advertises a single JSONRPC interface at publicURL/invoke,
// which matches the mux path registered in cmd/worker/main.go.
func AgentCard(publicURL string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "orders-agent",
		Description:        "Управляет заказами, статусами, статистикой и возвратами.",
		Version:            "0.1.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(publicURL+"/invoke", a2a.TransportProtocolJSONRPC),
		},
		Skills: []a2a.AgentSkill{{
			ID:          "manage_orders",
			Name:        "Управление заказами",
			Description: "Поиск заказов, статусы, статистика продаж и оформление возвратов.",
		}},
	}
}
