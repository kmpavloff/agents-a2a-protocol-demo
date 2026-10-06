package io.github.kmpavloff.a2ademo.common.config;

import org.yaml.snakeyaml.Yaml;

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * YAML config loader with env-var overrides, mirroring the Go internal/config
 * package. The same {@code configs/*.yaml} files and env variables are shared
 * with the Go implementation. A missing file is tolerated (env-only setup).
 */
public final class ConfigLoader {

    private ConfigLoader() {}

    /**
     * orderLinkBase is the base URL of the customer-facing order page; widgets
     * carry base/&lt;id&gt; links so a client can open the order card. Empty
     * disables links.
     */
    public record WorkerConfig(
            String listenAddr, String publicUrl, String dataPath, String orderLinkBase, LlmConfig llm) {
        public int port() {
            return parsePort(listenAddr);
        }
    }

    /** listenAddr/publicUrl используются только в режиме --web (A2A-сервер + фронтенд). */
    public record OrchestratorConfig(String listenAddr, String publicUrl, String workerUrl,
                                     List<AgentConfig> agents, String agentsOverlayPath,
                                     String a2aLogPath, LlmConfig llm) {
        public int port() {
            return parsePort(listenAddr);
        }
    }

    public static WorkerConfig loadWorker(String path) {
        Map<String, Object> y = readYaml(path);
        String listenAddr = env("WORKER_LISTEN_ADDR", str(y, "listen_addr", ":8081"));
        String publicUrl = env("WORKER_PUBLIC_URL", str(y, "public_url", "http://localhost:8081"));
        String dataPath = env("WORKER_DATA_PATH", str(y, "data_path", "data/orders.json"));
        String orderLinkBase = env("ORDER_LINK_BASE",
                str(y, "order_link_base", "https://shop.example.com/orders"));
        LlmConfig llm = llm(y);
        require(llm.baseUrl(), "worker config: llm.base_url is required (yaml or LLM_BASE_URL)");
        return new WorkerConfig(listenAddr, publicUrl, dataPath, orderLinkBase, llm);
    }

    public static OrchestratorConfig loadOrchestrator(String path) {
        Map<String, Object> y = readYaml(path);
        String listenAddr = env("ORCHESTRATOR_LISTEN_ADDR", str(y, "listen_addr", ":8080"));
        String publicUrl = env("ORCHESTRATOR_PUBLIC_URL", str(y, "public_url", "http://localhost:8080"));
        // Умолчания у worker_url нет намеренно, как и в Go: иначе переменная
        // WORKER_URL и список agents молча спорили бы друг с другом.
        String workerUrl = env("WORKER_URL", str(y, "worker_url", ""));
        String logPath = env("A2A_LOG_PATH", str(y, "a2a_log_path", "a2a-orchestrator.log"));
        String overlayPath = env("A2A_AGENTS_OVERLAY_PATH",
                str(y, "agents_overlay_path", "configs/agents.local.yaml"));
        LlmConfig llm = llm(y);
        require(llm.baseUrl(), "orchestrator config: llm.base_url is required (yaml or LLM_BASE_URL)");

        List<AgentConfig> agents = agents(y);
        // Совместимость: одиночный worker_url становится единственным агентом.
        if (agents.isEmpty() && !workerUrl.isBlank()) {
            agents = List.of(new AgentConfig("orders", "Агент заказов", workerUrl,
                    "", "", false, "", "", AuthConfig.NONE));
        }
        if (agents.isEmpty()) {
            throw new IllegalStateException(
                    "orchestrator config: at least one agent (agents: or worker_url:) is required");
        }
        return new OrchestratorConfig(listenAddr, publicUrl, workerUrl,
                normalizeAgents(agents), overlayPath, logPath, llm);
    }

    /** Записи agents: из YAML, без умолчаний и проверок — их делает normalizeAgents. */
    @SuppressWarnings("unchecked")
    private static List<AgentConfig> agents(Map<String, Object> y) {
        if (!(y.get("agents") instanceof List<?> list)) {
            return List.of();
        }
        List<AgentConfig> out = new ArrayList<>(list.size());
        for (Object item : list) {
            if (!(item instanceof Map<?, ?> raw)) {
                continue;
            }
            Map<String, Object> a = (Map<String, Object>) raw;
            Map<String, Object> auth = section(a, "auth");
            out.add(new AgentConfig(
                    str(a, "id", ""), str(a, "name", ""), str(a, "url", ""),
                    str(a, "card_path", ""), str(a, "skill", ""),
                    Boolean.TRUE.equals(a.get("verbatim")),
                    str(a, "timeout", ""), str(a, "description", ""),
                    new AuthConfig(str(auth, "type", ""), str(auth, "username", ""), str(auth, "password", "")),
                    tls(section(a, "tls"))));
        }
        return out;
    }

    /** Блок tls: агента — общий разбор для orchestrator.yaml и overlay. */
    public static TlsConfig tls(Map<String, Object> t) {
        return new TlsConfig(str(t, "cert_file", ""), str(t, "key_file", ""), str(t, "ca_file", ""),
                Boolean.TRUE.equals(t.get("insecure_skip_verify")));
    }

    /**
     * Подставляет умолчания и env-перекрытия, затем валидирует список.
     * Применяется и к списку из YAML, и к слитому с overlay — поэтому env
     * остаётся последним словом в обоих случаях.
     */
    public static List<AgentConfig> normalizeAgents(List<AgentConfig> agents) {
        Set<String> seen = new HashSet<>(agents.size());
        List<AgentConfig> out = new ArrayList<>(agents.size());
        for (AgentConfig a : agents) {
            // Адрес перекрывается окружением: в контейнере агент живёт по
            // другому имени, чем на машине разработчика, а конфиг один и тот же.
            String urlEnv = System.getenv(AgentConfig.envVar(a.id(), "URL"));
            if (urlEnv != null && !urlEnv.isBlank()) {
                a = a.withUrl(urlEnv);
            }
            // Пути к сертификатам — по той же причине: в контейнере файлы
            // монтируются в другое место.
            a = a.withTlsEnv();
            AgentConfig.validate(a);
            if (!seen.add(a.id())) {
                throw new IllegalStateException("orchestrator config: duplicate agent id \"" + a.id() + "\"");
            }
            if (a.cardPath().isEmpty()) {
                a = a.withCardPath(AgentConfig.DEFAULT_CARD_PATH);
            }
            String passEnv = System.getenv(AgentConfig.envVar(a.id(), "PASSWORD"));
            if (passEnv != null && !passEnv.isBlank()) {
                a = a.withPassword(passEnv);
            }
            out.add(a);
        }
        return List.copyOf(out);
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
