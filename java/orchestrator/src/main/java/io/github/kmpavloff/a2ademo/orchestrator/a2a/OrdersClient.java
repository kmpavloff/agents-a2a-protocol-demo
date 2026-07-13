package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.A2aTask;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.a2a.TaskState;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.BiConsumer;

/**
 * A2A client wrapper that delegates to the orders worker (port of
 * a2abridge/client.go): tracks pending input-required tasks per orchestrator
 * session so a follow-up user message resumes the exact same worker task, and
 * forwards widget DataParts to the UI, bypassing the LLM.
 */
public class OrdersClient {

    /**
     * Consecutive empty-message tool calls after which the agent loop must be
     * force-stopped (some models keep probing with an empty message forever).
     */
    public static final int EMPTY_CALL_LIMIT = 2;

    private final A2aClient client;
    private final WorkerProfile profile;
    private final Tracer trace;

    private final Map<String, Pending> pending = new ConcurrentHashMap<>();
    private final Map<String, Integer> emptyCalls = new ConcurrentHashMap<>();
    private volatile BiConsumer<String, Map<String, Object>> onWidget;
    private volatile FileHandler onFile;

    private record Pending(String taskId, String contextId) {}

    /** Receives downloadable files (raw parts) the worker attaches to artifacts. */
    public interface FileHandler {
        void accept(String sessionId, String filename, String mediaType, byte[] data);
    }

    public OrdersClient(A2aClient client, WorkerProfile profile, Tracer trace) {
        this.client = client;
        this.profile = profile;
        this.trace = trace;
    }

    public WorkerProfile profile() {
        return profile;
    }

    /** Registers a callback for widgets (DataParts) the worker emits. */
    public void setWidgetHandler(BiConsumer<String, Map<String, Object>> handler) {
        this.onWidget = handler;
    }

    /** Registers a callback for downloadable files (raw parts) the worker emits. */
    public void setFileHandler(FileHandler handler) {
        this.onFile = handler;
    }

    /**
     * Sends text to the orders worker, resuming a pending input-required task
     * when one exists for the session, and returns the agent's response text.
     */
    public String ask(String sessionId, String text) {
        trace.logf("──▶ orchestrator delegating to orders worker | session=%s", sessionId);

        A2aMessage msg;
        Pending p = pending.get(sessionId);
        if (p != null) {
            trace.logf("    resuming input-required task | taskID=%s contextID=%s", p.taskId(), p.contextId());
            msg = A2aMessage.forTask(A2aMessage.ROLE_USER, p.taskId(), p.contextId(), Part.text(text));
        } else {
            trace.logf("    starting new task (no pending task for session)");
            msg = A2aMessage.of(A2aMessage.ROLE_USER, Part.text(text));
        }
        trace.logf("    SendMessage role=user text=\"%s\"", text);

        A2aClient.SendResult res;
        try {
            res = client.sendMessage(msg);
        } catch (A2aClient.A2aException e) {
            trace.logf("    ✖ SendMessage failed: %s", e.getMessage());
            throw new A2aClient.A2aException("orders agent unreachable: " + e.getMessage(), e);
        }

        if (res.message() != null) {
            trace.logf("◀── response: Message (synchronous, no task) | parts=%d",
                    res.message().parts == null ? 0 : res.message().parts.size());
            pending.remove(sessionId);
            String result = res.message().firstText();
            if (result.isEmpty()) {
                result = "Готово.";
            }
            trace.logf("    ✔ result=\"%s\"", result);
            return result;
        }

        A2aTask task = res.task();
        trace.logf("◀── response: Task | id=%s contextID=%s state=%s", task.id, task.contextId,
                task.status == null ? "?" : task.status.state);
        if (task.status != null && TaskState.INPUT_REQUIRED.equals(task.status.state)) {
            pending.put(sessionId, new Pending(task.id, task.contextId));
            forwardWidget(sessionId, statusParts(task)); // e.g. confirmation widget
            String question = statusMessageText(task);
            trace.logf("    ⏸ input-required — stored pending task, asking user: \"%s\"", question);
            return "NEEDS_USER_INPUT: " + question;
        }

        pending.remove(sessionId);
        forwardWidget(sessionId, artifactParts(task)); // e.g. order / order-list widget
        forwardFiles(sessionId, artifactParts(task));  // e.g. the refund receipt
        String result = taskResultText(task);
        trace.logf("    ✔ terminal state, cleared pending | result=\"%s\"", result);
        return result;
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
        Pending p = pending.get(sessionId);
        return p == null ? "" : p.taskId();
    }

    private void forwardWidget(String sessionId, List<Part> parts) {
        BiConsumer<String, Map<String, Object>> handler = onWidget;
        if (handler == null) {
            return;
        }
        Map<String, Object> w = firstWidget(parts);
        if (w != null) {
            trace.logf("    ⟐ widget DataPart (%s) → UI, bypassing LLM", w.get("_kind"));
            handler.accept(sessionId, w);
        }
    }

    /**
     * Payload of the first DataPart whose metadata.kind marks it as a widget
     * ("widget/..."), with the kind injected under "_kind"; null when absent.
     */
    @SuppressWarnings("unchecked")
    static Map<String, Object> firstWidget(List<Part> parts) {
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

    /** Forwards every downloadable raw part (with a filename) to the file handler. */
    private void forwardFiles(String sessionId, List<Part> parts) {
        FileHandler handler = onFile;
        if (handler == null || parts == null) {
            return;
        }
        for (Part p : parts) {
            if (p == null || p.filename == null || p.filename.isEmpty() || p.raw == null) {
                continue;
            }
            byte[] data;
            try {
                data = java.util.Base64.getDecoder().decode(p.raw);
            } catch (IllegalArgumentException e) {
                continue;
            }
            trace.logf("    ⟐ file part \"%s\" (%s, %d bytes) → UI", p.filename, p.mediaType, data.length);
            handler.accept(sessionId, p.filename, p.mediaType, data);
        }
    }

    private static List<Part> statusParts(A2aTask t) {
        return t.status == null || t.status.message == null ? null : t.status.message.parts;
    }

    private static List<Part> artifactParts(A2aTask t) {
        return t.artifacts == null || t.artifacts.isEmpty() ? null : t.artifacts.getLast().parts;
    }

    static String statusMessageText(A2aTask t) {
        if (t.status != null && t.status.message != null && t.status.message.parts != null
                && !t.status.message.parts.isEmpty()) {
            return t.status.message.parts.getFirst().textOrEmpty();
        }
        return "Агенту по заказам нужны дополнительные данные.";
    }

    /**
     * Last artifact text of a completed task, falling back to the last history
     * message text, then "Готово." — empty text parts are skipped.
     */
    static String taskResultText(A2aTask t) {
        if (t.artifacts != null && !t.artifacts.isEmpty()) {
            for (Part p : t.artifacts.getLast().parts) {
                String txt = p.textOrEmpty();
                if (!txt.isEmpty()) {
                    return txt;
                }
            }
        }
        if (t.history != null && !t.history.isEmpty()) {
            for (Part p : t.history.getLast().parts) {
                String txt = p.textOrEmpty();
                if (!txt.isEmpty()) {
                    return txt;
                }
            }
        }
        return "Готово.";
    }
}
