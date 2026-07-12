package io.github.kmpavloff.a2ademo.common.config;

import org.yaml.snakeyaml.Yaml;

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;

/**
 * YAML config loader with env-var overrides, mirroring the Go internal/config
 * package. The same {@code configs/*.yaml} files and env variables are shared
 * with the Go implementation. A missing file is tolerated (env-only setup).
 */
public final class ConfigLoader {

    private ConfigLoader() {}

    public record WorkerConfig(String listenAddr, String publicUrl, String dataPath, LlmConfig llm) {
        public int port() {
            return parsePort(listenAddr);
        }
    }

    /** listenAddr/publicUrl are used only in --web mode (A2A server + frontend). */
    public record OrchestratorConfig(
            String listenAddr, String publicUrl, String workerUrl, String a2aLogPath, LlmConfig llm) {
        public int port() {
            return parsePort(listenAddr);
        }
    }

    public static WorkerConfig loadWorker(String path) {
        Map<String, Object> y = readYaml(path);
        String listenAddr = env("WORKER_LISTEN_ADDR", str(y, "listen_addr", ":8081"));
        String publicUrl = env("WORKER_PUBLIC_URL", str(y, "public_url", "http://localhost:8081"));
        String dataPath = env("WORKER_DATA_PATH", str(y, "data_path", "data/orders.json"));
        LlmConfig llm = llm(y);
        require(llm.baseUrl(), "worker config: llm.base_url is required (yaml or LLM_BASE_URL)");
        return new WorkerConfig(listenAddr, publicUrl, dataPath, llm);
    }

    public static OrchestratorConfig loadOrchestrator(String path) {
        Map<String, Object> y = readYaml(path);
        String listenAddr = env("ORCHESTRATOR_LISTEN_ADDR", str(y, "listen_addr", ":8080"));
        String publicUrl = env("ORCHESTRATOR_PUBLIC_URL", str(y, "public_url", "http://localhost:8080"));
        String workerUrl = env("WORKER_URL", str(y, "worker_url", "http://localhost:8081"));
        String logPath = env("A2A_LOG_PATH", str(y, "a2a_log_path", "a2a-orchestrator.log"));
        LlmConfig llm = llm(y);
        require(llm.baseUrl(), "orchestrator config: llm.base_url is required (yaml or LLM_BASE_URL)");
        return new OrchestratorConfig(listenAddr, publicUrl, workerUrl, logPath, llm);
    }

    private static LlmConfig llm(Map<String, Object> y) {
        Map<String, Object> l = section(y, "llm");
        return new LlmConfig(
                env("LLM_BASE_URL", str(l, "base_url", "")),
                env("LLM_MODEL", str(l, "model", "local-model")),
                env("LLM_API_KEY", str(l, "api_key", "lm-studio")));
    }

    private static Map<String, Object> readYaml(String path) {
        Path p = Path.of(path);
        if (!Files.exists(p)) {
            return Map.of();
        }
        try (InputStream in = Files.newInputStream(p)) {
            Map<String, Object> m = new Yaml().load(in);
            return m == null ? Map.of() : m;
        } catch (IOException e) {
            throw new IllegalStateException("read config " + path + ": " + e.getMessage(), e);
        }
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> section(Map<String, Object> y, String key) {
        Object v = y.get(key);
        return v instanceof Map<?, ?> m ? (Map<String, Object>) m : Map.of();
    }

    private static String str(Map<String, Object> y, String key, String def) {
        Object v = y.get(key);
        return v == null || String.valueOf(v).isBlank() ? def : String.valueOf(v);
    }

    private static String env(String key, String cur) {
        String v = System.getenv(key);
        return v == null || v.isBlank() ? cur : v;
    }

    private static void require(String v, String message) {
        if (v == null || v.isBlank()) {
            throw new IllegalStateException(message);
        }
    }

    static int parsePort(String listenAddr) {
        String s = listenAddr == null ? "" : listenAddr.trim();
        int idx = s.lastIndexOf(':');
        if (idx >= 0) {
            s = s.substring(idx + 1);
        }
        try {
            return Integer.parseInt(s);
        } catch (NumberFormatException e) {
            throw new IllegalStateException("invalid listen_addr: " + listenAddr);
        }
    }
}
