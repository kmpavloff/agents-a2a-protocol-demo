package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTimeoutPreemptively;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Один ход разговора с удалённым агентом. */
class RemoteTest {

    HttpServer server;
    String base;
    final Deque<String> results = new ArrayDeque<>();
    final List<JsonNode> seenRequests = new ArrayList<>();
    String extensions = "";
    String cardCapabilities = "{}";

    @BeforeEach
    void setUp() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        base = "http://127.0.0.1:" + server.getAddress().getPort();
        server.createContext("/.well-known/agent-card.json", ex -> respond(ex, """
                {"name":"orders-agent","description":"Управляет заказами.","version":"0.1.0",
                 "capabilities":%s,
                 "defaultInputModes":["text/plain"],"defaultOutputModes":["text/plain"],
                 "supportedInterfaces":[{"url":"%s/invoke","protocolBinding":"JSONRPC"}],
                 "skills":[]}
                """.formatted(cardCapabilities, base)));
        server.createContext("/invoke", ex -> {
            extensions = String.valueOf(ex.getRequestHeaders().getFirst("A2A-Extensions"));
            JsonNode req = Json.MAPPER.readTree(ex.getRequestBody());
            seenRequests.add(req);
            respond(ex, "{\"jsonrpc\":\"2.0\",\"id\":" + req.path("id") + ",\"result\":" + results.pop() + "}");
        });
        server.start();
    }

    @AfterEach
    void tearDown() {
        server.stop(0);
    }

    private static void respond(HttpExchange ex, String body) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().add("Content-Type", "application/json");
        ex.sendResponseHeaders(200, b.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(b);
        }
    }

    private Remote remote(AgentConfig cfg) {
        return new Remote(cfg, Tracer.noop());
    }

    private AgentConfig cfg() {
        return new AgentConfig("orders", "Агент заказов", base, "", "", false, "", "", AuthConfig.NONE);
    }

    // Соединение открывается лениво: выключенный агент не мешает оркестратору
    // стартовать, и «недоступен» до первой попытки — домысел, а не факт.
    @Test
    void connectsLazilyAndReportsProbeState() {
        Remote r = remote(cfg());
        assertFalse(r.probed(), "до первой попытки пробы не было");
        assertFalse(r.available());

        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"},"
                + "\"artifacts\":[{\"parts\":[{\"text\":\"готово\"}]}]}}");
        r.ask("s1", "статус 1041", false);

        assertTrue(r.probed());
        assertTrue(r.available());
        assertEquals("ask_orders_agent", r.profile().toolName());
    }

    @Test
    void unreachableAgentIsMarkedUnavailable() {
        AgentConfig broken = cfg().withUrl("http://127.0.0.1:1");
        Remote r = remote(broken);
        assertThrows(A2aClient.A2aException.class, r::connect);
        assertTrue(r.probed());
        assertFalse(r.available());
    }

    // metadata.skill включает у внешнего агента инструменты нужного навыка.
    @Test
    void sendsTheConfiguredSkillInMessageMetadata() {
        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}");
        remote(cfg().withUrl(base)).ask("s1", "привет", false);
        assertEquals(null, seenRequests.getFirst().path("params").path("message").get("metadata"));
    }

    // Задача, вставшая в input-required, запоминается и продолжается тем же
    // сообщением: агент ждёт ответа пользователя.
    @Test
    void storesAndResumesAPendingTask() {
        results.push("{\"task\":{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_INPUT_REQUIRED\","
                + "\"message\":{\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"Подтвердите возврат (да/нет)\"}]}}}}");
        Remote r = remote(cfg());
        Remote.Reply first = r.ask("s1", "верни 1041", false);

        assertTrue(first.needsInput());
        assertEquals("Подтвердите возврат (да/нет)", first.text());
        assertEquals("t1", r.pendingTaskId("s1"));

        results.push("{\"task\":{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"},"
                + "\"artifacts\":[{\"parts\":[{\"text\":\"Возврат оформлен\"}]}]}}");
        Remote.Reply second = r.ask("s1", "да", false);

        assertEquals("Возврат оформлен", second.text());
        assertEquals("", r.pendingTaskId("s1"), "терминальная задача забывается");
        JsonNode resume = seenRequests.get(1).path("params").path("message");
        assertEquals("t1", resume.path("taskId").asText());
        assertEquals("c1", resume.path("contextId").asText());
    }

    // Провалившаяся задача — ошибка, а не ответ: иначе сбой агента попал бы в
    // ленту как обычная реплика.
    @Test
    void turnFailureIsAnErrorNotAReply() {
        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_FAILED\","
                + "\"message\":{\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"timed out\\nсм. docs\"}]}}}}");
        Remote.TurnFailedException e = assertThrows(Remote.TurnFailedException.class,
                () -> remote(cfg()).ask("s1", "привет", false));
        assertEquals("timed out", e.firstLine(), "в ленту уходит одна строка");
    }

    // Задача в работе опрашивается через GetTask.
    @Test
    void pollsAWorkingTaskUntilItIsTerminal() {
        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}");
        results.addLast("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"},"
                + "\"artifacts\":[{\"parts\":[{\"text\":\"готово\"}]}]}}");
        assertEquals("готово", remote(cfg()).ask("s1", "привет", false).text());
        assertEquals("GetTask", seenRequests.get(1).path("method").asText());
    }

    // Разметку просим только у агента, который объявил её в карточке, и только
    // когда клиент выбрал режим виджетов.
    @Test
    void requestsA2uiOnlyFromAnAgentThatDeclaresIt() {
        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}");
        remote(cfg()).ask("s1", "привет", true);
        assertEquals("null", extensions, "карточка A2UI не объявляет — заголовка быть не должно");
    }

    @Test
    void ingestsAgentAuthoredMarkupFromTheStatusMessage() {
        results.push(("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\","
                + "\"message\":{\"role\":\"ROLE_AGENT\",\"parts\":["
                + "{\"text\":\"Вот заказ\"},"
                + "{\"data\":[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"s1\"}}],"
                + "\"metadata\":{\"mimeType\":\"%s\"}}]}}}}").formatted(A2ui.MIME_TYPE));
        Remote.Reply reply = remote(cfg()).ask("s1", "статус 1041", true);
        assertEquals("Вот заказ", reply.text(), "разметка не должна стать ответом пользователю");
        assertEquals(1, reply.a2ui().size());
    }

    // Ход ограничен целиком, включая опрос GetTask: агент, зависший в WORKING
    // навсегда, не должен держать вызывающий поток бесконечно.
    @Test
    void boundsTheWholeTurnAcrossPolling() {
        String working = "{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}";
        results.push(working);
        for (int i = 0; i < 20; i++) {
            results.addLast(working);
        }
        AgentConfig shortTimeout = new AgentConfig("orders", "Агент заказов", base, "", "", false, "3s", "", AuthConfig.NONE);
        Remote r = remote(shortTimeout);

        A2aClient.A2aException e = assertTimeoutPreemptively(Duration.ofSeconds(10), () ->
                assertThrows(A2aClient.A2aException.class, () -> r.ask("s1", "привет", false)));
        assertTrue(e.getMessage().contains("t1"), "в сообщении назван id задачи: " + e.getMessage());
        assertTrue(e.getMessage().contains("TASK_STATE_WORKING"), "в сообщении названо состояние: " + e.getMessage());
    }

    @Test
    void defaultToolNameIsDerivedFromTheAgentId() {
        assertEquals("ask_ouroboros", Remote.defaultToolName("ouroboros"));
        assertEquals("ask_my_agent", Remote.defaultToolName("my-agent"));
        assertEquals("ask_agent", Remote.defaultToolName("—"));
    }
}
