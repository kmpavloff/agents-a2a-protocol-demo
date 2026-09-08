package io.github.kmpavloff.a2ademo.common.config;

import java.time.Duration;
import java.util.regex.Pattern;

/**
 * Один удалённый A2A-агент, с которым умеет говорить оркестратор (порт
 * config.AgentConfig).
 *
 * <p>Запись состоит только из строк, bool и такой же записи {@link AuthConfig},
 * поэтому сравнивается обычным {@code equals} — на этом держится пересборка
 * реестра при правке конфига.
 *
 * @param name    подпись в селекторе UI; пусто → имя из AgentCard
 * @param url     вытесняет адрес, объявленный в самой карточке
 * @param cardPath путь к AgentCard: не все агенты кладут её в канонический
 *                 {@value #DEFAULT_CARD_PATH}
 * @param skill   уезжает в metadata.skill каждого сообщения — так внешний агент
 *                понимает, какой набор инструментов включать
 * @param verbatim при явном выборе этого агента в UI его ответ уходит в браузер
 *                 без локальной LLM — ни пересказа, ни лишней задержки
 * @param description вытесняет вывод из AgentCard: у агента может быть сотня
 *                    навыков, и промпт локальной модели такого не переживёт
 */
public record AgentConfig(String id, String name, String url, String cardPath, String skill,
                          boolean verbatim, String timeout, String description, AuthConfig auth) {

    public static final String DEFAULT_CARD_PATH = "/.well-known/agent-card.json";
    public static final Duration DEFAULT_TIMEOUT = Duration.ofSeconds(120);

    private static final Pattern ID_RE = Pattern.compile("^[a-z][a-z0-9_-]*$");

    public AgentConfig {
        id = id == null ? "" : id;
        name = name == null ? "" : name;
        url = url == null ? "" : url;
        cardPath = cardPath == null ? "" : cardPath;
        skill = skill == null ? "" : skill;
        timeout = timeout == null ? "" : timeout;
        description = description == null ? "" : description;
        auth = auth == null ? AuthConfig.NONE : auth;
    }

    /** Таймаут SendMessage с подстановкой умолчания. */
    public Duration timeoutDuration() {
        if (timeout.isEmpty()) {
            return DEFAULT_TIMEOUT;
        }
        try {
            Duration d = GoDuration.parse(timeout);
            return d.isZero() || d.isNegative() ? DEFAULT_TIMEOUT : d;
        } catch (IllegalArgumentException e) {
            return DEFAULT_TIMEOUT;
        }
    }

    /**
     * Имя переменной окружения для поля агента: пароль незачем держать в файле,
     * а адрес приходится подменять при запуске в контейнере.
     *
     * @param field "URL" или "PASSWORD"
     */
    public static String envVar(String id, String field) {
        return "A2A_AGENT_" + id.toUpperCase().replace('-', '_') + "_" + field;
    }

    /**
     * Проверяет одну запись — то же, что делает загрузка конфига, но без
     * env-перекрытий и без проверки на дубликаты. Отдельно нужна затем, что
     * запись из UI проверяется до того, как попадёт в список.
     */
    public static void validate(AgentConfig a) {
        if (!ID_RE.matcher(a.id()).matches()) {
            throw new IllegalArgumentException("agent id \"" + a.id() + "\" must match " + ID_RE.pattern());
        }
        if (a.url().isEmpty()) {
            throw new IllegalArgumentException("agent \"" + a.id() + "\": url is required");
        }
        if (!a.timeout().isEmpty()) {
            Duration d;
            try {
                d = GoDuration.parse(a.timeout());
            } catch (IllegalArgumentException e) {
                throw new IllegalArgumentException("agent \"" + a.id() + "\": bad timeout \"" + a.timeout() + "\"");
            }
            if (d.isZero() || d.isNegative()) {
                throw new IllegalArgumentException("agent \"" + a.id() + "\": bad timeout \"" + a.timeout() + "\"");
            }
        }
        String type = a.auth().type();
        if (!type.isEmpty() && !type.equalsIgnoreCase("basic")) {
            throw new IllegalArgumentException("agent \"" + a.id() + "\": unsupported auth type \"" + type + "\"");
        }
    }

    public AgentConfig withId(String v) {
        return new AgentConfig(v, name, url, cardPath, skill, verbatim, timeout, description, auth);
    }

    public AgentConfig withUrl(String v) {
        return new AgentConfig(id, name, v, cardPath, skill, verbatim, timeout, description, auth);
    }

    public AgentConfig withCardPath(String v) {
        return new AgentConfig(id, name, url, v, skill, verbatim, timeout, description, auth);
    }

    public AgentConfig withPassword(String v) {
        return new AgentConfig(id, name, url, cardPath, skill, verbatim, timeout, description, auth.withPassword(v));
    }
}
