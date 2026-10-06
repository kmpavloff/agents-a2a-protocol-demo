package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpServer;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Транспорт до удалённого агента: авторизация, свой путь карточки, чужие ответы. */
class A2aClientTest {

    HttpServer server;
    String base;
    final List<String> seenAuth = new ArrayList<>();
    final List<String> seenExtensions = new ArrayList<>();
    final List<String> seenLegacyExtensions = new ArrayList<>();
    final List<String> seenUpgrade = new ArrayList<>();
    final List<JsonNode> seenRequests = new ArrayList<>();
    String sendResult = "{\"task\":{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}";

    @BeforeEach
    void setUp() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        base = "http://127.0.0.1:" + server.getAddress().getPort();
        // Карточка лежит НЕ по каноническому пути и объявляет нерабочий 0.0.0.0.
        server.createContext("/.well-known/agent.json", ex -> respond(ex, """
                {"name":"ouroboros","description":"Магазин.","version":"1.0","capabilities":{},
                 "securitySchemes":{"basic":{"type":"http","scheme":"basic"}},
                 "supportedInterfaces":[{"url":"http://0.0.0.0:18800/invoke","protocolBinding":"JSONRPC"}],
                 "skills":[]}
                """));
        server.createContext("/invoke", ex -> {
            seenAuth.add(String.valueOf(ex.getRequestHeaders().getFirst("Authorization")));
            seenExtensions.add(String.valueOf(ex.getRequestHeaders().getFirst("A2A-Extensions")));
            // Легаси-имя — вся причина, по которой заголовок шлётся дважды: без
            // записи здесь регрессия («шлём только A2A-Extensions») осталась бы
            // незамеченной.
            seenLegacyExtensions.add(String.valueOf(ex.getRequestHeaders().getFirst("X-A2A-Extensions")));
            seenUpgrade.add(String.valueOf(ex.getRequestHeaders().getFirst("Upgrade")));
            JsonNode req = Json.MAPPER.readTree(ex.getRequestBody());
            seenRequests.add(req);
            respond(ex, "{\"jsonrpc\":\"2.0\",\"id\":" + req.path("id") + ",\"result\":" + sendResult + "}");
        });
        server.start();
    }

    @AfterEach
    void tearDown() {
        server.stop(0);
    }

    private static void respond(com.sun.net.httpserver.HttpExchange ex, String body) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().add("Content-Type", "application/json");
        ex.sendResponseHeaders(200, b.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(b);
        }
    }

    private A2aClient client() {
        AgentConfig cfg = new AgentConfig("ouroboros", "", base, "/.well-known/agent.json",
                List.of("shop"), true, "30s", "Магазин.", new AuthConfig("basic", "u", "p"));
        return A2aClient.resolve(cfg, Tracer.noop()).client();
    }

    @Test
    void resolvesACardAtANonCanonicalPathAndSurvivesSecuritySchemes() {
        assertNotNull(client());
    }

    // Хост из карточки бывает нерабочим (живой агент объявляет 0.0.0.0), поэтому
    // схему и хост берём из конфига — по нему мы карточку и получили.
    @Test
    void mergesTheDeclaredPathOntoTheConfiguredHost() {
        assertEquals("http://127.0.0.1:9000/invoke",
                A2aClient.mergeEndpoint("http://127.0.0.1:9000", "http://0.0.0.0:18800/invoke"));
        assertEquals("http://127.0.0.1:9000",
                A2aClient.mergeEndpoint("http://127.0.0.1:9000", "http://0.0.0.0:18800/"));
    }

    // Карточка чужая, ей нельзя доверять: если объявленный URL не парсится,
    // остаёмся на адресе из конфига, а не на обломке из карточки.
    @Test
    void mergeEndpointFallsBackToConfigWhenTheDeclaredUrlIsMalformed() {
        assertEquals("http://127.0.0.1:9000",
                A2aClient.mergeEndpoint("http://127.0.0.1:9000", "http://0.0.0.0:18800/inv oke"));
    }

    @Test
    void sendsBasicAuthAndTheExtensionHeader() {
        client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), List.of("https://a2ui.org/x"));
        assertEquals("Basic " + java.util.Base64.getEncoder().encodeToString("u:p".getBytes(StandardCharsets.UTF_8)),
                seenAuth.getFirst());
        assertEquals("https://a2ui.org/x", seenExtensions.getFirst());
        // Заголовок едет под обоими именами: A2A 1.0 знает A2A-Extensions,
        // спека A2UI v0.9 — только легаси X-A2A-Extensions.
        assertEquals("https://a2ui.org/x", seenLegacyExtensions.getFirst());
    }

    // acceptedOutputModes — не триггер A2UI (им остаются заголовок и
    // a2uiClientCapabilities), но сторонний агент вправе на него смотреть, и
    // Java обязана слать то же, что и Go.
    @Test
    void includesAcceptedOutputModesWhenExtensionsAreRequested() {
        client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), List.of("https://a2ui.org/x"));
        JsonNode configuration = seenRequests.getFirst().path("params").path("configuration");
        assertTrue(configuration.isObject(), "configuration must be present when extensions are requested");
        List<String> modes = new ArrayList<>();
        configuration.path("acceptedOutputModes").forEach(n -> modes.add(n.asText()));
        assertEquals(List.of("text/plain", "application/a2ui+json"), modes);
    }

    @Test
    void omitsAcceptedOutputModesWhenNoExtensionsAreRequested() {
        client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), null);
        assertTrue(seenRequests.getFirst().path("params").path("configuration").isMissingNode());
    }

    // Агент кладёт объект задачи в result напрямую, без oneof-обёртки A2A 1.0.
    @Test
    void wrapsABareTaskResult() {
        sendResult = "{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}";
        A2aClient.SendResult res = client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), null);
        assertNotNull(res.task());
        assertEquals("t1", res.task().id);
    }

    @Test
    void wrapsABareMessageResult() {
        sendResult = "{\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"готово\"}]}";
        A2aClient.SendResult res = client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), null);
        assertNotNull(res.message());
        assertEquals("готово", res.message().firstText());
    }

    @Test
    void leavesAProperlyWrappedResultAlone() {
        JsonNode wrapped = Json.MAPPER.createObjectNode().set("task", Json.MAPPER.createObjectNode());
        assertTrue(A2aClient.wrapBareResult(wrapped).has("task"));
    }

    // По умолчанию JDK шлёт h2c-апгрейд (заголовки Connection/Upgrade) на
    // обычном http://, и живой сторонний агент на это отвечает ошибкой —
    // клиент обязан явно закрепить HTTP/1.1, как это делает Go.
    @Test
    void doesNotSendAnH2cUpgradePreflightOnPlainHttp() {
        client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), null);
        assertEquals("null", seenUpgrade.getFirst());
    }

    @Test
    void pollsGetTask() {
        sendResult = "{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}";
        assertEquals("t1", client().getTask("t1").id);
    }
}
