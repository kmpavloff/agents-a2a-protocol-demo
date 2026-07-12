package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.A2aTask;
import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.common.rpc.JsonRpc;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Minimal A2A 1.0 client over the JSONRPC binding, compatible with a2a-go v2
 * servers: resolves the AgentCard from /.well-known/agent-card.json, picks the
 * JSONRPC interface, and issues SendMessage calls.
 */
public class A2aClient {

    private final HttpClient http = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(10))
            .build();
    private final AtomicLong nextId = new AtomicLong(1);
    private final String invokeUrl;

    private A2aClient(String invokeUrl) {
        this.invokeUrl = invokeUrl;
    }

    /** Fetches the card at {@code baseUrl} and builds a client for its JSONRPC interface. */
    public static Resolved resolve(String baseUrl) {
        String base = baseUrl.endsWith("/") ? baseUrl.substring(0, baseUrl.length() - 1) : baseUrl;
        AgentCard card;
        try {
            HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();
            HttpRequest req = HttpRequest.newBuilder(URI.create(base + AgentCard.WELL_KNOWN_PATH))
                    .timeout(Duration.ofSeconds(15))
                    .GET().build();
            HttpResponse<String> resp = http.send(req, HttpResponse.BodyHandlers.ofString());
            if (resp.statusCode() / 100 != 2) {
                throw new A2aException("agent card HTTP " + resp.statusCode() + " at " + base);
            }
            card = Json.MAPPER.readValue(resp.body(), AgentCard.class);
        } catch (IOException | InterruptedException e) {
            if (e instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw new A2aException("resolve agent card at " + base + ": " + e.getMessage(), e);
        }

        String url = null;
        if (card.supportedInterfaces != null) {
            for (AgentCard.AgentInterface i : card.supportedInterfaces) {
                if (AgentCard.TRANSPORT_JSONRPC.equals(i.protocolBinding)) {
                    url = i.url;
                    break;
                }
            }
        }
        if (url == null) {
            throw new A2aException("agent card of \"" + card.name + "\" advertises no JSONRPC interface");
        }
        return new Resolved(card, new A2aClient(url));
    }

    public record Resolved(AgentCard card, A2aClient client) {}

    /** Result of SendMessage: exactly one of task / message is set (the oneof StreamResponse). */
    public record SendResult(A2aTask task, A2aMessage message) {}

    public SendResult sendMessage(A2aMessage message) {
        ObjectNode params = Json.MAPPER.createObjectNode();
        params.set("message", Json.MAPPER.valueToTree(message));

        JsonRpc.Request rpc = new JsonRpc.Request();
        rpc.id = Json.MAPPER.getNodeFactory().numberNode(nextId.getAndIncrement());
        rpc.method = JsonRpc.METHOD_SEND_MESSAGE;
        rpc.params = params;

        JsonNode result = call(rpc);
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

    private JsonNode call(JsonRpc.Request rpc) {
        HttpResponse<String> resp;
        try {
            HttpRequest req = HttpRequest.newBuilder(URI.create(invokeUrl))
                    .header("Content-Type", "application/json")
                    .timeout(Duration.ofMinutes(10))
                    .POST(HttpRequest.BodyPublishers.ofString(Json.MAPPER.writeValueAsString(rpc)))
                    .build();
            resp = http.send(req, HttpResponse.BodyHandlers.ofString());
        } catch (IOException | InterruptedException e) {
            if (e instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw new A2aException("A2A request to " + invokeUrl + " failed: " + e.getMessage(), e);
        }
        if (resp.statusCode() / 100 != 2) {
            throw new A2aException("A2A HTTP " + resp.statusCode() + " from " + invokeUrl);
        }
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
