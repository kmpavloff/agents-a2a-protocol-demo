package io.github.kmpavloff.a2ademo.common.llm;

import java.util.List;

/**
 * One OpenAI chat-completions message. {@code toolCalls} is set on assistant
 * messages that request tools; {@code toolCallId} on role=tool result messages.
 */
public record ChatMessage(String role, String content, List<ToolCall> toolCalls, String toolCallId) {

    public static ChatMessage system(String content) {
        return new ChatMessage("system", content, null, null);
    }

    public static ChatMessage user(String content) {
        return new ChatMessage("user", content, null, null);
    }

    public static ChatMessage assistant(String content) {
        return new ChatMessage("assistant", content, null, null);
    }

    public static ChatMessage assistantToolCall(ToolCall call) {
        return new ChatMessage("assistant", null, List.of(call), null);
    }

    public static ChatMessage tool(String toolCallId, String content) {
        return new ChatMessage("tool", content, null, toolCallId);
    }
}
