package io.github.kmpavloff.a2ademo.worker.a2a;

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
import io.github.kmpavloff.a2ademo.worker.agent.WorkerAgent;
import org.springframework.http.MediaType;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

/**
 * The worker's A2A 1.0 JSONRPC binding: the /invoke endpoint plus the
 * well-known AgentCard. Wire format matches a2a-go v2, so the Go orchestrator
 * can talk to this worker unchanged.
 */
@RestController
public class A2aController {

    private final WorkerAgent agent;
    private final AgentCard card;
    private final Tracer trace;
    private final Map<String, A2aTask> tasks = new ConcurrentHashMap<>();

    public A2aController(WorkerAgent agent, AgentCard card, Tracer trace) {
        this.agent = agent;
        this.card = card;
        this.trace = trace;
    }

    @GetMapping(value = AgentCard.WELL_KNOWN_PATH, produces = MediaType.APPLICATION_JSON_VALUE)
    public AgentCard agentCard() {
        return card;
    }

    @PostMapping(value = "/invoke", produces = MediaType.APPLICATION_JSON_VALUE)
    public JsonRpc.Response invoke(@RequestBody String body) {
        JsonRpc.Request req;
        try {
            req = Json.MAPPER.readValue(body, JsonRpc.Request.class);
        } catch (Exception e) {
            return JsonRpc.Response.fail(null, JsonRpc.CODE_PARSE_ERROR, "parse error: " + e.getMessage());
        }
        if (!JsonRpc.VERSION.equals(req.jsonrpc) || req.id == null || req.id.isNull()) {
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_INVALID_REQUEST, "invalid request");
        }
        try {
            return switch (req.method == null ? "" : req.method) {
                case JsonRpc.METHOD_SEND_MESSAGE -> sendMessage(req);
                case JsonRpc.METHOD_GET_TASK -> getTask(req);
                case JsonRpc.METHOD_CANCEL_TASK -> cancelTask(req);
                default -> JsonRpc.Response.fail(req.id, JsonRpc.CODE_METHOD_NOT_FOUND,
                        "method not found: " + req.method);
            };
        } catch (Exception e) {
            trace.logf("  ✖ runner error: %s", e.getMessage());
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_INVALID_REQUEST, String.valueOf(e.getMessage()));
        }
    }

    private JsonRpc.Response sendMessage(JsonRpc.Request req) throws Exception {
        JsonNode msgNode = req.params == null ? null : req.params.get("message");
        if (msgNode == null || msgNode.isNull()) {
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_INVALID_REQUEST, "message is required");
        }
        A2aMessage message = Json.MAPPER.treeToValue(msgNode, A2aMessage.class);
        String userText = message.firstText();

        boolean newTask = message.taskId == null || message.taskId.isEmpty();
        A2aTask task;
        if (newTask) {
            task = new A2aTask();
            task.id = UUID.randomUUID().toString();
            task.contextId = message.contextId == null || message.contextId.isEmpty()
                    ? UUID.randomUUID().toString()
                    : message.contextId;
            tasks.put(task.id, task);
        } else {
            task = tasks.get(message.taskId);
            if (task == null) {
                return JsonRpc.Response.fail(req.id, JsonRpc.CODE_TASK_NOT_FOUND,
                        "task not found: " + message.taskId);
            }
        }
        trace.logf("▶ incoming A2A request | contextID=%s newTask=%s", task.contextId, newTask);
        if (!newTask) {
            trace.logf("  resuming stored task | id=%s", task.id);
        }
        task.addHistory(message);

        WorkerAgent.Outcome outcome = agent.run(task.contextId, userText);
        switch (outcome) {
            case WorkerAgent.Outcome.InputRequired paused -> {
                // Text part is the fallback for UI-less clients; the DataPart
                // carries the confirmation-widget spec when there is one.
                List<Part> parts = new ArrayList<>();
                parts.add(Part.text(paused.question()));
                if (paused.widget() != null) {
                    parts.add(widgetPart(paused.widgetKind(), paused.widget()));
                }
                A2aMessage ask = A2aMessage.of(A2aMessage.ROLE_AGENT, parts.toArray(Part[]::new));
                task.status = TaskStatus.of(TaskState.INPUT_REQUIRED, ask);
            }
            case WorkerAgent.Outcome.Completed done -> {
                List<Part> parts = new ArrayList<>();
                parts.add(Part.text(done.text()));
                if (done.widget() != null) {
                    trace.logf("  → including %s DataPart in artifact", done.widget().get("kind"));
                    parts.add(widgetPartFromState(done.widget()));
                }
                if (done.file() != null) {
                    trace.logf("  → including file %s (%s, %d bytes)",
                            done.file().filename(), done.file().mediaType(), done.file().content().length);
                    Part file = new Part();
                    file.raw = Base64.getEncoder().encodeToString(done.file().content());
                    file.filename = done.file().filename();
                    file.mediaType = done.file().mediaType();
                    parts.add(file);
                }
                task.addArtifact(Artifact.of(parts));
                task.status = TaskStatus.of(TaskState.COMPLETED, null);
            }
        }
        return JsonRpc.Response.ok(req.id, Map.of("task", task));
    }

    private JsonRpc.Response getTask(JsonRpc.Request req) {
        String id = req.params == null ? "" : req.params.path("id").asText("");
        A2aTask task = tasks.get(id);
        if (task == null) {
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_TASK_NOT_FOUND, "task not found: " + id);
        }
        return JsonRpc.Response.ok(req.id, Map.of("task", task));
    }

    private JsonRpc.Response cancelTask(JsonRpc.Request req) {
        String id = req.params == null ? "" : req.params.path("id").asText("");
        A2aTask task = tasks.get(id);
        if (task == null) {
            return JsonRpc.Response.fail(req.id, JsonRpc.CODE_TASK_NOT_FOUND, "task not found: " + id);
        }
        task.status = TaskStatus.of(TaskState.CANCELED, null);
        return JsonRpc.Response.ok(req.id, Map.of("task", task));
    }

    /** Wraps structured data as a DataPart tagged with the widget kind in metadata. */
    private static Part widgetPart(String kind, Map<String, Object> payload) {
        return Part.data(payload, Map.of("kind", kind, "version", 1));
    }

    /**
     * Turns a tool-stashed widget map (kind under "kind") into a DataPart: the
     * kind moves to part metadata, the rest becomes the payload.
     */
    private static Part widgetPartFromState(Map<String, Object> widget) {
        String kind = String.valueOf(widget.get("kind"));
        Map<String, Object> payload = new LinkedHashMap<>(widget);
        payload.remove("kind");
        return widgetPart(kind, payload);
    }
}
