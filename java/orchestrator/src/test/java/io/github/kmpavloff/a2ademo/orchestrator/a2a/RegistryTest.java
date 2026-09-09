package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
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
}
