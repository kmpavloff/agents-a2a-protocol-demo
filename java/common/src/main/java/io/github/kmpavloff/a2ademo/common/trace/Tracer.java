package io.github.kmpavloff.a2ademo.common.trace;

import java.io.IOException;
import java.io.PrintStream;
import java.io.Writer;
import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.List;

/**
 * Line-oriented protocol tracer, mirroring the Go a2abridge.Tracer output:
 * {@code 2026/07/12 10:00:00 [A2A worker] message}. Thread-safe.
 */
public final class Tracer {
    private static final DateTimeFormatter TS = DateTimeFormatter.ofPattern("yyyy/MM/dd HH:mm:ss");

    private final List<Object> sinks = new ArrayList<>(); // PrintStream or Writer
    private final String prefix;

    public Tracer(String prefix, Object... sinks) {
        this.prefix = prefix;
        for (Object s : sinks) {
            if (s != null) {
                this.sinks.add(s);
            }
        }
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
