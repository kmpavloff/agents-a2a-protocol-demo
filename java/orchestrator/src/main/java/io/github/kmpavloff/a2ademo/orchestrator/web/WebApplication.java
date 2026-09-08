package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;

/**
 * Spring Boot configuration for --web mode. The collaborators are built in
 * OrchestratorApplication.main (config, worker card resolution, LLM) before
 * the context starts and handed over via the static holder.
 */
@SpringBootApplication
public class WebApplication {

    private static OrchestratorWebExecutor executorHolder;
    private static AgentCard cardHolder;
    private static Tracer traceHolder;
    private static Registry registryHolder;

    /** Hands over the pre-built collaborators before the context starts. */
    public static void configure(OrchestratorWebExecutor executor, AgentCard card, Tracer trace, Registry registry) {
        executorHolder = executor;
        cardHolder = card;
        traceHolder = trace;
        registryHolder = registry;
    }

    @Bean
    OrchestratorWebExecutor webExecutor() {
        return executorHolder;
    }

    @Bean
    AgentCard orchestratorCard() {
        return cardHolder;
    }

    @Bean
    Tracer tracer() {
        return traceHolder;
    }

    @Bean
    Registry registry() {
        return registryHolder;
    }
}
