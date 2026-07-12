package io.github.kmpavloff.a2ademo.common.a2a;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/** A2A 1.0 Task. */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class A2aTask {
    public String id;
    public String contextId;
    public TaskStatus status;
    public List<Artifact> artifacts;
    public List<A2aMessage> history;
    public Map<String, Object> metadata;

    public A2aTask() {}

    public void addHistory(A2aMessage m) {
        if (history == null) {
            history = new ArrayList<>();
        }
        history.add(m);
    }

    public void addArtifact(Artifact a) {
        if (artifacts == null) {
            artifacts = new ArrayList<>();
        }
        artifacts.add(a);
    }
}
