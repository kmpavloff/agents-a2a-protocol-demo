package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.orchestrator.store.AgentStore;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;

import java.net.URISyntaxException;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Пять эндпоинтов экрана «Настройки». */
class AgentConfigControllerTest {

    private static AgentConfigController controller(Path dir) {
        return new AgentConfigController(new AgentStore(
                List.of(new AgentConfig("orders", "Агент заказов", "http://a", "", List.of(), false, "", "",
                        new AuthConfig("basic", "u", "секрет"))),
                dir.resolve("agents.local.yaml")));
    }

    private static AgentConfigController.AgentIn in(String id, String url) {
        AgentConfigController.AgentIn a = new AgentConfigController.AgentIn();
        a.id = id;
        a.url = url;
        return a;
    }

    @Test
    void listNeverLeaksThePasswordAndDescribesResettability(@TempDir Path dir) {
        List<AgentConfigController.AgentOut> out = controller(dir).list();
        AgentConfigController.AgentOut a = out.getFirst();

        assertEquals("orders", a.id);
        assertEquals(AgentStore.SOURCE_FILE, a.source);
        assertTrue(a.auth.hasPassword, "признак «пароль задан» есть");
        assertEquals(List.of(), a.envLocked);
        // Сбрасывать нечего: агент из файла ещё не правился.
        assertFalse(a.canReset);
    }

    @Test
    void canResetOnlyWhenThereIsBothAFileVersionAndAnEdit(@TempDir Path dir) {
        AgentConfigController c = controller(dir);
        c.update("orders", in("orders", "http://127.0.0.1:9000"));
        assertTrue(c.list().getFirst().canReset);

        c.reset("orders");
        assertFalse(c.list().getFirst().canReset);
    }

    @Test
    void createReturns201AndConflictOnADuplicate(@TempDir Path dir) {
        AgentConfigController c = controller(dir);
        assertEquals(HttpStatus.CREATED, c.create(in("shop", "http://b")).getStatusCode());
        assertEquals(HttpStatus.CONFLICT, c.create(in("shop", "http://b")).getStatusCode());
    }

    // Пути mTLS ходят через форму туда и обратно; битый путь — это 400 при
    // сохранении, а не сюрприз при первом запросе к агенту.
    @Test
    void tlsPathsRoundTripAndBrokenOnesAreRejected(@TempDir Path dir) throws URISyntaxException {
        AgentConfigController c = controller(dir);
        AgentConfigController.AgentIn a = in("shop", "https://localhost:9100");
        a.tls.certFile = fixture("client.crt");
        a.tls.keyFile = fixture("client.key");
        a.tls.caFile = fixture("ca.crt");
        a.tls.insecureSkipVerify = true;
        assertEquals(HttpStatus.CREATED, c.create(a).getStatusCode());

        AgentConfigController.AgentOut out = c.list().get(1);
        assertEquals(a.tls.certFile, out.tls.certFile);
        assertEquals(a.tls.keyFile, out.tls.keyFile);
        assertEquals(a.tls.caFile, out.tls.caFile);
        assertTrue(out.tls.insecureSkipVerify);

        a.tls.certFile = "/нет/такого.crt";
        assertEquals(HttpStatus.BAD_REQUEST, c.update("shop", a).getStatusCode());
    }

    private static String fixture(String name) throws URISyntaxException {
        return Path.of(AgentConfigControllerTest.class.getResource("/tls/" + name).toURI()).toString();
    }

    @Test
    void badRecordIsARequestError(@TempDir Path dir) {
        assertEquals(HttpStatus.BAD_REQUEST, controller(dir).create(in("shop", "")).getStatusCode());
    }

    @Test
    void missingAgentIsANotFound(@TempDir Path dir) {
        AgentConfigController c = controller(dir);
        assertEquals(HttpStatus.NOT_FOUND, c.update("nope", in("nope", "http://x")).getStatusCode());
        assertEquals(HttpStatus.NOT_FOUND, c.delete("nope").getStatusCode());
        assertEquals(HttpStatus.NOT_FOUND, c.reset("nope").getStatusCode());
    }

    @Test
    void deleteReturns204(@TempDir Path dir) {
        assertEquals(HttpStatus.NO_CONTENT, controller(dir).delete("orders").getStatusCode());
    }
}
