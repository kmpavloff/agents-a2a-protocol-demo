package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * Идентификаторы поверхностей A2UI, разведённые по ходам (порт хвоста
 * internal/a2ui/ingest.go).
 */
public final class Surfaces {

    private Surfaces() {}

    /** Сообщения, у которых есть surfaceId. */
    private static final List<String> SURFACE_ID_KEYS =
            List.of("createSurface", "updateComponents", "updateDataModel", "deleteSurface");

    /** Суффикс, который навешивает {@link #retag}: «-t» и номер хода. */
    private static final Pattern TURN_SUFFIX = Pattern.compile("-t\\d+$");

    /**
     * Снимает суффикс хода — обратная операция к {@link #retag}.
     *
     * <p>Суффикс распознаётся по форме, а не запоминается: клик может прийти на
     * любом последующем ходу, и номер, под которым поверхность создавалась, к
     * этому моменту уже не тот. Агентский id, сам оканчивающийся на «-t» с
     * цифрами, будет укорочен ошибочно — но это ровно тот исход, который без
     * снятия суффикса получался бы всегда.
     */
    public static String untag(String id) {
        if (id == null) {
            return "";
        }
        return TURN_SUFFIX.matcher(id).replaceAll("");
    }

    /**
     * Приписывает суффикс ко всем surfaceId в наборе сообщений. Ссылки внутри
     * набора переписываются согласованно, так что updateComponents по-прежнему
     * попадает в свою поверхность. Входные сообщения не изменяются.
     */
    @SuppressWarnings("unchecked")
    public static List<Map<String, Object>> retag(List<Map<String, Object>> msgs, String suffix) {
        if (suffix == null || suffix.isEmpty() || msgs == null) {
            return msgs;
        }
        List<Map<String, Object>> out = new ArrayList<>(msgs.size());
        for (Map<String, Object> m : msgs) {
            Map<String, Object> next = new LinkedHashMap<>(m);
            for (String key : SURFACE_ID_KEYS) {
                if (!(next.get(key) instanceof Map<?, ?> raw)) {
                    continue;
                }
                Map<String, Object> payload = (Map<String, Object>) raw;
                if (!(payload.get("surfaceId") instanceof String id) || id.isEmpty()) {
                    continue;
                }
                Map<String, Object> copied = new LinkedHashMap<>(payload);
                copied.put("surfaceId", id + suffix);
                next.put(key, copied);
            }
            out.add(next);
        }
        return out;
    }
}
