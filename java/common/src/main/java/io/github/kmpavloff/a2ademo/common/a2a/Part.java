package io.github.kmpavloff.a2ademo.common.a2a;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.Map;

/**
 * A2A 1.0 content part — the flattened proto-oneof shape used by a2a-go v2:
 * exactly one of {@code text}, {@code data}, {@code raw}, {@code url} is set.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class Part {
    public String text;
    public Object data;
    public String raw;      // base64
    public String url;
    public String filename;
    public String mediaType;
    public Map<String, Object> metadata;

    public Part() {}

    public static Part text(String text) {
        Part p = new Part();
        p.text = text;
        return p;
    }

    public static Part data(Object data, Map<String, Object> metadata) {
        Part p = new Part();
        p.data = data;
        p.metadata = metadata;
        return p;
    }

    /** Text content of the part, or "" when it is not a text part (mirrors a2a-go Part.Text()). */
    public String textOrEmpty() {
        return text == null ? "" : text;
    }
}
