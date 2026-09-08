package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.A2aTask;
import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.a2a.TaskState;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2uiIngest;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2uiParts;

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Одно соединение с одним удалённым A2A-агентом: карточка, аутентификация,
 * сессии и один ход разговора (порт a2abridge/remote.go).
 *
 * <p>Знает про особенности чужих агентов (нестандартный путь карточки, нерабочий
 * адрес внутри неё, metadata.skill), но ничего не знает про LLM и про домен
 * заказов.
 */
public class Remote {

    /** Как часто опрашивать GetTask, пока задача в работе. Контракт предписывает 1–2 секунды. */
    private static final Duration POLL_INTERVAL = Duration.ofSeconds(2);

    /** Скачанный агентом файл, переданный дальше как есть. */
    public record AttachedFile(String name, String mediaType, byte[] data) {}

    /**
     * Что удалённый агент вернул за один ход.
     *
     * @param needsInput задача встала в input-required: агент ждёт ответа
     *                   пользователя и продолжит ТУ ЖЕ задачу
     * @param a2ui       готовые к рендеру сообщения A2UI (уже нормализованные)
     * @param widgets    доменные виджеты нашего воркера (DataPart с metadata.kind)
     */
    public record Reply(String text, boolean needsInput, List<Map<String, Object>> a2ui,
                        List<Map<String, Object>> widgets, List<AttachedFile> files) {}

    /**
     * Агент ответил, но ход не удался: задача пришла в FAILED/REJECTED/CANCELED,
     * а причина лежит в её статусном сообщении.
     *
     * <p>Отдельный тип нужен, чтобы интерфейс не путал это с недоступностью:
     * агент на связи и отвечает, просто не смог выполнить именно этот запрос.
     * Формулировка «агент недоступен» на таком сбое — неправда, а пользователь
     * по ней пойдёт проверять сеть вместо того, чтобы повторить ход.
     */
    public static class TurnFailedException extends RuntimeException {
        private final String reason;

        public TurnFailedException(String agentId, String state, String reason) {
            super("agent \"" + agentId + "\" turn failed (" + state + "): " + reason);
            this.reason = reason;
        }

        /**
         * Первая содержательная строка причины. Агенты кладут в неё
         * многострочный текст — сообщение об ошибке плюс ссылку на документацию,
         * — а в ленте нужна одна строка.
         */
        public String firstLine() {
            for (String line : reason.split("\n")) {
                String s = line.trim();
                if (!s.isEmpty()) {
                    return s;
                }
            }
            return reason;
        }
    }

    private record Pending(String taskId, String contextId) {}

    private final AgentConfig cfg;
    private final Tracer trace;
    private final Object connectLock = new Object();

    private volatile A2aClient client;
    private volatile AgentCard card;
    private volatile WorkerProfile profile = new WorkerProfile("", "", "");
    private volatile boolean available;
    private volatile boolean probed;
    private volatile String toolName = "";

    private final Map<String, Pending> pending = new ConcurrentHashMap<>();
    private final Map<String, String> contexts = new ConcurrentHashMap<>();

    /**
     * Создаёт соединение, но ещё не открывает его: карточка резолвится лениво
     * при первом обращении, чтобы выключенный агент в локальной сети не мешал
     * оркестратору стартовать.
     */
    public Remote(AgentConfig cfg, Tracer trace) {
        this.cfg = cfg;
        this.trace = trace;
    }

    public String id() {
        return cfg.id();
    }

    /** Подпись агента для UI: из конфига, иначе из карточки, иначе id. */
    public String name() {
        if (!cfg.name().isEmpty()) {
            return cfg.name();
        }
        AgentCard c = card;
        if (c != null && c.name != null && !c.name.isEmpty()) {
            return c.name;
        }
        return cfg.id();
    }

    public String description() {
        if (!cfg.description().isEmpty()) {
            return cfg.description();
        }
        AgentCard c = card;
        return c == null || c.description == null ? "" : c.description;
    }

    public boolean verbatim() {
        return cfg.verbatim();
    }

    /** Удалось ли последнее подключение. */
    public boolean available() {
        return available;
    }

    /**
     * Была ли вообще попытка подключиться. Пока её не было, «недоступен» — не
     * факт, а домысел, и показывать его пользователю нельзя.
     */
    public boolean probed() {
        return probed;
    }

    public WorkerProfile profile() {
        return profile;
    }

    /**
     * Перекрывает имя делегирующего инструмента. Нужен реестру для разрешения
     * коллизий, когда два агента вывели одно и то же имя.
     */
    public void setToolName(String name) {
        this.toolName = name;
        WorkerProfile p = profile;
        if (!p.toolName().isEmpty()) {
            profile = new WorkerProfile(name, p.toolDesc(), p.summary());
        }
    }

    /** Имя инструмента из id агента: ask_&lt;slug&gt;. */
    public static String defaultToolName(String id) {
        return "ask_" + WorkerProfile.slug(id);
    }

    /**
     * Резолвит AgentCard и создаёт клиента. Идемпотентен; после неудачи
     * следующий вызов пробует снова, поэтому выключенный агент оживает сам.
     */
    public void connect() {
        if (client != null) {
            return;
        }
        synchronized (connectLock) {
            if (client != null) {
                return;
            }
            probed = true;
            A2aClient.Resolved resolved;
            try {
                resolved = A2aClient.resolve(cfg, trace);
            } catch (RuntimeException e) {
                markUnavailable();
                throw e;
            }
            card = resolved.card();
            client = resolved.client();
            available = true;
            profile = buildProfile(resolved.card());
            trace.logf("resolved AgentCard \"%s\" of agent \"%s\" at %s (tool \"%s\")",
                    resolved.card().name, cfg.id(), cfg.url(), profile.toolName());
        }
    }

    /**
     * Роняет пометку доступности и заставляет следующий connect заново резолвить
     * карточку: соединение могло умереть вместе с агентом.
     */
    private void markUnavailable() {
        available = false;
        client = null;
    }

    /**
     * Выводит профиль агента. Описание из конфига вытесняет карточку целиком:
     * у внешнего агента может быть сотня навыков, и промпт локальной модели
     * такого не переживёт.
     */
    private WorkerProfile buildProfile(AgentCard c) {
        if (cfg.description().isEmpty()) {
            WorkerProfile p = WorkerProfile.fromCard(c);
            return toolName.isEmpty() ? p : new WorkerProfile(toolName, p.toolDesc(), p.summary());
        }
        return WorkerProfile.fromConfig(cfg.id(), cfg.name(), cfg.description(), c, toolName);
    }

    /**
     * Объявляет ли агент способность отдавать A2UI: только такому есть смысл
     * слать запрос на generative UI.
     */
    @SuppressWarnings("unchecked")
    private boolean acceptsA2ui() {
        AgentCard c = card;
        if (c == null) {
            return false;
        }
        if (c.defaultOutputModes != null && c.defaultOutputModes.contains(A2ui.MIME_TYPE)) {
            return true;
        }
        if (!(c.capabilities.get("extensions") instanceof List<?> exts)) {
            return false;
        }
        for (Object e : exts) {
            if (e instanceof Map<?, ?> ext && ext.get("uri") instanceof String uri
                    && (uri.equals(A2ui.EXTENSION_URI) || uri.equals(A2ui.LEGACY_EXTENSION_URI))) {
                return true;
            }
        }
        return false;
    }

    /** Id зависшей input-required задачи сессии, или "". */
    public String pendingTaskId(String sessionId) {
        Pending p = pending.get(sessionId);
        return p == null ? "" : p.taskId();
    }

    /** Одно текстовое сообщение агенту. */
    public Reply ask(String sessionId, String text, boolean wantA2ui) {
        return ask(sessionId, Part.text(text), text, wantA2ui);
    }

    /**
     * Нажатие кнопки — штатным событием A2UI, а не пересказом на человеческом
     * языке. Так это описывает схема client_to_server, и так его ждёт агент,
     * собранный на любом из референсных SDK.
     */
    public Reply askAction(String sessionId, String name, String surfaceId, String sourceComponentId,
                           Map<String, Object> ctx, boolean wantA2ui) {
        return ask(sessionId, A2uiParts.action(name, surfaceId, sourceComponentId, ctx, Instant.now()),
                "action " + name, wantA2ui);
    }

    /**
     * Общий путь обоих способов обратиться к агенту. {@code echo} попадает
     * только в трейс: части бывают не текстовые, а лог должен показывать, что
     * именно ушло.
     */
    private Reply ask(String sessionId, Part outgoing, String echo, boolean wantA2ui) {
        connect();
        A2aClient c = client;
        Pending p = pending.get(sessionId);
        String contextId = contexts.get(sessionId);
        boolean a2ui = wantA2ui && acceptsA2ui();

        trace.logf("──▶ delegating to agent \"%s\" | session=%s", cfg.id(), sessionId);

        A2aMessage msg;
        if (p != null) {
            trace.logf("    resuming input-required task | taskID=%s contextID=%s", p.taskId(), p.contextId());
            msg = A2aMessage.forTask(A2aMessage.ROLE_USER, p.taskId(), p.contextId(), outgoing);
        } else {
            msg = A2aMessage.of(A2aMessage.ROLE_USER, outgoing);
            if (contextId != null && !contextId.isEmpty()) {
                msg.contextId = contextId;
            }
        }
        Map<String, Object> meta = new LinkedHashMap<>();
        if (!cfg.skill().isEmpty()) {
            meta.put("skill", cfg.skill());
        }
        // Какие каталоги умеет наш рендерер. Штатный признак «клиент говорит на
        // A2UI»: acceptedOutputModes спека таким признаком не считает.
        if (a2ui) {
            meta.put("a2uiClientCapabilities", A2ui.clientCapabilities());
        }
        if (!meta.isEmpty()) {
            msg.metadata = meta;
        }
        // contextId в трейсе — потому что «агент забывает разговор» это первый
        // вопрос, который приходится проверять.
        String sentCtx = p != null ? p.contextId() : msg.contextId;
        trace.logf("    SendMessage role=user skill=\"%s\" contextId=%s text=\"%s\"",
                cfg.skill(), sentCtx == null || sentCtx.isEmpty() ? "(новый разговор)" : sentCtx,
                Tracer.maskCardLike(echo));

        A2aClient.SendResult res;
        try {
            res = c.sendMessage(msg, a2ui ? List.of(A2ui.EXTENSION_URI, A2ui.LEGACY_EXTENSION_URI) : null);
        } catch (A2aClient.A2aException e) {
            trace.logf("    ✖ SendMessage failed: %s", e.getMessage());
            // Сорвавшийся запрос — единственный честный признак, что агент лёг:
            // connect после первого успеха уже не переспрашивает карточку.
            markUnavailable();
            throw new A2aClient.A2aException("agent \"" + cfg.id() + "\" unreachable: " + e.getMessage(), e);
        }

        if (res.message() != null) {
            trace.logf("◀── response: Message (synchronous, no task) | parts=%d",
                    res.message().parts == null ? 0 : res.message().parts.size());
            pending.remove(sessionId);
            // Синхронный ответ тоже несёт контекст разговора: без этого агент,
            // отвечающий Message вместо Task, начинал бы беседу заново.
            if (res.message().contextId != null && !res.message().contextId.isEmpty()) {
                contexts.put(sessionId, res.message().contextId);
            }
            return replyFromParts(res.message().parts, null, "");
        }
        return replyFromTask(sessionId, awaitTerminal(c, res.task()));
    }

    /** Опрашивает GetTask, пока задача не выйдет из рабочего состояния. */
    private A2aTask awaitTerminal(A2aClient c, A2aTask task) {
        while (true) {
            String state = task.status == null ? "" : task.status.state;
            if (!TaskState.WORKING.equals(state) && !TaskState.SUBMITTED.equals(state)) {
                return task;
            }
            trace.logf("    … task %s is %s, polling GetTask in %s", task.id, state, POLL_INTERVAL);
            try {
                Thread.sleep(POLL_INTERVAL);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                throw new A2aClient.A2aException("agent \"" + cfg.id() + "\": interrupted while polling " + task.id);
            }
            task = c.getTask(task.id);
        }
    }

    /** Собирает ответ из терминальной задачи и запоминает состояние сессии. */
    private Reply replyFromTask(String sessionId, A2aTask task) {
        String state = task.status == null ? "" : task.status.state;
        trace.logf("◀── response: Task | id=%s contextID=%s state=%s", task.id, task.contextId, state);

        if (task.contextId != null && !task.contextId.isEmpty()) {
            contexts.put(sessionId, task.contextId);
        }
        if (TaskState.INPUT_REQUIRED.equals(state)) {
            pending.put(sessionId, new Pending(task.id, task.contextId));
            Reply reply = replyFromParts(statusParts(task), null, statusMessageText(task));
            reply = new Reply(reply.text(), true, reply.a2ui(), reply.widgets(), reply.files());
            trace.logf("    ⏸ input-required — stored pending task, asking user: \"%s\"", reply.text());
            return reply;
        }
        pending.remove(sessionId);

        if (TaskState.FAILED.equals(state) || TaskState.REJECTED.equals(state) || TaskState.CANCELED.equals(state)) {
            String why = statusMessageRaw(task).trim();
            trace.logf("    ✖ задача завершилась неуспехом: state=%s reason=\"%s\"", state, why);
            throw new TurnFailedException(cfg.id(), state, why);
        }

        // Текст берём из статусного сообщения, если агент положил его туда (так
        // предписывает контракт), иначе — из артефакта, как делает наш воркер.
        String fallback = taskResultText(task);
        String fromStatus = firstProseText(statusParts(task));
        if (!fromStatus.isEmpty()) {
            fallback = fromStatus;
        }
        Reply reply = replyFromParts(statusParts(task), artifactParts(task), fallback);
        trace.logf("    ✔ terminal state | text=\"%s\" a2ui=%d widgets=%d files=%d",
                reply.text(), reply.a2ui().size(), reply.widgets().size(), reply.files().size());
        return reply;
    }

    /**
     * Раскладывает части ответа по слоям: текст, A2UI, доменные виджеты, файлы.
     *
     * <p>statusP и artifactP разделены из-за A2UI: спека расширения кладёт
     * разметку в части сообщения, и оттуда её читает стандартный клиент.
     * Артефакт — второе место, куда её кладут живые агенты; берём его, только
     * если в сообщении разметки не было, иначе поверхность приехала бы дважды.
     */
    private Reply replyFromParts(List<Part> statusP, List<Part> artifactP, String fallback) {
        List<Part> parts = new ArrayList<>();
        if (artifactP != null) {
            parts.addAll(artifactP);
        }
        if (statusP != null) {
            parts.addAll(statusP);
        }
        String text = fallback == null ? "" : fallback;
        if (text.isEmpty()) {
            text = firstProseText(parts);
        }
        if (text.isBlank()) {
            text = "Готово.";
        }
        List<Map<String, Object>> a2ui = A2uiIngest.ingest(statusP);
        if (a2ui.isEmpty()) {
            a2ui = A2uiIngest.ingest(artifactP);
        }
        List<Map<String, Object>> widgets = new ArrayList<>();
        Map<String, Object> w = OrdersClient.firstWidget(parts);
        if (w != null) {
            widgets.add(w);
        }
        List<AttachedFile> files = new ArrayList<>();
        for (Part p : parts) {
            if (p == null || p.filename == null || p.filename.isEmpty() || p.raw == null) {
                continue;
            }
            try {
                files.add(new AttachedFile(p.filename, p.mediaType, Base64.getDecoder().decode(p.raw)));
            } catch (IllegalArgumentException ignored) {
                // битый base64 — часть пропускаем, ход не роняем
            }
        }
        return new Reply(text, false, a2ui, widgets, files);
    }

    private static List<Part> statusParts(A2aTask t) {
        return t.status == null || t.status.message == null ? null : t.status.message.parts;
    }

    private static List<Part> artifactParts(A2aTask t) {
        return t.artifacts == null || t.artifacts.isEmpty() ? null : t.artifacts.getLast().parts;
    }

    /**
     * Первая человекочитаемая текстовая часть. Разметка интерфейса пропускается:
     * A2UI приезжает и текстовой частью, и показывать её пользователю нельзя.
     */
    static String firstProseText(List<Part> parts) {
        if (parts == null) {
            return "";
        }
        for (Part p : parts) {
            if (p == null || A2uiParts.isA2ui(p)) {
                continue;
            }
            String txt = p.textOrEmpty();
            if (!txt.isEmpty()) {
                return txt;
            }
        }
        return "";
    }

    private static String statusMessageRaw(A2aTask t) {
        return t.status == null || t.status.message == null ? "" : firstProseText(t.status.message.parts);
    }

    private static String statusMessageText(A2aTask t) {
        String txt = statusMessageRaw(t);
        return txt.isEmpty() ? "Агенту по заказам нужны дополнительные данные." : txt;
    }

    private static String taskResultText(A2aTask t) {
        String txt = firstProseText(artifactParts(t));
        if (!txt.isEmpty()) {
            return txt;
        }
        if (t.history != null && !t.history.isEmpty()) {
            txt = firstProseText(t.history.getLast().parts);
            if (!txt.isEmpty()) {
                return txt;
            }
        }
        return "Готово.";
    }
}
