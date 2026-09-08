package io.github.kmpavloff.a2ademo.orchestrator;

import io.github.kmpavloff.a2ademo.common.config.ConfigLoader;
import io.github.kmpavloff.a2ademo.common.llm.OpenAiChatModel;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.A2aClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Remote;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.WorkerProfile;
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

        // A2A protocol trace goes to a file so it does not clutter the REPL. In
        // --web mode there is no REPL, so mirror the trace to stdout too.
        FileWriter logFile = new FileWriter(cfg.a2aLogPath(), true);
        Tracer trace = web
                ? new Tracer("[A2A client] ", System.out, logFile)
                : new Tracer("[A2A client] ", logFile);
        console.logf("A2A protocol trace → %s%s", cfg.a2aLogPath(), web ? " + stdout" : "");

        // Временно: клиент по-прежнему строится только на первом агенте списка.
        // Полная разводка по всему списку agents — в задаче 19.
        String workerUrl = cfg.agents().getFirst().url();
        Remote remote = new Remote(cfg.agents().getFirst(), trace);
        try {
            remote.connect();
        } catch (A2aClient.A2aException e) {
            console.logf("orders client (is the worker running at %s?): %s", workerUrl, e.getMessage());
            logFile.close();
            System.exit(1);
            return;
        }

        OrdersClient orders = new OrdersClient(remote, trace);
        WorkerProfile profile = orders.profile();
        trace.logf("derived delegating tool \"%s\" from card", profile.toolName());
        OpenAiChatModel model = new OpenAiChatModel(cfg.llm());
        console.logf("orchestrator | LLM=%s model=\"%s\" | worker=%s",
                cfg.llm().baseUrl(), cfg.llm().model(), workerUrl);
        console.logf("orchestrator tools (1):");
        console.logf("  - %s: %s", profile.toolName(), profile.toolDesc());

        SessionStore sessions = new SessionStore();
        OrchestratorAgent agent = new OrchestratorAgent(model, List.of(orders), orders.profile().summary(), sessions);

        if (web) {
            // Тот же временный однагентный реестр, что и remote/orders выше: полная
            // разводка по всему списку agents — в задаче 19.
            Registry registry = new Registry(List.of(cfg.agents().getFirst()), trace);
            // Хранилище для экрана настроек уже видит весь список agents (сам
            // список редактировать можно), но не подписано на onChange: правки
            // из UI не долетают до живого реестра — эта разводка тоже в задаче 19.
            AgentStore agentStore = new AgentStore(cfg.agents(), Path.of(cfg.agentsOverlayPath()));
            WebApplication.configure(
                    new OrchestratorWebExecutor(registry,
                            (tools, summary) -> new OrchestratorAgent(model, tools, summary, sessions),
                            sessions, trace),
                    OrchestratorCards.agentCard(cfg.publicUrl()),
                    trace,
                    registry,
                    agentStore);
            SpringApplication app = new SpringApplication(WebApplication.class);
            app.setDefaultProperties(Map.of(
                    "server.port", cfg.port(),
                    "spring.main.banner-mode", "off",
                    "logging.level.root", "warn"));
            app.run(args);
            console.logf("orchestrator web UI on %s", cfg.listenAddr());
            return; // Spring keeps the JVM alive; the trace file stays open for the server's lifetime.
        }

        try (logFile) {
            new Repl(agent, orders).run();
        }
    }
}
