package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import org.junit.jupiter.api.Test;

import com.sun.net.httpserver.HttpServer;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.fail;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotSame;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Набор агентов: порядок, состояние для UI и пересборка при правке конфига. */
class RegistryTest {

    private static AgentConfig agent(String id, String url) {
        return new AgentConfig(id, "", url, "", "", false, "", "", AuthConfig.NONE);
    }

    // Порядок задаёт пункты селектора и выбор агента для терминального REPL.
    @Test
    void keepsTheConfiguredOrder() {
        Registry reg = new Registry(List.of(agent("orders", "http://a"), agent("shop", "http://b")), Tracer.noop());
        assertEquals(List.of("orders", "shop"), reg.ids());
        assertEquals("orders", reg.first().id());
    }

    // Пока попытки подключения не было, «недоступен» — домысел, и клиенту его
    // показывать нельзя.
    @Test
    void listReportsProbeStateSeparatelyFromAvailability() {
        Registry reg = new Registry(List.of(agent("orders", "http://127.0.0.1:1")), Tracer.noop());
        List<Registry.AgentInfo> before = reg.list();
        assertEquals(1, before.size());
        assertFalse(before.getFirst().available());
    }

    @Test
    void clientForIsCreatedOnceAndPrepared() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        boolean[] prepared = {false};
        reg.setClientInit(c -> prepared[0] = true);
        OrdersClient first = reg.clientFor("orders");
        assertSame(first, reg.clientFor("orders"), "клиент создаётся один раз");
        assertTrue(prepared[0], "новый клиент обязан пройти подготовку");
    }

    // Клиент, созданный до подписки на clientInit, тоже обязан пройти
    // подготовку — иначе виджеты уже работающего агента молча пропадали бы.
    @Test
    void setClientInitPreparesAlreadyExistingClients() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        OrdersClient existing = reg.clientFor("orders");
        boolean[] prepared = {false};
        reg.setClientInit(c -> {
            if (c == existing) {
                prepared[0] = true;
            }
        });
        assertTrue(prepared[0], "уже созданный клиент обязан пройти подготовку задним числом");
    }

    // Нетронутый агент остаётся тем же Remote: в нём живое соединение,
    // разобранная карточка и зависшие input-required задачи.
    @Test
    void applyKeepsUnchangedAgentsAndRebuildsChangedOnes() {
        Registry reg = new Registry(List.of(agent("orders", "http://a"), agent("shop", "http://b")), Tracer.noop());
        Remote orders = reg.get("orders").orElseThrow();
        Remote shop = reg.get("shop").orElseThrow();
        long gen = reg.generation();

        reg.apply(List.of(agent("orders", "http://a"), agent("shop", "http://CHANGED")));

        assertSame(orders, reg.get("orders").orElseThrow(), "нетронутый агент не пересоздаётся");
        assertNotSame(shop, reg.get("shop").orElseThrow(), "изменённый пересоздаётся целиком");
        assertTrue(reg.generation() > gen, "поколение обязано вырасти");
    }

    @Test
    void applyWithoutChangesLeavesTheGenerationAlone() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        long gen = reg.generation();
        reg.apply(List.of(agent("orders", "http://a")));
        assertEquals(gen, reg.generation());
    }

    // Порядок фиксирует пункты селектора UI и выбор агента для REPL, поэтому
    // одна лишь перестановка обязана поднять поколение, а не только правка
    // конфига отдельного агента.
    @Test
    void applyReorderBumpsGenerationAndKeepsRemotes() {
        Registry reg = new Registry(List.of(agent("orders", "http://a"), agent("shop", "http://b")), Tracer.noop());
        Remote orders = reg.get("orders").orElseThrow();
        Remote shop = reg.get("shop").orElseThrow();
        long gen = reg.generation();

        reg.apply(List.of(agent("shop", "http://b"), agent("orders", "http://a")));

        assertEquals(List.of("shop", "orders"), reg.ids());
        assertTrue(reg.generation() > gen, "перестановка без изменения конфигов обязана поднять поколение");
        assertSame(orders, reg.get("orders").orElseThrow(), "перестановка не меняет конфиг — Remote тот же");
        assertSame(shop, reg.get("shop").orElseThrow(), "перестановка не меняет конфиг — Remote тот же");
    }

    @Test
    void applyCanRemoveAndAddAgents() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        reg.apply(List.of(agent("shop", "http://b")));
        assertEquals(List.of("shop"), reg.ids());
        assertTrue(reg.get("orders").isEmpty());
    }

    @Test
    void firstIsNullWhenEveryAgentIsGone() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        reg.apply(List.of());
        assertEquals(null, reg.first());
    }

    /**
     * Агент, чья карточка «висит», пока тест не откроет ворота, — как у живого
     * агента, отвечающего секундами. arrived срабатывает, когда запрос дошёл.
     */
    private record Gated(HttpServer server, String url, CountDownLatch arrived, CountDownLatch gate) {
        static Gated start() throws IOException {
            HttpServer s = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
            CountDownLatch arrived = new CountDownLatch(1);
            CountDownLatch gate = new CountDownLatch(1);
            s.createContext("/.well-known/agent-card.json", ex -> {
                arrived.countDown();
                try {
                    gate.await(10, TimeUnit.SECONDS);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                }
                byte[] b = """
                        {"name":"slow","description":"d","version":"1","capabilities":{},"skills":[]}
                        """.getBytes(StandardCharsets.UTF_8);
                ex.getResponseHeaders().add("Content-Type", "application/json");
                ex.sendResponseHeaders(200, b.length);
                try (OutputStream os = ex.getResponseBody()) {
                    os.write(b);
                }
            });
            s.setExecutor(java.util.concurrent.Executors.newCachedThreadPool());
            s.start();
            return new Gated(s, "http://127.0.0.1:" + s.getAddress().getPort(), arrived, gate);
        }
    }

    // Пока первое подключение в пути, агент ещё не проверен: «не отвечает» на
    // этом месте — домысел, а не факт.
    @Test
    void listIsNotProbedWhileTheFirstConnectIsInFlight() throws Exception {
        Gated g = Gated.start();
        try {
            Registry reg = new Registry(List.of(agent("slow", g.url())), Tracer.noop());
            reg.list(); // запускает фоновую проверку
            assertTrue(g.arrived().await(5, TimeUnit.SECONDS));
            Registry.AgentInfo during = reg.list().getFirst();
            assertFalse(during.probed(), "подключение ещё идёт, а агент уже помечен проверенным: " + during);

            g.gate().countDown();
            long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
            while (System.nanoTime() < deadline) {
                Registry.AgentInfo after = reg.list().getFirst();
                if (after.probed()) {
                    assertTrue(after.available(), "живой агент помечен недоступным: " + after);
                    return;
                }
                Thread.sleep(20);
            }
            fail("фоновая проверка не завершилась");
        } finally {
            g.gate().countDown();
            g.server().stop(0);
        }
    }

    // Ход, пришедший во время первого подключения, должен его дождаться, а не
    // счесть агента лежащим: иначе запрос сразу после загрузки страницы
    // остаётся без инструмента вполне живого агента.
    @Test
    void aTurnDuringTheFirstConnectWaitsForIt() throws Exception {
        Gated g = Gated.start();
        try {
            Registry reg = new Registry(List.of(agent("slow", g.url())), Tracer.noop());
            reg.list();
            assertTrue(g.arrived().await(5, TimeUnit.SECONDS));
            CompletableFuture<List<OrdersClient>> turn = CompletableFuture.supplyAsync(reg::availableClients);
            Thread.sleep(100); // ход уже внутри, пока ворота закрыты
            g.gate().countDown();
            assertEquals(1, turn.get(10, TimeUnit.SECONDS).size(), "живой агент выпал из хода");
        } finally {
            g.gate().countDown();
            g.server().stop(0);
        }
    }
}
