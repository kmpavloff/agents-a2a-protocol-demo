package io.github.kmpavloff.a2ademo.orchestrator.web;

import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpServer;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolCall;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;
import io.github.kmpavloff.a2ademo.common.rpc.JsonRpc;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.A2aClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.WorkerProfile;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.Deque;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * The --web A2UI gateway end-to-end (Java counterpart of the Go widget e2e
 * tests): browser-style JSON-RPC against the controller, canned worker A2A
 * server underneath, scripted stub LLM.
 */
class WebE2eTest {

    static class StubModel implements ChatModel {
        final Deque<Completion> script = new ArrayDeque<>();

        StubModel then(Completion c) {
            script.add(c);
            return this;
        }

        @Override
        public Completion complete(List<ChatMessage> messages, List<ToolSpec> tools) {
            if (script.isEmpty()) {
                throw new AssertionError("stub LLM called more times than scripted");
            }
            return script.pop();
        }
    }

    HttpServer worker;
    final Deque<String> workerResults = new ArrayDeque<>();
    StubModel model;
    A2aWebController controller;

    @BeforeEach
    void setUp() throws IOException {
        worker = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        String base = "http://127.0.0.1:" + worker.getAddress().getPort();
        worker.createContext("/.well-known/agent-card.json", ex -> respond(ex, """
                {"name":"orders-agent","description":"Управляет заказами.","version":"0.1.0","capabilities":{},
                 "defaultInputModes":["text/plain"],"defaultOutputModes":["text/plain"],
                 "supportedInterfaces":[{"url":"%s/invoke","protocolBinding":"JSONRPC","protocolVersion":"1.0"}],
                 "skills":[]}
                """.formatted(base)));
        worker.createContext("/invoke", ex -> {
            JsonNode req = Json.MAPPER.readTree(ex.getRequestBody());
            respond(ex, "{\"jsonrpc\":\"2.0\",\"id\":" + req.path("id") + ",\"result\":" + workerResults.pop() + "}");
        });
        worker.start();

        A2aClient.Resolved resolved = A2aClient.resolve(base);
        OrdersClient orders = new OrdersClient(resolved.client(), WorkerProfile.fromCard(resolved.card()), Tracer.noop());
        model = new StubModel();
        OrchestratorAgent agent = new OrchestratorAgent(model, orders);
        OrchestratorWebExecutor executor = new OrchestratorWebExecutor(agent, orders, Tracer.noop());
        controller = new A2aWebController(executor, OrchestratorCards.agentCard("http://localhost:8080"), Tracer.noop());
    }

    @AfterEach
    void tearDown() {
        worker.stop(0);
    }

    private static void respond(com.sun.net.httpserver.HttpExchange ex, String body) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().set("Content-Type", "application/json");
        ex.sendResponseHeaders(200, b.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(b);
        }
    }

    private JsonNode invoke(String partsJson, String contextId, boolean a2ui) throws IOException {
        String ctx = contextId == null ? "" : "\"contextId\":\"" + contextId + "\",";
        String body = """
                {"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{
                 "messageId":"m-web","role":"ROLE_USER",%s"parts":[%s]}}}
                """.formatted(ctx, partsJson);
        JsonRpc.Response resp = controller
                .invoke(body, a2ui ? List.of(A2ui.EXTENSION_URI) : null)
                .getBody();
        return Json.MAPPER.readTree(Json.MAPPER.writeValueAsString(resp));
    }

    @Test
    void textTurnEmitsA2uiParts() throws IOException {
        workerResults.add("""
                {"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"},
                 "artifacts":[{"artifactId":"a1","parts":[{"text":"Вот детали вашего заказа:"},
                   {"data":{"title":"Заказ 1041","order":{"id":"1041","item":"USB-C хаб","status_label":"доставлен",
                    "amount":34.5,"currency":"EUR","customer":"alice","created":"2026-06-10"}},
                    "metadata":{"kind":"widget/order","version":1}}]}]}}
                """);
        model.then(ChatModel.Completion.call(new ToolCall("c1", "ask_orders_agent", "{\"message\":\"статус заказа 1041\"}")))
                .then(ChatModel.Completion.text("Вот детали вашего заказа:"));

        JsonNode task = invoke("{\"text\":\"статус заказа 1041\"}", null, true).path("result").path("task");
        assertEquals("TASK_STATE_COMPLETED", task.path("status").path("state").asText());
        JsonNode parts = task.path("artifacts").get(0).path("parts");
        assertEquals(3, parts.size(), "text + createSurface + updateComponents: " + parts);
        assertEquals("Вот детали вашего заказа:", parts.get(0).path("text").asText());
        assertEquals("application/a2ui+json", parts.get(1).path("mediaType").asText());
        assertTrue(parts.get(1).path("data").has("createSurface"));
        assertTrue(parts.get(2).path("data").has("updateComponents"));
    }

    @Test
    void withoutA2uiHeaderWidgetsAreDropped() throws IOException {
        workerResults.add("""
                {"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"},
                 "artifacts":[{"artifactId":"a1","parts":[{"text":"Готово."},
                   {"data":{"title":"Заказ 1041","order":{"id":"1041"}},"metadata":{"kind":"widget/order","version":1}}]}]}}
                """);
        model.then(ChatModel.Completion.call(new ToolCall("c1", "ask_orders_agent", "{\"message\":\"статус заказа 1041\"}")))
                .then(ChatModel.Completion.text("Готово."));

        JsonNode parts = invoke("{\"text\":\"статус заказа 1041\"}", null, false)
                .path("result").path("task").path("artifacts").get(0).path("parts");
        assertEquals(1, parts.size(), "A2UI inactive → text-only artifact: " + parts);
    }

    @Test
    void cardFormSubmitResumesWorkerWithCardBypassingLlm() throws IOException {
        // Turn 1: worker pauses input-required with the confirmation widget.
        workerResults.add("""
                {"task":{"id":"t7","contextId":"wc7","status":{"state":"TASK_STATE_INPUT_REQUIRED",
                 "message":{"messageId":"m1","role":"ROLE_AGENT","parts":[
                   {"text":"Подтвердите оформление возврата по заказу 1041? (да/нет)"},
                   {"data":{"type":"confirmation","message":"?","order_id":"1041","actions":[]},
                    "metadata":{"kind":"widget/confirmation","version":1}}]}}}}
                """);
        model.then(ChatModel.Completion.call(new ToolCall("c1", "ask_orders_agent", "{\"message\":\"верни деньги за 1041\"}")))
                .then(ChatModel.Completion.text("Подтвердите?"));
        JsonNode task1 = invoke("{\"text\":\"верни деньги за 1041\"}", null, true).path("result").path("task");
        String contextId = task1.path("contextId").asText();

        // Turn 2: approve → worker asks for the card (form widget), LLM bypassed.
        workerResults.add("""
                {"task":{"id":"t7","contextId":"wc7","status":{"state":"TASK_STATE_INPUT_REQUIRED",
                 "message":{"messageId":"m2","role":"ROLE_AGENT","parts":[
                   {"text":"Укажите номер карты для возврата средств по заказу 1041 (13–19 цифр)."},
                   {"data":{"type":"form","title":"Реквизиты для возврата","message":"Укажите номер карты","severity":"info","order_id":"1041",
                     "fields":[{"id":"card_number","label":"Номер карты"}],
                     "actions":[{"id":"submit_refund_details","label":"Вернуть на карту"}]},
                    "metadata":{"kind":"widget/refund_form","version":1}}]}}}}
                """);
        JsonNode task2 = invoke("""
                {"data":{"version":"v0.9","action":{"name":"approve_refund","context":{"order_id":"1041"}}},
                 "mediaType":"application/a2ui+json"}
                """, contextId, true).path("result").path("task");
        JsonNode parts2 = task2.path("artifacts").get(0).path("parts");
        boolean hasTextField = parts2.toString().contains("TextField");
        assertTrue(hasTextField, "approve must produce the card form as A2UI: " + parts2);

        // Turn 3: submit the form — the card number resumes the worker directly
        // (empty LLM script proves the bypass) and the receipt file passes through.
        workerResults.add("""
                {"task":{"id":"t7","contextId":"wc7","status":{"state":"TASK_STATE_COMPLETED"},
                 "artifacts":[{"artifactId":"a1","parts":[
                   {"text":"Возврат оформлен. Средства поступят на карту •••• 1111."},
                   {"data":{"order_id":"1041","card_last4":"1111","receipt_id":"RF-1"},
                    "metadata":{"kind":"widget/refund_receipt","version":1}},
                   {"raw":"0JrQstC40YLQsNC90YbQuNGP","filename":"receipt-1041.txt","mediaType":"text/plain"}]}]}}
                """);
        JsonNode task3 = invoke("""
                {"data":{"version":"v0.9","action":{"name":"submit_refund_details",
                  "context":{"order_id":"1041","card_number":"4111 1111 1111 1111"}}},
                 "mediaType":"application/a2ui+json"}
                """, contextId, true).path("result").path("task");
        assertEquals("TASK_STATE_COMPLETED", task3.path("status").path("state").asText());
        JsonNode parts3 = task3.path("artifacts").get(0).path("parts");
        assertTrue(parts3.get(0).path("text").asText().contains("•••• 1111"));
        JsonNode file = parts3.get(parts3.size() - 1);
        assertEquals("receipt-1041.txt", file.path("filename").asText());
        assertEquals("text/plain", file.path("mediaType").asText());
        assertEquals("0JrQstC40YLQsNC90YbQuNGP", file.path("raw").asText(), "receipt bytes must pass through unchanged");
    }

    @Test
    void confirmationButtonResumesWorkerDirectlyBypassingLlm() throws IOException {
        // Turn 1: worker pauses input-required with a confirmation widget.
        workerResults.add("""
                {"task":{"id":"t9","contextId":"wc9","status":{"state":"TASK_STATE_INPUT_REQUIRED",
                 "message":{"messageId":"m1","role":"ROLE_AGENT","parts":[
                   {"text":"Подтвердите оформление возврата по заказу 1041? (да/нет)"},
                   {"data":{"type":"confirmation","title":"Подтверждение возврата",
                     "message":"Подтвердите оформление возврата по заказу 1041? (да/нет)","order_id":"1041",
                     "actions":[{"id":"approve","label":"Оформить возврат"},{"id":"decline","label":"Отмена"}]},
                    "metadata":{"kind":"widget/confirmation","version":1}}]}}}}
                """);
        model.then(ChatModel.Completion.call(new ToolCall("c1", "ask_orders_agent", "{\"message\":\"верни деньги за 1041\"}")))
                .then(ChatModel.Completion.text("Подтвердите оформление возврата по заказу 1041? (да/нет)"));

        JsonNode task1 = invoke("{\"text\":\"верни деньги за 1041\"}", null, true).path("result").path("task");
        String contextId = task1.path("contextId").asText();
        JsonNode parts1 = task1.path("artifacts").get(0).path("parts");
        assertTrue(parts1.size() >= 3, "confirmation widget must ride as A2UI parts: " + parts1);

        // Turn 2: the approve button. The stub LLM script is EMPTY — a model
        // call would throw — proving the resume bypasses the LLM.
        workerResults.add("""
                {"task":{"id":"t9","contextId":"wc9","status":{"state":"TASK_STATE_COMPLETED"},
                 "artifacts":[{"artifactId":"a2","parts":[{"text":"Готово, возврат оформлен."}]}]}}
                """);
        String actionPart = """
                {"data":{"version":"v0.9","action":{"name":"approve_refund","context":{"order_id":"1041"}}},
                 "mediaType":"application/a2ui+json"}
                """;
        JsonNode task2 = invoke(actionPart, contextId, true).path("result").path("task");
        assertEquals("TASK_STATE_COMPLETED", task2.path("status").path("state").asText());
        assertEquals("Готово, возврат оформлен.",
                task2.path("artifacts").get(0).path("parts").get(0).path("text").asText());
    }

    @Test
    void a2uiExtensionEchoedInResponseHeader() throws IOException {
        workerResults.add("""
                {"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"},
                 "artifacts":[{"artifactId":"a1","parts":[{"text":"Готово."}]}]}}
                """);
        model.then(ChatModel.Completion.call(new ToolCall("c1", "ask_orders_agent", "{\"message\":\"привет\"}")))
                .then(ChatModel.Completion.text("Готово."));
        var entity = controller.invoke("""
                {"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{
                 "messageId":"m-web","role":"ROLE_USER","parts":[{"text":"привет"}]}}}
                """, List.of(A2ui.EXTENSION_URI));
        assertEquals(A2ui.EXTENSION_URI, entity.getHeaders().getFirst("A2A-Extensions"));
    }

    @Test
    void orchestratorCardAdvertisesA2uiExtension() throws Exception {
        JsonNode card = Json.MAPPER.readTree(Json.MAPPER.writeValueAsString(controller.agentCard()));
        assertEquals("orders-orchestrator", card.path("name").asText());
        assertEquals(A2ui.EXTENSION_URI,
                card.path("capabilities").path("extensions").get(0).path("uri").asText());
        assertEquals("JSONRPC", card.path("supportedInterfaces").get(0).path("protocolBinding").asText());
    }
}
