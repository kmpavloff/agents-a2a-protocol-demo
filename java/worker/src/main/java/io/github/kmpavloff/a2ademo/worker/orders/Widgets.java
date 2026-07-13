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
    public static final String KIND_REFUND_FORM = "widget/refund_form";
    public static final String KIND_REFUND_RECEIPT = "widget/refund_receipt";

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

    /**
     * Builds the customer-facing order-card link from the configured base URL,
     * or "" when links are disabled (empty base). Built by CODE so the LLM can
     * never invent or mangle a link.
     */
    public static String orderUrl(String base, String id) {
        if (base == null || base.isEmpty()) {
            return "";
        }
        return (base.endsWith("/") ? base.substring(0, base.length() - 1) : base) + "/" + id;
    }

    /** Single-order display widget; url is the order-card link ("" omits it). */
    public static Map<String, Object> orderWidget(Order o, String url) {
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
        if (url != null && !url.isEmpty()) {
            order.put("url", url);
        }

        Map<String, Object> w = new LinkedHashMap<>();
        w.put("kind", KIND_ORDER);
        w.put("title", "Заказ " + o.id());
        w.put("order", order);
        return w;
    }

    /** Order-list widget; linkBase is the order-card base URL ("" omits row links). */
    public static Map<String, Object> orderListWidget(String customer, List<Order> list, String linkBase) {
        List<Map<String, Object>> rows = new ArrayList<>(list.size());
        for (Order o : list) {
            Map<String, Object> row = new LinkedHashMap<>();
            row.put("id", o.id());
            row.put("item", o.item());
            row.put("status_label", statusLabel(o.status()));
            row.put("amount", o.amount());
            row.put("currency", o.currency());
            row.put("created", o.created());
            String url = orderUrl(linkBase, o.id());
            if (!url.isEmpty()) {
                row.put("url", url);
            }
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
     * Refund-receipt widget from a just-refunded order. The payment context
     * (masked card, receipt id, timestamp) is appended later by the agent
     * layer, which owns the card-collection step.
     */
    public static Map<String, Object> refundReceiptWidget(Order o) {
        Map<String, Object> w = new LinkedHashMap<>();
        w.put("kind", KIND_REFUND_RECEIPT);
        w.put("title", "Квитанция о возврате");
        w.put("order_id", o.id());
        w.put("item", o.item());
        w.put("amount", o.amount());
        w.put("currency", o.currency());
        return w;
    }

    /**
     * Card-details form widget payload shown after the refund confirmation
     * (kind is applied by the A2A layer). isError marks a validation re-ask.
     */
    public static Map<String, Object> refundFormWidget(String orderId, String message, boolean isError) {
        Map<String, Object> w = new LinkedHashMap<>();
        w.put("type", "form");
        w.put("title", "Реквизиты для возврата");
        w.put("message", message);
        w.put("severity", isError ? "error" : "info");
        w.put("order_id", orderId);
        w.put("fields", List.of(Map.of(
                "id", "card_number", "label", "Номер карты", "placeholder", "0000 0000 0000 0000")));
        w.put("actions", List.of(
                Map.of("id", "submit_refund_details", "label", "Вернуть на карту", "style", "primary"),
                Map.of("id", "decline", "label", "Отмена", "style", "secondary")));
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
