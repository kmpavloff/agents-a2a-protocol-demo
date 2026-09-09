package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;

/**
 * Одна overlay-запись: агент целиком плюс признак «скрыт».
 *
 * <p>{@code hidden} живёт здесь, а не в {@link AgentConfig}: он описывает не
 * агента, а решение UI убрать его из списка. Агент, заведённый в YAML, иначе не
 * удаляется — базовый файл мы не трогаем.
 */
public record AgentOverride(AgentConfig agent, boolean hidden) {

    public String id() {
        return agent.id();
    }
}
