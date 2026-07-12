package io.github.kmpavloff.a2ademo.common.llm;

import com.fasterxml.jackson.databind.JsonNode;
import io.github.kmpavloff.a2ademo.common.Json;

import java.util.LinkedHashMap;
import java.util.Map;

/** A function tool call requested by the model. Arguments are the raw JSON string. */
public record ToolCall(String id, String name, String argumentsJson) {

    /** Parsed arguments; malformed JSON yields an empty map (mirrors the Go adapter). */
    public Map<String, Object> args() {
        Map<String, Object> out = new LinkedHashMap<>();
        if (argumentsJson == null || argumentsJson.isBlank()) {
            return out;
        }
        try {
            JsonNode n = Json.MAPPER.readTree(argumentsJson);
            if (n.isObject()) {
                out.putAll(Json.MAPPER.convertValue(n, new com.fasterxml.jackson.core.type.TypeReference<Map<String, Object>>() {}));
            }
        } catch (Exception ignored) {
            // fall through to empty map
        }
        return out;
    }

    /** First non-blank string value among the given synonym keys, trimmed; "" if none. */
    public String firstArg(String... keys) {
        Map<String, Object> a = args();
        for (String k : keys) {
            Object v = a.get(k);
            if (v instanceof String s && !s.trim().isEmpty()) {
                return s.trim();
            }
        }
        return "";
    }
}
