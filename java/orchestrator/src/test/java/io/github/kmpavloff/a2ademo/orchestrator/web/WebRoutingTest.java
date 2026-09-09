package io.github.kmpavloff.a2ademo.orchestrator.web;

import com.fasterxml.jackson.databind.JsonNode;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import io.github.kmpavloff.a2ademo.orchestrator.store.AgentStore;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.web.servlet.context.ServletWebServerApplicationContext;
import org.springframework.context.ConfigurableApplicationContext;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Регресс на баг маршрутизации, который и был поводом для этой ветки:
 * {@link WebUiController}'s {@code @GetMapping("/**")} перехватывал
 * {@code /api/agents} и отдавал index.html вместо JSON. Все остальные тесты
 * контроллеров вызывают методы напрямую, в обход Spring, и потому не могли
 * поймать регрессию — она держится целиком на порядке диспетчеризации
 * реального DispatcherServlet между двумя контроллерами одного контекста.
 *
 * <p>Поднимает настоящий {@link WebApplication} на случайном порту — тем же
 * путём, что и {@code OrchestratorApplication} (статические holder'ы плюс
 * {@link SpringApplication#run}) — вместо {@code @SpringBootTest}/MockMvc:
 * оркестраторский модуль не тянет {@code spring-boot-starter-test}, и заводить
 * новую зависимость ради одного теста не стоит того, когда
 * {@code spring-boot-starter-web} и так поднимает встроенный сервер.
 */
class WebRoutingTest {

    private ConfigurableApplicationContext ctx;

    @AfterEach
    void tearDown() {
        if (ctx != null) {
            ctx.close();
        }
    }

    @Test
    void apiAgentsIsNotSwallowedByTheSpaCatchAll(@TempDir Path tmp) throws IOException, InterruptedException {
        AgentConfig agent = new AgentConfig("orders", "Агент заказов", "http://127.0.0.1:1",
                "", "", false, "", "", AuthConfig.NONE);
        Registry registry = new Registry(List.of(agent), Tracer.noop());
        OrchestratorWebExecutor executor = new OrchestratorWebExecutor(registry,
                (tools, summary) -> {
                    throw new AssertionError("GET /api/agents must not touch the agent/LLM path");
                }, Tracer.noop());
        AgentStore store = new AgentStore(List.of(agent), tmp.resolve("agents.local.yaml"));

        WebApplication.configure(executor, OrchestratorCards.agentCard("http://localhost:0"), Tracer.noop(),
                registry, store);
        SpringApplication app = new SpringApplication(WebApplication.class);
        app.setDefaultProperties(Map.of(
                "server.port", "0",
                "spring.main.banner-mode", "off",
                "logging.level.root", "warn"));
        ctx = app.run();
        int port = ((ServletWebServerApplicationContext) ctx).getWebServer().getPort();

        HttpClient http = HttpClient.newHttpClient();
        HttpResponse<String> resp = http.send(
                HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/api/agents")).GET().build(),
                HttpResponse.BodyHandlers.ofString());

        assertEquals(200, resp.statusCode());
        String contentType = resp.headers().firstValue("Content-Type").orElse("");
        assertTrue(contentType.contains("application/json"),
                "the SPA fallback would have answered text/html instead: " + contentType);

        JsonNode body = Json.MAPPER.readTree(resp.body());
        assertTrue(body.isArray(), "the SPA fallback would have answered an HTML document, not a JSON array");
        assertEquals(1, body.size());
        assertEquals("orders", body.get(0).path("id").asText());
    }
}
