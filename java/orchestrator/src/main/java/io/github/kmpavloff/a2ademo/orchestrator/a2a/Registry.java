package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicLong;
import java.util.function.Consumer;

/**
 * Набор удалённых агентов из конфига, в порядке объявления (порт
 * a2abridge/registry.go).
 *
 * <p>Порядок важен: первый агент обслуживает терминальный REPL, и он же задаёт
 * стабильный порядок пунктов в селекторе UI.
 */
public class Registry {

    /** То, что оркестратор рассказывает браузеру про доступных агентов. */
    public record AgentInfo(String id, String name, String description,
                            boolean verbatim, boolean available, boolean probed) {}

    private final Tracer trace;
    private final Object lock = new Object();

    private List<String> order = new ArrayList<>();
    private Map<String, Remote> remotes = new LinkedHashMap<>();
    private Map<String, OrdersClient> clients = new LinkedHashMap<>();
    private Map<String, AgentConfig> cfgs = new LinkedHashMap<>();
    private Consumer<OrdersClient> clientInit;

    /** Идущие фоновые проверки, чтобы не плодить их на одного агента. */
    private final Set<String> probing = ConcurrentHashMap.newKeySet();

    /** Поколение состава; растёт при каждом изменении списка. */
    private final AtomicLong generation = new AtomicLong();

    /** Демон-пул: фоновые проверки не должны удерживать JVM при выходе. */
    private final ExecutorService probes = Executors.newCachedThreadPool(r -> {
        Thread t = new Thread(r, "agent-probe");
        t.setDaemon(true);
        return t;
    });

    /** Создаёт реестр по конфигу. Соединения не открываются: агенты подключаются лениво. */
    public Registry(List<AgentConfig> agents, Tracer trace) {
        this.trace = trace;
        for (AgentConfig cfg : agents) {
            order.add(cfg.id());
            remotes.put(cfg.id(), new Remote(cfg, trace));
            cfgs.put(cfg.id(), cfg);
        }
    }

    public List<String> ids() {
        synchronized (lock) {
            return List.copyOf(order);
        }
    }

    public Optional<Remote> get(String id) {
        synchronized (lock) {
            return Optional.ofNullable(remotes.get(id));
        }
    }

    /** Первый агент из конфига — тот, с кем работает REPL; null, если список пуст. */
    public Remote first() {
        synchronized (lock) {
            return order.isEmpty() ? null : remotes.get(order.getFirst());
        }
    }

    /** Делегирующий клиент агента; создаётся при первом обращении. */
    public OrdersClient clientFor(String id) {
        OrdersClient created;
        Consumer<OrdersClient> init;
        synchronized (lock) {
            OrdersClient existing = clients.get(id);
            if (existing != null) {
                return existing;
            }
            Remote r = remotes.get(id);
            if (r == null) {
                return null;
            }
            created = new OrdersClient(r, trace);
            clients.put(id, created);
            init = clientInit;
        }
        // Подготовка — вне блокировки: обработчики ставит исполнитель, и звать
        // чужой код под своим мьютексом незачем.
        if (init != null) {
            init.accept(created);
        }
        return created;
    }

    /**
     * Делегирующие клиенты доступных агентов — набор для режима «Авто», где
     * модель сама выбирает, к кому обратиться. Недоступные пропускаются:
     * инструмент, который заведомо не работает, только сбивает модель с толку.
     */
    public List<OrdersClient> availableClients() {
        List<OrdersClient> out = new ArrayList<>();
        Set<String> seen = new java.util.HashSet<>();
        for (String id : ids()) {
            Remote r = get(id).orElse(null);
            if (r == null || !ready(r)) {
                continue;
            }
            // Имена инструментов выводятся из карточек и могут совпасть;
            // коллизию разрешаем детерминированно, в порядке конфига.
            String name = r.profile().toolName();
            if (seen.contains(name)) {
                String fallback = Remote.defaultToolName(id);
                for (int n = 2; seen.contains(fallback); n++) {
                    fallback = Remote.defaultToolName(id) + "_" + n;
                }
                trace.logf("⚠ tool name \"%s\" is already taken — renaming agent \"%s\" tool to \"%s\"",
                        name, id, fallback);
                r.setToolName(fallback);
            }
            OrdersClient c = clientFor(id);
            if (c == null) {
                continue;
            }
            seen.add(c.profile().toolName());
            out.add(c);
        }
        return out;
    }

    /** Блоки возможностей доступных агентов для промпта. */
    public List<String> summaries() {
        List<String> out = new ArrayList<>();
        for (String id : ids()) {
            Remote r = get(id).orElse(null);
            if (r == null || !ready(r)) {
                continue;
            }
            String s = r.profile().summary();
            if (!s.isEmpty()) {
                out.add(s);
            }
        }
        return out;
    }

    /**
     * Готовит агента к участию в ходе. Уже подключённый проходит мгновенно;
     * заведомо лежащий (проверяли — не ответил) пропускается сразу, а его
     * возвращение к жизни заметит фоновая проверка из {@link #list()}.
     */
    private boolean ready(Remote r) {
        if (r.available()) {
            return true;
        }
        if (r.probed()) {
            probeAsync(r);
            return false;
        }
        try {
            r.connect();
            return true;
        } catch (RuntimeException e) {
            trace.logf("agent \"%s\" unavailable, skipping its tool: %s", r.id(), e.getMessage());
            return false;
        }
    }

    /**
     * Проверяет доступность агента в фоне, по одной проверке на агента
     * одновременно. Синхронно этого делать нельзя: карточка у занятого агента
     * отвечает секундами, а /api/agents браузер дёргает раз в полминуты и после
     * каждого хода.
     */
    private void probeAsync(Remote r) {
        String id = r.id();
        if (!probing.add(id)) {
            return;
        }
        probes.execute(() -> {
            try {
                r.connect();
            } catch (RuntimeException e) {
                trace.logf("agent \"%s\" unavailable: %s", id, e.getMessage());
            } finally {
                probing.remove(id);
            }
        });
    }

    /**
     * Описывает агентов для UI. Отдаёт последнее известное состояние сразу и
     * запускает фоновое обновление — эндпоинт не должен ждать медленного агента.
     */
    public List<AgentInfo> list() {
        List<AgentInfo> out = new ArrayList<>();
        for (String id : ids()) {
            Remote r = get(id).orElse(null);
            if (r == null) {
                continue;
            }
            probeAsync(r);
            out.add(new AgentInfo(r.id(), r.name(), r.description(), r.verbatim(), r.available(), r.probed()));
        }
        return out;
    }

    /**
     * Задаёт подготовку клиента — навешивание обработчиков виджетов, A2UI, файлов
     * и текста. Разовым циклом это делать нельзя: клиент, созданный после правки
     * конфига, остался бы без обработчиков, и виджеты нового агента молча
     * пропадали бы.
     */
    public void setClientInit(Consumer<OrdersClient> fn) {
        List<OrdersClient> existing;
        synchronized (lock) {
            clientInit = fn;
            existing = List.copyOf(clients.values());
        }
        for (OrdersClient c : existing) {
            fn.accept(c);
        }
    }

    /**
     * Номер состава агентов. Растёт при любом изменении списка; исполнитель по
     * нему понимает, что кэш собранных агентов пора выбросить.
     */
    public long generation() {
        return generation.get();
    }

    /**
     * Приводит реестр к новому списку агентов.
     *
     * <p>Сверка идёт по id: {@link AgentConfig} — запись из строк и bool, так что
     * сравнение обычное. Нетронутый агент остаётся тем же {@link Remote} — в нём
     * живое соединение, разобранная карточка и зависшие input-required задачи, и
     * терять их из-за правки соседа нельзя. Изменённый пересоздаётся целиком.
     */
    public void apply(List<AgentConfig> agents) {
        synchronized (lock) {
            List<String> nextOrder = new ArrayList<>(agents.size());
            Map<String, Remote> nextRemotes = new LinkedHashMap<>(agents.size());
            Map<String, OrdersClient> nextClients = new LinkedHashMap<>(agents.size());
            Map<String, AgentConfig> nextCfgs = new LinkedHashMap<>(agents.size());
            boolean changed = agents.size() != order.size();

            for (AgentConfig cfg : agents) {
                nextOrder.add(cfg.id());
                nextCfgs.put(cfg.id(), cfg);
                Remote old = remotes.get(cfg.id());
                if (old != null && cfg.equals(cfgs.get(cfg.id()))) {
                    nextRemotes.put(cfg.id(), old);
                    OrdersClient c = clients.get(cfg.id());
                    if (c != null) {
                        nextClients.put(cfg.id(), c);
                    }
                    continue;
                }
                changed = true;
                trace.logf("agent \"%s\": конфиг изменился — пересоздаём соединение", cfg.id());
                nextRemotes.put(cfg.id(), new Remote(cfg, trace));
            }
            if (!changed && !nextOrder.equals(order)) {
                changed = true;
            }
            order = nextOrder;
            remotes = nextRemotes;
            clients = nextClients;
            cfgs = nextCfgs;
            if (changed) {
                generation.incrementAndGet();
            }
        }
    }
}
