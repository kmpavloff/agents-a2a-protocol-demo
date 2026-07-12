package io.github.kmpavloff.a2ademo.orchestrator.web;

import com.fasterxml.jackson.databind.JsonNode;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.A2aTask;
import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.common.a2a.Artifact;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.a2a.TaskState;
import io.github.kmpavloff.a2ademo.common.a2a.TaskStatus;
import io.github.kmpavloff.a2ademo.common.rpc.JsonRpc;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;
import java.util.Map;
import java.util.UUID;

/**
 * The orchestrator's A2A 1.0 JSONRPC binding for browser A2UI clients: /invoke
 * plus the well-known AgentCard. The A2UI extension is negotiated via the
 * A2A-Extensions request header (echoed back on activation, as a2a-go clients
 * expect). Each turn is a fresh task driven straight to completed — clients
 * reuse only the contextId across turns.
 */
@RestController
public class A2aWebController {

    private final OrchestratorWebExecutor executor;
    private final AgentCard card;
    private final Tracer trace;

    public A2aWebController(OrchestratorWebExecutor executor, AgentCard card, Tracer trace) {
        this.executor = executor;
        this.card = card;
        this.trace = trace;
    }

    @GetMapping(value = AgentCard.WELL_KNOWN_PATH, produces = MediaType.APPLICATION_JSON_VALUE)
    public AgentCard agentCard() {
        return card;
    }

    @PostMapping(value = "/invoke", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<JsonRpc.Response> invoke(
            @RequestBody String body,
            @RequestHeader(value = "A2A-Extensions", required = false) List<String> extensions) {
        boolean a2uiRequested = extensions != null && extensions.stream()
                .flatMap(v -> java.util.Arrays.stream(v.split("[,\\s]+")))
                .anyMatch(uri -> uri.equals(A2ui.EXTENSION_URI));

        JsonRpc.Response response = handle(body, a2uiRequested);
        ResponseEntity.BodyBuilder builder = ResponseEntity.ok();
        if (a2uiRequested && response.error == null) {
            builder.header("A2A-Extensions", A2ui.EXTENSION_URI);
        }
        return builder.body(response);
    }

    private JsonRpc.Response handle(String body, boolean a2uiActive) {
        JsonRpc.Request req;
        try {
            req = Json.MAPPER.readValue(body, JsonRpc.Request.class);
        } catch (Exception e) {
            return JsonRpc.Response.fail(null, JsonRpc.CODE_PARSE_ERROR, "parse error: " + e.getMessage());
        }
        if (!JsonRpc.VERSION.equals(req.jsonrpc) || req.id == null || req.id.isNull()) {
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_INVALID_REQUEST, "invalid request");
        }
        if (!JsonRpc.METHOD_SEND_MESSAGE.equals(req.method)) {
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_METHOD_NOT_FOUND, "method not found: " + req.method);
        }
        try {
            return sendMessage(req, a2uiActive);
        } catch (Exception e) {
            trace.logf("  ✖ executor error: %s", e.getMessage());
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_INVALID_REQUEST, String.valueOf(e.getMessage()));
        }
    }

    private JsonRpc.Response sendMessage(JsonRpc.Request req, boolean a2uiActive) throws Exception {
        JsonNode msgNode = req.params == null ? null : req.params.get("message");
        if (msgNode == null || msgNode.isNull()) {
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_INVALID_REQUEST, "message is required");
        }
        A2aMessage message = Json.MAPPER.treeToValue(msgNode, A2aMessage.class);

        // The orchestrator drives every task straight to a terminal state, so a
        // follow-up referencing a taskId has nothing to resume (parity with
        // a2asrv's "task in a terminal state" rejection).
        if (message.taskId != null && !message.taskId.isEmpty()) {
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_TASK_NOT_FOUND,
                    "task not found (orchestrator tasks are single-turn): " + message.taskId);
        }

        A2aTask task = new A2aTask();
        task.id = UUID.randomUUID().toString();
        task.contextId = message.contextId == null || message.contextId.isEmpty()
                ? UUID.randomUUID().toString()
                : message.contextId;
        task.addHistory(message);

        List<Part> parts = executor.execute(task.contextId, message, a2uiActive);
        task.addArtifact(Artifact.of(parts));
        task.status = TaskStatus.of(TaskState.COMPLETED, null);
        return JsonRpc.Response.ok(req.id, Map.of("task", task));
    }
}
