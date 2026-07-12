package io.github.kmpavloff.a2ademo.common.llm;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.config.LlmConfig;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.List;

/**
 * OpenAI-compatible chat-completions client (LM Studio, remote providers),
 * mirroring the Go internal/llm adapter: non-streaming, tool calls take
 * precedence over text, only the first tool call of a turn is used.
 */
public final class OpenAiChatModel implements ChatModel {
    private final HttpClient http = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(10))
            .build();
    private final LlmConfig cfg;

    public OpenAiChatModel(LlmConfig cfg) {
        this.cfg = cfg;
    }

    public String modelName() {
        return cfg.model();
    }

    @Override
    public Completion complete(List<ChatMessage> messages, List<ToolSpec> tools) {
        ObjectNode body = Json.MAPPER.createObjectNode();
        body.put("model", cfg.model());
        ArrayNode msgs = body.putArray("messages");
        for (ChatMessage m : messages) {
            msgs.add(toOpenAiMessage(m));
        }
        if (tools != null && !tools.isEmpty()) {
            ArrayNode ts = body.putArray("tools");
            for (ToolSpec t : tools) {
                ObjectNode tool = ts.addObject();
                tool.put("type", "function");
                ObjectNode fn = tool.putObject("function");
                fn.put("name", t.name());
                fn.put("description", t.description());
                fn.set("parameters", Json.MAPPER.valueToTree(t.parametersSchema()));
            }
        }

        String base = cfg.baseUrl().endsWith("/") ? cfg.baseUrl().substring(0, cfg.baseUrl().length() - 1) : cfg.baseUrl();
        HttpRequest req = HttpRequest.newBuilder(URI.create(base + "/chat/completions"))
                .header("Content-Type", "application/json")
                .header("Authorization", "Bearer " + cfg.apiKey())
                .timeout(Duration.ofMinutes(10))
                .POST(HttpRequest.BodyPublishers.ofString(body.toString()))
                .build();

        HttpResponse<String> resp;
        try {
            resp = http.send(req, HttpResponse.BodyHandlers.ofString());
        } catch (IOException | InterruptedException e) {
            if (e instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw new LlmException("LLM request failed (" + cfg.baseUrl() + "): " + e.getMessage(), e);
        }
        if (resp.statusCode() / 100 != 2) {
            throw new LlmException("LLM returned HTTP " + resp.statusCode() + ": " + truncate(resp.body()));
        }

        try {
            JsonNode root = Json.MAPPER.readTree(resp.body());
            JsonNode choices = root.path("choices");
            if (!choices.isArray() || choices.isEmpty()) {
                return Completion.text("");
            }
            JsonNode msg = choices.get(0).path("message");
            JsonNode toolCalls = msg.path("tool_calls");
            if (toolCalls.isArray() && !toolCalls.isEmpty()) {
                JsonNode tc = toolCalls.get(0);
                String name = tc.path("function").path("name").asText("");
                String id = tc.path("id").asText("");
                if (id.isEmpty()) {
                    id = "call_" + name;
                }
                return Completion.call(new ToolCall(id, name, tc.path("function").path("arguments").asText("")));
            }
            return Completion.text(msg.path("content").asText(""));
        } catch (IOException e) {
            throw new LlmException("LLM response parse error: " + e.getMessage(), e);
        }
    }

    private ObjectNode toOpenAiMessage(ChatMessage m) {
        ObjectNode n = Json.MAPPER.createObjectNode();
        n.put("role", m.role());
        if (m.toolCalls() != null && !m.toolCalls().isEmpty()) {
            ArrayNode calls = n.putArray("tool_calls");
            for (ToolCall tc : m.toolCalls()) {
                ObjectNode c = calls.addObject();
                c.put("id", tc.id());
                c.put("type", "function");
                ObjectNode fn = c.putObject("function");
                fn.put("name", tc.name());
                fn.put("arguments", tc.argumentsJson() == null || tc.argumentsJson().isBlank() ? "{}" : tc.argumentsJson());
            }
        } else {
            n.put("content", m.content() == null ? "" : m.content());
        }
        if (m.toolCallId() != null) {
            n.put("tool_call_id", m.toolCallId());
        }
        return n;
    }

    private static String truncate(String s) {
        return s != null && s.length() > 400 ? s.substring(0, 400) + "…" : String.valueOf(s);
    }

    public static class LlmException extends RuntimeException {
        public LlmException(String message) {
            super(message);
        }

        public LlmException(String message, Throwable cause) {
            super(message, cause);
        }
    }
}
