package io.github.kmpavloff.a2ademo.common.config;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Список агентов в конфиге оркестратора — та же форма, что читает Go. */
class AgentsConfigTest {

    private static Path write(Path dir, String yaml) throws IOException {
        Path p = dir.resolve("orchestrator.yaml");
        Files.writeString(p, yaml);
        return p;
    }

    @Test
    void readsTheAgentsListWithDefaults(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                listen_addr: ":8080"
                public_url: "http://localhost:8080"
                agents:
                  - id: orders
                    name: "Агент заказов"
                    url: "http://localhost:8081"
                  - id: ouroboros
                    url: "http://192.168.1.68:18800"
                    card_path: "/.well-known/agent.json"
                    skill: "shop"
                    verbatim: true
                    timeout: "180s"
                    description: "Заказы интернет-магазина."
                    auth:
                      type: basic
                      username: ouroboros
                      password: test
                llm:
                  base_url: "http://localhost:1234/v1"
                """);

        List<AgentConfig> agents = ConfigLoader.loadOrchestrator(cfg.toString()).agents();

        assertEquals(2, agents.size());
        assertEquals("orders", agents.getFirst().id());
        assertEquals(AgentConfig.DEFAULT_CARD_PATH, agents.getFirst().cardPath(), "путь карточки по умолчанию");
        assertEquals(Duration.ofSeconds(120), agents.getFirst().timeoutDuration());
        assertEquals(Duration.ofSeconds(180), agents.get(1).timeoutDuration());
        assertTrue(agents.get(1).verbatim());
        assertEquals("ouroboros", agents.get(1).auth().username());
    }

    // Исторический одиночный агент: из worker_url синтезируется запись orders,
    // и только когда список agents пуст.
    @Test
    void fallsBackToWorkerUrlWhenTheListIsEmpty(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                worker_url: "http://localhost:8081"
                llm:
                  base_url: "http://localhost:1234/v1"
                """);

        List<AgentConfig> agents = ConfigLoader.loadOrchestrator(cfg.toString()).agents();
        assertEquals(1, agents.size());
        assertEquals("orders", agents.getFirst().id());
        assertEquals("http://localhost:8081", agents.getFirst().url());
    }

    @Test
    void refusesAConfigWithNoAgentAtAll(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                llm:
                  base_url: "http://localhost:1234/v1"
                """);
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> ConfigLoader.loadOrchestrator(cfg.toString()));
        assertTrue(e.getMessage().contains("at least one agent"), e.getMessage());
    }

    @Test
    void overlayPathDefaultsToTheGoLocation(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                worker_url: "http://localhost:8081"
                llm:
                  base_url: "http://localhost:1234/v1"
                """);
        assertEquals("configs/agents.local.yaml", ConfigLoader.loadOrchestrator(cfg.toString()).agentsOverlayPath());
    }

    @Test
    void rejectsABadId() {
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("Orders", "", "http://x", "", "", false, "", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "", "", "", false, "", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "http://x", "", "", false, "нет", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "http://x", "", "", false, "", "", new AuthConfig("bearer", "u", "p"))));
    }

    @Test
    void envVarNameMatchesGo() {
        assertEquals("A2A_AGENT_ORDERS_URL", AgentConfig.envVar("orders", "URL"));
        assertEquals("A2A_AGENT_MY_AGENT_PASSWORD", AgentConfig.envVar("my-agent", "PASSWORD"));
    }

    @Test
    void parsesGoDurationSyntax() {
        assertEquals(Duration.ofSeconds(180), GoDuration.parse("180s"));
        assertEquals(Duration.ofMinutes(3), GoDuration.parse("3m"));
        assertEquals(Duration.ofSeconds(90), GoDuration.parse("1m30s"));
        assertEquals(Duration.ofMillis(1500), GoDuration.parse("1500ms"));
        assertThrows(IllegalArgumentException.class, () -> GoDuration.parse("180"));
        assertThrows(IllegalArgumentException.class, () -> GoDuration.parse("PT180S"));
    }
}
