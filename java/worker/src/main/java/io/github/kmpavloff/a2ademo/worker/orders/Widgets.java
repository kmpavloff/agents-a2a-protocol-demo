package io.github.kmpavloff.a2ademo.worker.orders;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Structured widgets built by CODE from store data (never by the LLM), as in
 * internal/orders/widgets.go. Each map carries its kind under "kind"; the A2A
 * layer moves the kind into DataPart metadata.
 */
public final class Widgets {
    public static final String KIND_ORDER = "widget/order";
    public static final String KIND_ORDER_LIST = "widget/order_list";
    public static final String KIND_CONFIRMATION = "widget/confirmation";

    private Widgets() {}

    public static String statusLabel(String status) {
        return switch (status) {
            case "delivered" -> "доставлен";
            case "shipped" -> "отправлен";
            case "processing" -> "в обработке";
            case "refunded" -> "возврат оформлен";
            default -> status;
        };
    }

    public static Map<String, Object> orderWidget(Order o) {
        Map<String, Object> order = new LinkedHashMap<>();
        order.put("id", o.id());
        order.put("item", o.item());
        order.put("status", o.status());
        order.put("status_label", statusLabel(o.status()));
        order.put("amount", o.amount());
        order.put("currency", o.currency());
        order.put("customer", o.customer());
        order.put("created", o.created());
        order.put("refundable", o.refundable());

        Map<String, Object> w = new LinkedHashMap<>();
        w.put("kind", KIND_ORDER);
        w.put("title", "Заказ " + o.id());
        w.put("order", order);
        return w;
    }

    public static Map<String, Object> orderListWidget(String customer, List<Order> list) {
        List<Map<String, Object>> rows = new ArrayList<>(list.size());
        for (Order o : list) {
            Map<String, Object> row = new LinkedHashMap<>();
            row.put("id", o.id());
            row.put("item", o.item());
            row.put("status_label", statusLabel(o.status()));
            row.put("amount", o.amount());
            row.put("currency", o.currency());
            row.put("created", o.created());
            rows.add(row);
        }
        Map<String, Object> w = new LinkedHashMap<>();
        w.put("kind", KIND_ORDER_LIST);
        w.put("title", "Последние заказы: " + customer);
        w.put("customer", customer);
        w.put("orders", rows);
        return w;
    }

    /**
     * Confirmation-dialog widget payload (kind is applied by the A2A layer).
     * Assembled from the captured initiate_refund call; the refund has not run
     * yet, so the amount is unknown and intentionally omitted.
     */
    public static Map<String, Object> refundConfirmWidget(String orderId, String message) {
        Map<String, Object> w = new LinkedHashMap<>();
        w.put("type", "confirmation");
        w.put("title", "Подтверждение возврата");
        w.put("message", message);
        w.put("order_id", orderId);
        w.put("actions", List.of(
                Map.of("id", "approve", "label", "Оформить возврат", "style", "danger"),
                Map.of("id", "decline", "label", "Отмена", "style", "secondary")));
        return w;
    }
}
