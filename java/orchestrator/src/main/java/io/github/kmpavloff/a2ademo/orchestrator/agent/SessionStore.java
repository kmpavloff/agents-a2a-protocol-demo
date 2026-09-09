package io.github.kmpavloff.a2ademo.orchestrator.agent;

import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * История разговоров, общая для всех сборок агента.
 *
 * <p>Набор инструментов меняется при переключении агента в селекторе, и агент
 * из-за этого пересобирается; история же привязана к contextId разговора и
 * переезжать вместе с выбором не должна. В Go ту же роль играет одна
 * {@code session.InMemoryService()} на все runner'ы.
 */
public class SessionStore {

    private final Map<String, List<ChatMessage>> sessions = new ConcurrentHashMap<>();

    /** Изменяемая история сессии; создаётся при первом обращении. */
    public List<ChatMessage> history(String sessionId) {
        return sessions.computeIfAbsent(sessionId, k -> new ArrayList<>());
    }
}
