package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.common.util.Cards;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Remote;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2uiParts;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.Surfaces;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;

import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeSet;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Один ход браузерного разговора: выбор агента, HITL-кнопки, локальная модель и
 * сборка ответных частей (порт a2abridge/orchserver.go).
 */
public class OrchestratorWebExecutor {

    /** Значение agentId, означающее «модель выбирает агента сама». */
    public static final String AUTO_AGENT_ID = "auto";

    /** Собирает агента под конкретный набор делегирующих инструментов. */
    @FunctionalInterface
    public interface AgentBuilder {
        OrchestratorAgent build(List<OrdersClient> tools, String summary);
    }

    private final Registry reg;
    private final AgentBuilder build;
    private final Tracer trace;

    /**
     * Разводит поверхности разных ходов. Сквозной, а не по сессиям: нужна лишь
     * уникальность в пределах страницы, а карта по сессиям росла бы без конца —
     * «конца сессии» в протоколе нет.
     */
    private final AtomicLong surfaceSeq = new AtomicLong();

    /** agentId (или ключ набора «Авто») → собранный агент. */
    private final Map<String, OrchestratorAgent> agents = new ConcurrentHashMap<>();

    /** Поколение реестра, под которое собран кэш агентов. */
    private final AtomicLong gen = new AtomicLong();

    /** Охраняет инвалидацию кэша агентов: очистка и публикация поколения — одна атомарная операция. */
    private final Object cacheLock = new Object();

    private final Map<String, List<Map<String, Object>>> widgets = new ConcurrentHashMap<>();
    private final Map<String, List<Map<String, Object>>> a2uis = new ConcurrentHashMap<>();
    private final Map<String, List<Remote.AttachedFile>> files = new ConcurrentHashMap<>();
    private final Map<String, List<String>> texts = new ConcurrentHashMap<>();

    public OrchestratorWebExecutor(Registry reg, AgentBuilder build, Tracer trace) {
        this.reg = reg;
        this.build = build;
        this.trace = trace;
        // Обработчики вешаются на КАЖДОГО клиента, включая созданных после
        // правки конфига: разовый цикл оставил бы новых без них, и их виджеты
        // молча пропадали бы.
        reg.setClientInit(c -> {
            c.setWidgetHandler((s, w) -> widgets.computeIfAbsent(s, k -> new ArrayList<>()).add(w));
            c.setA2uiHandler((s, msgs) -> a2uis.computeIfAbsent(s, k -> new ArrayList<>()).addAll(msgs));
            c.setFileHandler((s, name, mime, data) ->
                    files.computeIfAbsent(s, k -> new ArrayList<>()).add(new Remote.AttachedFile(name, mime, data)));
            c.setTextHandler((s, t) -> texts.computeIfAbsent(s, k -> new ArrayList<>()).add(t));
        });
        gen.set(reg.generation());
    }

    /** Один ход. Возвращает части завершающего сообщения задачи. */
    public List<Part> execute(String sessionId, A2aMessage message, boolean a2uiActive) {
        long reqStart = System.nanoTime();
        trace.logf("▶ orchestrator A2A request | contextID=%s a2ui=%s inParts=%d",
                sessionId, a2uiActive, message.parts == null ? 0 : message.parts.size());

        String userText = "";
        String actionName = "";
        String actionSurface = "";
        String actionSource = "";
        Map<String, Object> actionCtx = Map.of();
        if (message.parts != null) {
            for (Part p : message.parts) {
                A2ui.Action a = A2ui.parseAction(p.data);
                if (a != null) {
                    actionName = a.name();
                    actionCtx = a.context();
                    A2ui.Origin origin = A2ui.actionOrigin(p.data);
                    actionSurface = origin.surfaceId();
                    actionSource = origin.sourceComponentId();
                    userText = actionToPrompt(actionName, actionCtx);
                    trace.logf("  A2UI action \"%s\" ctx=%s → user text \"%s\"",
                            actionName, redactCard(actionCtx), safeActionEcho(actionName, userText));
                    break;
                }
                if (!p.textOrEmpty().isEmpty()) {
                    userText = p.textOrEmpty();
                }
            }
        }

        String agentId = selectAgent(message);
        long turn = surfaceSeq.incrementAndGet();
        trace.logf("  agent selection: %s | ход #%d", agentId, turn);

        drain(sessionId); // сбросить слоты сессии перед ходом

        // Явно выбранный verbatim-агент отвечает пользователю напрямую: его
        // текст и его A2UI уходят в браузер нетронутыми. Гонять локальную модель
        // поверх готового ответа значило бы пересказать его и добавить свою
        // задержку к задержке удалённого агента.
        Remote remote = reg.get(agentId).orElse(null);
        if (remote != null && !agentId.equals(AUTO_AGENT_ID) && remote.verbatim()) {
            trace.logf("  verbatim agent \"%s\" → no local LLM", agentId);
            try {
                Remote.Reply reply;
                if (!actionName.isEmpty()) {
                    // Поверхность возвращается агенту под его собственным
                    // именем: в ленте она переименована по ходам, а сопоставить
                    // событие агент может только со своим id.
                    String surface = Surfaces.untag(actionSurface);
                    trace.logf("  A2UI action \"%s\" → событие агенту | surface=\"%s\" source=\"%s\" ctx=%s",
                            actionName, surface, actionSource, redactCard(actionCtx));
                    reply = remote.askAction(sessionId, actionName, surface, actionSource, actionCtx, a2uiActive);
                } else {
                    reply = remote.ask(sessionId, userText, a2uiActive);
                }
                return emit(reply.text(), reply.widgets(), reply.a2ui(), reply.files(),
                        a2uiActive, turn, "verbatim", reqStart);
            } catch (RuntimeException e) {
                trace.logf("  ✖ verbatim turn failed: %s", e.getMessage());
                return emit(agentErrorText(remote.name(), e), List.of(), List.of(), List.of(),
                        a2uiActive, turn, "verbatim error", reqStart);
            }
        }

        // Кнопка на зависшем HITL-шаге продолжает задачу НАПРЯМУЮ каноническим
        // ответом, минуя модель: та пересказывает «да» целым предложением, и
        // fail-closed парсер воркера такой ответ отвергает, а данные карты через
        // модель вообще проходить не должны.
        boolean directResume = actionName.equals("approve_refund")
                || actionName.equals("decline_refund")
                || actionName.equals("submit_refund_details");
        OrdersClient pendingClient = directResume ? pendingClient(agentId, sessionId) : null;
        if (pendingClient != null) {
            String canonical = actionToText(actionName, actionCtx);
            trace.logf("  confirmation button \"%s\" → resuming worker directly with \"%s\" (LLM bypassed)",
                    actionName, safeActionEcho(actionName, canonical));
            try {
                String result = pendingClient.ask(sessionId, canonical, a2uiActive);
                Drained d = drain(sessionId);
                return emit(stripNeedsInput(result), d.widgets(), d.a2ui(), d.files(),
                        a2uiActive, turn, "direct HITL resume", reqStart);
            } catch (RuntimeException e) {
                trace.logf("  ✖ direct resume error: %s", e.getMessage());
                return emit(agentErrorText(pendingClient.remote().name(), e), List.of(), List.of(), List.of(),
                        a2uiActive, turn, "direct resume error", reqStart);
            }
        }

        OrchestratorAgent agent;
        try {
            agent = agentFor(agentId);
        } catch (RuntimeException e) {
            trace.logf("  ✖ agent unavailable: %s", e.getMessage());
            return emit("Не удалось обратиться к агенту: " + e.getMessage(), List.of(), List.of(), List.of(),
                    a2uiActive, turn, "agent error", reqStart);
        }

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
        }, a2uiActive);
        trace.logf("  LLM finished in %dms | toolCalls=%d finalText=\"%s\"",
                (System.nanoTime() - llmStart) / 1_000_000, toolCalls[0], finalText.trim());

        Drained d = drain(sessionId);
        // Увидит ли пользователь карточку: в текстовом режиме собранные виджеты
        // до него не доедут, и подводка модели останется единственным ответом.
        boolean visual = a2uiActive && (!d.widgets().isEmpty() || !d.a2ui().isEmpty());
        String answer = pickAnswer(finalText, d.texts(), visual);
        if (!answer.equals(finalText)) {
            trace.logf("  карточки не будет, а подводка модели пуста по содержанию — "
                    + "в ленту уходит ответ агента (%d символов вместо %d)",
                    answer.length(), finalText.trim().length());
        }
        return emit(answer, d.widgets(), d.a2ui(), d.files(), a2uiActive, turn, "llm turn", reqStart);
    }

    /**
     * Читает выбранного в UI агента из метаданных сообщения. Незнакомый id молча
     * откатывается в «Авто»: браузер с устаревшим списком не должен ломать
     * разговор.
     */
    private String selectAgent(A2aMessage msg) {
        if (msg == null || msg.metadata == null) {
            return AUTO_AGENT_ID;
        }
        if (!(msg.metadata.get("agentId") instanceof String id) || id.isEmpty() || id.equals(AUTO_AGENT_ID)) {
            return AUTO_AGENT_ID;
        }
        if (reg.get(id).isEmpty()) {
            trace.logf("⚠ unknown agentId \"%s\" — falling back to auto", id);
            return AUTO_AGENT_ID;
        }
        return id;
    }

    /**
     * Агент под выбор пользователя: со всеми инструментами в режиме «Авто» и
     * ровно с одним при явном выборе. Кэшируется по выбору.
     */
    private OrchestratorAgent agentFor(String agentId) {
        // Состав агентов мог измениться из UI. Кэш собран под прежний: в нём и
        // старое описание в промпте, и инструмент удалённого агента.
        long current = reg.generation();
        if (current != gen.get()) {
            synchronized (cacheLock) {
                // Перепроверка под блокировкой: пока мы её брали, кэш мог уже
                // почистить сосед. Сначала чистим, потом публикуем поколение —
                // иначе поток, увидевший новое значение, пройдёт мимо ветки
                // инвалидации и достанет устаревшего агента из ещё не очищенного кэша.
                if (current != gen.get()) {
                    agents.clear();
                    gen.set(current);
                }
            }
        }

        List<OrdersClient> tools;
        String summary;
        String cacheKey;
        if (agentId.equals(AUTO_AGENT_ID)) {
            tools = reg.availableClients();
            summary = String.join("\n\n", reg.summaries());
            // Кэш «Авто» привязан к набору ДОСТУПНЫХ агентов: лежавший в момент
            // первой сборки агент иначе остался бы без инструмента навсегда,
            // хотя UI уже показывает его живым.
            TreeSet<String> names = new TreeSet<>();
            for (OrdersClient c : tools) {
                names.add(c.profile().toolName());
            }
            cacheKey = AUTO_AGENT_ID + "|" + String.join(",", names);
        } else {
            Remote remote = reg.get(agentId).orElseThrow(
                    () -> new IllegalStateException("unknown agent \"" + agentId + "\""));
            remote.connect();
            tools = List.of(reg.clientFor(agentId));
            summary = remote.profile().summary();
            cacheKey = agentId;
        }
        if (tools.isEmpty()) {
            throw new IllegalStateException("нет доступных агентов");
        }
        return agents.computeIfAbsent(cacheKey, k -> build.build(tools, summary));
    }

    /**
     * Клиент того агента, у которого для сессии висит input-required задача,
     * чтобы HITL-кнопка продолжила именно её. При явном выборе рассматривается
     * только выбранный агент.
     */
    private OrdersClient pendingClient(String agentId, String sessionId) {
        List<String> ids = agentId.equals(AUTO_AGENT_ID) ? reg.ids() : List.of(agentId);
        for (String id : ids) {
            Remote r = reg.get(id).orElse(null);
            if (r == null || r.pendingTaskId(sessionId).isEmpty()) {
                continue;
            }
            return reg.clientFor(id);
        }
        return null;
    }

    /** Всё, что собралось за ход в слотах сессии. */
    private record Drained(List<Map<String, Object>> widgets, List<Map<String, Object>> a2ui,
                           List<Remote.AttachedFile> files, List<String> texts) {}

    private Drained drain(String sessionId) {
        List<Map<String, Object>> w = widgets.remove(sessionId);
        List<Map<String, Object>> a = a2uis.remove(sessionId);
        List<Remote.AttachedFile> f = files.remove(sessionId);
        List<String> t = texts.remove(sessionId);
        return new Drained(w == null ? List.of() : w, a == null ? List.of() : a,
                f == null ? List.of() : f, t == null ? List.of() : t);
    }

    /**
     * Что показать пользователю за ход, отработанный локальной моделью.
     *
     * <p>Промпт запрещает модели называть значения: их покажет карточка, а
     * модель может ошибиться. Для нашего воркера это верно всегда — он шлёт
     * виджет на каждый ответ. Для внешнего агента нет: он вправе прислать один
     * текст, и тогда от подводки «Вот детали вашего заказа:» пользователю нет
     * никакой пользы — данные остались только в ответе агента. То же в текстовом
     * режиме, где виджеты выбрасываются на нашей стороне.
     */
    public static String pickAnswer(String llmText, List<String> agentTexts, boolean visual) {
        if (visual) {
            return llmText;
        }
        String agent = String.join("\n\n", agentTexts).trim();
        String llm = llmText == null ? "" : llmText.trim();
        return agent.isEmpty() || agent.length() <= llm.length() ? llmText : agent;
    }

    /** Канонический ответ для наших HITL-кнопок. */
    static String actionToText(String name, Map<String, Object> ctx) {
        return switch (name) {
            case "approve_refund" -> "да";
            case "decline_refund" -> "нет";
            // Номер карты из TextField формы: связка {path} разрешена рендерером
            // в момент клика. Продолжается напрямую — никогда через модель.
            case "submit_refund_details" -> ctx.get("card_number") instanceof String s ? s : "";
            default -> "Пользователь нажал действие: " + name;
        };
    }

    /**
     * Действие A2UI как фраза для агента, говорящего только текстом. У наших
     * HITL-кнопок есть канонические ответы; чужая кнопка описывается вместе с
     * контекстом — без него модель видит «нажал return_order» и не знает, какой
     * заказ.
     */
    public static String actionToPrompt(String name, Map<String, Object> ctx) {
        if (name.equals("approve_refund") || name.equals("decline_refund")
                || name.equals("submit_refund_details")) {
            return actionToText(name, ctx);
        }
        String label = ctx.get("label") instanceof String s && !s.isEmpty() ? s : name;
        TreeSet<String> details = new TreeSet<>();
        ctx.forEach((k, v) -> {
            if (!k.equals("label")) {
                details.add(k + ": " + v);
            }
        });
        String out = "Пользователь нажал кнопку «" + label + "»";
        return details.isEmpty() ? out : out + " (" + String.join(", ", details) + ")";
    }

    /**
     * Сбой хода строкой для ленты. Провалившийся ход и недоступный агент — разные
     * беды, и валить их в одну формулировку нельзя: «недоступен» отправит
     * пользователя чинить сеть там, где агент на связи и просто не справился.
     */
    public static String agentErrorText(String name, RuntimeException e) {
        if (e instanceof Remote.TurnFailedException failed) {
            String why = failed.firstLine();
            return why.isEmpty()
                    ? "Агент \"" + name + "\" не смог выполнить запрос."
                    : "Агент \"" + name + "\" не смог выполнить запрос: " + why;
        }
        return "Агент \"" + name + "\" недоступен: " + e.getMessage();
    }

    /** Копия контекста без номера карты — для трейса; исходную карту не правим. */
    private static Map<String, Object> redactCard(Map<String, Object> ctx) {
        Map<String, Object> out = new LinkedHashMap<>(ctx);
        out.remove("card_number");
        return out;
    }

    /** Текст действия для трейса: платёжные данные маскируются. */
    private static String safeActionEcho(String name, String userText) {
        return name.equals("submit_refund_details") ? Cards.mask(Cards.digits(userText)) : userText;
    }

    /** Убирает служебный префикс NEEDS_USER_INPUT из напрямую продолженного результата. */
    private static String stripNeedsInput(String s) {
        String t = s.trim();
        return t.startsWith("NEEDS_USER_INPUT:") ? t.substring("NEEDS_USER_INPUT:".length()).trim() : t;
    }

    /** Собирает части ответа и пишет итог хода в трейс. */
    private List<Part> emit(String text, List<Map<String, Object>> ws, List<Map<String, Object>> msgs,
                            List<Remote.AttachedFile> fs, boolean a2uiActive, long turn,
                            String what, long reqStart) {
        List<Part> parts = new ArrayList<>();
        String body = text == null || text.isBlank() ? "Готово." : text.trim();
        parts.add(Part.text(body));
        parts.addAll(widgetParts(a2uiActive, ws));
        parts.addAll(agentMarkupParts(a2uiActive, msgs, turn));
        parts.addAll(fileParts(fs));
        trace.logf("  → emit: completed message | %s | parts=%d requestTook=%dms",
                what, parts.size(), (System.nanoTime() - reqStart) / 1_000_000);
        return parts;
    }

    /** Доменные виджеты, отображённые в A2UI, когда расширение активно. */
    private List<Part> widgetParts(boolean a2uiActive, List<Map<String, Object>> ws) {
        if (ws.isEmpty()) {
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
            parts.add(A2uiParts.message(msgs));
        }
        return parts;
    }

    /**
     * Разметка, написанная самим агентом. В отличие от виджетов её не надо
     * отображать — она уже прошла нормализацию; но каждому ходу нужна своя
     * поверхность: повторный createSurface с тем же id рендерер не переживает.
     */
    private List<Part> agentMarkupParts(boolean a2uiActive, List<Map<String, Object>> msgs, long turn) {
        if (msgs.isEmpty()) {
            return List.of();
        }
        if (!a2uiActive) {
            trace.logf("  A2UI inactive — %d agent message(s) dropped, text-only response", msgs.size());
            return List.of();
        }
        List<Map<String, Object>> retagged = Surfaces.retag(msgs, "-t" + turn);
        trace.logf("  A2UI: %d message(s) from the agent passed through (%s)", retagged.size(), A2ui.MIME_TYPE);
        return List.of(A2uiParts.message(retagged));
    }

    /** Файлы уходят как есть, независимо от A2UI: это части протокола, а не UI. */
    private static List<Part> fileParts(List<Remote.AttachedFile> fs) {
        List<Part> parts = new ArrayList<>();
        for (Remote.AttachedFile f : fs) {
            Part p = new Part();
            p.raw = Base64.getEncoder().encodeToString(f.data());
            p.filename = f.name();
            p.mediaType = f.mediaType();
            parts.add(p);
        }
        return parts;
    }
}
