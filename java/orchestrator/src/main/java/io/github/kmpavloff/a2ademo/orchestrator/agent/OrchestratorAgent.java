package io.github.kmpavloff.a2ademo.orchestrator.agent;

import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolCall;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * The orchestrator LlmAgent (port of internal/agent/orchestrator.go): one
 * delegating tool per selected worker agent, derived from its AgentCard, plus
 * the same domain-neutral system prompt. Conversation history lives in the
 * shared {@link SessionStore}, not in this instance, because rebuilding the
 * agent (the tool set changes when the user picks a different agent) must not
 * lose the conversation.
 */
public class OrchestratorAgent {

    /** Same guard as the worker: a looping model must not call tools forever. */
    public static final int MAX_TOOL_CALLS_PER_TURN = 12;

    /** %1$s is the delegating tool name(s), %2$s the worker capabilities block. */
    static final String INSTRUCTION_TEMPLATE = """
            Вы — оркестратор клиентской поддержки. Пользователь общается только с вами. Всю предметную работу вы выполняете, делегируя её инструментам: %1$s.

            %2$s

            Правила вызова %1$s:
            - По любому запросу, относящемуся к тому, что умеет агент (см. выше), вызывайте %1$s.
            - Если инструментов несколько, выбирайте тот, чей агент умеет нужное; не вызывайте два инструмента подряд с одним и тем же запросом.
            - В поле "message" передавайте полный, самодостаточный запрос. ВСЕГДА дословно копируйте все конкретные детали пользователя — номера, названия, периоды и точное действие — вместо того чтобы обобщать их. Если пользователь назвал идентификатор, срок или конкретное действие, перенесите их в message без изменений.
            - Передавайте только те детали, которые дал пользователь. Ничего не выдумывайте от себя.
            - НИКОГДА не вызывайте %1$s с пустым полем "message". Формулируйте осмысленный запрос за один вызов; не делайте пустых или пробных вызовов.
            - Если %1$s вернул подсказку о недостающих данных, немедленно вызовите его снова, скопировав исходный запрос пользователя в message.
            - Если %1$s вернул строку, начинающуюся с "NEEDS_USER_INPUT:", задайте пользователю ровно этот вопрос. Когда он ответит, снова вызовите %1$s, передав его ответ.
            - Результат инструмента уже отражает то, что реально произошло — сообщайте его напрямую. Никогда не говорите, что «сейчас сделаете» или «подождите минуту».
            - Данные заказа показываются пользователю отдельной КАРТОЧКОЙ. Передайте короткую НЕЙТРАЛЬНУЮ фразу-комментарий (например «Вот детали вашего заказа:», «Готово, возврат оформлен.») и НЕ называйте конкретных значений — НЕ упоминайте статус, сумму, дату, товар: данные показаны карточкой, вы можете ошибиться. Не пересказывайте детали и не стройте таблицы.

            Отвечайте коротко и дружелюбно на русском языке.""";

    /** Progress callback so the REPL can echo the agent↔LLM loop like the Go TUI. */
    public interface TurnListener {
        default void onToolCall(String name, String argsJson) {}

        default void onToolResult(String name) {}
    }

    private final ChatModel model;
    private final Map<String, OrdersClient> byTool = new LinkedHashMap<>();
    private final List<ToolSpec> toolSpecs = new ArrayList<>();
    private final String instruction;
    private final SessionStore sessions;

    /**
     * @param tools   делегирующие клиенты выбранных агентов: в режиме «Авто» их
     *                несколько, при явном выборе — ровно один
     * @param summary блок возможностей для промпта, собранный из карточек
     */
    public OrchestratorAgent(ChatModel model, List<OrdersClient> tools, String summary, SessionStore sessions) {
        this.model = model;
        this.sessions = sessions;
        List<String> names = new ArrayList<>(tools.size());
        for (OrdersClient c : tools) {
            String name = c.profile().toolName();
            byTool.put(name, c);
            names.add(name);
            toolSpecs.add(new ToolSpec(name, c.profile().toolDesc(), Map.of(
                    "type", "object",
                    "properties", Map.of("message", Map.of(
                            "type", "string",
                            "description", "Что спросить или сообщить удалённому агенту")),
                    "required", List.of("message"))));
        }
        this.instruction = buildInstruction(String.join(", ", names), summary);
    }

    /** Промпт под конкретный набор инструментов и блок возможностей. */
    public static String buildInstruction(String toolNames, String summary) {
        return String.format(INSTRUCTION_TEMPLATE, toolNames, summary);
    }

    /** Runs one user turn and returns the assistant's final text. */
    public String runTurn(String sessionId, String userText, TurnListener listener) {
        List<ChatMessage> history = sessions.history(sessionId);
        history.add(ChatMessage.user(userText));

        int toolCalls = 0;
        while (true) {
            List<ChatMessage> request = new ArrayList<>(history.size() + 1);
            request.add(ChatMessage.system(instruction));
            request.addAll(history);
            ChatModel.Completion completion = model.complete(request, toolSpecs);

            ToolCall call = completion.toolCall();
            if (call == null) {
                String text = completion.content() == null ? "" : completion.content().trim();
                history.add(ChatMessage.assistant(text));
                return text;
            }

            toolCalls++;
            if (toolCalls > MAX_TOOL_CALLS_PER_TURN) {
                String text = "Не удалось обработать запрос за отведённое число шагов. Попробуйте переформулировать.";
                history.add(ChatMessage.assistant(text));
                return text;
            }

            listener.onToolCall(call.name(), call.argumentsJson());

            OrdersClient target = byTool.get(call.name());
            if (target == null) {
                // Модель выдумала имя инструмента. Отвечаем ей текстом, а не
                // молчанием: иначе она повторит вызов до упора в лимит.
                String reply = "Инструмента " + call.name() + " нет. Доступные: " + String.join(", ", byTool.keySet());
                history.add(ChatMessage.assistantToolCall(call));
                history.add(ChatMessage.tool(call.id(), reply));
                listener.onToolResult(call.name());
                continue;
            }

            String message = call.firstArg("message");
            String result;
            boolean stop = false;
            if (message.isEmpty()) {
                OrdersClient.EmptyReply er = target.emptyMessageReply(sessionId);
                result = er.reply();
                stop = er.stop();
            } else {
                target.clearEmpty(sessionId);
                result = target.ask(sessionId, message);
            }
            history.add(ChatMessage.assistantToolCall(call));
            history.add(ChatMessage.tool(call.id(), result));
            listener.onToolResult(call.name());
            if (stop) {
                // Same effect as adk SkipSummarization: halt the (otherwise
                // unbounded) loop instead of inviting yet another empty call.
                history.add(ChatMessage.assistant(result));
                return result;
            }
        }
    }
}
