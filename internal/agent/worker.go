// Package agent builds the orchestrator and worker LlmAgents.
package agent

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const workerInstruction = `Вы — orders-agent. Вы управляете заказами клиентов с помощью предоставленных инструментов.
Используйте инструменты, чтобы искать заказы, проверять статусы и статистику, а также оформлять возвраты.
Если вам не хватает данных, чтобы выполнить запрос (например, какой именно заказ возвращать, когда подходит несколько), ответьте РОВНО одной строкой:
NEED_INPUT: <ваш вопрос>
и ничем больше. В остальных случаях отвечайте пользователю ясно и кратко на русском языке.`

// NewWorker creates an adk LlmAgent for managing orders with the given model and tools.
func NewWorker(model adkmodel.LLM, tools []tool.Tool) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "orders_agent",
		Description: "Управляет заказами, статусами, статистикой и возвратами.",
		Model:       model,
		Instruction: workerInstruction,
		Tools:       tools,
	})
}
