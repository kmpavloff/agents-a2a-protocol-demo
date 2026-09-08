package io.github.kmpavloff.a2ademo.common.config;

/**
 * Как аутентифицироваться у удалённого агента. Поддерживается только HTTP
 * Basic — единственная схема, встреченная у живых агентов этого демо.
 */
public record AuthConfig(String type, String username, String password) {

    public static final AuthConfig NONE = new AuthConfig("", "", "");

    public AuthConfig {
        type = type == null ? "" : type;
        username = username == null ? "" : username;
        password = password == null ? "" : password;
    }

    /** Нужно ли подставлять заголовок Basic на каждый запрос. */
    public boolean basic() {
        return type.equalsIgnoreCase("basic") && !username.isEmpty();
    }

    public AuthConfig withPassword(String p) {
        return new AuthConfig(type, username, p);
    }
}
