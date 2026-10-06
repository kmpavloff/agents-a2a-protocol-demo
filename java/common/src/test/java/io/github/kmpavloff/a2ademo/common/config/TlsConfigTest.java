package io.github.kmpavloff.a2ademo.common.config;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.net.URISyntaxException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Разбор PEM и валидация TLS-настроек агента — то же, что делает Go. */
class TlsConfigTest {

    static String fixture(String name) {
        try {
            return Path.of(TlsConfigTest.class.getResource("/tls/" + name).toURI()).toString();
        } catch (URISyntaxException e) {
            throw new IllegalStateException(e);
        }
    }

    private static AgentConfig agent(String url, TlsConfig tls) {
        return new AgentConfig("o", "", url, "", List.of(), false, "", "", AuthConfig.NONE, tls);
    }

    private static final TlsConfig FULL =
            new TlsConfig(fixture("client.crt"), fixture("client.key"), fixture("ca.crt"), false);

    @Test
    void acceptsAFullMutualTlsSetup() {
        assertDoesNotThrow(() -> AgentConfig.validate(agent("https://x", FULL)));
        assertDoesNotThrow(() -> AgentConfig.validate(agent("https://x", new TlsConfig("", "", "", true))));
    }

    @Test
    void rejectsBrokenSetups(@TempDir Path dir) throws IOException {
        Path junk = Files.writeString(dir.resolve("junk.pem"), "мусор");
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(agent("http://x", FULL)),
                "tls по http://");
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                agent("https://x", new TlsConfig(fixture("client.crt"), "", "", false))), "cert без key");
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                agent("https://x", new TlsConfig("/nope.crt", "/nope.key", "", false))), "нет файла");
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                agent("https://x", new TlsConfig("", "", junk.toString(), false))), "ca_file не PEM");
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                agent("https://x", new TlsConfig(fixture("server.crt"), fixture("client.key"), "", false))),
                "ключ не от этого сертификата");
    }

    // В JDK 21 нет разбора SEC1/PKCS#1 — отказ должен подсказывать, как
    // перевести ключ, а не просто сообщать о неудаче.
    @Test
    void explainsHowToConvertANonPkcs8Key() {
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> new TlsConfig(fixture("client.crt"), fixture("client-sec1.key"), "", false).sslContext());
        assertTrue(e.getMessage().contains("openssl pkcs8 -topk8"), e.getMessage());
    }

    @Test
    void enabledOnlyWhenSomethingIsSet() {
        assertFalse(TlsConfig.NONE.enabled());
        assertFalse(new TlsConfig(null, null, null, false).enabled());
        assertTrue(new TlsConfig("", "", "", true).enabled());
    }

    @Test
    void readsTheTlsBlockFromYaml(@TempDir Path dir) throws IOException {
        Path cfg = Files.writeString(dir.resolve("orchestrator.yaml"), """
                agents:
                  - id: ouroboros
                    url: "https://127.0.0.1:18800"
                    tls:
                      cert_file: "%s"
                      key_file: "%s"
                      ca_file: "%s"
                      insecure_skip_verify: true
                llm:
                  base_url: "http://localhost:1234/v1"
                """.formatted(FULL.certFile(), FULL.keyFile(), FULL.caFile()));
        AgentConfig a = ConfigLoader.loadOrchestrator(cfg.toString()).agents().get(0);
        assertEquals(new TlsConfig(FULL.certFile(), FULL.keyFile(), FULL.caFile(), true), a.tls());
    }
}
