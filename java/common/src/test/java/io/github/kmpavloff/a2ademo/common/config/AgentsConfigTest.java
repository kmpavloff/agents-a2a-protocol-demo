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
        // Прежнее одиночное skill: читается как список из одного навыка.
        assertEquals(List.of("shop"), agents.get(1).skills());
    }

    @Test
    void readsAndValidatesTheSkillsList(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                agents:
                  - id: ouroboros
                    url: "http://x"
                    skills: ["shop", "support"]
                llm:
                  base_url: "http://localhost:1234/v1"
                """);
        AgentConfig a = ConfigLoader.loadOrchestrator(cfg.toString()).agents().getFirst();
        assertEquals(List.of("shop", "support"), a.skills());
        assertTrue(a.hasSkill("support"));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("o", "", "http://x", "", List.of("shop", " "), false, "", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("o", "", "http://x", "", List.of("shop", "shop"), false, "", "", AuthConfig.NONE)));
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
                new AgentConfig("Orders", "", "http://x", "", List.of(), false, "", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "", "", List.of(), false, "", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "http://x", "", List.of(), false, "нет", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "http://x", "", List.of(), false, "", "", new AuthConfig("bearer", "u", "p"))));
    }

    // Go — switch по a.Auth.Type с точным совпадением "" или "basic" — регистр
    // не приводится, в отличие от AuthConfig.basic() на стороне вызова.
    @Test
    void rejectsANonLowercaseAuthType() {
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "http://x", "", List.of(), false, "", "", new AuthConfig("Basic", "u", "p"))));
    }

    @Test
    void rejectsADuplicateAgentId() {
        List<AgentConfig> agents = List.of(
                new AgentConfig("orders", "", "http://a", "", List.of(), false, "", "", AuthConfig.NONE),
                new AgentConfig("orders", "", "http://b", "", List.of(), false, "", "", AuthConfig.NONE));
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> ConfigLoader.normalizeAgents(agents));
        assertTrue(e.getMessage().contains("duplicate agent id"), e.getMessage());
    }

    @Test
    void envVarNameMatchesGo() {
        assertEquals("A2A_AGENT_ORDERS_URL", AgentConfig.envVar("orders", "URL"));
        assertEquals("A2A_AGENT_MY_AGENT_PASSWORD", AgentConfig.envVar("my-agent", "PASSWORD"));
    }

    // Под турецкой локалью toUpperCase() без Locale.ROOT превращает "i" в
    // "İ" — тогда id "invoices" даёт не A2A_AGENT_INVOICES_URL, а имя с
    // не-ASCII буквой, и Java с Go расходятся в том, какую переменную
    // окружения читать. envVar обязан быть locale-независимым.
    @Test
    void envVarNameIsLocaleIndependent() {
        java.util.Locale original = java.util.Locale.getDefault();
        try {
            java.util.Locale.setDefault(new java.util.Locale("tr", "TR"));
            assertEquals("A2A_AGENT_INVOICES_URL", AgentConfig.envVar("invoices", "URL"));
        } finally {
            java.util.Locale.setDefault(original);
        }
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
