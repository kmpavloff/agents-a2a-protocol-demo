package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Согласование расширения A2UI. Обе ревизии объявляются и обе принимаются:
 * полезная нагрузка у них общая, а клиент, знающий только 0.9, по одному лишь
 * URI 0.9.1 нас за A2UI-агента не примет. Фронтенд этого проекта как раз шлёт
 * предыдущую ревизию — см. комментарий в web/src/client.ts.
 */
class ExtensionNegotiationTest {

    @Test
    @SuppressWarnings("unchecked")
    void cardAdvertisesBothRevisions() {
        AgentCard card = OrchestratorCards.agentCard("http://localhost:8080");
        List<Map<String, Object>> exts = (List<Map<String, Object>>) card.capabilities.get("extensions");
        List<Object> uris = exts.stream().map(e -> e.get("uri")).toList();
        assertTrue(uris.contains(A2ui.EXTENSION_URI), "новая ревизия: " + uris);
        assertTrue(uris.contains(A2ui.LEGACY_EXTENSION_URI), "предыдущая ревизия: " + uris);
    }

    @Test
    void activatesOnEitherUriAndEchoesTheRequestedOne() {
        assertEquals(A2ui.EXTENSION_URI, A2aWebController.negotiate(List.of(A2ui.EXTENSION_URI)));
        assertEquals(A2ui.LEGACY_EXTENSION_URI, A2aWebController.negotiate(List.of(A2ui.LEGACY_EXTENSION_URI)));
    }

    // Spring разбивает значение заголовка по запятым, поэтому в списке может
    // приехать несколько URI сразу.
    @Test
    void activatesWhenTheHeaderCarriesSeveralUris() {
        assertEquals(A2ui.EXTENSION_URI,
                A2aWebController.negotiate(List.of("https://example.org/other", A2ui.EXTENSION_URI)));
    }

    @Test
    void staysInactiveWithoutTheHeader() {
        assertEquals("", A2aWebController.negotiate(null));
        assertEquals("", A2aWebController.negotiate(List.of("https://example.org/other")));
    }
}
