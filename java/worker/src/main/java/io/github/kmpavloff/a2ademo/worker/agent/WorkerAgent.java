package io.github.kmpavloff.a2ademo.worker.agent;

import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolCall;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.common.util.Cards;
import io.github.kmpavloff.a2ademo.worker.orders.OrderTools;
import io.github.kmpavloff.a2ademo.worker.orders.Widgets;

import java.nio.charset.StandardCharsets;
import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * The worker's LLM agent loop with the two A2A pause points, ported from the Go
 * a2abridge executor: the NEED_INPUT clarification sentinel and the
 * human-in-the-loop confirmation that guards initiate_refund. Conversation
 * history is kept per A2A contextId so a resumed task continues where it
 * stopped instead of starting over.
 */
public class WorkerAgent {

    /** Sentinel the worker LLM emits when it needs clarification from the caller. */
    public static final String NEED_INPUT_PREFIX = "NEED_INPUT:";

    /**
     * Cap on tool calls per turn: a looping model would otherwise call tools
     * forever (the same guard as maxToolCallsPerTurn in the Go executor).
     */
    public static final int MAX_TOOL_CALLS_PER_TURN = 12;

    public static final String INSTRUCTION = """
            Вы — orders-agent. Вы управляете заказами клиентов с помощью предоставленных инструментов.
            Используйте инструменты, чтобы искать заказы, проверять статусы и статистику, а также оформлять возвраты.

            Правила вызова инструментов:
            - ВСЕГДА заполняйте обязательные аргументы инструмента значениями из запроса. НИКОГДА не вызывайте инструмент с пустыми аргументами.
            - get_order_status, initiate_refund: номер заказа → order_id (например, order_id="1041").
            - list_recent_orders: имя клиента → customer (например, customer="alice"). Имя клиента — это короткий логин/имя (alice, bob), а НЕ email и НЕ номер заказа; никогда не спрашивайте email.
            - get_sales_stats: период → period в формате ГГГГ-ММ.
            - find_order: номер заказа или текст названия товара → query.
            - Если нужное значение явно указано в запросе — извлеките его и передайте в соответствующий аргумент, не переспрашивая пользователя.
            - Если инструмент вернул подсказку о недостающем аргументе (например, «Не указано имя клиента»), НЕ придумывайте ответ — сразу вызовите инструмент снова, заполнив пропущенный аргумент.

            Правила ответа:
            - Сообщайте ТОЛЬКО то, что реально вернули инструменты. НИКОГДА не выдумывайте статус, сумму, наличие или результат возврата. Если инструмент не дал данных — не угадывайте.
            - Если данных не хватает, чтобы вызвать инструмент (например, какой именно заказ возвращать, когда подходит несколько, и номер не указан), ответьте РОВНО одной строкой:
            NEED_INPUT: <ваш вопрос>
            и ничем больше.
            - Данные заказа (статус, список заказов, результат возврата) показываются пользователю ОТДЕЛЬНОЙ КАРТОЧКОЙ. Поэтому, когда вы посмотрели или изменили данные по заказу, отвечайте ОДНОЙ короткой НЕЙТРАЛЬНОЙ фразой-указателем на карточку. В комментарии НЕ называйте никаких конкретных значений — НЕ упоминайте статус, сумму, дату, товар или имя клиента: эти данные уже в карточке, а вы можете ошибиться. ПРАВИЛЬНО: «Вот детали вашего заказа:», «Последние заказы — в карточке ниже:», «Готово, возврат оформлен.». НЕПРАВИЛЬНО: «Ваш заказ 1041 доставлен на сумму 34.50 EUR» — называет данные.

            В остальных случаях отвечайте пользователю ясно и кратко на русском языке.""";

    private static final String GRAY = "\u001B[90m";
    private static final String RESET = "\u001B[0m";

    private final ChatModel model;
    private final OrderTools tools;
    private final Tracer trace;

    private final Map<String, List<ChatMessage>> sessions = new ConcurrentHashMap<>();
    private final Map<String, PendingHITL> pendingConfirm = new ConcurrentHashMap<>();

    /**
     * The two-step refund pause: first the yes/no confirmation, then (after
     * approval) the card details the money goes to.
     */
    private record PendingHITL(boolean awaitingCard, String callId, String orderId) {}

    /** A downloadable file attached to a completed turn (e.g. the refund receipt). */
    public record ReceiptFile(String filename, String mediaType, byte[] content) {}

    /** Outcome of one A2A turn: completed with text (+ optional widget/file), or paused for input. */
    public sealed interface Outcome {
        record Completed(String text, Map<String, Object> widget, ReceiptFile file) implements Outcome {
            public Completed(String text, Map<String, Object> widget) {
                this(text, widget, null);
            }
        }

        record InputRequired(String question, Map<String, Object> widget, String widgetKind) implements Outcome {}
    }

    public WorkerAgent(ChatModel model, OrderTools tools, Tracer trace) {
        this.model = model;
        this.tools = tools;
        this.trace = trace;
    }

    /** True when the session is waiting on a refund confirmation answer. */
    public boolean awaitingConfirmation(String contextId) {
        return pendingConfirm.containsKey(contextId);
    }

    /** Runs one A2A turn for the given contextId (= session id) and user text. */
    public Outcome run(String contextId, String userText) {
        List<ChatMessage> history = sessions.computeIfAbsent(contextId, k -> new ArrayList<>());

        PendingHITL pending = pendingConfirm.remove(contextId);
        if (pending != null && !pending.awaitingCard()) {
            return onConfirmationAnswer(contextId, history, pending, userText);
        }
        if (pending != null && pending.awaitingCard()) {
            return onCardAnswer(contextId, history, pending, userText);
        }

        trace.logf("  user text=\"%s\" — running orders agent (LLM + tools)", userText);
        history.add(ChatMessage.user(userText));
        return runLoop(contextId, history, 0);
    }

    /** Step 1 of the refund HITL: the yes/no confirmation answer. */
    private Outcome onConfirmationAnswer(String contextId, List<ChatMessage> history,
                                         PendingHITL pending, String userText) {
        boolean approved = parseAffirmative(userText);
        trace.logf("  confirmation answer=\"%s\" → approved=%s", userText, approved);
        if (!approved) {
            // Close the dangling tool call so the history stays valid for the
            // next turn, then short-circuit without asking the LLM.
            history.add(ChatMessage.tool(pending.callId(),
                    "Пользователь отклонил возврат. Возврат НЕ выполнен."));
            trace.logf("  → emit: artifact + completed | refund declined by user");
            return new Outcome.Completed("Возврат отменён по вашему решению.", null);
        }
        // Approved → the refund still does NOT run: ask for the card the money
        // goes to. The same A2A task pauses input-required a second time.
        pendingConfirm.put(contextId, new PendingHITL(true, pending.callId(), pending.orderId()));
        String question = String.format(
                "Укажите номер карты для возврата средств по заказу %s (13–19 цифр).", pending.orderId());
        trace.logf("  → emit: input-required (card details) | order=%s", pending.orderId());
        return new Outcome.InputRequired(question,
                copyOf(Widgets.refundFormWidget(pending.orderId(), question, false)),
                Widgets.KIND_REFUND_FORM);
    }

    /**
     * Step 2: the card-number reply. Validated by CODE (Luhn), never by the
     * LLM; the full number is neither logged nor echoed back.
     */
    private Outcome onCardAnswer(String contextId, List<ChatMessage> history,
                                 PendingHITL pending, String userText) {
        String digits = Cards.digits(userText);
        if (digits.isEmpty()) {
            trace.logf("  card reply carries no digits → treating as cancellation");
            history.add(ChatMessage.tool(pending.callId(),
                    "Пользователь не передал реквизиты. Возврат НЕ выполнен."));
            return new Outcome.Completed("Возврат отменён: реквизиты карты не получены.", null);
        }
        if (!Cards.valid(digits)) {
            pendingConfirm.put(contextId, pending); // stay at the card step
            trace.logf("  card %s failed validation → re-asking", Cards.mask(digits));
            String errText = "Номер карты некорректен. Проверьте его и отправьте ещё раз (13–19 цифр).";
            return new Outcome.InputRequired(errText,
                    copyOf(Widgets.refundFormWidget(pending.orderId(), errText, true)),
                    Widgets.KIND_REFUND_FORM);
        }
        String last4 = digits.substring(digits.length() - 4);
        trace.logf("  card %s accepted → executing refund", Cards.mask(digits));

        // The refund runs NOW, by code, with the captured order id — the model
        // never re-decides the arguments of a money move.
        OrderTools.ToolResult result = tools.execute(new ToolCall(pending.callId(), "initiate_refund",
                "{\"order_id\":\"" + pending.orderId() + "\"}"));
        trace.logf("%s  · инструмент initiate_refund → LLM: результат, снова спрашиваю LLM%s", GRAY, RESET);
        history.add(ChatMessage.tool(pending.callId(), result.text()));
        Outcome outcome = runLoop(contextId, history, 1);

        // Success → enrich the tool's receipt widget with the payment context
        // and attach the downloadable receipt file.
        if (outcome instanceof Outcome.Completed
                && result.widget() != null
                && Widgets.KIND_REFUND_RECEIPT.equals(result.widget().get("kind"))) {
            Map<String, Object> receipt = result.widget();
            ReceiptFile file = enrichReceipt(receipt, last4);
            String text = String.format(
                    "Возврат оформлен. Средства поступят на карту •••• %s. Квитанция — во вложении.", last4);
            trace.logf("  → receipt %s (%s, %d bytes)", receipt.get("receipt_id"),
                    file.filename(), file.content().length);
            return new Outcome.Completed(text, receipt, file);
        }
        return outcome;
    }

    /**
     * Stamps the payment context (masked card, receipt id, creation time,
     * filename) onto the receipt widget and builds the downloadable file.
     */
    private static ReceiptFile enrichReceipt(Map<String, Object> w, String cardLast4) {
        LocalDateTime now = LocalDateTime.now();
        String receiptId = "RF-" + w.get("order_id") + "-"
                + now.format(DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss"));
        String created = now.format(DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss"));
        String filename = "receipt-" + w.get("order_id") + ".txt";
        w.put("receipt_id", receiptId);
        w.put("card_last4", cardLast4);
        w.put("created", created);
        w.put("filename", filename);

        String content = """
                КВИТАНЦИЯ О ВОЗВРАТЕ
                =====================
                Квитанция №:      %s
                Дата:             %s
                Заказ:            #%s — %s
                Сумма возврата:   %s %s
                Карта получателя: •••• %s
                Статус:           возврат оформлен

                Документ сформирован автоматически агентом orders-agent (A2A demo).
                """.formatted(receiptId, created, w.get("order_id"), w.get("item"),
                w.get("amount"), w.get("currency"), cardLast4);
        return new ReceiptFile(filename, "text/plain", content.getBytes(StandardCharsets.UTF_8));
    }

    /** Defensive copy of a widget payload (the builders return fresh maps, but cheap). */
    private static Map<String, Object> copyOf(Map<String, Object> w) {
        return new LinkedHashMap<>(w);
    }

    private Outcome runLoop(String contextId, List<ChatMessage> history, int toolCalls) {
        Map<String, Object> capturedWidget = null;
        long start = System.nanoTime();
        trace.logf("%s  · агент → LLM: запрос%s", GRAY, RESET);

        while (true) {
            List<ChatMessage> request = new ArrayList<>(history.size() + 1);
            request.add(ChatMessage.system(INSTRUCTION));
            request.addAll(history);
            ChatModel.Completion completion = model.complete(request, tools.specs());

            ToolCall call = completion.toolCall();
            if (call == null) {
                String text = completion.content() == null ? "" : completion.content().trim();
                history.add(ChatMessage.assistant(text));
                trace.logf("  LLM+tools finished in %dms (toolCalls=%d)", elapsedMs(start), toolCalls);
                trace.logf("  agent produced finalText=\"%s\"", text);
                if (text.startsWith(NEED_INPUT_PREFIX)) {
                    String question = text.substring(NEED_INPUT_PREFIX.length()).trim();
                    trace.logf("  → emit: input-required | question=\"%s\"", question);
                    return new Outcome.InputRequired(question, null, null);
                }
                String artifactText = text.isEmpty() ? "Готово." : text;
                trace.logf("  → emit: artifact + completed | artifact=\"%s\"", artifactText);
                return new Outcome.Completed(artifactText, capturedWidget);
            }

            toolCalls++;
            if (toolCalls > MAX_TOOL_CALLS_PER_TURN) {
                trace.logf("✖ tool-call limit (%d) exceeded — force-stopping the agent loop | session=%s",
                        MAX_TOOL_CALLS_PER_TURN, contextId);
                return new Outcome.Completed(
                        "Не удалось обработать запрос за отведённое число шагов — возможно, модель зациклилась. Попробуйте переформулировать запрос.",
                        null);
            }

            // HITL: initiate_refund with a concrete order id pauses the task for
            // user confirmation instead of executing (empty probing calls do not).
            if (call.name().equals("initiate_refund")) {
                String orderId = call.firstArg(OrderTools.ORDER_ID_KEYS);
                if (!orderId.isEmpty()) {
                    history.add(ChatMessage.assistantToolCall(call));
                    pendingConfirm.put(contextId, new PendingHITL(false, call.id(), orderId));
                    String question = String.format("Подтвердите оформление возврата по заказу %s? (да/нет)", orderId);
                    trace.logf("  ⏸ tool confirmation requested | callID=%s question=\"%s\"", call.id(), question);
                    trace.logf("  → emit: input-required (confirmation) | question=\"%s\"", question);
                    return new Outcome.InputRequired(question,
                            Widgets.refundConfirmWidget(orderId, question), Widgets.KIND_CONFIRMATION);
                }
            }

            trace.logf("%s  · LLM → агент: вызвать %s(%s)%s", GRAY, call.name(), compactArgs(call), RESET);
            OrderTools.ToolResult result = tools.execute(call);
            if (result.widget() != null) {
                capturedWidget = result.widget();
                trace.logf("%s  · инструмент → виджет %s%s", GRAY, capturedWidget.get("kind"), RESET);
            }
            trace.logf("%s  · инструмент %s → LLM: результат, снова спрашиваю LLM%s", GRAY, call.name(), RESET);
            history.add(ChatMessage.assistantToolCall(call));
            history.add(ChatMessage.tool(call.id(), result.text()));
        }
    }

    /**
     * Interprets a free-text reply to the refund yes/no question, failing CLOSED
     * via an allowlist: approved only when the reply is non-empty and EVERY word
     * is a recognised affirmative/confirm word (port of parseAffirmative).
     */
    public static boolean parseAffirmative(String text) {
        String lower = text == null ? "" : text.trim().toLowerCase();
        List<String> words = new ArrayList<>();
        StringBuilder cur = new StringBuilder();
        for (int i = 0; i < lower.length(); i++) {
            char c = lower.charAt(i);
            if (Character.isLetter(c)) {
                cur.append(c);
            } else if (!cur.isEmpty()) {
                words.add(cur.toString());
                cur.setLength(0);
            }
        }
        if (!cur.isEmpty()) {
            words.add(cur.toString());
        }
        if (words.isEmpty()) {
            return false;
        }
        for (String w : words) {
            if (!isAffirmativeWord(w)) {
                return false;
            }
        }
        return true;
    }

    private static boolean isAffirmativeWord(String w) {
        switch (w) {
            case "да", "ага", "угу", "давай", "конечно", "ладно", "хорошо",
                 "yes", "yeah", "yep", "y", "ок", "ok", "okay":
                return true;
            default:
                return w.startsWith("подтвер") || w.startsWith("оформ");
        }
    }

    private static String compactArgs(ToolCall call) {
        Map<String, Object> args = call.args();
        if (args.isEmpty()) {
            return "";
        }
        try {
            return Json.MAPPER.writeValueAsString(args);
        } catch (Exception e) {
            return String.valueOf(args);
        }
    }

    private static long elapsedMs(long startNanos) {
        return (System.nanoTime() - startNanos) / 1_000_000;
    }
}
