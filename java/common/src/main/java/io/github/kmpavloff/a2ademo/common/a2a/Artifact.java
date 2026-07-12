package io.github.kmpavloff.a2ademo.common.a2a;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/** A2A 1.0 Artifact. */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class Artifact {
    public String artifactId;
    public String name;
    public String description;
    public List<Part> parts = new ArrayList<>();
    public Map<String, Object> metadata;

    public Artifact() {}

    public static Artifact of(List<Part> parts) {
        Artifact a = new Artifact();
        a.artifactId = UUID.randomUUID().toString();
        a.parts = new ArrayList<>(parts);
        return a;
    }
}
