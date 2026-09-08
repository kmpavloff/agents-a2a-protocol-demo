package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.BiConsumer;

/**
 * Делегирующий инструмент поверх {@link Remote} (порт a2abridge/client.go):
 * протокол, сессии и разбор ответа по слоям ведёт {@code Remote}, а этот класс
 * — то, что видит модель, — раскладывает слои по обработчикам UI и отдаёт
 * модели только текст.
 */
public class OrdersClient {

    /**
     * Consecutive empty-message tool calls after which the agent loop must be
     * force-stopped (some models keep probing with an empty message forever).
     */
    public static final int EMPTY_CALL_LIMIT = 2;

    private final Remote remote;
    private final Tracer trace;

    private final Map<String, Integer> emptyCalls = new ConcurrentHashMap<>();
    private volatile BiConsumer<String, Map<String, Object>> onWidget;
    private volatile BiConsumer<String, List<Map<String, Object>>> onA2ui;
    private volatile FileHandler onFile;
    private volatile BiConsumer<String, String> onText;

    /** Receives downloadable files (raw parts) the worker attaches to artifacts. */
    public interface FileHandler {
        void accept(String sessionId, String filename, String mediaType, byte[] data);
    }

    public OrdersClient(Remote remote, Tracer trace) {
        this.remote = remote;
        this.trace = trace;
    }

    public Remote remote() {
        return remote;
    }

    public WorkerProfile profile() {
        return remote.profile();
    }

    /** Registers a callback for widgets (DataParts) the worker emits. */
    public void setWidgetHandler(BiConsumer<String, Map<String, Object>> handler) {
        this.onWidget = handler;
    }

    /** Registers a callback for downloadable files (raw parts) the worker emits. */
    public void setFileHandler(FileHandler handler) {
        this.onFile = handler;
    }

    /** Разметка, которую агент написал сам (в отличие от виджетов, что мапит шлюз). */
    public void setA2uiHandler(BiConsumer<String, List<Map<String, Object>>> handler) {
        this.onA2ui = handler;
    }

    /**
     * Собственный текст ответа агента. Не для показа: он уходит модели как
     * результат инструмента и обычно ей же и пересказывается. Нужен как запас на
     * ход, в котором карточки не окажется, — тогда данные есть только здесь.
     */
    public void setTextHandler(BiConsumer<String, String> handler) {
        this.onText = handler;
    }

    /**
     * Шлёт текст удалённому агенту и возвращает то, что увидит модель. Виджеты,
     * разметка и файлы уходят в UI мимо неё.
     */
    public String ask(String sessionId, String text, boolean wantA2ui) {
        Remote.Reply reply = remote.ask(sessionId, text, wantA2ui);
        forward(sessionId, reply);
        return reply.needsInput() ? "NEEDS_USER_INPUT: " + reply.text() : reply.text();
    }

    /** Раскладывает слои ответа по зарегистрированным обработчикам. */
    private void forward(String sessionId, Remote.Reply reply) {
        BiConsumer<String, Map<String, Object>> widget = onWidget;
        if (widget != null) {
            for (Map<String, Object> w : reply.widgets()) {
                trace.logf("    ⟐ widget DataPart (%s) → UI, bypassing LLM", w.get("_kind"));
                widget.accept(sessionId, w);
            }
        }
        BiConsumer<String, List<Map<String, Object>>> a2ui = onA2ui;
        if (a2ui != null && !reply.a2ui().isEmpty()) {
            trace.logf("    ⟐ %d A2UI message(s) from the agent → UI", reply.a2ui().size());
            a2ui.accept(sessionId, reply.a2ui());
        }
        FileHandler file = onFile;
        if (file != null) {
            for (Remote.AttachedFile f : reply.files()) {
                trace.logf("    ⟐ file part \"%s\" (%s, %d bytes) → UI", f.name(), f.mediaType(), f.data().length);
                file.accept(sessionId, f.name(), f.mediaType(), f.data());
            }
        }
        BiConsumer<String, String> textHandler = onText;
        if (textHandler != null && !reply.text().isBlank()) {
            textHandler.accept(sessionId, reply.text());
        }
    }

    /**
     * Records one consecutive empty-message call and returns the tool reply plus
     * whether the agent loop must be force-stopped.
     */
    public EmptyReply emptyMessageReply(String sessionId) {
        int n = emptyCalls.merge(sessionId, 1, Integer::sum);
        if (n >= EMPTY_CALL_LIMIT) {
            trace.logf("✖ %d empty tool calls in a row — force-stopping the agent loop | session=%s", n, sessionId);
            return new EmptyReply(
                    "Запрос не выполнен: поле message пустое. Ответьте пользователю обычным текстом (например, уточните, что он хочет узнать о заказах) — НЕ вызывайте инструмент снова.",
                    true);
        }
        trace.logf("✖ empty message (#%d of %d) | session=%s", n, EMPTY_CALL_LIMIT, sessionId);
        return new EmptyReply(
                "Пустой запрос: укажите конкретный вопрос или действие по заказам в поле message. Если запрос пользователя неясен, задайте ему уточняющий вопрос обычным текстом, не вызывая инструмент с пустым message.",
                false);
    }

    public record EmptyReply(String reply, boolean stop) {}

    /** Resets the consecutive empty-call counter (called on every real delegation). */
    public void clearEmpty(String sessionId) {
        emptyCalls.remove(sessionId);
    }

    /** A2A task id pending for the session, or "" — used by the web executor and tests. */
    public String pendingTaskId(String sessionId) {
        return remote.pendingTaskId(sessionId);
    }

    /**
     * Payload of the first DataPart whose metadata.kind marks it as a widget
     * ("widget/..."), with the kind injected under "_kind"; null when absent.
     */
    @SuppressWarnings("unchecked")
    public static Map<String, Object> firstWidget(List<Part> parts) {
        if (parts == null) {
            return null;
        }
        for (Part p : parts) {
            if (p == null || p.metadata == null) {
                continue;
            }
            Object kind = p.metadata.get("kind");
            if (!(kind instanceof String k) || !k.startsWith("widget/")) {
                continue;
            }
            if (!(p.data instanceof Map<?, ?> data)) {
                continue;
            }
            Map<String, Object> out = new LinkedHashMap<>();
            out.put("_kind", k);
            out.putAll((Map<String, Object>) data);
            return out;
        }
        return null;
    }
}
