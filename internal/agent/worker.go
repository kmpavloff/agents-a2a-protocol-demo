// Package agent builds the orchestrator and worker LlmAgents.
package agent

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const workerInstruction = `You are the orders-agent. You manage customer orders using the provided tools.
Use tools to look up orders, statuses, statistics, and to initiate refunds.
If you are missing information required to act (for example, which order id to refund when several match), reply with exactly one line:
NEED_INPUT: <your question>
and nothing else. Otherwise answer the user clearly and concisely.`

// NewWorker creates an adk LlmAgent for managing orders with the given model and tools.
func NewWorker(model adkmodel.LLM, tools []tool.Tool) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "orders_agent",
		Description: "Manages orders, statuses, statistics and refunds.",
		Model:       model,
		Instruction: workerInstruction,
		Tools:       tools,
	})
}
