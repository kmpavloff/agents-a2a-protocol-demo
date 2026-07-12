package io.github.kmpavloff.a2ademo.worker.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolCall;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.worker.agent.WorkerAgent;
import io.github.kmpavloff.a2ademo.worker.orders.OrderStore;
import io.github.kmpavloff.a2ademo.worker.orders.OrderTools;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayDeque;
import java.util.Deque;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Full A2A JSON-RPC round-trips against the worker controller with a scripted
 * stub LLM — the Java counterpart of the Go internal/a2abridge e2e tests:
 * SendMessage → input-required → resume (same task) → completed.
 */
class WorkerA2aE2eTest {

    /** FIFO-scripted ChatModel: each turn pops the next canned completion. */
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

    OrderStore store;
    StubModel model;
    A2aController controller;

    @BeforeEach
    void setUp() throws IOException {
        Path seed = Files.createTempFile("orders", ".json");
        Files.writeString(seed, """
                {"orders":[
                  {"id":"1041","customer":"alice","item":"USB-C хаб","amount":34.50,"currency":"EUR","status":"delivered","created":"2026-06-10","refundable":true}
                ],"sales_stats":[]}
                """);
        store = OrderStore.load(seed.toString());
        model = new StubModel();
        WorkerAgent agent = new WorkerAgent(model, new OrderTools(store), Tracer.noop());
        controller = new A2aController(agent, WorkerCards.agentCard("http://localhost:8081"), Tracer.noop());
    }

    private JsonNode invoke(String requestJson) throws IOException {
        Object resp = controller.invoke(requestJson);
        return Json.MAPPER.readTree(Json.MAPPER.writeValueAsString(resp));
    }

    private static String sendMessageJson(String text, String taskId, String contextId) {
        String taskRef = taskId == null ? ""
                : "\"taskId\":\"" + taskId + "\",\"contextId\":\"" + contextId + "\",";
        return """
                {"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{
                 "messageId":"m-test","role":"ROLE_USER",%s"parts":[{"text":"%s"}]}}}
                """.formatted(taskRef, text);
    }

    @Test
    void statusRequestCompletesWithWidgetArtifact() throws IOException {
        model.then(ChatModel.Completion.call(new ToolCall("c1", "get_order_status", "{\"order_id\":\"1041\"}")))
                .then(ChatModel.Completion.text("Вот детали вашего заказа:"));

        JsonNode task = invoke(sendMessageJson("статус заказа 1041", null, null)).path("result").path("task");
        assertEquals("TASK_STATE_COMPLETED", task.path("status").path("state").asText());
        JsonNode parts = task.path("artifacts").get(0).path("parts");
        assertEquals("Вот детали вашего заказа:", parts.get(0).path("text").asText());
        assertEquals("widget/order", parts.get(1).path("metadata").path("kind").asText());
        assertEquals("1041", parts.get(1).path("data").path("order").path("id").asText());
        assertFalse(parts.get(1).path("data").has("kind"), "kind must move to part metadata");
    }

    @Test
    void refundPausesForConfirmationAndResumesSameTask() throws IOException {
        model.then(ChatModel.Completion.call(new ToolCall("c1", "initiate_refund", "{\"order_id\":\"1041\"}")));

        JsonNode task = invoke(sendMessageJson("верни деньги за 1041", null, null)).path("result").path("task");
        String taskId = task.path("id").asText();
        String contextId = task.path("contextId").asText();
        assertEquals("TASK_STATE_INPUT_REQUIRED", task.path("status").path("state").asText());
        JsonNode ask = task.path("status").path("message");
        assertEquals("ROLE_AGENT", ask.path("role").asText());
        assertEquals("Подтвердите оформление возврата по заказу 1041? (да/нет)",
                ask.path("parts").get(0).path("text").asText());
        assertEquals("widget/confirmation", ask.path("parts").get(1).path("metadata").path("kind").asText());
        // The refund must NOT have run yet.
        assertEquals("delivered", store.get("1041").orElseThrow().status());

        // Resume the SAME task with the user's confirmation.
        model.then(ChatModel.Completion.text("Готово, возврат оформлен."));
        JsonNode resumed = invoke(sendMessageJson("да", taskId, contextId)).path("result").path("task");
        assertEquals(taskId, resumed.path("id").asText());
        assertEquals("TASK_STATE_COMPLETED", resumed.path("status").path("state").asText());
        assertEquals("Готово, возврат оформлен.",
                resumed.path("artifacts").get(0).path("parts").get(0).path("text").asText());
        assertEquals("refunded", store.get("1041").orElseThrow().status());
    }

    @Test
    void refundDeclinedShortCircuits() throws IOException {
        model.then(ChatModel.Completion.call(new ToolCall("c1", "initiate_refund", "{\"order_id\":\"1041\"}")));
        JsonNode task = invoke(sendMessageJson("верни деньги за 1041", null, null)).path("result").path("task");

        JsonNode resumed = invoke(sendMessageJson("нет, не надо",
                task.path("id").asText(), task.path("contextId").asText())).path("result").path("task");
        assertEquals("TASK_STATE_COMPLETED", resumed.path("status").path("state").asText());
        assertEquals("Возврат отменён по вашему решению.",
                resumed.path("artifacts").get(0).path("parts").get(0).path("text").asText());
        assertEquals("delivered", store.get("1041").orElseThrow().status(), "declined refund must not run");
    }

    @Test
    void needInputSentinelBecomesInputRequiredAndResumes() throws IOException {
        model.then(ChatModel.Completion.text("NEED_INPUT: Какой именно заказ вернуть — #1023 или #1041?"));

        JsonNode task = invoke(sendMessageJson("верни мой последний заказ", null, null)).path("result").path("task");
        assertEquals("TASK_STATE_INPUT_REQUIRED", task.path("status").path("state").asText());
        assertEquals("Какой именно заказ вернуть — #1023 или #1041?",
                task.path("status").path("message").path("parts").get(0).path("text").asText());

        model.then(ChatModel.Completion.call(new ToolCall("c2", "get_order_status", "{\"order_id\":\"1041\"}")))
                .then(ChatModel.Completion.text("Вот детали вашего заказа:"));
        JsonNode resumed = invoke(sendMessageJson("#1041",
                task.path("id").asText(), task.path("contextId").asText())).path("result").path("task");
        assertEquals("TASK_STATE_COMPLETED", resumed.path("status").path("state").asText());
    }

    @Test
    void unknownTaskIdIsAnRpcError() throws IOException {
        JsonNode resp = invoke(sendMessageJson("привет", "no-such-task", "ctx"));
        assertTrue(resp.has("error"));
        assertEquals(-32001, resp.path("error").path("code").asInt());
    }

    @Test
    void unknownMethodIsMethodNotFound() throws IOException {
        JsonNode resp = invoke("{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"NoSuchMethod\",\"params\":{}}");
        assertEquals(-32601, resp.path("error").path("code").asInt());
    }

    @Test
    void agentCardAdvertisesJsonRpcInterface() throws Exception {
        JsonNode card = Json.MAPPER.readTree(Json.MAPPER.writeValueAsString(controller.agentCard()));
        assertEquals("orders-agent", card.path("name").asText());
        JsonNode iface = card.path("supportedInterfaces").get(0);
        assertEquals("http://localhost:8081/invoke", iface.path("url").asText());
        assertEquals("JSONRPC", iface.path("protocolBinding").asText());
        assertEquals("1.0", iface.path("protocolVersion").asText());
        assertTrue(card.has("capabilities"));
    }
}
