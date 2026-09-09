package io.github.kmpavloff.a2ademo.common.config;

import java.time.Duration;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Разбор длительности в синтаксисе Go ("180s", "1m30s"). Нужен потому, что
 * конфиг общий с Go-реализацией, а {@link Duration#parse} ждёт ISO-8601
 * ("PT180S") и такую строку не примет.
 */
public final class GoDuration {

    private GoDuration() {}

    private static final Pattern UNIT = Pattern.compile("(\\d+(?:\\.\\d+)?)(ns|us|µs|ms|s|m|h)");

    /** @throws IllegalArgumentException если строка пуста или разобрана не целиком */
    public static Duration parse(String s) {
        String in = s == null ? "" : s.trim();
        if (in.isEmpty()) {
            throw new IllegalArgumentException("empty duration");
        }
        boolean negative = in.startsWith("-");
        if (negative || in.startsWith("+")) {
            in = in.substring(1);
        }
        Matcher m = UNIT.matcher(in);
        long nanos = 0;
        int consumed = 0;
        while (m.find() && m.start() == consumed) {
            double value = Double.parseDouble(m.group(1));
            nanos += Math.round(value * unitNanos(m.group(2)));
            consumed = m.end();
        }
        if (consumed != in.length()) {
            throw new IllegalArgumentException("bad duration: " + s);
        }
        return Duration.ofNanos(negative ? -nanos : nanos);
    }

    private static long unitNanos(String unit) {
        return switch (unit) {
            case "ns" -> 1L;
            case "us", "µs" -> 1_000L;
            case "ms" -> 1_000_000L;
            case "s" -> 1_000_000_000L;
            case "m" -> 60L * 1_000_000_000L;
            case "h" -> 3_600L * 1_000_000_000L;
            default -> throw new IllegalArgumentException("unknown unit: " + unit);
        };
    }
}
