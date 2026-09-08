package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Сборка A2UI-части A2A-сообщения. Правило проекта: принимаем либерально,
 * отдаём строго по спеке — metadata.mimeType и массив сообщений.
 */
class A2uiPartsTest {

    @Test
    void marksThePartBothWaysAndWrapsMessagesInAnArray() throws Exception {
        Part p = A2uiParts.message(List.of(Map.of("version", A2ui.VERSION, "createSurface", Map.of("surfaceId", "s1"))));

        assertEquals(A2ui.MIME_TYPE, p.mediaType, "родное поле A2A — для нашего кода и живых агентов");
        assertEquals(A2ui.MIME_TYPE, p.metadata.get(A2ui.MIME_KEY), "по этой пометке часть опознаёт стандартный клиент");

        String json = Json.MAPPER.writeValueAsString(p);
        assertTrue(json.contains("\"data\":[{"), "data обязана быть массивом сообщений: " + json);
    }

    @Test
    void actionPartCarriesTheClientToServerEnvelope() {
        Part p = A2uiParts.action("approve_refund", "s1", "approve",
                Map.of("order_id", "1041"), Instant.parse("2026-09-08T10:00:00Z"));

        A2ui.Action a = A2ui.parseAction(p.data);
        assertEquals("approve_refund", a.name());
        assertEquals("1041", a.context().get("order_id"));
        assertEquals(new A2ui.Origin("s1", "approve"), A2ui.actionOrigin(p.data));

        @SuppressWarnings("unchecked")
        Map<String, Object> action = (Map<String, Object>) ((List<Map<String, Object>>) p.data).getFirst().get("action");
        assertEquals("2026-09-08T10:00:00Z", action.get("timestamp"), "RFC3339 в UTC, как в Go");
    }

    @Test
    void recognisesItsOwnPartsAndIgnoresWidgetParts() {
        assertTrue(A2uiParts.isA2ui(A2uiParts.message(List.of(Map.of("version", A2ui.VERSION)))));
        Part widget = Part.data(Map.of("id", "1041"), Map.of("kind", "widget/order"));
        assertTrue(!A2uiParts.isA2ui(widget));
        assertTrue(!A2uiParts.isA2ui(null));
    }
}
