package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;

import java.util.List;
import java.util.Map;

/** Builds the orchestrator's AgentCard for browser A2UI clients (port of orchcard.go). */
public final class OrchestratorCards {

    private OrchestratorCards() {}

    public static AgentCard agentCard(String publicUrl) {
        AgentCard card = new AgentCard();
        card.name = "orders-orchestrator";
        card.description = "Оркестратор поддержки: делегирует работу с заказами и отдаёт A2UI-виджеты.";
        card.version = "0.1.0";
        card.defaultInputModes = List.of("text/plain");
        card.defaultOutputModes = List.of("text/plain");
        // Advertise the A2UI A2A-extension so clients activate generative UI.
        card.capabilities = Map.of("extensions", List.of(Map.of(
                "uri", A2ui.EXTENSION_URI,
                "description", "Отдаёт интерфейс через A2UI (generative UI).")));
        card.supportedInterfaces = List.of(AgentCard.AgentInterface.jsonrpc(publicUrl + "/invoke"));
        return card;
    }
}
