package io.github.kmpavloff.a2ademo.common.a2a;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.time.Instant;

/** A2A 1.0 TaskStatus. Timestamp is kept as an RFC3339 string. */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class TaskStatus {
    public String state;
    public A2aMessage message;
    public String timestamp;

    public TaskStatus() {}

    public static TaskStatus of(String state, A2aMessage message) {
        TaskStatus s = new TaskStatus();
        s.state = state;
        s.message = message;
        s.timestamp = Instant.now().toString();
        return s;
    }
}
