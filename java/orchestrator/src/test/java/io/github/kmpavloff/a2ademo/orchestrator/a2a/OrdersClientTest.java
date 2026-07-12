package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpServer;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * OrdersClient against a canned A2A JSON-RPC server: verifies the outgoing
 * wire format (method, role, parts, task resume ids) and the input-required →
 * resume bookkeeping, mirroring the Go client tests.
 */
class OrdersClientTest {

    HttpServer server;
    String baseUrl;
    final Deque<String> cannedResults = new ArrayDeque<>();
    final List<JsonNode> received = new ArrayList<>();

    @BeforeEach
    void startServer() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        baseUrl = "http://127.0.0.1:" + server.getAddress().getPort();
        server.createContext("/.well-known/agent-card.json", exchange -> {
            String card = """
                    {"name":"orders-agent","description":"Управляет заказами.","version":"0.1.0",
                     "capabilities":{},"defaultInputModes":["text/plain"],"defaultOutputModes":["text/plain"],
                     "supportedInterfaces":[{"url":"%s/invoke","protocolBinding":"JSONRPC","protocolVersion":"1.0"}],
                     "skills":[{"id":"manage_orders","name":"Управление заказами","description":"Возвраты.","tags":["заказы"]}]}
                    """.formatted(baseUrl);
            respond(exchange, card);
        });
        server.createContext("/invoke", exchange -> {
            JsonNode req = Json.MAPPER.readTree(exchange.getRequestBody());
            synchronized (received) {
                received.add(req);
            }
            String result = cannedResults.pop();
            respond(exchange, "{\"jsonrpc\":\"2.0\",\"id\":" + req.path("id") + ",\"result\":" + result + "}");
        });
        server.start();
    }

    private static void respond(com.sun.net.httpserver.HttpExchange exchange, String body) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(200, b.length);
        try (OutputStream os = exchange.getResponseBody()) {
            os.write(b);
        }
    }

    @AfterEach
    void stopServer() {
        server.stop(0);
    }

    private OrdersClient client() {
        A2aClient.Resolved resolved = A2aClient.resolve(baseUrl);
        return new OrdersClient(resolved.client(), WorkerProfile.fromCard(resolved.card()), Tracer.noop());
    }

    @Test
    void resolveDerivesToolNameFromCard() {
        assertEquals("ask_orders_agent", client().profile().toolName());
    }

    @Test
    void completedTaskReturnsArtifactTextAndForwardsWidget() {
        cannedResults.add("""
                {"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"},
                 "artifacts":[{"artifactId":"a1","parts":[{"text":"Вот детали вашего заказа:"},
                   {"data":{"title":"Заказ 1041","order":{"id":"1041"}},"metadata":{"kind":"widget/order","version":1}}]}]}}
                """);
        OrdersClient c = client();
        List<Map<String, Object>> widgets = new ArrayList<>();
        c.setWidgetHandler((session, w) -> widgets.add(w));

        String result = c.ask("s1", "статус заказа 1041");
        assertEquals("Вот детали вашего заказа:", result);
        assertEquals(1, widgets.size());
        assertEquals("widget/order", widgets.getFirst().get("_kind"));
        assertEquals("", c.pendingTaskId("s1"), "no pending task after terminal state");

        JsonNode sent = received.getFirst();
        assertEquals("SendMessage", sent.path("method").asText());
        JsonNode msg = sent.path("params").path("message");
        assertEquals("ROLE_USER", msg.path("role").asText());
        assertEquals("статус заказа 1041", msg.path("parts").get(0).path("text").asText());
        assertTrue(msg.path("taskId").isMissingNode(), "new task must not reference a taskId");
    }

    @Test
    void inputRequiredStoresPendingAndResumesSameTask() {
        cannedResults.add("""
                {"task":{"id":"t9","contextId":"c9","status":{"state":"TASK_STATE_INPUT_REQUIRED",
                 "message":{"messageId":"m1","role":"ROLE_AGENT","parts":[{"text":"Подтвердите возврат? (да/нет)"}]}}}}
                """);
        cannedResults.add("""
                {"task":{"id":"t9","contextId":"c9","status":{"state":"TASK_STATE_COMPLETED"},
                 "artifacts":[{"artifactId":"a1","parts":[{"text":"Возврат оформлен."}]}]}}
                """);
        OrdersClient c = client();

        String first = c.ask("s1", "верни деньги за 1041");
        assertEquals("NEEDS_USER_INPUT: Подтвердите возврат? (да/нет)", first);
        assertEquals("t9", c.pendingTaskId("s1"));

        String second = c.ask("s1", "да");
        assertEquals("Возврат оформлен.", second);
        assertEquals("", c.pendingTaskId("s1"));

        JsonNode resume = received.get(1).path("params").path("message");
        assertEquals("t9", resume.path("taskId").asText());
        assertEquals("c9", resume.path("contextId").asText());
        assertEquals("да", resume.path("parts").get(0).path("text").asText());
    }

    @Test
    void emptyMessageRepliesEscalateToStop() {
        OrdersClient c = client();
        OrdersClient.EmptyReply first = c.emptyMessageReply("s1");
        assertTrue(!first.stop());
        OrdersClient.EmptyReply second = c.emptyMessageReply("s1");
        assertTrue(second.stop());
        c.clearEmpty("s1");
        assertTrue(!c.emptyMessageReply("s1").stop(), "counter resets after a real message");
    }
}
