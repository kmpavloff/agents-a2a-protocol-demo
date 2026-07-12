package io.github.kmpavloff.a2ademo.common.llm;

import java.util.List;

/** Minimal chat-model abstraction so tests can plug in a deterministic stub. */
public interface ChatModel {

    /**
     * One completion turn. Returns either plain text (content) or the first
     * requested tool call — tool calls take precedence, as in the Go adapter.
     */
    Completion complete(List<ChatMessage> messages, List<ToolSpec> tools);

    record Completion(String content, ToolCall toolCall) {
        public static Completion text(String content) {
            return new Completion(content, null);
        }

        public static Completion call(ToolCall call) {
            return new Completion(null, call);
        }
    }
}
