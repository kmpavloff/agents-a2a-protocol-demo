package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Port of the Go orchcard/profile tests. */
class WorkerProfileTest {

    @Test
    void sanitizeToolName() {
        assertEquals("ask_orders_agent", WorkerProfile.sanitizeToolName("orders-agent"));
        assertEquals("ask_My_Agent_2", WorkerProfile.sanitizeToolName("My Agent 2"));
        assertEquals("ask_agent", WorkerProfile.sanitizeToolName("агент-заказов"));
        assertEquals("ask_agent", WorkerProfile.sanitizeToolName(""));
    }

    @Test
    void profileFromCardDerivesToolAndSummary() {
        AgentCard card = new AgentCard();
        card.name = "orders-agent";
        card.description = "Управляет заказами.";
        AgentCard.AgentSkill skill = new AgentCard.AgentSkill();
        skill.name = "Управление заказами";
        skill.description = "Поиск и возвраты.";
        skill.examples = List.of("статус заказа 1041");
        card.skills = List.of(skill);

        WorkerProfile p = WorkerProfile.fromCard(card);
        assertEquals("ask_orders_agent", p.toolName());
        assertTrue(p.toolDesc().contains("Управляет заказами."));
        assertTrue(p.toolDesc().contains("Навык «Управление заказами»"));
        assertTrue(p.toolDesc().contains("«статус заказа 1041»"));
        assertTrue(p.toolDesc().endsWith(WorkerProfile.NEEDS_INPUT_TAIL));
        assertTrue(p.summary().contains("Агент по имени «orders-agent» умеет:"));
        assertTrue(p.summary().contains("- Управление заказами — Поиск и возвраты."));
    }

    @Test
    void nullCardYieldsSafeFallbacks() {
        WorkerProfile p = WorkerProfile.fromCard(null);
        assertEquals("ask_agent", p.toolName());
        assertTrue(p.summary().contains("«агент»"));
    }
}
