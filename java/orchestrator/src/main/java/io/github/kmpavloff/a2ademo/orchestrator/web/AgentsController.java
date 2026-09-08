package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import org.springframework.http.MediaType;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

/**
 * Список агентов, между которыми можно переключаться в браузере (порт
 * internal/webui/agents.go).
 *
 * <p>Отвечает мгновенно: {@link Registry#list()} отдаёт последнее известное
 * состояние и лишь запускает фоновую проверку — ждать медленного агента этому
 * эндпоинту нельзя, браузер дёргает его после каждого хода.
 */
@RestController
public class AgentsController {

    private final Registry registry;

    public AgentsController(Registry registry) {
        this.registry = registry;
    }

    @GetMapping(value = "/api/agents", produces = MediaType.APPLICATION_JSON_VALUE)
    public List<Registry.AgentInfo> agents() {
        return registry.list();
    }
}
