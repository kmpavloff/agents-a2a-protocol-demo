package io.github.kmpavloff.a2ademo.worker.orders;

import io.github.kmpavloff.a2ademo.common.llm.ToolCall;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

class OrderToolsTest {

    static Path seed;

    @BeforeAll
    static void writeSeed() throws IOException {
        seed = Files.createTempFile("orders", ".json");
        Files.writeString(seed, """
                {
                  "orders": [
                    { "id": "1023", "customer": "alice", "item": "Механическая клавиатура", "amount": 89.90, "currency": "EUR", "status": "delivered", "created": "2026-06-01", "refundable": true },
                    { "id": "1041", "customer": "alice", "item": "USB-C хаб", "amount": 34.50, "currency": "EUR", "status": "delivered", "created": "2026-06-10", "refundable": true },
                    { "id": "1055", "customer": "alice", "item": "Подставка для ноутбука", "amount": 45.00, "currency": "EUR", "status": "shipped", "created": "2026-06-18", "refundable": false }
                  ],
                  "sales_stats": [
                    { "period": "2026-06", "orders": 198, "revenue": 9120.10, "currency": "EUR" }
                  ]
                }
                """);
    }

    private OrderTools tools() {
        return new OrderTools(OrderStore.load(seed.toString()), "https://shop.test/orders");
    }

    @Test
    @SuppressWarnings("unchecked")
    void getOrderStatusFormatsLikeGo() {
        OrderTools.ToolResult r = tools().getOrderStatus("1041");
        assertEquals("Заказ 1041 (USB-C хаб): статус — доставлен. Сумма: 34.50 EUR.", r.text());
        assertNotNull(r.widget());
        assertEquals("widget/order", r.widget().get("kind"));
        var order = (java.util.Map<String, Object>) r.widget().get("order");
        assertEquals("https://shop.test/orders/1041", order.get("url"));
    }

    @Test
    void getOrderStatusMissingIdHints() {
        OrderTools.ToolResult r = tools().getOrderStatus("");
        assertTrue(r.text().startsWith("Не указан номер заказа."));
        assertNull(r.widget());
    }

    @Test
    void getOrderStatusUnknownOrder() {
        assertEquals("Заказ 9999 не найден.", tools().getOrderStatus("9999").text());
    }

    @Test
    @SuppressWarnings("unchecked")
    void listRecentOrdersSortsNewestFirstAndBuildsWidget() {
        OrderTools.ToolResult r = tools().listRecentOrders("alice");
        int i1055 = r.text().indexOf("#1055");
        int i1041 = r.text().indexOf("#1041");
        int i1023 = r.text().indexOf("#1023");
        assertTrue(i1055 >= 0 && i1055 < i1041 && i1041 < i1023, "newest first: " + r.text());
        assertEquals("widget/order_list", r.widget().get("kind"));
        var rows = (java.util.List<java.util.Map<String, Object>>) r.widget().get("orders");
        assertEquals("https://shop.test/orders/1055", rows.getFirst().get("url"));
    }

    @Test
    void orderUrlBuildsFromBase() {
        assertEquals("https://shop.test/orders/1041", Widgets.orderUrl("https://shop.test/orders", "1041"));
        assertEquals("https://shop.test/orders/1041", Widgets.orderUrl("https://shop.test/orders/", "1041"));
        assertEquals("", Widgets.orderUrl("", "1041"), "empty base must disable links");
    }

    @Test
    void salesStats() {
        assertEquals("Продажи за 2026-06: 198 заказов, выручка 9120.10 EUR.", tools().getSalesStats("2026-06"));
        assertEquals("Нет статистики продаж за период \"2026-01\".", tools().getSalesStats("2026-01"));
    }

    @Test
    void refundFlowsMirrorGoErrors() {
        OrderTools t = tools();
        OrderTools.ToolResult ok = t.initiateRefund("1041");
        assertEquals("Возврат по заказу 1041 оформлен (34.50 EUR).", ok.text());
        assertNotNull(ok.widget(), "successful refund must ship the receipt widget");
        assertEquals("widget/refund_receipt", ok.widget().get("kind"));
        assertEquals("1041", ok.widget().get("order_id"));
        assertEquals("Невозможно оформить возврат: заказ 1055 не подлежит возврату.", t.initiateRefund("1055").text());
        assertNull(t.initiateRefund("1055").widget(), "failed refund must not ship a receipt");
        assertEquals("Невозможно оформить возврат: заказ 9999 не найден.", t.initiateRefund("9999").text());
    }

    @Test
    void findOrderByIdAndBySubstring() {
        OrderTools t = tools();
        assertTrue(t.findOrder("#1041").contains("Заказ 1041"));
        assertTrue(t.findOrder("клавиатура").contains("#1023"));
        assertEquals("По запросу \"тостер\" ничего не найдено.", t.findOrder("тостер"));
    }

    @Test
    void toolCallSynonymKeysAccepted() {
        ToolCall call = new ToolCall("c1", "get_order_status", "{\"order_number\":\"1041\"}");
        assertTrue(tools().execute(call).text().contains("Заказ 1041"));
    }
}
