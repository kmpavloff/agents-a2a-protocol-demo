package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Разбор входящего события A2UI. Полезная нагрузка приезжает массивом
 * сообщений — так её шлёт фронтенд и так требует спека 0.9.1.
 */
class A2uiActionTest {

    private static Object arrayPayload() {
        return List.of(Map.of(
                "version", "v0.9.1",
                "action", Map.of(
                        "name", "approve_refund",
                        "surfaceId", "confirmation-1-t3",
                        "sourceComponentId", "approve",
                        "timestamp", "2026-09-08T10:00:00Z",
                        "context", Map.of("order_id", "1041"))));
    }

    @Test
    void parsesActionFromArrayPayload() {
        A2ui.Action a = A2ui.parseAction(arrayPayload());
        assertNotNull(a, "действие в массиве должно разбираться");
        assertEquals("approve_refund", a.name());
        assertEquals("1041", a.context().get("order_id"));
    }

    // Одиночный объект принимаем по-прежнему: так шлют клиенты, писавшиеся до
    // 0.9.1, и наш собственный код в тестах.
    @Test
    void parsesActionFromBareObject() {
        A2ui.Action a = A2ui.parseAction(Map.of(
                "version", "v0.9",
                "action", Map.of("name", "decline_refund", "context", Map.of())));
        assertNotNull(a);
        assertEquals("decline_refund", a.name());
    }

    @Test
    void actionOriginReadsSurfaceAndComponent() {
        A2ui.Origin o = A2ui.actionOrigin(arrayPayload());
        assertEquals("confirmation-1-t3", o.surfaceId());
        assertEquals("approve", o.sourceComponentId());
    }

    @Test
    void returnsNullWhenPayloadCarriesNoAction() {
        assertNull(A2ui.parseAction(List.of(Map.of("version", "v0.9.1", "createSurface", Map.of()))));
        assertNull(A2ui.parseAction("не объект"));
        assertNull(A2ui.parseAction(null));
    }

    // Спека опознаёт A2UI-часть по metadata.mimeType; mediaType — второй, менее
    // формальный способ. Принимаем оба и оба типа.
    @Test
    void recognisesBothMarkersAndBothMimeTypes() {
        assertTrue(A2ui.isA2ui(A2ui.MIME_TYPE, null));
        assertTrue(A2ui.isA2ui(A2ui.LEGACY_MIME_TYPE, null));
        assertTrue(A2ui.isA2ui(null, Map.of(A2ui.MIME_KEY, A2ui.MIME_TYPE)));
        assertTrue(A2ui.isA2ui("text/plain", Map.of(A2ui.MIME_KEY, A2ui.LEGACY_MIME_TYPE)));
        assertTrue(!A2ui.isA2ui("text/plain", Map.of("kind", "widget/order")));
    }

    // Все пять полей действия обязательны по схеме client_to_server, а конверт —
    // ровно version + action.
    @Test
    void newActionCarriesAllRequiredFields() {
        Map<String, Object> msg = A2ui.newAction("return_order", "s1", "btn", Map.of("id", "7"), "2026-09-08T10:00:00Z");
        assertEquals(A2ui.VERSION, msg.get("version"));
        assertEquals(Map.of("version", A2ui.VERSION, "action", msg.get("action")), msg);
        @SuppressWarnings("unchecked")
        Map<String, Object> action = (Map<String, Object>) msg.get("action");
        assertEquals(
                java.util.Set.of("name", "surfaceId", "sourceComponentId", "timestamp", "context"),
                action.keySet());
        assertEquals("s1", action.get("surfaceId"));
    }

    @Test
    void clientCapabilitiesAnnounceTheBasicCatalogUnderTheV09Key() {
        Map<String, Object> caps = A2ui.clientCapabilities();
        assertEquals(java.util.Set.of(A2ui.CAPABILITIES_KEY), caps.keySet());
        assertEquals(Map.of("supportedCatalogIds", List.of(A2ui.CATALOG_ID)), caps.get(A2ui.CAPABILITIES_KEY));
    }
}
