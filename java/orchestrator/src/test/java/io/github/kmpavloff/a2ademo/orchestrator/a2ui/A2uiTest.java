package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

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

    @SuppressWarnings("unchecked")
    private static List<String> texts(List<Map<String, Object>> msgs) {
        Map<String, Object> uc = (Map<String, Object>) msgs.get(1).get("updateComponents");
        return ((List<Map<String, Object>>) uc.get("components")).stream()
                .filter(c -> "Text".equals(c.get("component")))
                .map(c -> String.valueOf(c.get("text")))
                .toList();
    }

    @Test
    void orderWidgetWithUrlGetsMarkdownLink() {
        List<Map<String, Object>> msgs = A2ui.fromWidget(Map.of(
                "_kind", "widget/order",
                "title", "Заказ 1041",
                "order", Map.of("id", "1041", "item", "USB-C хаб", "status_label", "доставлен",
                        "amount", 34.5, "currency", "EUR", "url", "https://shop.test/orders/1041")));
        assertTrue(texts(msgs).contains("[Открыть карточку заказа →](https://shop.test/orders/1041)"),
                "components: " + texts(msgs));
    }

    @Test
    void orderListRowLinksTheNumber() {
        List<Map<String, Object>> msgs = A2ui.fromWidget(Map.of(
                "_kind", "widget/order_list",
                "title", "Последние заказы: alice",
                "orders", List.of(Map.of("id", "1041", "item", "USB-C хаб", "status_label", "доставлен",
                        "amount", 34.5, "currency", "EUR", "created", "2026-06-10",
                        "url", "https://shop.test/orders/1041"))));
        assertTrue(texts(msgs).contains(
                        "[#1041](https://shop.test/orders/1041)  USB-C хаб — доставлен (34.5 EUR, 2026-06-10)"),
                "components: " + texts(msgs));
    }

    @Test
    void orderWidgetWithoutUrlHasNoLink() {
        List<Map<String, Object>> msgs = A2ui.fromWidget(Map.of(
                "_kind", "widget/order",
                "title", "Заказ 1041",
                "order", Map.of("id", "1041", "item", "USB-C хаб")));
        assertTrue(texts(msgs).stream().noneMatch(s -> s.startsWith("[")),
                "no link expected: " + texts(msgs));
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
