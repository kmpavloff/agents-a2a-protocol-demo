package io.github.kmpavloff.a2ademo.orchestrator.agent;

import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolCall;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * The orchestrator LlmAgent (port of internal/agent/orchestrator.go): a single
 * delegating tool, derived from the worker's AgentCard, plus the same
 * domain-neutral system prompt. Conversation history is kept per session.
 */
public class OrchestratorAgent {

    /** Same guard as the worker: a looping model must not call tools forever. */
    public static final int MAX_TOOL_CALLS_PER_TURN = 12;

    /** %1$s is the delegating tool name, %2$s the worker capabilities block. */
    static final String INSTRUCTION_TEMPLATE = """
            Вы — оркестратор клиентской поддержки. Пользователь общается только с вами. Всю предметную работу вы выполняете, делегируя её инструменту %1$s.

            %2$s

            Правила вызова %1$s:
            - По любому запросу, относящемуся к тому, что умеет агент (см. выше), вызывайте %1$s.
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
    private final OrdersClient orders;
    private final String instruction;
    private final ToolSpec askTool;
    private final Map<String, List<ChatMessage>> sessions = new ConcurrentHashMap<>();

    public OrchestratorAgent(ChatModel model, OrdersClient orders) {
        this.model = model;
        this.orders = orders;
        this.instruction = String.format(INSTRUCTION_TEMPLATE,
                orders.profile().toolName(), orders.profile().summary());
        this.askTool = new ToolSpec(
                orders.profile().toolName(),
                orders.profile().toolDesc(),
                Map.of(
                        "type", "object",
                        "properties", Map.of("message", Map.of(
                                "type", "string",
                                "description", "Что спросить или сообщить агенту по заказам")),
                        "required", List.of("message")));
    }

    /** Runs one user turn and returns the assistant's final text. */
    public String runTurn(String sessionId, String userText, TurnListener listener) {
        List<ChatMessage> history = sessions.computeIfAbsent(sessionId, k -> new ArrayList<>());
        history.add(ChatMessage.user(userText));

        int toolCalls = 0;
        while (true) {
            List<ChatMessage> request = new ArrayList<>(history.size() + 1);
            request.add(ChatMessage.system(instruction));
            request.addAll(history);
            ChatModel.Completion completion = model.complete(request, List.of(askTool));

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
            String message = call.firstArg("message");
            String result;
            boolean stop = false;
            if (message.isEmpty()) {
                OrdersClient.EmptyReply er = orders.emptyMessageReply(sessionId);
                result = er.reply();
                stop = er.stop();
            } else {
                orders.clearEmpty(sessionId);
                result = orders.ask(sessionId, message);
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
