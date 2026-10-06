package io.github.kmpavloff.a2ademo.orchestrator.web;

import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;
import io.github.kmpavloff.a2ademo.orchestrator.agent.SessionStore;
import io.github.kmpavloff.a2ademo.orchestrator.store.AgentStore;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Мультиагентная разводка целиком (java-аналог того, что задача 19 собирает в
 * {@code OrchestratorApplication.main}): живой {@link Registry}, работающий
 * {@link AgentStore} с overlay-файлом на диске и {@code store.onChange} реально
 * подписанным на {@code registry::apply} — как в main, а не мокнутым.
 *
 * <p>Два поддельных удалённых агента, каждый за собственным встроенным
 * {@link HttpServer}: один агент недостаточен, чтобы отличить явный выбор
 * агента и режим «Авто» друг от друга или проверить, что правка списка не
 * ломает уже сконфигурированного соседа.
 */
class MultiAgentE2eTest {

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

    HttpServer shopWorker;
    HttpServer ordersWorker;
    final Deque<String> shopResults = new ArrayDeque<>();
    final List<JsonNode> shopRequests = new ArrayList<>();
    final Deque<String> ordersResults = new ArrayDeque<>();
    String ordersBase;
    StubModel model;
    Registry registry;
    AgentStore store;
    OrchestratorWebExecutor executor;

    @BeforeEach
    void setUp(@TempDir Path dir) throws IOException {
        shopWorker = fakeWorker("shop-agent", "Отвечает по заказам магазина.", shopResults, shopRequests);
        ordersWorker = fakeWorker("orders-agent", "Управляет заказами.", ordersResults, new ArrayList<>());
        String shopBase = "http://127.0.0.1:" + shopWorker.getAddress().getPort();
        ordersBase = "http://127.0.0.1:" + ordersWorker.getAddress().getPort();

        // "shop" — verbatim: его ответ должен уходить в браузер без локальной
        // модели. "orders" — обычный, участвует в наборе инструментов «Авто».
        List<AgentConfig> agents = List.of(
                new AgentConfig("shop", "", shopBase, "", List.of("shop", "support"), true, "", "", AuthConfig.NONE),
                new AgentConfig("orders", "", ordersBase, "", List.of(), false, "", "", AuthConfig.NONE));
        store = new AgentStore(agents, dir.resolve("agents.local.yaml"));
        registry = new Registry(store.agents(), Tracer.noop());
        // То самое, что до задачи 19 не было сделано в main(): без этой строки
        // store.create() ниже правил бы только overlay-файл, а живой реестр
        // остался бы прежним.
        store.onChange(registry::apply);

        model = new StubModel();
        // SessionStore принадлежит замыканию, которое собирает агента, а не
        // исполнителю: разговор должен пережить переключение агента в
        // селекторе, а не сбрасываться вместе с ним.
        SessionStore sessions = new SessionStore();
        executor = new OrchestratorWebExecutor(registry,
                (tools, summary) -> new OrchestratorAgent(model, tools, summary, sessions),
                Tracer.noop());
    }

    @AfterEach
    void tearDown() {
        shopWorker.stop(0);
        ordersWorker.stop(0);
    }

    private static HttpServer fakeWorker(String name, String description, Deque<String> results,
                                         List<JsonNode> seen) throws IOException {
        HttpServer s = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        String base = "http://127.0.0.1:" + s.getAddress().getPort();
        s.createContext("/.well-known/agent-card.json", ex -> respond(ex, """
                {"name":"%s","description":"%s","version":"0.1.0","capabilities":{},
                 "defaultInputModes":["text/plain"],"defaultOutputModes":["text/plain"],
                 "supportedInterfaces":[{"url":"%s/invoke","protocolBinding":"JSONRPC","protocolVersion":"1.0"}],
                 "skills":[]}
                """.formatted(name, description, base)));
        s.createContext("/invoke", ex -> {
            JsonNode req = Json.MAPPER.readTree(ex.getRequestBody());
            seen.add(req);
            respond(ex, "{\"jsonrpc\":\"2.0\",\"id\":" + req.path("id") + ",\"result\":" + results.pop() + "}");
        });
        s.start();
        return s;
    }

    private static void respond(HttpExchange ex, String body) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().set("Content-Type", "application/json");
        ex.sendResponseHeaders(200, b.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(b);
        }
    }

    /** Завершённая задача с единственным текстовым артефактом — минимальный ответ воркера. */
    private static String taskWithText(String text) {
        return """
                {"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"},
                 "artifacts":[{"artifactId":"a1","parts":[{"text":"%s"}]}]}}
                """.formatted(text);
    }

    // Явно выбранный verbatim-агент отвечает без локальной модели.
    @Test
    void anExplicitlyChosenVerbatimAgentAnswersWithoutTheLocalModel() {
        // модель не скриптуется вовсе: любой её вызов уронит StubModel
        A2aMessage msg = A2aMessage.of(A2aMessage.ROLE_USER, Part.text("статус 1041"));
        msg.metadata = Map.of("agentId", "shop");
        shopResults.push(taskWithText("Заказ 1041 доставлен"));

        List<Part> parts = executor.execute("c1", msg, true);

        assertEquals("Заказ 1041 доставлен", parts.getFirst().textOrEmpty());
    }

    // Навык из второго селектора браузера доходит до агента в metadata.skill,
    // а «без навыка» и чужой навык его не передают — даже если раньше в этом
    // разговоре навык был выбран.
    @Test
    void theSelectedSkillReachesTheAgent() {
        for (int i = 0; i < 3; i++) {
            shopResults.addLast(taskWithText("ок"));
        }
        for (String skill : List.of("support", "", "чужой")) {
            A2aMessage msg = A2aMessage.of(A2aMessage.ROLE_USER, Part.text("статус"));
            msg.metadata = Map.of("agentId", "shop", "skill", skill);
            executor.execute("c1", msg, false);
        }
        JsonNode first = shopRequests.get(0).path("params").path("message").path("metadata");
        assertEquals("support", first.path("skill").asText());
        for (int i = 1; i < 3; i++) {
            assertTrue(shopRequests.get(i).path("params").path("message").path("metadata").path("skill").isMissingNode(),
                    "навык не должен уходить: запрос #" + i);
        }
    }

    // Незнакомый agentId молча откатывается в «Авто»: браузер с устаревшим
    // списком не должен ломать разговор.
    @Test
    void anUnknownAgentIdFallsBackToAuto() {
        A2aMessage msg = A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет"));
        msg.metadata = Map.of("agentId", "нет такого");
        model.then(new ChatModel.Completion("здравствуйте", null));

        assertEquals("здравствуйте", executor.execute("c1", msg, true).getFirst().textOrEmpty());
    }

    // Правка списка из UI применяется к живому реестру без перезапуска.
    @Test
    void anEditFromTheUiReachesTheLiveRegistry() {
        store.create(new AgentConfig("extra", "", ordersBase, "", List.of(), false, "", "", AuthConfig.NONE));
        assertTrue(registry.ids().contains("extra"));
    }
}
