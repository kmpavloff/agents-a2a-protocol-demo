package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;

import java.util.ArrayList;
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

    /** sessionID → widgets produced during the current turn. */
    private final Map<String, List<Map<String, Object>>> widgets = new ConcurrentHashMap<>();

    public OrchestratorWebExecutor(OrchestratorAgent agent, OrdersClient orders, Tracer trace) {
        this.agent = agent;
        this.orders = orders;
        this.trace = trace;
        // One global handler routes each widget to its session's slot.
        orders.setWidgetHandler((sessionId, w) ->
                widgets.computeIfAbsent(sessionId, k -> new ArrayList<>()).add(w));
    }

    /**
     * Maps an A2UI button action name to the user text the orchestrator LLM
     * should process. Unknown actions fall back to a descriptive line.
     */
    static String actionToText(String name) {
        return switch (name) {
            case "approve_refund" -> "да";
            case "decline_refund" -> "нет";
            default -> "Пользователь нажал действие: " + name;
        };
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
                    userText = actionToText(actionName);
                    trace.logf("  A2UI action \"%s\" ctx=%s → user text \"%s\"", actionName, action.context(), userText);
                    break;
                }
                if (!p.textOrEmpty().isEmpty()) {
                    userText = p.textOrEmpty();
                }
            }
        }

        // Reset this session's widget slot before the run.
        widgets.remove(sessionId);

        // A yes/no button on a pending confirmation resumes the worker task
        // DIRECTLY with the canonical answer, bypassing the orchestrator LLM.
        // The LLM tends to paraphrase "да" into a full sentence, which the
        // worker's fail-closed confirmation parser then rejects — silently
        // declining a refund the user actually approved.
        if ((actionName.equals("approve_refund") || actionName.equals("decline_refund"))
                && !orders.pendingTaskId(sessionId).isEmpty()) {
            trace.logf("  confirmation button \"%s\" → resuming worker directly with \"%s\" (LLM bypassed)",
                    actionName, userText);
            String result = orders.ask(sessionId, userText);
            trace.logf("  → emit: artifact + completed | direct confirmation resume | result=\"%s\"", result);
            return List.of(Part.text(orDefault(result)));
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

        // Drain this session's widget slot unconditionally so text-only sessions
        // don't leak a map entry; only emit A2UI parts when the extension is active.
        List<Map<String, Object>> ws = widgets.remove(sessionId);

        List<Part> parts = new ArrayList<>();
        parts.add(Part.text(orDefault(finalText.trim())));
        if (a2uiActive) {
            if (ws != null) {
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
            }
        } else if (ws != null && !ws.isEmpty()) {
            trace.logf("  A2UI inactive — %d widget(s) dropped, text-only response", ws.size());
        }
        trace.logf("  → emit: artifact + completed | textPart=1 a2uiParts=%d requestTook=%dms",
                parts.size() - 1, (System.nanoTime() - reqStart) / 1_000_000);
        return parts;
    }

    private static String orDefault(String s) {
        return s == null || s.isBlank() ? "Готово." : s;
    }
}
