package io.github.kmpavloff.a2ademo.orchestrator;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.ConfigLoader;
import io.github.kmpavloff.a2ademo.common.llm.OpenAiChatModel;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Remote;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;
import io.github.kmpavloff.a2ademo.orchestrator.agent.SessionStore;
import io.github.kmpavloff.a2ademo.orchestrator.store.AgentStore;
import io.github.kmpavloff.a2ademo.orchestrator.tui.Repl;
import io.github.kmpavloff.a2ademo.orchestrator.web.OrchestratorCards;
import io.github.kmpavloff.a2ademo.orchestrator.web.OrchestratorWebExecutor;
import io.github.kmpavloff.a2ademo.orchestrator.web.WebApplication;
import org.springframework.boot.SpringApplication;

import java.io.FileWriter;
import java.io.IOException;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;

/**
 * orchestrator — Java port of cmd/orchestrator. Runs as a single jar:
 * {@code java -jar a2a-demo-orchestrator.jar [--web] [path/to/orchestrator.yaml]}.
 * Default is the terminal REPL; {@code --web} serves the A2UI web UI + A2A
 * server instead, exactly like the Go binary. Reads the same
 * configs/orchestrator.yaml + env overrides as the Go implementation.
 */
public class OrchestratorApplication {

    public static void main(String[] args) throws IOException {
        boolean web = false;
        String configPath = "configs/orchestrator.yaml";
        for (String a : args) {
            if (a.equals("--web") || a.equals("-web")) {
                web = true;
            } else if (!a.startsWith("--")) {
                configPath = a;
            }
        }
        ConfigLoader.OrchestratorConfig cfg = ConfigLoader.loadOrchestrator(configPath);

        Tracer console = new Tracer("", System.out);
        FileWriter logFile = new FileWriter(cfg.a2aLogPath(), true);
        Tracer trace = web
                ? new Tracer("[A2A client] ", System.out, logFile)
                : new Tracer("[A2A client] ", logFile);
        console.logf("A2A protocol trace → %s%s", cfg.a2aLogPath(), web ? " + stdout" : "");

        // Правки из UI лежат отдельным файлом поверх рукописного конфига.
        AgentStore store = new AgentStore(cfg.agents(), Path.of(cfg.agentsOverlayPath()));
        List<AgentConfig> agents = store.agents();
        Registry registry = new Registry(agents, trace);
        // Правка из UI доезжает до живого реестра: новый агент появляется в
        // селекторе и в режиме «Авто» без перезапуска.
        store.onChange(registry::apply);

        OpenAiChatModel model = new OpenAiChatModel(cfg.llm());
        SessionStore sessions = new SessionStore();
        console.logf("orchestrator | LLM=%s model=\"%s\"", cfg.llm().baseUrl(), cfg.llm().model());
        console.logf("agents (%d), overlay %s:", agents.size(), cfg.agentsOverlayPath());
        for (AgentConfig a : agents) {
            // Пароль сюда не попадает намеренно: логи демо показывают целиком.
            console.logf("  - %s → %s (card %s, skills=%s, verbatim=%s, timeout=%s)",
                    a.id(), a.url(), a.cardPath(), a.skills(), a.verbatim(), a.timeoutDuration());
        }

        if (web) {
            OrchestratorWebExecutor executor = new OrchestratorWebExecutor(registry,
                    (tools, summary) -> new OrchestratorAgent(model, tools, summary, sessions),
                    trace);
            WebApplication.configure(executor, OrchestratorCards.agentCard(cfg.publicUrl()),
                    trace, registry, store);
            SpringApplication app = new SpringApplication(WebApplication.class);
            app.setDefaultProperties(Map.of(
                    "server.port", cfg.port(),
                    "spring.main.banner-mode", "off",
                    "logging.level.root", "warn"));
            app.run(args);
            console.logf("orchestrator web UI on %s", cfg.listenAddr());
            return; // Spring держит JVM; файл трейса живёт вместе с сервером.
        }

        // Терминальный REPL выбора агента не даёт и работает с первым агентом
        // списка. Базовый конфиг гарантирует минимум одного, но overlay может
        // скрыть их все — тогда first() вернёт null.
        Remote first = registry.first();
        if (first == null) {
            console.logf("нет ни одного агента: все скрыты в %s — удалите файл или верните агента через UI",
                    cfg.agentsOverlayPath());
            logFile.close();
            System.exit(1);
            return;
        }
        try {
            first.connect();
        } catch (RuntimeException e) {
            console.logf("agent \"%s\" (запущен ли он?): %s", first.id(), e.getMessage());
            logFile.close();
            System.exit(1);
            return;
        }
        OrdersClient orders = registry.clientFor(first.id());
        console.logf("orchestrator tools (1):");
        console.logf("  - %s: %s", orders.profile().toolName(), orders.profile().toolDesc());

        try (logFile) {
            new Repl(new OrchestratorAgent(model, List.of(orders), orders.profile().summary(), sessions),
                    orders).run();
        }
    }
}
