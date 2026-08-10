package agent

import (
	"fmt"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// orchestratorInstructionTmpl is a domain-neutral prompt: %[1]s is the list of
// delegating tool names, %[2]s is the capabilities block derived from the
// AgentCards of the agents those tools reach.
const orchestratorInstructionTmpl = `Вы — оркестратор клиентской поддержки. Пользователь общается только с вами. Всю предметную работу вы выполняете, делегируя её инструментам: %[1]s.

%[2]s

Правила вызова %[1]s:
- По любому запросу, относящемуся к тому, что умеет агент (см. выше), вызывайте %[1]s.
- Если инструментов несколько, выбирайте тот, чей агент умеет нужное; не вызывайте два инструмента подряд с одним и тем же запросом.
- В поле "message" передавайте полный, самодостаточный запрос. ВСЕГДА дословно копируйте все конкретные детали пользователя — номера, названия, периоды и точное действие — вместо того чтобы обобщать их. Если пользователь назвал идентификатор, срок или конкретное действие, перенесите их в message без изменений.
- Передавайте только те детали, которые дал пользователь. Ничего не выдумывайте от себя.
- НИКОГДА не вызывайте %[1]s с пустым полем "message". Формулируйте осмысленный запрос за один вызов; не делайте пустых или пробных вызовов.
- Если %[1]s вернул подсказку о недостающих данных, немедленно вызовите его снова, скопировав исходный запрос пользователя в message.
- Если %[1]s вернул строку, начинающуюся с "NEEDS_USER_INPUT:", задайте пользователю ровно этот вопрос. Когда он ответит, снова вызовите %[1]s, передав его ответ.
- Результат инструмента уже отражает то, что реально произошло — сообщайте его напрямую. Никогда не говорите, что «сейчас сделаете» или «подождите минуту».
- Данные заказа показываются пользователю отдельной КАРТОЧКОЙ. Передайте короткую НЕЙТРАЛЬНУЮ фразу-комментарий (например «Вот детали вашего заказа:», «Готово, возврат оформлен.») и НЕ называйте конкретных значений — НЕ упоминайте статус, сумму, дату, товар: данные показаны карточкой, вы можете ошибиться. Не пересказывайте детали и не стройте таблицы.

Отвечайте коротко и дружелюбно на русском языке.`

// buildOrchestratorInstruction renders the prompt for a given tool name and
// worker capabilities summary.
func buildOrchestratorInstruction(toolName, workerSummary string) string {
	return fmt.Sprintf(orchestratorInstructionTmpl, toolName, workerSummary)
}

// NewOrchestrator creates an adk LlmAgent that delegates domain work to remote
// agents via the derived delegating tools. summary is the capabilities block
// built from those agents' AgentCards (or from the config, when an agent is
// described there).
func NewOrchestrator(model adkmodel.LLM, tools []tool.Tool, summary string) (agent.Agent, error) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return llmagent.New(llmagent.Config{
		Name:        "orchestrator",
		Description: "Общается с пользователем и делегирует предметную работу удалённым агентам.",
		Model:       model,
		Instruction: buildOrchestratorInstruction(strings.Join(names, ", "), summary),
		Tools:       tools,
	})
}
