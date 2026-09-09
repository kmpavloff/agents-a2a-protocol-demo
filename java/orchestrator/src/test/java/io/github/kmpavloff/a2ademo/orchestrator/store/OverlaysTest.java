package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;

/** Overlay-правки поверх рукописного списка агентов. */
class OverlaysTest {

    private static AgentConfig agent(String id, String url) {
        return new AgentConfig(id, "", url, "", "", false, "", "", AuthConfig.NONE);
    }

    private static List<AgentConfig> base() {
        return List.of(agent("orders", "http://localhost:8081"), agent("ouroboros", "http://192.168.1.68:18800"));
    }

    // Перекрытие идёт целой записью и не двигает агента по списку: порядок
    // задаёт пункты селектора, и агент с новым адресом не должен прыгать в конец.
    @Test
    void overridesInPlace() {
        List<AgentConfig> got = Overlays.merge(base(),
                List.of(new AgentOverride(agent("orders", "http://127.0.0.1:9000"), false)));

        assertEquals(2, got.size());
        assertEquals("orders", got.getFirst().id());
        assertEquals("http://127.0.0.1:9000", got.getFirst().url());
        assertEquals("http://192.168.1.68:18800", got.get(1).url(), "соседа правка не трогает");
    }

    @Test
    void hiddenAgentsDropOutOfTheEffectiveList() {
        List<AgentConfig> got = Overlays.merge(base(),
                List.of(new AgentOverride(agent("ouroboros", ""), true)));
        assertEquals(List.of("orders"), got.stream().map(AgentConfig::id).toList());
    }

    @Test
    void agentsCreatedInTheUiGoToTheEnd() {
        List<AgentConfig> got = Overlays.merge(base(),
                List.of(new AgentOverride(agent("newbie", "http://c"), false)));
        assertEquals(List.of("orders", "ouroboros", "newbie"), got.stream().map(AgentConfig::id).toList());
    }

    @Test
    void emptyOverlayChangesNothing() {
        assertEquals(base(), Overlays.merge(base(), List.of()));
    }
}
