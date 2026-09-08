package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Overlay-файл: машинный, читается Go и Java одинаково. */
class OverlayFileTest {

    // Отсутствие файла — обычное состояние свежего клона, а не ошибка.
    @Test
    void missingFileIsAnEmptyOverlay(@TempDir Path dir) {
        assertEquals(List.of(), OverlayFile.load(dir.resolve("agents.local.yaml")));
    }

    @Test
    void roundTripsThroughTheGoFileFormat(@TempDir Path dir) throws IOException {
        Path p = dir.resolve("nested/agents.local.yaml");
        List<AgentOverride> over = List.of(
                new AgentOverride(new AgentConfig("orders", "Мой воркер", "http://127.0.0.1:9000",
                        "/.well-known/agent.json", "shop", true, "180s", "Заказы.",
                        new AuthConfig("basic", "u", "секрет")), false),
                new AgentOverride(new AgentConfig("ouroboros", "", "", "", "", false, "", "", AuthConfig.NONE), true));

        OverlayFile.save(p, over);

        String yaml = Files.readString(p);
        assertTrue(yaml.startsWith("#"), "шапка объясняет, что файл машинный: " + yaml);
        assertTrue(yaml.contains("card_path:"), "ключи в snake_case, как в Go: " + yaml);
        assertTrue(yaml.contains("hidden: true"));

        List<AgentOverride> back = OverlayFile.load(p);
        assertEquals(over, back);
    }

    @Test
    void overwritesTheWholeFile(@TempDir Path dir) {
        Path p = dir.resolve("agents.local.yaml");
        OverlayFile.save(p, List.of(new AgentOverride(
                new AgentConfig("a", "", "http://a", "", "", false, "", "", AuthConfig.NONE), false)));
        OverlayFile.save(p, List.of());
        assertEquals(List.of(), OverlayFile.load(p));
    }

    // Битый overlay — отказ, а не молчаливое игнорирование: агенты, заведённые
    // через UI, не должны исчезать незаметно.
    @Test
    void refusesABrokenFile(@TempDir Path dir) throws IOException {
        Path p = dir.resolve("agents.local.yaml");
        Files.writeString(p, "agents: [ этот список не закрыт");
        assertThrows(IllegalStateException.class, () -> OverlayFile.load(p));
    }
}
