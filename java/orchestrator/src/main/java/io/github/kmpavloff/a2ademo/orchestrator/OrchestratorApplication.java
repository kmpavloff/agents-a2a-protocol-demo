package io.github.kmpavloff.a2ademo.orchestrator;

import io.github.kmpavloff.a2ademo.common.config.ConfigLoader;
import io.github.kmpavloff.a2ademo.common.llm.OpenAiChatModel;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.A2aClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.WorkerProfile;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;
import io.github.kmpavloff.a2ademo.orchestrator.tui.Repl;
import io.github.kmpavloff.a2ademo.orchestrator.web.OrchestratorCards;
import io.github.kmpavloff.a2ademo.orchestrator.web.OrchestratorWebExecutor;
import io.github.kmpavloff.a2ademo.orchestrator.web.WebApplication;
import org.springframework.boot.SpringApplication;

import java.io.FileWriter;
import java.io.IOException;
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

        A2aClient.Resolved resolved;
        try {
            resolved = A2aClient.resolve(cfg.workerUrl());
        } catch (A2aClient.A2aException e) {
            console.logf("orders client (is the worker running at %s?): %s", cfg.workerUrl(), e.getMessage());
            logFile.close();
            System.exit(1);
            return;
        }
        trace.logf("resolved worker AgentCard \"%s\" at %s", resolved.card().name, cfg.workerUrl());

        WorkerProfile profile = WorkerProfile.fromCard(resolved.card());
        trace.logf("derived delegating tool \"%s\" from card", profile.toolName());

        OrdersClient orders = new OrdersClient(resolved.client(), profile, trace);
        OpenAiChatModel model = new OpenAiChatModel(cfg.llm());
        console.logf("orchestrator | LLM=%s model=\"%s\" | worker=%s",
                cfg.llm().baseUrl(), cfg.llm().model(), cfg.workerUrl());
        console.logf("orchestrator tools (1):");
        console.logf("  - %s: %s", profile.toolName(), profile.toolDesc());

        OrchestratorAgent agent = new OrchestratorAgent(model, orders);

        if (web) {
            WebApplication.configure(
                    new OrchestratorWebExecutor(agent, orders, trace),
                    OrchestratorCards.agentCard(cfg.publicUrl()),
                    trace);
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
