package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;

import java.util.List;

/**
 * Everything the orchestrator learns about the worker from its AgentCard: the
 * delegating tool's name/description plus a capabilities block for the system
 * prompt (port of a2abridge/profile.go).
 */
public record WorkerProfile(String toolName, String toolDesc, String summary) {

    /** Appended to the tool description so the LLM knows how to handle a bounce. */
    static final String NEEDS_INPUT_TAIL =
            "Если он вернёт NEEDS_USER_INPUT, задайте пользователю этот вопрос, затем снова вызовите инструмент с его ответом.";

    public static WorkerProfile fromCard(AgentCard card) {
        String name = card == null || card.name == null ? "" : card.name;
        String desc = card == null || card.description == null ? "" : card.description;
        List<AgentCard.AgentSkill> skills = card == null || card.skills == null ? List.of() : card.skills;

        StringBuilder td = new StringBuilder("Делегировать запрос удалённому агенту.");
        if (!desc.isEmpty()) {
            td.append(" ").append(desc);
        }
        for (AgentCard.AgentSkill s : skills) {
            td.append(String.format(" Навык «%s»: %s", s.name, s.description));
            if (s.examples != null && !s.examples.isEmpty()) {
                td.append(" Примеры: ").append(quoteExamples(s.examples, ", ")).append(".");
            }
        }
        td.append(" ").append(NEEDS_INPUT_TAIL);

        String agentName = name.isEmpty() ? "агент" : name;
        StringBuilder sm = new StringBuilder(String.format("Агент по имени «%s» умеет:", agentName));
        for (AgentCard.AgentSkill s : skills) {
            sm.append(String.format("%n- %s — %s", s.name, s.description));
            if (s.examples != null && !s.examples.isEmpty()) {
                sm.append(" (примеры: ").append(quoteExamples(s.examples, "; ")).append(")");
            }
        }

        return new WorkerProfile(sanitizeToolName(name), td.toString(), sm.toString());
    }

    /**
     * Turns a card name into a valid function-tool name of the form ask_&lt;slug&gt;.
     * Only ASCII letters/digits survive; a name with no usable ASCII falls back
     * to the generic "ask_agent".
     */
    static String sanitizeToolName(String name) {
        String slug = name == null ? "" : name.replaceAll("[^a-zA-Z0-9]+", "_");
        slug = slug.replaceAll("^_+|_+$", "");
        if (slug.isEmpty()) {
            return "ask_agent";
        }
        return "ask_" + slug;
    }

    private static String quoteExamples(List<String> examples, String sep) {
        StringBuilder b = new StringBuilder();
        for (int i = 0; i < examples.size(); i++) {
            if (i > 0) {
                b.append(sep);
            }
            b.append("«").append(examples.get(i)).append("»");
        }
        return b.toString();
    }
}
