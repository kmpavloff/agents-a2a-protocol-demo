package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;

/** Port of the Go internal/a2ui tests. */
class A2uiTest {

    @Test
    @SuppressWarnings("unchecked")
    void fromWidgetConfirmation() {
        List<Map<String, Object>> msgs = A2ui.fromWidget(Map.of(
                "_kind", "widget/confirmation",
                "title", "Подтверждение возврата",
                "message", "Оформить возврат по заказу 1055? (да/нет)",
                "order_id", "1055",
                "actions", List.of(
                        Map.of("id", "approve", "label", "Оформить возврат"),
                        Map.of("id", "decline", "label", "Отмена"))));
        assertNotNull(msgs, "confirmation widget should map");
        assertEquals(2, msgs.size(), "want createSurface + updateComponents");
        assertEquals("v0.9", msgs.get(0).get("version"));
        assertNotNull(msgs.get(0).get("createSurface"));

        Map<String, Object> uc = (Map<String, Object>) msgs.get(1).get("updateComponents");
        assertNotNull(uc);
        List<Map<String, Object>> comps = (List<Map<String, Object>>) uc.get("components");
        int buttons = 0;
        int actions = 0;
        String msgText = "";
        for (Map<String, Object> c : comps) {
            if ("Button".equals(c.get("component"))) {
                buttons++;
                if (c.get("action") instanceof Map<?, ?> a && a.get("event") instanceof Map<?, ?>) {
                    actions++;
                }
            }
            if ("msg".equals(c.get("id"))) {
                msgText = String.valueOf(c.get("text"));
            }
        }
        assertEquals(2, buttons);
        assertEquals(2, actions);
        assertEquals("Оформить возврат по заказу 1055?", msgText, "(да/нет) hint must be dropped");
    }

    @Test
    void fromWidgetUnknownKind() {
        assertNull(A2ui.fromWidget(Map.of("_kind", "widget/nope")));
    }

    @Test
    void parseAction() {
        A2ui.Action a = A2ui.parseAction(Map.of(
                "version", "v0.9",
                "action", Map.of("name", "approve_refund", "context", Map.of("order_id", "1055"))));
        assertNotNull(a);
        assertEquals("approve_refund", a.name());
        assertEquals("1055", a.context().get("order_id"));
    }

    @Test
    void parseActionRejectsNonAction() {
        assertNull(A2ui.parseAction(Map.of("useStreaming", false)));
        assertNull(A2ui.parseAction(null));
        assertNull(A2ui.parseAction("not a map"));
    }
}
