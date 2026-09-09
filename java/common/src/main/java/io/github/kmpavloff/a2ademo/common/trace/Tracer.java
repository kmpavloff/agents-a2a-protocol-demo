package io.github.kmpavloff.a2ademo.common.trace;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.io.PrintStream;
import java.io.Writer;
import java.nio.charset.StandardCharsets;
import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.List;
import java.util.regex.Pattern;

/**
 * Line-oriented protocol tracer, mirroring the Go a2abridge.Tracer output:
 * {@code 2026/07/12 10:00:00 [A2A worker] message}. Thread-safe.
 */
public final class Tracer {
    private static final DateTimeFormatter TS = DateTimeFormatter.ofPattern("yyyy/MM/dd HH:mm:ss");

    /**
     * Включает дамп полных тел A2A-обмена. Отдельная переменная, а не поле
     * конфига: это инструмент разбирательства, который включают на один прогон.
     */
    public static final String DEBUG_ENV_VAR = "A2A_DEBUG";

    /**
     * Предел на одну запись. Разметка A2UI бывает под два десятка килобайт, и
     * без предела один ход способен утопить весь лог.
     */
    private static final int MAX_DUMP_BYTES = 64 * 1024;

    /**
     * Последовательность из 13–19 цифр, возможно разбитая пробелами или
     * дефисами. Маскируется везде, где текст попадает в лог.
     */
    private static final Pattern CARD_LIKE = Pattern.compile("\\b(?:\\d[ -]?){13,19}\\b");

    private static final String CARD_MASK = "«номер карты скрыт»";

    private static final ObjectMapper DUMP_MAPPER = new ObjectMapper();

    private final List<Object> sinks = new ArrayList<>(); // PrintStream or Writer
    private final String prefix;
    private final boolean debug;

    public Tracer(String prefix, Object... sinks) {
        this(prefix, debugEnabled(), sinks);
    }

    Tracer(String prefix, boolean debug, Object[] sinks) {
        this.prefix = prefix;
        this.debug = debug;
        for (Object s : sinks) {
            if (s != null) {
                this.sinks.add(s);
            }
        }
    }

    /** Прячет номера карт в произвольном тексте. */
    public static String maskCardLike(String s) {
        return s == null ? "" : CARD_LIKE.matcher(s).replaceAll(CARD_MASK);
    }

    /** Читает A2A_DEBUG. Пустое и «0»/«false» — выключено; любое иное значение включает. */
    private static boolean debugEnabled() {
        String v = System.getenv(DEBUG_ENV_VAR);
        if (v == null || v.isBlank()) {
            return false;
        }
        return !v.equalsIgnoreCase("0") && !v.equalsIgnoreCase("false");
    }

    /** Включён ли дамп тел. Нужен вызывающему, чтобы не собирать дорогой JSON впустую. */
    public boolean debug() {
        return debug;
    }

    /**
     * Печатает тело A2A-обмена целиком: label — что это и в какую сторону.
     * Молчит, когда debug выключен.
     *
     * <p>JSON переформатируется с отступами: читать однострочный ответ с
     * разметкой A2UI невозможно, а ради этого дамп и включают. Неразбираемое
     * тело печатается как есть — в разбирательстве важнее увидеть мусор, чем не
     * увидеть ничего.
     */
    public void dump(String label, String body) {
        if (!debug || body == null || body.isEmpty()) {
            return;
        }
        String out = body;
        try {
            out = DUMP_MAPPER.writerWithDefaultPrettyPrinter()
                    .writeValueAsString(DUMP_MAPPER.readTree(body));
        } catch (Exception ignored) {
            // тело не JSON — печатаем как есть
        }
        out = maskCardLike(out);
        String suffix = "";
        // Измеряем размер в UTF-8 байтах, обрезаем на границе символа если нужно.
        // Go обрезает точно в байте, что может расколоть многобайтовую последовательность.
        // Здесь обрезаем на границе UTF-8 последовательности: отступаем от бюджета,
        // пока видим байты продолжения (форма 10xxxxxx), затем декодируем префикс.
        byte[] outBytes = out.getBytes(StandardCharsets.UTF_8);
        if (outBytes.length > MAX_DUMP_BYTES) {
            int cut = MAX_DUMP_BYTES;
            // Отступаем к началу UTF-8 последовательности: продолжающие байты имеют форму 10xxxxxx.
            // Иначе на границе обреза оставался бы разрубленный символ — ровно та болячка,
            // которая есть у Go, и повторять её незачем.
            while (cut > 0 && (outBytes[cut] & 0xC0) == 0x80) {
                cut--;
            }
            out = new String(outBytes, 0, cut, StandardCharsets.UTF_8);
            suffix = System.lineSeparator() + "    … обрезано";
        }
        logf("⇄ %s:%n    %s%s", label, out.replace("\n", System.lineSeparator() + "    "), suffix);
    }

    public static Tracer noop() {
        return new Tracer("");
    }

    public synchronized void logf(String fmt, Object... args) {
        if (sinks.isEmpty()) {
            return;
        }
        String line = TS.format(LocalDateTime.now()) + " " + prefix + String.format(fmt, args) + System.lineSeparator();
        for (Object s : sinks) {
            try {
                if (s instanceof PrintStream ps) {
                    ps.print(line);
                    ps.flush();
                } else if (s instanceof Writer w) {
                    w.write(line);
                    w.flush();
                }
            } catch (IOException ignored) {
                // tracing must never break the agent
            }
        }
    }
}
