package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.common.util.Cards;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;

import java.util.ArrayList;
import java.util.Base64;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * One A2A turn of the orchestrator web server (port of orchserver.go): maps
 * worker widgets to A2UI JSON and translates incoming A2UI button actions into
 * worker-task resumes, bypassing the LLM for pending refund confirmations.
 */
public class OrchestratorWebExecutor {

    private final OrchestratorAgent agent;
    private final OrdersClient orders;
    private final Tracer trace;

    /** A downloadable file the worker attached, passed through as a raw part. */
    private record AttachedFile(String name, String mediaType, byte[] data) {}

    /** sessionID → widgets/files produced during the current turn. */
    private final Map<String, List<Map<String, Object>>> widgets = new ConcurrentHashMap<>();
    private final Map<String, List<AttachedFile>> files = new ConcurrentHashMap<>();

    public OrchestratorWebExecutor(OrchestratorAgent agent, OrdersClient orders, Tracer trace) {
        this.agent = agent;
        this.orders = orders;
        this.trace = trace;
        // One global handler routes each widget/file to its session's slot.
        orders.setWidgetHandler((sessionId, w) ->
                widgets.computeIfAbsent(sessionId, k -> new ArrayList<>()).add(w));
        orders.setFileHandler((sessionId, filename, mediaType, data) ->
                files.computeIfAbsent(sessionId, k -> new ArrayList<>())
                        .add(new AttachedFile(filename, mediaType, data)));
    }

    /**
     * Maps an A2UI button action name + context to the user text the worker
     * should receive. Unknown actions fall back to a descriptive line.
     */
    static String actionToText(String name, Map<String, Object> ctx) {
        return switch (name) {
            case "approve_refund" -> "да";
            case "decline_refund" -> "нет";
            // The card number from the form's TextField ({path} binding resolved
            // by the renderer at click time). Resumed directly — never via the LLM.
            case "submit_refund_details" -> ctx.get("card_number") instanceof String s ? s : "";
            default -> "Пользователь нажал действие: " + name;
        };
    }

    /** Trace-safe echo of an action's user text: payment details are masked. */
    private static String safeActionEcho(String name, String userText) {
        if (name.equals("submit_refund_details")) {
            return Cards.mask(Cards.digits(userText));
        }
        return userText;
    }

    /** Runs one turn and returns the artifact parts (text first, then A2UI parts). */
    public List<Part> execute(String sessionId, A2aMessage message, boolean a2uiActive) {
        long reqStart = System.nanoTime();
        trace.logf("▶ orchestrator A2A request | contextID=%s a2ui=%s inParts=%d",
                sessionId, a2uiActive, message.parts == null ? 0 : message.parts.size());

        // Parse input: an A2UI action DataPart, or plain text.
        String userText = "";
        String actionName = "";
        if (message.parts != null) {
            for (Part p : message.parts) {
                A2ui.Action action = A2ui.parseAction(p.data);
                if (action != null) {
                    actionName = action.name();
                    userText = actionToText(actionName, action.context());
                    action.context().remove("card_number"); // keep the number out of the trace
                    trace.logf("  A2UI action \"%s\" ctx=%s → user text \"%s\"",
                            actionName, action.context(), safeActionEcho(actionName, userText));
                    break;
                }
                if (!p.textOrEmpty().isEmpty()) {
                    userText = p.textOrEmpty();
                }
            }
        }

        // Reset this session's widget/file slots before the run.
        widgets.remove(sessionId);
        files.remove(sessionId);

        // A button on a pending HITL step (yes/no confirmation, or the card
        // form) resumes the worker task DIRECTLY with the canonical answer,
        // bypassing the orchestrator LLM. The LLM tends to paraphrase "да" into
        // a full sentence, which the worker's fail-closed confirmation parser
        // rejects; and card details should never pass through an LLM at all.
        boolean directResume = actionName.equals("approve_refund")
                || actionName.equals("decline_refund")
                || actionName.equals("submit_refund_details");
        if (directResume && !orders.pendingTaskId(sessionId).isEmpty()) {
            trace.logf("  confirmation button \"%s\" → resuming worker directly with \"%s\" (LLM bypassed)",
                    actionName, safeActionEcho(actionName, userText));
            String result = orders.ask(sessionId, userText);
            // The resume may complete (receipt widget + file) or pause again
            // (the card form after "да") — emit widgets/files like a normal turn.
            List<Part> parts = new ArrayList<>();
            parts.add(Part.text(orDefault(stripNeedsInput(result))));
            parts.addAll(a2uiParts(a2uiActive, widgets.remove(sessionId)));
            parts.addAll(fileParts(files.remove(sessionId)));
            trace.logf("  → emit: artifact + completed | direct HITL resume | parts=%d", parts.size());
            return parts;
        }

        // Run the orchestrator LLM. The delegating tool forwards any widget
        // through the session handler above.
        trace.logf("  · оркестратор → LLM: \"%s\"", userText);
        long llmStart = System.nanoTime();
        int[] toolCalls = {0};
        String finalText = agent.runTurn(sessionId, userText, new OrchestratorAgent.TurnListener() {
            @Override
            public void onToolCall(String name, String argsJson) {
                toolCalls[0]++;
                trace.logf("  · LLM → инструмент: %s(%s) [#%d]", name,
                        argsJson == null ? "" : argsJson.trim(), toolCalls[0]);
            }

            @Override
            public void onToolResult(String name) {
                trace.logf("  · инструмент %s → LLM: результат", name);
            }
        });
        trace.logf("  LLM finished in %dms | toolCalls=%d finalText=\"%s\"",
                (System.nanoTime() - llmStart) / 1_000_000, toolCalls[0], finalText.trim());

        // Drain this session's widget/file slots unconditionally so text-only
        // sessions don't leak map entries; A2UI parts only when active.
        List<Part> parts = new ArrayList<>();
        parts.add(Part.text(orDefault(finalText.trim())));
        parts.addAll(a2uiParts(a2uiActive, widgets.remove(sessionId)));
        parts.addAll(fileParts(files.remove(sessionId)));
        trace.logf("  → emit: artifact + completed | textPart=1 extraParts=%d requestTook=%dms",
                parts.size() - 1, (System.nanoTime() - reqStart) / 1_000_000);
        return parts;
    }

    /** Maps collected widgets to A2UI DataParts when the extension is active. */
    private List<Part> a2uiParts(boolean a2uiActive, List<Map<String, Object>> ws) {
        if (ws == null || ws.isEmpty()) {
            return List.of();
        }
        if (!a2uiActive) {
            trace.logf("  A2UI inactive — %d widget(s) dropped, text-only response", ws.size());
            return List.of();
        }
        List<Part> parts = new ArrayList<>();
        for (Map<String, Object> w : ws) {
            List<Map<String, Object>> msgs = A2ui.fromWidget(w);
            if (msgs == null) {
                continue;
            }
            trace.logf("  A2UI: widget %s → %d message(s) (%s)", w.get("_kind"), msgs.size(), A2ui.MIME_TYPE);
            for (Map<String, Object> m : msgs) {
                Part part = Part.data(m, null);
                part.mediaType = A2ui.MIME_TYPE;
                parts.add(part);
            }
        }
        return parts;
    }

    /**
     * Passes worker-attached files through as A2A raw parts (attached
     * regardless of A2UI: files are plain protocol parts, not generative UI).
     */
    private static List<Part> fileParts(List<AttachedFile> fs) {
        if (fs == null || fs.isEmpty()) {
            return List.of();
        }
        List<Part> parts = new ArrayList<>();
        for (AttachedFile f : fs) {
            Part p = new Part();
            p.raw = Base64.getEncoder().encodeToString(f.data());
            p.filename = f.name();
            p.mediaType = f.mediaType();
            parts.add(p);
        }
        return parts;
    }

    /**
     * Removes the delegating tool's NEEDS_USER_INPUT sentinel from a directly
     * resumed result, leaving the bare question for the browser (the widget
     * carries the interactive form).
     */
    private static String stripNeedsInput(String s) {
        String t = s == null ? "" : s.trim();
        if (t.startsWith("NEEDS_USER_INPUT:")) {
            t = t.substring("NEEDS_USER_INPUT:".length()).trim();
        }
        return t;
    }

    private static String orDefault(String s) {
        return s == null || s.isBlank() ? "Готово." : s;
    }
}
