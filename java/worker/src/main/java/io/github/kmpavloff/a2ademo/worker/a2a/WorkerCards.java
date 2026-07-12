package io.github.kmpavloff.a2ademo.worker.a2a;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;

import java.util.List;

/** Builds the worker's AgentCard (port of a2abridge.AgentCard). */
public final class WorkerCards {

    private WorkerCards() {}

    public static AgentCard agentCard(String publicUrl) {
        AgentCard card = new AgentCard();
        card.name = "orders-agent";
        card.description = "Управляет заказами, статусами, статистикой и возвратами.";
        card.version = "0.1.0";
        card.defaultInputModes = List.of("text/plain");
        card.defaultOutputModes = List.of("text/plain");
        card.supportedInterfaces = List.of(AgentCard.AgentInterface.jsonrpc(publicUrl + "/invoke"));

        AgentCard.AgentSkill skill = new AgentCard.AgentSkill();
        skill.id = "manage_orders";
        skill.name = "Управление заказами";
        skill.description = "Поиск заказов по номеру или товару, статусы, статистика продаж за период и оформление возвратов.";
        skill.tags = List.of("заказы", "статусы", "статистика продаж", "возвраты", "поиск");
        skill.examples = List.of(
                "верни деньги за заказ 1041",
                "статус заказа 1041",
                "последние заказы alice",
                "статистика продаж за 2026-06");
        card.skills = List.of(skill);
        return card;
    }
}
