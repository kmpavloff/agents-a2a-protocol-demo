package io.github.kmpavloff.a2ademo.worker;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.common.config.ConfigLoader;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.OpenAiChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.worker.a2a.WorkerCards;
import io.github.kmpavloff.a2ademo.worker.agent.WorkerAgent;
import io.github.kmpavloff.a2ademo.worker.orders.OrderStore;
import io.github.kmpavloff.a2ademo.worker.orders.OrderTools;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;

import java.util.Map;

/**
 * orders-agent — the A2A worker (Java port of cmd/worker). Runs as a single
 * jar: {@code java -jar a2a-demo-worker.jar [path/to/worker.yaml]}. Reads the
 * same configs/worker.yaml + env overrides as the Go implementation.
 */
@SpringBootApplication
public class WorkerApplication {

    private static ConfigLoader.WorkerConfig config;

    public static void main(String[] args) {
        String configPath = args.length > 0 ? args[0] : "configs/worker.yaml";
        config = ConfigLoader.loadWorker(configPath);

        SpringApplication app = new SpringApplication(WorkerApplication.class);
        app.setDefaultProperties(Map.of("server.port", config.port()));
        app.run(args);

        new Tracer("", System.out).logf("orders-agent listening on %s", config.listenAddr());
    }

    @Bean
    ConfigLoader.WorkerConfig workerConfig() {
        return config;
    }

    @Bean
    Tracer tracer() {
        // A2A protocol trace to stdout so the worker's activity is visible.
        return new Tracer("[A2A worker] ", System.out);
    }

    @Bean
    OrderStore orderStore(ConfigLoader.WorkerConfig cfg) {
        return OrderStore.load(cfg.dataPath());
    }

    @Bean
    OrderTools orderTools(OrderStore store) {
        return new OrderTools(store);
    }

    @Bean
    ChatModel chatModel(ConfigLoader.WorkerConfig cfg, Tracer tracer) {
        tracer.logf("orders-agent | LLM=%s model=\"%s\" | data=%s | listen=%s",
                cfg.llm().baseUrl(), cfg.llm().model(), cfg.dataPath(), cfg.listenAddr());
        return new OpenAiChatModel(cfg.llm());
    }

    @Bean
    WorkerAgent workerAgent(ChatModel model, OrderTools tools, Tracer tracer) {
        tracer.logf("orders-agent tools (%d):", tools.specs().size());
        for (ToolSpec t : tools.specs()) {
            tracer.logf("  - %s: %s", t.name(), t.description());
        }
        return new WorkerAgent(model, tools, tracer);
    }

    @Bean
    AgentCard agentCard(ConfigLoader.WorkerConfig cfg) {
        return WorkerCards.agentCard(cfg.publicUrl());
    }
}
