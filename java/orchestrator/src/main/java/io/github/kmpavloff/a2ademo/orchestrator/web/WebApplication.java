package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
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

    /** Hands over the pre-built collaborators before the context starts. */
    public static void configure(OrchestratorWebExecutor executor, AgentCard card, Tracer trace) {
        executorHolder = executor;
        cardHolder = card;
        traceHolder = trace;
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
}
