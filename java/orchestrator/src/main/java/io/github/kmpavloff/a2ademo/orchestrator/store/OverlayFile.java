package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.config.ConfigLoader;
import io.github.kmpavloff.a2ademo.common.config.TlsConfig;
import org.yaml.snakeyaml.DumperOptions;
import org.yaml.snakeyaml.Yaml;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** Overlay-файл со списком агентов: чтение и атомарная запись. */
public final class OverlayFile {

    private OverlayFile() {}

    /** Объясняет тому, кто откроет файл руками, почему его правки проживут до первого сохранения из UI. */
    private static final String HEADER = """
            # Управляется из веб-интерфейса (экран «Настройки»).
            # Правки руками будут перезаписаны при следующем сохранении.
            """;

    /** Читает overlay. Отсутствие файла — обычное состояние свежего клона, а не ошибка. */
    @SuppressWarnings("unchecked")
    public static List<AgentOverride> load(Path path) {
        if (!Files.exists(path)) {
            return List.of();
        }
        Map<String, Object> doc;
        try (InputStream in = Files.newInputStream(path)) {
            doc = new Yaml().load(in);
        } catch (IOException e) {
            throw new IllegalStateException("read agents overlay " + path + ": " + e.getMessage(), e);
        } catch (RuntimeException e) {
            throw new IllegalStateException("parse agents overlay " + path + ": " + e.getMessage(), e);
        }
        if (doc == null || !(doc.get("agents") instanceof List<?> list)) {
            return List.of();
        }
        List<AgentOverride> out = new ArrayList<>(list.size());
        for (Object item : list) {
            if (!(item instanceof Map<?, ?> raw)) {
                continue;
            }
            Map<String, Object> a = (Map<String, Object>) raw;
            Map<String, Object> auth = a.get("auth") instanceof Map<?, ?> m
                    ? (Map<String, Object>) m : Map.of();
            out.add(new AgentOverride(new AgentConfig(
                    str(a, "id"), str(a, "name"), str(a, "url"), str(a, "card_path"),
                    AgentConfig.skillsFrom(a.get("skills"), a.get("skill")),
                    Boolean.TRUE.equals(a.get("verbatim")), str(a, "timeout"), str(a, "description"),
                    new AuthConfig(str(auth, "type"), str(auth, "username"), str(auth, "password")),
                    ConfigLoader.tls(a.get("tls") instanceof Map<?, ?> t ? (Map<String, Object>) t : Map.of())),
                    Boolean.TRUE.equals(a.get("hidden"))));
        }
        return List.copyOf(out);
    }

    /**
     * Пишет overlay целиком, атомарно: сначала временный файл рядом, потом
     * переименование. Оборванная запись иначе оставила бы половину списка
     * агентов, и оркестратор не поднялся бы вовсе.
     */
    public static void save(Path path, List<AgentOverride> over) {
        List<Object> agents = new ArrayList<>(over.size());
        for (AgentOverride o : over) {
            AgentConfig a = o.agent();
            Map<String, Object> m = new LinkedHashMap<>();
            m.put("id", a.id());
            m.put("name", a.name());
            m.put("url", a.url());
            m.put("card_path", a.cardPath());
            // Как omitempty у Go: пустой список не пишется.
            if (!a.skills().isEmpty()) {
                m.put("skills", new ArrayList<>(a.skills()));
            }
            m.put("verbatim", a.verbatim());
            m.put("timeout", a.timeout());
            m.put("description", a.description());
            // Явные put в порядке полей Go, а не Map.of(...): у Map.of порядок
            // итерации SALT-рандомизирован, и файл переписывался бы в разном
            // порядке ключей на каждый запуск JVM — не проблема для чтения (оба
            // читателя ищут по ключу), но плохо для файла, который открывают руками.
            Map<String, Object> auth = new LinkedHashMap<>();
            auth.put("type", a.auth().type());
            auth.put("username", a.auth().username());
            auth.put("password", a.auth().password());
            m.put("auth", auth);
            // Блок tls пишется только когда задан, и только непустые ключи —
            // как omitempty у Go: overlay обоих оркестраторов выглядит одинаково.
            TlsConfig t = a.tls();
            if (t.enabled()) {
                Map<String, Object> tls = new LinkedHashMap<>();
                putIf(tls, "cert_file", t.certFile());
                putIf(tls, "key_file", t.keyFile());
                putIf(tls, "ca_file", t.caFile());
                if (t.insecureSkipVerify()) {
                    tls.put("insecure_skip_verify", true);
                }
                m.put("tls", tls);
            }
            if (o.hidden()) {
                m.put("hidden", true);
            }
            agents.add(m);
        }
        DumperOptions opts = new DumperOptions();
        opts.setDefaultFlowStyle(DumperOptions.FlowStyle.BLOCK);
        String body = new Yaml(opts).dump(Map.of("agents", agents));

        Path dir = path.toAbsolutePath().getParent();
        Path tmp = null;
        try {
            Files.createDirectories(dir);
            // Временный файл — в том же каталоге: переименование атомарно только
            // в пределах одной файловой системы.
            tmp = Files.createTempFile(dir, ".agents-", ".yaml");
            try {
                // Пароли лежат открытым текстом, как и в orchestrator.yaml.
                Files.setPosixFilePermissions(tmp, PosixFilePermissions.fromString("rw-------"));
            } catch (UnsupportedOperationException ignored) {
                // не POSIX-система — пропускаем
            } catch (IOException e) {
                // chmod реально не удался (ACL, квота, диск) — в отличие от
                // отсутствия POSIX-прав, это не повод молча писать файл с
                // паролями в открытом доступе.
                throw new IllegalStateException("chmod temp overlay " + tmp + ": " + e.getMessage(), e);
            }
            Files.writeString(tmp, HEADER + body, StandardCharsets.UTF_8);
            Files.move(tmp, path, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            tmp = null;
        } catch (IOException e) {
            throw new IllegalStateException("write agents overlay " + path + ": " + e.getMessage(), e);
        } finally {
            if (tmp != null) {
                try {
                    Files.deleteIfExists(tmp);
                } catch (IOException ignored) {
                    // временный файл останется — на работу это не влияет
                }
            }
        }
    }

    private static void putIf(Map<String, Object> m, String key, String v) {
        if (!v.isEmpty()) {
            m.put(key, v);
        }
    }

    private static String str(Map<String, Object> m, String key) {
        Object v = m.get(key);
        return v == null ? "" : String.valueOf(v);
    }
}
