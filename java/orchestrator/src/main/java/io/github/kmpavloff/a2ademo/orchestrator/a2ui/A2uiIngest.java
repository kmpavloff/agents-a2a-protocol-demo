package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.Part;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Достаёт A2UI-сообщения из частей ответа удалённого агента и приводит их к
 * форме, которую понимает наш рендерер: плоские компоненты basic-каталога v0.9
 * (порт internal/a2ui/ingest.go).
 *
 * <p>Толерантен по входу, потому что живой агент отдаёт не то, что обещает его
 * контракт: A2UI приезжает и как DataPart с массивом сообщений, и как одиночный
 * объект, и как ТЕКСТОВАЯ часть с mediaType application/a2ui+json, внутри
 * которой JSON-строка. Компоненты приезжают и плоскими, и обёрнутыми
 * ({@code {"Text": {...}}}), и типов, которых нет в объявленном самим агентом
 * каталоге.
 *
 * <p>Инвариант: {@code ingest} никогда не бросает исключение. Худший исход —
 * блёклый, но валидный набор компонентов: чужой агент не должен ронять наш UI.
 */
public final class A2uiIngest {

    private A2uiIngest() {}

    /** Сообщения A2UI, которые понимает наш рендерер. Остальные отбрасываются. */
    private static final List<String> KNOWN_MESSAGES =
            List.of("createSurface", "updateComponents", "updateDataModel", "deleteSurface");

    /**
     * 18 компонентов basic-каталога v0.9. Ingest обязан выдавать только их:
     * объявленный агентами каталог именно этот, и наш рендерер отвергает всё,
     * чего в нём нет.
     */
    private static final Set<String> BASIC_COMPONENTS = Set.of(
            "Text", "Image", "Icon", "Video", "AudioPlayer",
            "Row", "Column", "List", "Card", "Tabs",
            "Modal", "Divider", "Button", "TextField",
            "CheckBox", "ChoicePicker", "Slider", "DateTimeInput");

    /** Состояние одного вызова: счётчик id и поверхности с назначенным корнем. */
    private static final class State {
        int gen;
        final Set<String> rooted = new HashSet<>();
    }

    public static List<Map<String, Object>> ingest(List<Part> parts) {
        List<Map<String, Object>> out = new ArrayList<>();
        if (parts == null) {
            return out;
        }
        State st = new State();
        for (Part p : parts) {
            if (!A2uiParts.isA2ui(p)) {
                continue;
            }
            for (Map<String, Object> raw : messagesFrom(p)) {
                Map<String, Object> m = normalizeMessage(raw, st);
                if (m != null) {
                    out.add(m);
                }
            }
        }
        return out;
    }

    /** Разбирает нагрузку части в список сырых сообщений, принимая все три раскладки. */
    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> messagesFrom(Part p) {
        Object payload = p.data;
        if (payload == null) {
            String text = p.textOrEmpty().trim();
            if (text.isEmpty()) {
                return List.of();
            }
            try {
                payload = Json.MAPPER.readValue(text, Object.class);
            } catch (Exception e) {
                return List.of();
            }
        }
        if (payload instanceof List<?> list) {
            List<Map<String, Object>> msgs = new ArrayList<>(list.size());
            for (Object item : list) {
                if (item instanceof Map<?, ?> m) {
                    msgs.add((Map<String, Object>) m);
                }
            }
            return msgs;
        }
        if (payload instanceof Map<?, ?> m) {
            return List.of((Map<String, Object>) m);
        }
        return List.of();
    }

    /**
     * Переписывает версию сообщения на нашу и нормализует компоненты. null —
     * для сообщений неизвестного вида и для набора, из которого ничего не
     * осталось.
     */
    @SuppressWarnings("unchecked")
    private static Map<String, Object> normalizeMessage(Map<String, Object> msg, State st) {
        String kind = "";
        for (String k : KNOWN_MESSAGES) {
            if (msg.containsKey(k)) {
                kind = k;
                break;
            }
        }
        if (kind.isEmpty()) {
            return null;
        }
        Map<String, Object> out = new LinkedHashMap<>(msg);
        out.put("version", A2ui.VERSION);

        if (kind.equals("createSurface") && out.get("createSurface") instanceof Map<?, ?> raw) {
            // Компоненты мы приводим к basic-каталогу, поэтому и объявлять надо
            // его: иначе клиент отвергнет поверхность, которую Ingest уже сделал
            // пригодной к отрисовке.
            Map<String, Object> copied = new LinkedHashMap<>((Map<String, Object>) raw);
            copied.put("catalogId", A2ui.CATALOG_ID);
            out.put("createSurface", copied);
        }
        if (!kind.equals("updateComponents")) {
            return out;
        }
        if (!(out.get("updateComponents") instanceof Map<?, ?> rawPayload)) {
            return null;
        }
        Map<String, Object> payload = (Map<String, Object>) rawPayload;
        List<Map<String, Object>> raw = componentList(payload.get("components"));
        if (raw.isEmpty()) {
            return null;
        }
        List<Map<String, Object>> comps = new ArrayList<>();
        for (Map<String, Object> c : raw) {
            comps.addAll(normalizeComponent(c, st));
        }
        if (comps.isEmpty()) {
            return null;
        }
        labelButtonActions(comps);
        // Рендерер строит дерево от компонента с id ровно "root"; без него
        // поверхность навсегда остаётся в состоянии «Loading surface…». Чужие
        // агенты называют корень как угодно — назначаем его сами, один раз на
        // поверхность.
        String surfaceId = payload.get("surfaceId") instanceof String s ? s : "";
        if (!st.rooted.contains(surfaceId)) {
            comps = ensureRoot(comps);
            st.rooted.add(surfaceId);
        }
        Map<String, Object> next = new LinkedHashMap<>(payload);
        next.put("components", comps);
        out.put("updateComponents", next);
        return out;
    }

    /** Приводит поле components к списку объектов. */
    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> componentList(Object v) {
        if (!(v instanceof List<?> list)) {
            return List.of();
        }
        List<Map<String, Object>> out = new ArrayList<>(list.size());
        for (Object item : list) {
            if (item instanceof Map<?, ?> m) {
                out.add((Map<String, Object>) m);
            }
        }
        return out;
    }

    /** Тип компонента и его свойства. */
    private record Unwrapped(String type, Map<String, Object> props) {}

    /**
     * Разбирает обёрнутую форму компонента ({@code {"Text": {...}}}), которую
     * шлёт внешний агент, в пару «тип, свойства». Плоская форма возвращается как
     * есть. Свойства всегда копируются: дальше их правят на месте.
     */
    @SuppressWarnings("unchecked")
    private static Unwrapped unwrap(Map<String, Object> c) {
        if (c.get("component") instanceof String t && !t.isEmpty()) {
            Map<String, Object> props = new LinkedHashMap<>(c);
            props.remove("component");
            return new Unwrapped(t, props);
        }
        if (c.size() == 1) {
            for (Map.Entry<String, Object> e : c.entrySet()) {
                if (e.getValue() instanceof Map<?, ?> props) {
                    return new Unwrapped(e.getKey(), new LinkedHashMap<>((Map<String, Object>) props));
                }
            }
        }
        return null;
    }

    /**
     * Превращает один входящий компонент в один или несколько компонентов
     * basic-каталога. Id контейнера всегда сохраняется: на него ссылается
     * children родителя, и потеря id разорвала бы дерево.
     */
    private static List<Map<String, Object>> normalizeComponent(Map<String, Object> c, State st) {
        Unwrapped u = unwrap(c);
        if (u == null) {
            return List.of();
        }
        String typ = u.type();
        Map<String, Object> props = u.props();
        String id = props.get("id") instanceof String s && !s.isEmpty() ? s : "gen" + (++st.gen);
        props.remove("id");

        switch (typ) {
            case "Button":
                return normalizeButton(id, props);
            case "Heading":
                return List.of(text(id, str(props.get("text")), "h3"));
            case "Metric": {
                String label = str(props.get("label"));
                String value = String.valueOf(props.get("value"));
                return List.of(text(id, label.isEmpty() ? value : "**" + label + ":** " + value, "body"));
            }
            case "Callout":
                return normalizeCallout(id, props);
            case "Table":
                return List.of(text(id, markdownTable(props), "body"));
            default:
                break;
        }
        if (!BASIC_COMPONENTS.contains(typ)) {
            // Незнакомый компонент лучше показать дампом, чем потерять молча.
            return List.of(text(id, unknownDump(typ, props), "body"));
        }
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("id", id);
        out.put("component", typ);
        out.putAll(props);
        normalizeAction(out);
        return List.of(out);
    }

    /**
     * Приводит контекст события к объекту: схема A2UI требует «ключ → значение»,
     * а агент присылает массив пар {@code {key, value}}, на котором рендерер
     * соберёт бессмысленный контекст.
     *
     * <p>Заодно action и event пересобираются в свои карты: дальше их правит
     * {@link #labelButtonActions}, а приехавшие из JSON вложенные карты
     * принадлежат сообщению агента, и править их на месте нельзя.
     */
    @SuppressWarnings("unchecked")
    private static void normalizeAction(Map<String, Object> c) {
        if (!(c.get("action") instanceof Map<?, ?> rawAction)) {
            return;
        }
        Map<String, Object> action = new LinkedHashMap<>((Map<String, Object>) rawAction);
        c.put("action", action);
        if (!(action.get("event") instanceof Map<?, ?> rawEvent)) {
            return;
        }
        Map<String, Object> event = new LinkedHashMap<>((Map<String, Object>) rawEvent);
        action.put("event", event);
        if (event.get("context") instanceof List<?> pairs) {
            Map<String, Object> ctx = new LinkedHashMap<>();
            for (Object p : pairs) {
                if (p instanceof Map<?, ?> pair && pair.get("key") instanceof String key && !key.isEmpty()) {
                    ctx.put(key, pair.get("value"));
                }
            }
            event.put("context", ctx);
        } else if (event.get("context") instanceof Map<?, ?> m) {
            event.put("context", new LinkedHashMap<>((Map<String, Object>) m));
        }
    }

    /**
     * Приводит кнопку к форме каталога: у неё нет свойства label, подпись живёт
     * отдельным дочерним Text.
     */
    private static List<Map<String, Object>> normalizeButton(String id, Map<String, Object> props) {
        Map<String, Object> btn = new LinkedHashMap<>();
        btn.put("id", id);
        btn.put("component", "Button");
        for (Map.Entry<String, Object> e : props.entrySet()) {
            if (!e.getKey().equals("label")) {
                btn.put(e.getKey(), e.getValue());
            }
        }
        normalizeAction(btn);
        if (btn.containsKey("child")) {
            return List.of(btn);
        }
        String label = str(props.get("label"));
        if (label.isEmpty()) {
            label = "OK";
        }
        String labelId = id + "__lbl";
        btn.put("child", labelId);
        return List.of(btn, text(labelId, label, "body"));
    }

    /**
     * Разворачивает плашку в колонку из заголовка и текста, сохраняя id самой
     * плашки за колонкой.
     */
    private static List<Map<String, Object>> normalizeCallout(String id, Map<String, Object> props) {
        String titleId = id + "__t";
        String bodyId = id + "__b";
        String title = str(props.get("title"));
        String body = str(props.get("text"));
        List<Object> children = new ArrayList<>();
        List<Map<String, Object>> comps = new ArrayList<>();
        comps.add(null); // место под колонку
        if (!title.isEmpty()) {
            children.add(titleId);
            comps.add(text(titleId, title, "h3"));
        }
        if (!body.isEmpty()) {
            children.add(bodyId);
            comps.add(text(bodyId, body, "body"));
        }
        if (children.isEmpty()) {
            return List.of(text(id, "", "body"));
        }
        Map<String, Object> column = new LinkedHashMap<>();
        column.put("id", id);
        column.put("component", "Column");
        column.put("children", children);
        comps.set(0, column);
        return comps;
    }

    /**
     * Дописывает в контекст действия человекочитаемую подпись кнопки. Без неё
     * клиенту нечего показать в ленте, кроме служебного имени действия
     * («return_order» вместо «Подтвердить возврат»): в самом событии подписи
     * нет, она живёт в дочернем Text.
     */
    @SuppressWarnings("unchecked")
    private static void labelButtonActions(List<Map<String, Object>> comps) {
        Map<String, Map<String, Object>> byId = new HashMap<>(comps.size());
        for (Map<String, Object> c : comps) {
            if (c.get("id") instanceof String id) {
                byId.put(id, c);
            }
        }
        for (Map<String, Object> c : comps) {
            if (!"Button".equals(c.get("component"))) {
                continue;
            }
            if (!(c.get("action") instanceof Map<?, ?> rawAction)) {
                continue;
            }
            Map<String, Object> action = (Map<String, Object>) rawAction;
            if (!(action.get("event") instanceof Map<?, ?> rawEvent)) {
                continue;
            }
            Map<String, Object> event = (Map<String, Object>) rawEvent;
            Map<String, Object> ctx;
            if (event.get("context") instanceof Map<?, ?> m) {
                ctx = (Map<String, Object>) m;
            } else {
                ctx = new LinkedHashMap<>();
                event.put("context", ctx);
            }
            if (ctx.containsKey("label")) {
                continue;
            }
            Map<String, Object> child = byId.get(c.get("child") instanceof String s ? s : "");
            if (child != null && child.get("text") instanceof String label && !label.isEmpty()) {
                ctx.put("label", label);
            }
        }
    }

    /**
     * Гарантирует, что у набора есть компонент с id "root". Недостающий корень
     * добавляется отдельной колонкой поверх верхних компонентов —
     * переименовывать чужие id нельзя, на них могут ссылаться последующие
     * обновления той же поверхности.
     */
    private static List<Map<String, Object>> ensureRoot(List<Map<String, Object>> comps) {
        Set<String> referenced = new HashSet<>();
        for (Map<String, Object> c : comps) {
            if (c.get("child") instanceof String child) {
                referenced.add(child);
            }
            if (c.get("children") instanceof List<?> children) {
                for (Object ch : children) {
                    if (ch instanceof String name) {
                        referenced.add(name);
                    }
                }
            }
        }
        List<Object> tops = new ArrayList<>();
        for (Map<String, Object> c : comps) {
            String id = c.get("id") instanceof String s ? s : "";
            if (id.equals("root")) {
                return comps; // корень уже на месте
            }
            if (!referenced.contains(id)) {
                tops.add(id);
            }
        }
        if (tops.isEmpty()) {
            // Дерево замкнуто само на себя — вешаем корень на первый компонент.
            if (comps.isEmpty()) {
                return comps;
            }
            tops.add(String.valueOf(comps.getFirst().get("id")));
        }
        Map<String, Object> root = new LinkedHashMap<>();
        root.put("id", "root");
        root.put("component", "Column");
        root.put("children", tops);
        List<Map<String, Object>> out = new ArrayList<>(comps.size() + 1);
        out.add(root);
        out.addAll(comps);
        return out;
    }

    /**
     * Рисует таблицу разметкой: Text в браузере рендерится через markdown-it,
     * поэтому она станет настоящей &lt;table&gt;.
     */
    private static String markdownTable(Map<String, Object> props) {
        List<Map<String, Object>> cols = componentList(props.get("columns"));
        List<Map<String, Object>> rows = componentList(props.get("rows"));
        if (cols.isEmpty()) {
            return unknownDump("Table", props);
        }
        List<String> keys = new ArrayList<>(cols.size());
        StringBuilder b = new StringBuilder("|");
        for (Map<String, Object> c : cols) {
            String key = str(c.get("key"));
            String label = str(c.get("label"));
            keys.add(key);
            b.append(' ').append(label.isEmpty() ? key : label).append(" |");
        }
        b.append("\n|");
        for (int i = 0; i < keys.size(); i++) {
            b.append(" --- |");
        }
        for (Map<String, Object> r : rows) {
            b.append("\n|");
            for (String k : keys) {
                Object v = r.get(k);
                b.append(' ').append(v == null ? "" : v).append(" |");
            }
        }
        return b.toString();
    }

    /** Компактно показывает то, что мы не умеем отрисовать. */
    private static String unknownDump(String typ, Map<String, Object> props) {
        try {
            return "`" + typ + "` " + Json.MAPPER.writeValueAsString(props);
        } catch (Exception e) {
            return typ;
        }
    }

    private static Map<String, Object> text(String id, String s, String variant) {
        Map<String, Object> c = new LinkedHashMap<>();
        c.put("id", id);
        c.put("component", "Text");
        c.put("text", s);
        c.put("variant", variant);
        return c;
    }

    private static String str(Object v) {
        return v instanceof String s ? s : "";
    }
}
