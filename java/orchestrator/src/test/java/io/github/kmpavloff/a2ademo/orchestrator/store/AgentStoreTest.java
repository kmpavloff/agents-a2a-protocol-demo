package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Правки списка агентов, сделанные из UI. */
class AgentStoreTest {

    private static AgentConfig agent(String id, String url) {
        return new AgentConfig(id, "", url, "", "", false, "", "", AuthConfig.NONE);
    }

    private static List<AgentConfig> base() {
        return List.of(agent("orders", "http://localhost:8081"));
    }

    private static AgentStore store(Path dir) {
        return new AgentStore(base(), dir.resolve("agents.local.yaml"));
    }

    @Test
    void createsAndPersists(@TempDir Path dir) {
        AgentStore s = store(dir);
        s.create(agent("shop", "http://b"));

        assertEquals(List.of("orders", "shop"), s.agents().stream().map(AgentConfig::id).toList());
        assertEquals(List.of("orders", "shop"),
                new AgentStore(base(), dir.resolve("agents.local.yaml")).agents()
                        .stream().map(AgentConfig::id).toList());
    }

    @Test
    void refusesAnIdThatIsAlreadyTaken(@TempDir Path dir) {
        assertThrows(AgentStore.ExistsException.class, () -> store(dir).create(agent("orders", "http://x")));
    }

    // Пустой пароль означает «не менять»: прочитать текущий браузер не может, и
    // форма шлёт пустое поле каждый раз, когда пароль не трогали.
    @Test
    void anEmptyPasswordMeansKeepTheCurrentOne(@TempDir Path dir) {
        AgentStore s = new AgentStore(
                List.of(new AgentConfig("orders", "", "http://a", "", "", false, "", "",
                        new AuthConfig("basic", "u", "секрет"))),
                dir.resolve("agents.local.yaml"));

        s.update("orders", new AgentConfig("orders", "Новое имя", "http://a", "", "", false, "", "",
                new AuthConfig("basic", "u", "")));

        assertEquals("секрет", s.agents().getFirst().auth().password());
    }

    // Агент из YAML не удаляется, а помечается скрытым: базовый файл рукописный.
    @Test
    void deletingAFileAgentHidesIt(@TempDir Path dir) {
        AgentStore s = store(dir);
        s.delete("orders");

        assertEquals(List.of(), s.agents());
        AgentStore.Record rec = s.records().getFirst();
        assertTrue(rec.hidden(), "запись остаётся видимой на экране настроек, чтобы агента можно было вернуть");
        assertTrue(rec.inFile());
    }

    @Test
    void deletingAUiAgentRemovesItCompletely(@TempDir Path dir) {
        AgentStore s = store(dir);
        s.create(agent("shop", "http://b"));
        s.delete("shop");
        assertEquals(1, s.records().size());
    }

    // Сбрасывать некуда, если базовой версии нет: для агента, заведённого через
    // UI, «сброс» означал бы безвозвратное удаление — это дело delete.
    @Test
    void resetOnlyWorksForAgentsThatHaveAFileVersion(@TempDir Path dir) {
        AgentStore s = store(dir);
        s.update("orders", agent("orders", "http://127.0.0.1:9000"));
        s.reset("orders");
        assertEquals("http://localhost:8081", s.agents().getFirst().url());

        s.create(agent("shop", "http://b"));
        assertThrows(AgentStore.NotFoundException.class, () -> s.reset("shop"));
    }

    @Test
    void recordsNeverCarryThePasswordButSayWhetherItIsSet(@TempDir Path dir) {
        AgentStore s = new AgentStore(
                List.of(new AgentConfig("orders", "", "http://a", "", "", false, "", "",
                        new AuthConfig("basic", "u", "секрет"))),
                dir.resolve("agents.local.yaml"));

        AgentStore.Record rec = s.records().getFirst();
        assertEquals("", rec.agent().auth().password(), "пароль наружу не уходит никогда");
        assertTrue(rec.hasPassword());
        assertEquals(AgentStore.SOURCE_FILE, rec.source());
        assertFalse(rec.hidden());
    }

    @Test
    void notifiesTheSubscriberOnEveryChange(@TempDir Path dir) {
        AgentStore s = store(dir);
        List<List<AgentConfig>> seen = new ArrayList<>();
        s.onChange(seen::add);

        s.create(agent("shop", "http://b"));
        s.delete("shop");

        assertEquals(2, seen.size());
        assertEquals(List.of("orders"), seen.getLast().stream().map(AgentConfig::id).toList());
    }

    @Test
    void refusesAnInvalidRecordAndKeepsTheOldState(@TempDir Path dir) {
        AgentStore s = store(dir);
        assertThrows(IllegalArgumentException.class, () -> s.create(agent("shop", "")));
        assertEquals(List.of("orders"), s.agents().stream().map(AgentConfig::id).toList());
    }
}
