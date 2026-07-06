// Package a2abridge connects adk agents to the a2a-go server and client.
package a2abridge

import (
	"context"
	"encoding/json"
	"fmt"
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

// ANSI colors for the (gray) agent↔LLM loop trace lines on the worker's output.
const (
	gray  = "\033[90m"
	reset = "\033[0m"
)

// compactArgs renders function-call arguments as compact JSON for a one-line
// trace, falling back to Go's default formatting if marshaling fails.
func compactArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	if b, err := json.Marshal(args); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", args)
}

type executor struct {
	runner *runner.Runner
	trace  *Tracer
}

// NewExecutor wraps an adk Runner as an a2asrv.AgentExecutor.
// trace may be nil to disable protocol tracing.
func NewExecutor(r *runner.Runner, trace *Tracer) a2asrv.AgentExecutor {
	return &executor{runner: r, trace: trace}
}

// Execute implements a2asrv.AgentExecutor.
func (e *executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		newTask := ec.StoredTask == nil
		e.trace.Logf("▶ incoming A2A request | contextID=%s newTask=%v", ec.ContextID, newTask)

		// 1. Emit a submitted task if this is a new task.
		if newTask {
			e.trace.Logf("  → emit: submitted task")
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		} else {
			e.trace.Logf("  resuming stored task | id=%s", ec.StoredTask.ID)
		}

		// 2. Emit working status.
		e.trace.Logf("  → emit: working")
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// 3. Extract user text and run the adk runner.
		var userText string
		if ec.Message != nil && len(ec.Message.Parts) > 0 {
			userText = ec.Message.Parts[0].Text()
		}
		e.trace.Logf("  user text=%q — running orders agent (LLM + tools)", userText)

		// sessionID = A2A contextID for history continuity across turns.
		sessionID := ec.ContextID
		msg := genai.NewContentFromText(userText, genai.RoleUser)

		var finalText string
		e.trace.Logf("%s  · агент → LLM: запрос%s", gray, reset)
		for event, err := range e.runner.Run(ctx, "a2a-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				e.trace.Logf("  ✖ runner error: %v", err)
				yield(nil, err)
				return
			}
			if event == nil || event.Content == nil {
				continue
			}
			for _, p := range event.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					// LLM решило, какой инструмент по заказам вызвать.
					e.trace.Logf("%s  · LLM → агент: вызвать %s(%s)%s",
						gray, p.FunctionCall.Name, compactArgs(p.FunctionCall.Args), reset)
				case p.FunctionResponse != nil:
					// Результат инструмента уходит обратно в LLM.
					e.trace.Logf("%s  · инструмент %s → LLM: результат, снова спрашиваю LLM%s",
						gray, p.FunctionResponse.Name, reset)
				case p.Text != "":
					finalText = p.Text
				}
			}
		}

		// 4. Check for NEED_INPUT sentinel.
		trimmed := strings.TrimSpace(finalText)
		e.trace.Logf("  agent produced finalText=%q", trimmed)
		if strings.HasPrefix(trimmed, needInputPrefix) {
			question := strings.TrimSpace(strings.TrimPrefix(trimmed, needInputPrefix))
			e.trace.Logf("  → emit: input-required | question=%q", question)
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
		e.trace.Logf("  → emit: artifact + completed | artifact=%q", artifactText)
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
			Description: "Поиск заказов по номеру или товару, статусы, статистика продаж за период и оформление возвратов.",
			Tags:        []string{"заказы", "статусы", "статистика продаж", "возвраты", "поиск"},
			Examples: []string{
				"верни деньги за заказ 1041",
				"статус заказа 1041",
				"последние заказы alice",
				"статистика продаж за 2026-06",
			},
		}},
	}
}
