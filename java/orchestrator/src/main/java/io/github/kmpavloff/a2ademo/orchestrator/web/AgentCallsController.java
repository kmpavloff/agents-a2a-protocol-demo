package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.trace.AgentCallLog;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import org.springframework.http.MediaType;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

/**
 * Обмены оркестратора с агентами за разговор — для панели «A2A-протокол»
 * (порт webui.AgentCallsHandler): GET ?contextId=…&after=&lt;seq&gt;.
 * Пропущенный contextId или нечисловой after Spring сам отвечает 400.
 */
@RestController
public class AgentCallsController {

    private final Tracer trace;

    public AgentCallsController(Tracer trace) {
        this.trace = trace;
    }

    @GetMapping(value = "/api/agent-calls", produces = MediaType.APPLICATION_JSON_VALUE)
    public List<AgentCallLog.Call> calls(@RequestParam String contextId,
                                         @RequestParam(defaultValue = "0") long after) {
        return trace.calls().since(contextId, after);
    }
}
