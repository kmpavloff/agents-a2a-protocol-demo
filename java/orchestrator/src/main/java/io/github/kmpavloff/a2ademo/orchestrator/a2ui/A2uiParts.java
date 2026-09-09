package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import io.github.kmpavloff.a2ademo.common.a2a.Part;

import java.time.Instant;
import java.time.format.DateTimeFormatter;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * Всё знание о том, как A2UI выглядит частью A2A-сообщения (порт
 * a2abridge/a2uipart.go).
 *
 * <p>Правило: принимаем либерально (обе пометки, оба MIME — см.
 * {@link A2ui#isA2ui}), отдаём строго по спеке.
 */
public final class A2uiParts {

    private A2uiParts() {}

    /**
     * Часть с разметкой. Полезная нагрузка — массив сообщений, как требует
     * спека («The data field ... MUST be an array of messages»), даже когда
     * сообщение одно.
     *
     * <p>Пометок ставится две: metadata.mimeType — та, по которой часть опознаёт
     * стандартный клиент, и mediaType — родное поле A2A, по которому её узнаёт
     * наш собственный код и живые агенты. Вторая ничему не мешает и не заменяет
     * первую.
     */
    public static Part message(List<Map<String, Object>> msgs) {
        Part p = Part.data(new ArrayList<Object>(msgs), Map.of(A2ui.MIME_KEY, A2ui.MIME_TYPE));
        p.mediaType = A2ui.MIME_TYPE;
        return p;
    }

    /**
     * Событие клиента (нажатие кнопки) по схеме client_to_server: тот же тип
     * части, но полезная нагрузка — действие.
     */
    public static Part action(String name, String surfaceId, String sourceComponentId,
                              Map<String, Object> ctx, Instant at) {
        String ts = DateTimeFormatter.ISO_INSTANT.format(at.truncatedTo(ChronoUnit.SECONDS));
        return message(List.of(A2ui.newAction(name, surfaceId, sourceComponentId, ctx, ts)));
    }

    /** Несёт ли часть разметку A2UI. */
    public static boolean isA2ui(Part p) {
        return p != null && A2ui.isA2ui(p.mediaType, p.metadata);
    }
}
