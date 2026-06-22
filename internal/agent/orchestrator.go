package agent

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const orchestratorInstruction = `You are a customer-support orchestrator. The user talks only to you.
For anything about orders, statuses, sales statistics, or refunds, call the ask_orders_agent tool with a clear message.
If ask_orders_agent returns a line starting with "NEEDS_USER_INPUT:", ask the user exactly that question, then on their next message call ask_orders_agent again with their answer.
Keep replies short and friendly. Assume the current customer is "alice" unless told otherwise.`

// NewOrchestrator creates an adk LlmAgent that delegates order work to the
// orders worker agent via the ask_orders_agent tool.
func NewOrchestrator(model adkmodel.LLM, ordersTool tool.Tool) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "orchestrator",
		Description: "Talks to the user and delegates order work to the orders agent.",
		Model:       model,
		Instruction: orchestratorInstruction,
		Tools:       []tool.Tool{ordersTool},
	})
}
