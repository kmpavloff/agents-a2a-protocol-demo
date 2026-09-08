package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertSame;

/**
 * Суффикс хода у surfaceId. Агент нередко выводит идентификатор поверхности из
 * контекста, а контекст живёт всю сессию: второй ход прислал бы createSurface с
 * тем же id, и рендерер упал бы с «Surface … already exists».
 */
class SurfacesTest {

    @SuppressWarnings("unchecked")
    @Test
    void retagsEverySurfaceIdConsistently() {
        List<Map<String, Object>> msgs = List.of(
                Map.of("version", A2ui.VERSION, "createSurface", Map.of("surfaceId", "s1", "catalogId", A2ui.CATALOG_ID)),
                Map.of("version", A2ui.VERSION, "updateComponents", Map.of("surfaceId", "s1", "components", List.of())));

        List<Map<String, Object>> out = Surfaces.retag(msgs, "-t3");

        assertEquals("s1-t3", ((Map<String, Object>) out.get(0).get("createSurface")).get("surfaceId"));
        assertEquals("s1-t3", ((Map<String, Object>) out.get(1).get("updateComponents")).get("surfaceId"),
                "ссылка обновления обязана попасть в ту же поверхность");
        assertEquals("s1", ((Map<String, Object>) msgs.get(0).get("createSurface")).get("surfaceId"),
                "исходные сообщения не правятся на месте");
    }

    @Test
    void emptySuffixChangesNothing() {
        List<Map<String, Object>> msgs = List.of(Map.of("version", A2ui.VERSION));
        assertSame(msgs, Surfaces.retag(msgs, ""));
    }

    @Test
    void leavesMessagesWithoutASurfaceIdAlone() {
        List<Map<String, Object>> out = Surfaces.retag(
                List.of(Map.of("version", A2ui.VERSION, "createSurface", Map.of("catalogId", A2ui.CATALOG_ID))), "-t1");
        assertEquals(Map.of("catalogId", A2ui.CATALOG_ID), out.getFirst().get("createSurface"));
    }

    // На пути наружу суффикс снимается: в ленте поверхность зовётся s1-t3, а
    // агент знает её как s1, и сопоставить событие он может только со своим id.
    @Test
    void untagsBackToTheAgentsOwnId() {
        assertEquals("s1", Surfaces.untag("s1-t3"));
        assertEquals("s1", Surfaces.untag("s1"));
        assertEquals("confirmation-1", Surfaces.untag("confirmation-1-t42"));
        assertEquals("", Surfaces.untag(null));
    }
}
