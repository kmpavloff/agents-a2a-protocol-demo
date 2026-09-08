package io.github.kmpavloff.a2ademo.orchestrator.agent;

import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Промпт и история при нескольких делегирующих инструментах. */
class OrchestratorAgentToolsTest {

    /** Модель, которая ничего не вызывает и запоминает последний запрос. */
    static class RecordingModel implements ChatModel {
        List<ChatMessage> lastRequest;
        List<ToolSpec> lastTools;

        @Override
        public Completion complete(List<ChatMessage> messages, List<ToolSpec> tools) {
            lastRequest = new ArrayList<>(messages);
            lastTools = tools;
            return new Completion("готово", null);
        }
    }

    @Test
    void listsEveryToolNameInThePrompt() {
        String prompt = OrchestratorAgent.buildInstruction("ask_orders, ask_ouroboros", "Агент умеет…");
        assertTrue(prompt.contains("инструментам: ask_orders, ask_ouroboros"), prompt);
        assertTrue(prompt.contains("Если инструментов несколько"),
                "правило выбора между инструментами обязано быть в промпте");
        assertTrue(prompt.contains("Агент умеет…"));
    }

    // История принадлежит сессии, а не агенту: при переключении агента в
    // селекторе собирается новый агент, а разговор продолжается тот же.
    @Test
    void historySurvivesRebuildingTheAgent() {
        SessionStore sessions = new SessionStore();
        RecordingModel model = new RecordingModel();

        new OrchestratorAgent(model, List.of(), "", sessions).runTurn("s1", "первый вопрос", new OrchestratorAgent.TurnListener() {});
        new OrchestratorAgent(model, List.of(), "", sessions).runTurn("s1", "второй вопрос", new OrchestratorAgent.TurnListener() {});

        List<String> texts = model.lastRequest.stream().map(ChatMessage::content).toList();
        assertTrue(texts.contains("первый вопрос"), "первый ход обязан остаться в истории: " + texts);
        assertTrue(texts.contains("второй вопрос"));
    }

    @Test
    void exposesOneToolSpecPerAgent() {
        RecordingModel model = new RecordingModel();
        new OrchestratorAgent(model, List.of(), "", new SessionStore())
                .runTurn("s1", "вопрос", new OrchestratorAgent.TurnListener() {});
        assertEquals(List.of(), model.lastTools, "без агентов инструментов нет");
    }
}
