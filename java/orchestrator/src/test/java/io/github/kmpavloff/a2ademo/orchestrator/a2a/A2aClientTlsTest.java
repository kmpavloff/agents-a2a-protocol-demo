package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpsConfigurator;
import com.sun.net.httpserver.HttpsParameters;
import com.sun.net.httpserver.HttpsServer;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.config.TlsConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.URISyntaxException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.List;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLParameters;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertThrows;

/** Агент за mTLS: без клиентского сертификата рукопожатие рвётся, с ним проходит весь ход. */
class A2aClientTlsTest {

    HttpsServer server;
    String base;

    static String fixture(String name) {
        try {
            return Path.of(A2aClientTlsTest.class.getResource("/tls/" + name).toURI()).toString();
        } catch (URISyntaxException e) {
            throw new IllegalStateException(e);
        }
    }

    @BeforeEach
    void setUp() throws IOException {
        // Серверный SSLContext собирается тем же TlsConfig: ключ сервера плюс
        // доверие к CA, которым подписан клиент.
        SSLContext ctx = new TlsConfig(fixture("server.crt"), fixture("server.key"), fixture("ca.crt"), false)
                .sslContext();
        server = HttpsServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.setHttpsConfigurator(new HttpsConfigurator(ctx) {
            @Override
            public void configure(HttpsParameters params) {
                SSLParameters p = ctx.getDefaultSSLParameters();
                p.setNeedClientAuth(true);
                params.setSSLParameters(p);
            }
        });
        base = "https://127.0.0.1:" + server.getAddress().getPort();
        server.createContext("/.well-known/agent-card.json", ex -> respond(ex, """
                {"name":"tls","description":"d","version":"1.0","capabilities":{},
                 "supportedInterfaces":[{"url":"https://0.0.0.0:1/","protocolBinding":"JSONRPC"}],"skills":[]}
                """));
        server.createContext("/", ex -> {
            JsonNode req = Json.MAPPER.readTree(ex.getRequestBody());
            respond(ex, "{\"jsonrpc\":\"2.0\",\"id\":" + req.path("id")
                    + ",\"result\":{\"task\":{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}}");
        });
        server.start();
    }

    @AfterEach
    void tearDown() {
        server.stop(0);
    }

    private static void respond(com.sun.net.httpserver.HttpExchange ex, String body) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().add("Content-Type", "application/json");
        ex.sendResponseHeaders(200, b.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(b);
        }
    }

    private void askWith(TlsConfig tls) {
        AgentConfig cfg = new AgentConfig("tls", "", base, "", List.of(), false, "10s", "", AuthConfig.NONE, tls);
        A2aClient.resolve(cfg, Tracer.noop()).client()
                .sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), List.of());
    }

    @Test
    void passesWithClientCertificateAndCa() {
        assertDoesNotThrow(() -> askWith(
                new TlsConfig(fixture("client.crt"), fixture("client.key"), fixture("ca.crt"), false)));
    }

    @Test
    void passesWithClientCertificateAndSkipVerify() {
        assertDoesNotThrow(() -> askWith(
                new TlsConfig(fixture("client.crt"), fixture("client.key"), "", true)));
    }

    @Test
    void failsWithoutClientCertificate() {
        assertThrows(A2aClient.A2aException.class, () -> askWith(new TlsConfig("", "", fixture("ca.crt"), false)));
    }

    @Test
    void failsWhenTheServerIsSignedByAnotherCa() {
        assertThrows(A2aClient.A2aException.class, () -> askWith(
                new TlsConfig(fixture("client.crt"), fixture("client.key"), fixture("other-ca.crt"), false)));
    }
}
