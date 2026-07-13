package io.github.kmpavloff.a2ademo.worker.orders;

import io.github.kmpavloff.a2ademo.common.llm.ToolCall;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * The five order tools (port of internal/orders/tools.go): same names, same
 * argument synonyms tolerated for small models, same Russian result strings.
 */
public class OrderTools {
    /** Synonym keys under which models emit the order id / customer name. */
    public static final String[] ORDER_ID_KEYS = {"order_id", "order_number", "number", "id"};
    private static final String[] CUSTOMER_KEYS = {"customer", "customer_name", "name", "client"};

    private final OrderStore store;
    private final String orderLinkBase;

    /** orderLinkBase is the order-card base URL for widget links ("" disables links). */
    public OrderTools(OrderStore store, String orderLinkBase) {
        this.store = store;
        this.orderLinkBase = orderLinkBase == null ? "" : orderLinkBase;
    }

    /** A tool's text result plus an optional structured widget for the UI. */
    public record ToolResult(String text, Map<String, Object> widget) {
        public static ToolResult text(String text) {
            return new ToolResult(text, null);
        }
    }

    public List<ToolSpec> specs() {
        return List.of(
                new ToolSpec("find_order", "Найти заказ по номеру или тексту названия товара.",
                        schema(Map.of("query", prop("string", "Произвольный текст для поиска заказа")), List.of("query"))),
                new ToolSpec("get_order_status", "Узнать статус заказа по его номеру.",
                        idSchema()),
                new ToolSpec("list_recent_orders", "Показать последние заказы клиента по его имени (например, alice), новые сверху.",
                        customerSchema()),
                new ToolSpec("get_sales_stats", "Получить статистику продаж за период (ГГГГ-ММ).",
                        schema(Map.of("period", prop("string", "Период в формате ГГГГ-ММ")), List.of("period"))),
                new ToolSpec("initiate_refund", "Оформить возврат по заказу (по его номеру).",
                        idSchema()));
    }

    public ToolResult execute(ToolCall call) {
        return switch (call.name()) {
            case "find_order" -> ToolResult.text(findOrder(call.firstArg("query")));
            case "get_order_status" -> getOrderStatus(call.firstArg(ORDER_ID_KEYS));
            case "list_recent_orders" -> listRecentOrders(call.firstArg(CUSTOMER_KEYS));
            case "get_sales_stats" -> ToolResult.text(getSalesStats(call.firstArg("period")));
            case "initiate_refund" -> initiateRefund(call.firstArg(ORDER_ID_KEYS));
            default -> ToolResult.text("Неизвестный инструмент: " + call.name());
        };
    }

    ToolResult getOrderStatus(String id) {
        if (id.isEmpty()) {
            return ToolResult.text("Не указан номер заказа. Передайте order_id (например, 1041) и вызовите инструмент снова.");
        }
        return store.get(id)
                .map(o -> new ToolResult(
                        String.format(Locale.ROOT, "Заказ %s (%s): статус — %s. Сумма: %.2f %s.",
                                o.id(), o.item(), Widgets.statusLabel(o.status()), o.amount(), o.currency()),
                        Widgets.orderWidget(o, Widgets.orderUrl(orderLinkBase, o.id()))))
                .orElseGet(() -> ToolResult.text(String.format("Заказ %s не найден.", id)));
    }

    ToolResult listRecentOrders(String customer) {
        if (customer.isEmpty()) {
            return ToolResult.text("Не указано имя клиента. Передайте customer (например, alice) и вызовите инструмент снова.");
        }
        List<Order> list = store.byCustomer(customer);
        if (list.isEmpty()) {
            return ToolResult.text(String.format("Заказы для клиента \"%s\" не найдены.", customer));
        }
        StringBuilder b = new StringBuilder();
        b.append(String.format("Последние заказы клиента %s:%n", customer));
        for (Order o : list) {
            b.append(String.format(Locale.ROOT, "- #%s %s (%s, %.2f %s, %s)%n",
                    o.id(), o.item(), Widgets.statusLabel(o.status()), o.amount(), o.currency(), o.created()));
        }
        return new ToolResult(b.toString(), Widgets.orderListWidget(customer, list, orderLinkBase));
    }

    String getSalesStats(String period) {
        return store.stats(period)
                .map(st -> String.format(Locale.ROOT, "Продажи за %s: %d заказов, выручка %.2f %s.",
                        st.period(), st.orders(), st.revenue(), st.currency()))
                .orElse(String.format("Нет статистики продаж за период \"%s\".", period));
    }

    ToolResult initiateRefund(String id) {
        if (id.isEmpty()) {
            return ToolResult.text("Не указан номер заказа. Передайте order_id (например, 1041) и вызовите инструмент снова.");
        }
        try {
            Order o = store.refund(id);
            // A successful refund ships the receipt widget for the agent
            // layer to enrich with the payment context.
            return new ToolResult(
                    String.format(Locale.ROOT, "Возврат по заказу %s оформлен (%.2f %s).", o.id(), o.amount(), o.currency()),
                    Widgets.refundReceiptWidget(o));
        } catch (OrderStore.NotFoundException e) {
            return ToolResult.text(String.format("Невозможно оформить возврат: заказ %s не найден.", id));
        } catch (OrderStore.NotRefundableException e) {
            return ToolResult.text(String.format("Невозможно оформить возврат: заказ %s не подлежит возврату.", id));
        }
    }

    String findOrder(String query) {
        String q = query.startsWith("#") ? query.substring(1).trim() : query.trim();
        if (store.get(q).isPresent()) {
            return getOrderStatus(q).text();
        }
        StringBuilder b = new StringBuilder();
        int hits = 0;
        for (Order o : store.allOrders()) {
            if (o.item().toLowerCase().contains(query.toLowerCase())) {
                if (hits == 0) {
                    b.append(String.format("Совпадения по запросу \"%s\":%n", query));
                }
                b.append(String.format("- #%s %s (%s)%n", o.id(), o.item(), Widgets.statusLabel(o.status())));
                hits++;
            }
        }
        if (hits == 0) {
            return String.format("По запросу \"%s\" ничего не найдено.", query);
        }
        return b.toString();
    }

    // JSON Schema helpers — id/customer synonyms all optional, missing values
    // reported at runtime with a helpful hint (mirrors the Go idArgs/customerArgs).

    private static Map<String, Object> idSchema() {
        Map<String, Object> props = new LinkedHashMap<>();
        props.put("order_id", prop("string", "Номер заказа, например 1041"));
        props.put("order_number", prop("string", "Синоним order_id (номер заказа)"));
        props.put("number", prop("string", "Синоним order_id (номер заказа)"));
        props.put("id", prop("string", "Синоним order_id (номер заказа)"));
        return schema(props, List.of());
    }

    private static Map<String, Object> customerSchema() {
        Map<String, Object> props = new LinkedHashMap<>();
        props.put("customer", prop("string", "Имя клиента, например alice"));
        props.put("customer_name", prop("string", "Синоним customer (имя клиента)"));
        props.put("name", prop("string", "Синоним customer (имя клиента)"));
        props.put("client", prop("string", "Синоним customer (имя клиента)"));
        return schema(props, List.of());
    }

    private static Map<String, Object> prop(String type, String description) {
        return Map.of("type", type, "description", description);
    }

    private static Map<String, Object> schema(Map<String, Object> properties, List<String> required) {
        Map<String, Object> s = new LinkedHashMap<>();
        s.put("type", "object");
        s.put("properties", properties);
        if (!required.isEmpty()) {
            s.put("required", required);
        }
        return s;
    }
}
