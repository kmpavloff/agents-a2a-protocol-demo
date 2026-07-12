package io.github.kmpavloff.a2ademo.common.a2a;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/** A2A 1.0 Message. */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class A2aMessage {
    public static final String ROLE_USER = "ROLE_USER";
    public static final String ROLE_AGENT = "ROLE_AGENT";

    public String messageId;
    public String contextId;
    public String taskId;
    public String role;
    public List<Part> parts = new ArrayList<>();
    public List<String> extensions;
    public Map<String, Object> metadata;

    public A2aMessage() {}

    public static A2aMessage of(String role, Part... parts) {
        A2aMessage m = new A2aMessage();
        m.messageId = UUID.randomUUID().toString();
        m.role = role;
        m.parts = new ArrayList<>(List.of(parts));
        return m;
    }

    /** New message that resumes an existing task (mirrors a2a.NewMessageForTask). */
    public static A2aMessage forTask(String role, String taskId, String contextId, Part... parts) {
        A2aMessage m = of(role, parts);
        m.taskId = taskId;
        m.contextId = contextId;
        return m;
    }

    /** Text of the first part, or "" when absent. */
    public String firstText() {
        return parts == null || parts.isEmpty() ? "" : parts.getFirst().textOrEmpty();
    }
}
