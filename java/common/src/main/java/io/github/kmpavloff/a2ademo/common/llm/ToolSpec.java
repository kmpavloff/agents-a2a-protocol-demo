package io.github.kmpavloff.a2ademo.common.llm;

import java.util.Map;

/** Declaration of a function tool exposed to the model (name + JSON Schema). */
public record ToolSpec(String name, String description, Map<String, Object> parametersSchema) {}
