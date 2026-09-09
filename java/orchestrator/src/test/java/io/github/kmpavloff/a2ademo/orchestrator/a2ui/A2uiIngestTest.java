package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import io.github.kmpavloff.a2ademo.common.a2a.Part;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Приём разметки, которую агент написал сам. Толерантен по входу, потому что
 * живой агент отдаёт не то, что обещает его контракт.
 */
class A2uiIngestTest {

    private static Part a2uiPart(Object payload) {
        Part p = Part.data(payload, Map.of(A2ui.MIME_KEY, A2ui.MIME_TYPE));
        p.mediaType = A2ui.MIME_TYPE;
        return p;
    }

    private static Map<String, Object> map(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put((String) kv[i], kv[i + 1]);
        }
        return m;
    }

    @SafeVarargs
    private static List<Map<String, Object>> mutable(Map<String, Object>... comps) {
        return new ArrayList<>(List.of(comps));
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> payloadOf(Map<String, Object> msg, String key) {
        return (Map<String, Object>) msg.get(key);
    }

    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> componentsOf(Map<String, Object> msg) {
        return (List<Map<String, Object>>) payloadOf(msg, "updateComponents").get("components");
    }

    @Test
    void ignoresPartsThatAreNotA2ui() {
        assertTrue(A2uiIngest.ingest(List.of(Part.text("просто текст"))).isEmpty());
        assertTrue(A2uiIngest.ingest(null).isEmpty());
    }

    // Версия переписывается на нашу, а каталог объявляется наш: компоненты мы
    // приводим к basic-каталогу, и объявлять надо именно его.
    @Test
    void normalisesVersionAndCatalog() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.8", "createSurface", map("surfaceId", "s1", "catalogId", "https://example.org/own"))))));

        assertEquals(1, out.size());
        assertEquals(A2ui.VERSION, out.getFirst().get("version"));
        assertEquals(A2ui.CATALOG_ID, payloadOf(out.getFirst(), "createSurface").get("catalogId"));
    }

    // Разметка приезжает и текстовой частью, внутри которой JSON-строка.
    @Test
    void readsMarkupFromATextPart() {
        Part p = Part.text("[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"s1\"}}]");
        p.mediaType = A2ui.LEGACY_MIME_TYPE;
        assertEquals(1, A2uiIngest.ingest(List.of(p)).size());
    }

    // Рендерер строит дерево от компонента с id ровно "root"; чужие агенты
    // называют корень как угодно — назначаем его сами.
    @Test
    void addsAMissingRootOverTheTopLevelComponents() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "hdr", "component", "Text", "text", "Заказ 1041"),
                        map("id", "body", "component", "Text", "text", "Доставлен"))))))));

        List<Map<String, Object>> comps = componentsOf(out.getFirst());
        assertEquals("root", comps.getFirst().get("id"));
        assertEquals("Column", comps.getFirst().get("component"));
        assertEquals(List.of("hdr", "body"), comps.getFirst().get("children"));
    }

    @Test
    void keepsAnExistingRootAsIs() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "root", "component", "Column", "children", List.of("hdr")),
                        map("id", "hdr", "component", "Text", "text", "Заказ"))))))));

        assertEquals(2, componentsOf(out.getFirst()).size());
    }

    // Корень назначается один раз на поверхность: второе обновление той же
    // поверхности ссылается на уже существующее дерево.
    @Test
    void rootsEachSurfaceOnlyOnce() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "a", "component", "Text", "text", "раз")))),
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "b", "component", "Text", "text", "два"))))))));

        assertEquals("root", componentsOf(out.get(0)).getFirst().get("id"));
        assertEquals("b", componentsOf(out.get(1)).getFirst().get("id"), "второй раз корень не навешивается");
    }

    // Обёрнутая форма компонента: {"Text": {...}}.
    @Test
    void unwrapsTheWrappedComponentForm() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("Text", map("id", "hdr", "text", "Заказ", "variant", "h3")))))))));

        Map<String, Object> hdr = componentsOf(out.getFirst()).get(1);
        assertEquals("Text", hdr.get("component"));
        assertEquals("Заказ", hdr.get("text"));
    }

    // У кнопки в каталоге нет свойства label — подпись живёт отдельным Text.
    @Test
    void splitsAButtonLabelIntoAChildText() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "ok", "component", "Button", "label", "Подтвердить возврат",
                                "action", map("event", map("name", "return_order"))))))))));

        List<Map<String, Object>> comps = componentsOf(out.getFirst());
        Map<String, Object> btn = comps.stream().filter(c -> "ok".equals(c.get("id"))).findFirst().orElseThrow();
        Map<String, Object> label = comps.stream().filter(c -> "ok__lbl".equals(c.get("id"))).findFirst().orElseThrow();
        assertEquals("ok__lbl", btn.get("child"));
        assertEquals("Подтвердить возврат", label.get("text"));
        assertTrue(!btn.containsKey("label"), "label остаться в кнопке не должен");
    }

    // Подпись копируется в контекст действия: без неё клиенту нечего показать в
    // ленте, кроме служебного имени («return_order» вместо «Подтвердить возврат»).
    @SuppressWarnings("unchecked")
    @Test
    void copiesTheButtonLabelIntoTheActionContext() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "ok", "component", "Button", "label", "Подтвердить возврат",
                                "action", map("event", map("name", "return_order"))))))))));

        Map<String, Object> btn = componentsOf(out.getFirst()).stream()
                .filter(c -> "ok".equals(c.get("id"))).findFirst().orElseThrow();
        Map<String, Object> event = (Map<String, Object>) ((Map<String, Object>) btn.get("action")).get("event");
        assertEquals("Подтвердить возврат", ((Map<String, Object>) event.get("context")).get("label"));
    }

    // Схема требует «ключ → значение», а агент присылает массив пар.
    @SuppressWarnings("unchecked")
    @Test
    void turnsAPairListContextIntoAnObject() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "ok", "component", "Button", "child", "lbl",
                                "action", map("event", map("name", "return_order", "context",
                                        List.of(map("key", "order_id", "value", "1041"))))),
                        map("id", "lbl", "component", "Text", "text", "Вернуть"))))))));

        Map<String, Object> btn = componentsOf(out.getFirst()).stream()
                .filter(c -> "ok".equals(c.get("id"))).findFirst().orElseThrow();
        Map<String, Object> event = (Map<String, Object>) ((Map<String, Object>) btn.get("action")).get("event");
        assertEquals("1041", ((Map<String, Object>) event.get("context")).get("order_id"));
    }

    @Test
    void rendersATableAsMarkdown() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "t", "component", "Table",
                                "columns", List.of(map("key", "id", "label", "№"), map("key", "item", "label", "Товар")),
                                "rows", List.of(map("id", "1041", "item", "Наушники"))))))))));

        Map<String, Object> t = componentsOf(out.getFirst()).stream()
                .filter(c -> "t".equals(c.get("id"))).findFirst().orElseThrow();
        assertEquals("Text", t.get("component"));
        assertEquals("| № | Товар |\n| --- | --- |\n| 1041 | Наушники |", t.get("text"));
    }

    @Test
    void mapsHeadingAndMetricToText() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "h", "component", "Heading", "text", "Заказ 1041"),
                        map("id", "m", "component", "Metric", "label", "Сумма", "value", 4990))))))));

        List<Map<String, Object>> comps = componentsOf(out.getFirst());
        Map<String, Object> h = comps.stream().filter(c -> "h".equals(c.get("id"))).findFirst().orElseThrow();
        Map<String, Object> m = comps.stream().filter(c -> "m".equals(c.get("id"))).findFirst().orElseThrow();
        assertEquals("h3", h.get("variant"));
        assertEquals("**Сумма:** 4990", m.get("text"));
    }

    // Незнакомый компонент лучше показать дампом, чем потерять молча.
    @Test
    void dumpsAnUnknownComponentInsteadOfDroppingIt() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "x", "component", "Gauge", "percent", 42))))))));

        Map<String, Object> x = componentsOf(out.getFirst()).stream()
                .filter(c -> "x".equals(c.get("id"))).findFirst().orElseThrow();
        assertEquals("Text", x.get("component"));
        assertTrue(String.valueOf(x.get("text")).startsWith("`Gauge`"), "дамп: " + x.get("text"));
        assertTrue(String.valueOf(x.get("text")).contains("42"));
    }

    // Компонент без id получает сгенерированный: на id ссылается children
    // родителя, и потеря id разорвала бы дерево.
    @Test
    void generatesIdsForComponentsThatArrivedWithoutOne() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("component", "Text", "text", "раз"),
                        map("component", "Text", "text", "два"))))))));

        List<Map<String, Object>> comps = componentsOf(out.getFirst());
        assertEquals(List.of("gen1", "gen2"), comps.getFirst().get("children"));
    }

    @Test
    void dropsMessagesOfAnUnknownKind() {
        assertTrue(A2uiIngest.ingest(List.of(a2uiPart(List.of(map("version", "v0.9", "somethingElse", map()))))).isEmpty());
    }

    // Инвариант: мусор на входе не роняет UI.
    @Test
    void survivesMalformedPayloads() {
        assertTrue(A2uiIngest.ingest(List.of(a2uiPart("не JSON"))).isEmpty());
        assertTrue(A2uiIngest.ingest(List.of(a2uiPart(List.of("строка вместо сообщения")))).isEmpty());
        assertTrue(A2uiIngest.ingest(List.of(a2uiPart(map("version", "v0.9", "updateComponents",
                map("surfaceId", "s1", "components", "не список"))))).isEmpty());
    }
}
