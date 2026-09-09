package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Список агентов для селектора в браузере. */
class AgentsControllerTest {

    @Test
    void listsEveryAgentInConfigOrder() {
        Registry reg = new Registry(List.of(
                new AgentConfig("orders", "Агент заказов", "http://a", "", "", false, "", "", AuthConfig.NONE),
                new AgentConfig("shop", "Магазин", "http://b", "", "", true, "", "Заказы магазина.", AuthConfig.NONE)),
                Tracer.noop());

        List<Registry.AgentInfo> out = new AgentsController(reg).agents();

        assertEquals(List.of("orders", "shop"), out.stream().map(Registry.AgentInfo::id).toList());
        assertEquals("Магазин", out.get(1).name());
        assertTrue(out.get(1).verbatim());
        // Проб ещё не было — «недоступен» показывать нельзя.
        assertTrue(!out.getFirst().probed() || !out.getFirst().available());
    }

    @Test
    void returnsAnEmptyArrayRatherThanNull() {
        assertEquals(List.of(), new AgentsController(new Registry(List.of(), Tracer.noop())).agents());
    }
}
