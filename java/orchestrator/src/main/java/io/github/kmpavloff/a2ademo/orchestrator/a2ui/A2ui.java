package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Maps the demo's domain widgets to Google's A2UI v0.9 generative-UI JSON and
 * parses A2UI action events (port of internal/a2ui). This is the only class
 * that knows the A2UI wire format; the transport layer stays A2UI-agnostic.
 */
public final class A2ui {
    public static final String EXTENSION_URI = "https://a2ui.org/a2a-extension/a2ui/v0.9";
    public static final String MIME_TYPE = "application/a2ui+json";
    public static final String VERSION = "v0.9";
    public static final String CATALOG_ID = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json";

    /** Makes surface ids unique within the process (parity with the Go counter). */
    private static final AtomicInteger surfaceCounter = new AtomicInteger();

    private A2ui() {}

    private static String nextSurfaceId(String kind) {
        return kind + "-" + surfaceCounter.incrementAndGet();
    }

    /** A parsed incoming A2UI action event: {@code {name, context}}. */
    public record Action(String name, Map<String, Object> context) {}

    /**
     * Extracts an A2UI action event from a DataPart's data map. Shape:
     * {@code {"version":"v0.9","action":{"name":"...","context":{...}}}}.
     * Returns null when the map is not an action payload.
     */
    @SuppressWarnings("unchecked")
    public static Action parseAction(Object data) {
        if (!(data instanceof Map<?, ?> m)) {
            return null;
        }
        Object actionObj = m.get("action");
        if (!(actionObj instanceof Map<?, ?> action)) {
            return null;
        }
        Object name = action.get("name");
        if (!(name instanceof String s) || s.isEmpty()) {
            return null;
        }
        Map<String, Object> ctx = action.get("context") instanceof Map<?, ?> c
                ? new LinkedHashMap<>((Map<String, Object>) c)
                : new LinkedHashMap<>();
        return new Action(s, ctx);
    }

    /**
     * Converts a widget map (keyed by "_kind" plus payload) into the ordered
     * A2UI messages to emit, or null for an unknown kind.
     */
    @SuppressWarnings("unchecked")
    public static List<Map<String, Object>> fromWidget(Map<String, Object> w) {
        String kind = w.get("_kind") instanceof String s ? s : "";
        String title = w.get("title") instanceof String s ? s : "";
        switch (kind) {
            case "widget/confirmation": {
                String sid = nextSurfaceId("confirmation");
                String msg = w.get("message") instanceof String s ? s.trim() : "";
                // Drop the "(да/нет)" hint: redundant in the widget, where the
                // buttons already offer the choice. The text-fallback keeps it.
                if (msg.endsWith("(да/нет)")) {
                    msg = msg.substring(0, msg.length() - "(да/нет)".length()).trim();
                }
                String orderId = w.get("order_id") instanceof String s ? s : "";
                Map<String, Object> ctx = Map.of("order_id", orderId);
                List<Map<String, Object>> comps = new ArrayList<>();
                comps.add(component("root", "Column", "children", List.of("title", "msg", "actions")));
                comps.add(text("title", title, "h3"));
                comps.add(text("msg", msg, "body"));
                comps.add(component("actions", "Row", "children", List.of("approve", "decline")));
                comps.addAll(button("approve", "approve_lbl", "Оформить возврат", "primary", "approve_refund", ctx));
                comps.addAll(button("decline", "decline_lbl", "Отмена", "default", "decline_refund", ctx));
                return surface(sid, comps);
            }
            case "widget/refund_form": {
                String sid = nextSurfaceId("refund_form");
                String msg = w.get("message") instanceof String s ? s : "";
                String orderId = w.get("order_id") instanceof String s ? s : "";
                boolean isError = "error".equals(w.get("severity"));
                // The card number is typed into a TextField bound to the surface
                // data model; the submit button's context resolves {path} bindings
                // at click time, so the value reaches the agent in the action event.
                Map<String, Object> ctxSubmit = new LinkedHashMap<>();
                ctxSubmit.put("order_id", orderId);
                ctxSubmit.put("card_number", Map.of("path", "/card_number"));
                Map<String, Object> ctxDecline = Map.of("order_id", orderId);

                List<Map<String, Object>> comps = new ArrayList<>();
                comps.add(component("root", "Column", "children", List.of("title", "msg", "card", "actions")));
                comps.add(text("title", title, "h3"));
                comps.add(text("msg", msg, isError ? "h3" : "body"));
                Map<String, Object> card = new LinkedHashMap<>();
                card.put("id", "card");
                card.put("component", "TextField");
                card.put("label", "Номер карты");
                card.put("value", Map.of("path", "/card_number"));
                card.put("variant", "shortText");
                comps.add(card);
                comps.add(component("actions", "Row", "children", List.of("submit", "decline")));
                comps.addAll(button("submit", "submit_lbl", "Вернуть на карту", "primary", "submit_refund_details", ctxSubmit));
                comps.addAll(button("decline", "decline_lbl", "Отмена", "default", "decline_refund", ctxDecline));
                return surface(sid, comps);
            }
            case "widget/refund_receipt": {
                String sid = nextSurfaceId("refund_receipt");
                List<Object> children = new ArrayList<>(List.of("title"));
                List<Map<String, Object>> comps = new ArrayList<>();
                comps.add(component("root", "Column", "children", children));
                comps.add(text("title", title, "h3"));
                java.util.function.BiConsumer<String, String> add = (id, line) -> {
                    children.add(id);
                    comps.add(text(id, line, "body"));
                };
                if (w.get("receipt_id") != null) {
                    add.accept("rid", "Квитанция №: " + w.get("receipt_id"));
                }
                add.accept("order", "Заказ: #" + w.get("order_id") + " — " + w.get("item"));
                add.accept("amount", "Сумма возврата: " + fmt(w.get("amount")) + " " + w.get("currency"));
                if (w.get("card_last4") instanceof String last4 && !last4.isEmpty()) {
                    add.accept("card", "Карта получателя: •••• " + last4);
                }
                if (w.get("created") != null) {
                    add.accept("created", "Дата: " + w.get("created"));
                }
                add.accept("note", "Файл квитанции можно скачать ниже.");
                return surface(sid, comps);
            }
            case "widget/order": {
                String sid = nextSurfaceId("order");
                Map<String, Object> o = w.get("order") instanceof Map<?, ?> m
                        ? (Map<String, Object>) m
                        : Map.of();
                List<Object> children = new ArrayList<>(List.of("title"));
                List<Map<String, Object>> comps = new ArrayList<>();
                Map<String, Object> root = component("root", "Column", "children", children);
                comps.add(root);
                comps.add(text("title", title, "h3"));
                addField(comps, children, o, "item", "Товар:", "item");
                addField(comps, children, o, "status", "Статус:", "status_label");
                Object amt = o.get("amount");
                if (amt != null) {
                    children.add("amount");
                    comps.add(text("amount", "Сумма: " + fmt(amt) + " " + o.get("currency"), "body"));
                }
                addField(comps, children, o, "customer", "Клиент:", "customer");
                addField(comps, children, o, "created", "Дата:", "created");
                // Text renders markdown in the browser, so the order-card link
                // is a plain markdown link (URL built by code, not the LLM).
                if (o.get("url") instanceof String url && !url.isEmpty()) {
                    children.add("link");
                    comps.add(text("link", "[Открыть карточку заказа →](" + url + ")", "body"));
                }
                return surface(sid, comps);
            }
            case "widget/order_list": {
                String sid = nextSurfaceId("order_list");
                List<Object> rows = w.get("orders") instanceof List<?> l ? (List<Object>) l : List.of();
                List<Object> children = new ArrayList<>(List.of("title"));
                List<Map<String, Object>> comps = new ArrayList<>();
                comps.add(component("root", "Column", "children", children));
                comps.add(text("title", title, "h3"));
                int i = 0;
                for (Object r : rows) {
                    if (!(r instanceof Map<?, ?> o)) {
                        continue;
                    }
                    String id = "row" + i++;
                    children.add(id);
                    // The order number becomes a markdown link when the widget
                    // carries a per-row url, so each row opens its order card.
                    String num = "#" + o.get("id");
                    if (o.get("url") instanceof String url && !url.isEmpty()) {
                        num = "[" + num + "](" + url + ")";
                    }
                    String line = num + "  " + o.get("item") + " — " + o.get("status_label")
                            + " (" + fmt(o.get("amount")) + " " + o.get("currency") + ", " + o.get("created") + ")";
                    comps.add(text(id, line, "body"));
                }
                return surface(sid, comps);
            }
            default:
                return null;
        }
    }

    private static void addField(List<Map<String, Object>> comps, List<Object> children,
                                 Map<String, Object> o, String id, String label, String key) {
        Object v = o.get(key);
        if (v != null && !"".equals(v)) {
            children.add(id);
            comps.add(text(id, label + " " + v, "body"));
        }
    }

    private static String fmt(Object v) {
        return String.valueOf(v);
    }

    private static Map<String, Object> component(String id, String component, String key, Object value) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", id);
        m.put("component", component);
        m.put(key, value);
        return m;
    }

    /** Builds a Text component. */
    private static Map<String, Object> text(String id, String s, String variant) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", id);
        m.put("component", "Text");
        m.put("text", s);
        m.put("variant", variant);
        return m;
    }

    /**
     * Builds a Button (child = Text label) whose click emits an A2UI action
     * event {name, context}. The label is copied into a per-button context so a
     * client can echo a human-readable action instead of the raw action name.
     */
    private static List<Map<String, Object>> button(String id, String labelId, String label,
                                                    String variant, String actionName, Map<String, Object> ctx) {
        Map<String, Object> bctx = new LinkedHashMap<>();
        bctx.put("label", label);
        bctx.putAll(ctx);
        Map<String, Object> btn = new LinkedHashMap<>();
        btn.put("id", id);
        btn.put("component", "Button");
        btn.put("child", labelId);
        btn.put("variant", variant);
        btn.put("action", Map.of("event", Map.of("name", actionName, "context", bctx)));
        return List.of(btn, text(labelId, label, "body"));
    }

    /** Wraps components into the standard createSurface + updateComponents pair. */
    private static List<Map<String, Object>> surface(String surfaceId, List<Map<String, Object>> components) {
        Map<String, Object> create = new LinkedHashMap<>();
        create.put("version", VERSION);
        create.put("createSurface", Map.of("surfaceId", surfaceId, "catalogId", CATALOG_ID));
        Map<String, Object> update = new LinkedHashMap<>();
        update.put("version", VERSION);
        update.put("updateComponents", Map.of("surfaceId", surfaceId, "components", components));
        return List.of(create, update);
    }
}
