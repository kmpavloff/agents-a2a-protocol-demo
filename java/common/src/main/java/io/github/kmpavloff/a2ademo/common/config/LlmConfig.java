package io.github.kmpavloff.a2ademo.common.config;

/** Connection settings for the OpenAI-compatible LLM endpoint (e.g. LM Studio). */
public record LlmConfig(String baseUrl, String model, String apiKey) {}
