package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.A2aTask;
import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.rpc.JsonRpc;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.List;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Minimal A2A 1.0 client over the JSONRPC binding, compatible with a2a-go v2
 * servers: resolves the AgentCard from /.well-known/agent-card.json, picks the
 * JSONRPC interface, and issues SendMessage calls.
 */
public class A2aClient {

    private final HttpClient http;
    private final AtomicLong nextId = new AtomicLong(1);
    private final String invokeUrl;
    private final Duration timeout;
    private final AuthConfig auth;
    private final Tracer trace;

    private A2aClient(String invokeUrl, Duration timeout, AuthConfig auth, Tracer trace) {
        this.invokeUrl = invokeUrl;
        this.timeout = timeout;
        this.auth = auth;
        this.trace = trace;
        this.http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();
    }

    /** Прежняя форма без конфига: анонимный агент по каноническому пути карточки. */
    public static Resolved resolve(String baseUrl) {
        return resolve(new AgentConfig("agent", "", baseUrl, "", "", false, "", "", AuthConfig.NONE), Tracer.noop());
    }

    /** Читает карточку по адресу и пути из конфига и строит клиента её JSONRPC-интерфейса. */
    public static Resolved resolve(AgentConfig cfg, Tracer trace) {
        String base = cfg.url().endsWith("/") ? cfg.url().substring(0, cfg.url().length() - 1) : cfg.url();
        String cardPath = cfg.cardPath().isEmpty() ? AgentConfig.DEFAULT_CARD_PATH : cfg.cardPath();
        AgentCard card;
        try {
            HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();
            HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(base + cardPath))
                    .timeout(Duration.ofSeconds(15))
                    .GET();
            authorize(b, cfg.auth());
            HttpResponse<String> resp = http.send(b.build(), HttpResponse.BodyHandlers.ofString());
            if (resp.statusCode() / 100 != 2) {
                throw new A2aException("agent card HTTP " + resp.statusCode() + " at " + base + cardPath);
            }
            trace.dump("карточка агента " + cfg.id(), resp.body());
            // Нераспознанные блоки (securitySchemes и прочее) Jackson молча
            // пропускает: аутентификацию мы ставим сами из конфига.
            card = Json.MAPPER.readValue(resp.body(), AgentCard.class);
        } catch (IOException | InterruptedException e) {
            if (e instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw new A2aException("resolve agent card at " + base + cardPath + ": " + e.getMessage(), e);
        }

        String url = null;
        if (card.supportedInterfaces != null) {
            for (AgentCard.AgentInterface i : card.supportedInterfaces) {
                if (AgentCard.TRANSPORT_JSONRPC.equals(i.protocolBinding)) {
                    url = mergeEndpoint(base, i.url);
                    break;
                }
            }
        }
        if (url == null) {
            // Карточка может не объявлять интерфейсов вовсе — тогда работаем по
            // адресу из конфига, как это делает Go.
            url = base;
        }
        return new Resolved(card, new A2aClient(url, cfg.timeoutDuration(), cfg.auth(), trace));
    }

    /**
     * Рабочий адрес транспорта: схема и хост из конфига (по нему карточка и была
     * получена), путь из карточки. Так и нерабочий 0.0.0.0 внутри чужой карточки
     * не мешает, и объявленный агентом путь не теряется.
     */
    static String mergeEndpoint(String base, String declared) {
        try {
            URI b = URI.create(base);
            URI d = URI.create(declared);
            String path = d.getPath();
            if (path == null || path.isEmpty() || path.equals("/")) {
                return base;
            }
            String basePath = b.getPath() == null ? "" : b.getPath();
            if (basePath.endsWith("/")) {
                basePath = basePath.substring(0, basePath.length() - 1);
            }
            return new URI(b.getScheme(), b.getAuthority(),
                    basePath + (path.startsWith("/") ? path : "/" + path), null, null).toString();
        } catch (Exception e) {
            return declared;
        }
    }

    /** Заголовок Basic на каждый запрос; встроенной поддержки схемы у HttpClient для нас мало. */
    private static void authorize(HttpRequest.Builder b, AuthConfig auth) {
        if (!auth.basic()) {
            return;
        }
        String token = java.util.Base64.getEncoder()
                .encodeToString((auth.username() + ":" + auth.password()).getBytes(StandardCharsets.UTF_8));
        b.header("Authorization", "Basic " + token);
    }

    public record Resolved(AgentCard card, A2aClient client) {}

    /** Result of SendMessage: exactly one of task / message is set (the oneof StreamResponse). */
    public record SendResult(A2aTask task, A2aMessage message) {}

    public SendResult sendMessage(A2aMessage message, List<String> extensions) {
        ObjectNode params = Json.MAPPER.createObjectNode();
        params.set("message", Json.MAPPER.valueToTree(message));

        JsonRpc.Request rpc = new JsonRpc.Request();
        rpc.id = Json.MAPPER.getNodeFactory().numberNode(nextId.getAndIncrement());
        rpc.method = JsonRpc.METHOD_SEND_MESSAGE;
        rpc.params = params;

        JsonNode result = wrapBareResult(call(rpc, extensions));
        try {
            if (result.has("task")) {
                return new SendResult(Json.MAPPER.treeToValue(result.get("task"), A2aTask.class), null);
            }
            if (result.has("message")) {
                return new SendResult(null, Json.MAPPER.treeToValue(result.get("message"), A2aMessage.class));
            }
        } catch (IOException e) {
            throw new A2aException("decode SendMessage result: " + e.getMessage(), e);
        }
        throw new A2aException("unexpected SendMessage result keys: " + result);
    }

    /** Опрос задачи, пока она в работе: контракт внешнего агента предписывает поллинг. */
    public A2aTask getTask(String taskId) {
        ObjectNode params = Json.MAPPER.createObjectNode();
        params.put("id", taskId);
        JsonRpc.Request rpc = new JsonRpc.Request();
        rpc.id = Json.MAPPER.getNodeFactory().numberNode(nextId.getAndIncrement());
        rpc.method = JsonRpc.METHOD_GET_TASK;
        rpc.params = params;
        JsonNode result = wrapBareResult(call(rpc, null));
        try {
            return Json.MAPPER.treeToValue(result.has("task") ? result.get("task") : result, A2aTask.class);
        } catch (IOException e) {
            throw new A2aException("decode GetTask result: " + e.getMessage(), e);
        }
    }

    /**
     * Чинит ответ агента, который отдаёт результат не по A2A 1.0: спека ждёт
     * oneof-обёртку {@code {"task": …}} либо {@code {"message": …}}, а агент
     * кладёт объект напрямую. Корректный ответ проходит насквозь.
     */
    static JsonNode wrapBareResult(JsonNode result) {
        if (result == null || !result.isObject()) {
            return result;
        }
        for (String k : List.of("task", "message", "statusUpdate", "artifactUpdate")) {
            if (result.has(k)) {
                return result;
            }
        }
        String key = null;
        if (result.has("status") || result.has("artifacts")) {
            key = "task";
        } else if (result.has("parts") || result.has("role")) {
            key = "message";
        }
        if (key == null) {
            return result;
        }
        return Json.MAPPER.createObjectNode().set(key, result);
    }

    private JsonNode call(JsonRpc.Request rpc, List<String> extensions) {
        HttpResponse<String> resp;
        String body;
        try {
            body = Json.MAPPER.writeValueAsString(rpc);
            HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(invokeUrl))
                    .header("Content-Type", "application/json")
                    .timeout(timeout)
                    .POST(HttpRequest.BodyPublishers.ofString(body));
            authorize(b, auth);
            if (extensions != null && !extensions.isEmpty()) {
                String value = String.join(", ", extensions);
                // Под двумя именами: A2A 1.0 зовёт заголовок A2A-Extensions, а
                // спека A2UI v0.9 писалась под ранний A2A и знает только
                // X-A2A-Extensions — агент, собранный по ней, второго не увидит.
                b.header("A2A-Extensions", value);
                b.header("X-A2A-Extensions", value);
            }
            trace.dump("запрос агенту " + invokeUrl, body);
            resp = http.send(b.build(), HttpResponse.BodyHandlers.ofString());
        } catch (IOException | InterruptedException e) {
            if (e instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw new A2aException("A2A request to " + invokeUrl + " failed: " + e.getMessage(), e);
        }
        if (resp.statusCode() / 100 != 2) {
            throw new A2aException("A2A HTTP " + resp.statusCode() + " from " + invokeUrl);
        }
        trace.dump("ответ агента", resp.body());
        try {
            JsonNode root = Json.MAPPER.readTree(resp.body());
            JsonNode error = root.get("error");
            if (error != null && !error.isNull()) {
                throw new A2aException("A2A error " + error.path("code").asInt()
                        + ": " + error.path("message").asText());
            }
            JsonNode result = root.get("result");
            if (result == null || result.isNull()) {
                throw new A2aException("A2A response has no result");
            }
            return result;
        } catch (IOException e) {
            throw new A2aException("A2A response parse error: " + e.getMessage(), e);
        }
    }

    public static class A2aException extends RuntimeException {
        public A2aException(String message) {
            super(message);
        }

        public A2aException(String message, Throwable cause) {
            super(message, cause);
        }
    }
}
