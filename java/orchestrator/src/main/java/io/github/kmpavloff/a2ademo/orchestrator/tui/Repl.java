package io.github.kmpavloff.a2ademo.orchestrator.tui;

import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.InvalidPathException;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;

/**
 * Minimal line-oriented REPL (port of internal/tui/repl.go): widgets from the
 * worker render inline as cards; when a widget was shown this turn, a verbose
 * text echo is suppressed so the same data is not displayed twice.
 */
public class Repl {
    private static final String CYAN = "\u001B[36m";
    private static final String GRAY = "\u001B[90m";
    private static final String RESET = "\u001B[0m";

    private final OrchestratorAgent agent;
    private final OrdersClient orders;

    // Flips when a widget renders during the current turn. Safe as a plain
    // field: the handler fires synchronously on the REPL thread.
    private boolean widgetShown;

    public Repl(OrchestratorAgent agent, OrdersClient orders) {
        this.agent = agent;
        this.orders = orders;
    }

    public void run() throws IOException {
        System.out.printf("%sАссистент службы заказов.%s Введите запрос или 'exit' для выхода.%n", CYAN, RESET);
        orders.setWidgetHandler((sessionId, w) -> {
            renderWidget(w);
            widgetShown = true;
        });
        // Files (e.g. the refund receipt) are saved next to the REPL and the
        // path is printed, since a terminal cannot "download" anything.
        orders.setFileHandler((sessionId, filename, mediaType, data) -> {
            try {
                Path name = Path.of(Path.of(filename).getFileName().toString());
                Files.write(name, data);
                System.out.printf("%s💾 Квитанция сохранена: ./%s%s%n", CYAN, name, RESET);
            } catch (IOException | InvalidPathException e) {
                System.out.printf("%s[файл %s не сохранён: %s]%s%n", GRAY, filename, e.getMessage(), RESET);
            }
        });

        BufferedReader in = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
        final String sessionId = "tui-session";
        while (true) {
            System.out.printf("%sвы>%s ", CYAN, RESET);
            String line = in.readLine();
            if (line == null || line.equals("exit") || line.equals("quit") || line.equals("выход")) {
                return;
            }
            if (line.isEmpty()) {
                continue;
            }
            widgetShown = false;
            System.out.printf("%s  · агент → LLM: запрос%s%n", GRAY, RESET);
            String answer;
            try {
                answer = agent.runTurn(sessionId, line, new OrchestratorAgent.TurnListener() {
                    @Override
                    public void onToolCall(String name, String argsJson) {
                        System.out.printf("%s  · LLM → агент: вызвать %s(%s)%s%n", GRAY, name,
                                argsJson == null ? "" : argsJson.trim(), RESET);
                    }

                    @Override
                    public void onToolResult(String name) {
                        System.out.printf("%s  · инструмент → LLM: результат %s, снова спрашиваю LLM%s%n",
                                GRAY, name, RESET);
                    }
                });
            } catch (RuntimeException e) {
                System.out.printf("%s[ошибка] %s%s%n", GRAY, e.getMessage(), RESET);
                continue;
            }
            // When a widget was shown, keep only a short comment; drop a full
            // restatement so the text doesn't duplicate the widget.
            if (!answer.isEmpty() && (!widgetShown || isBriefComment(answer))) {
                System.out.printf("%sассистент>%s %s%n", CYAN, RESET, answer);
            }
        }
    }

    /** Renders a structured widget the worker returned in an A2A DataPart. */
    static void renderWidget(Map<String, Object> w) {
        String title = str(w.get("title"));
        System.out.printf("%n%s┌─ %s%s%n", CYAN, title, RESET);
        switch (String.valueOf(w.get("_kind"))) {
            case "widget/order" -> {
                if (w.get("order") instanceof Map<?, ?> o) {
                    field("Товар:", o.get("item"));
                    field("Статус:", o.get("status_label"));
                    if (o.get("amount") != null) {
                        System.out.printf("%s│%s %-9s %s %s%n", CYAN, RESET, "Сумма:", o.get("amount"), o.get("currency"));
                    }
                    field("Клиент:", o.get("customer"));
                    field("Дата:", o.get("created"));
                    field("Ссылка:", o.get("url"));
                }
            }
            case "widget/refund_receipt" -> {
                field("Квитанция №:", w.get("receipt_id"));
                System.out.printf("%s│%s %-12s #%s — %s%n", CYAN, RESET, "Заказ:", w.get("order_id"), w.get("item"));
                if (w.get("amount") != null) {
                    System.out.printf("%s│%s %-12s %s %s%n", CYAN, RESET, "Сумма:", w.get("amount"), w.get("currency"));
                }
                if (w.get("card_last4") instanceof String last4 && !last4.isEmpty()) {
                    System.out.printf("%s│%s %-12s •••• %s%n", CYAN, RESET, "Карта:", last4);
                }
                field("Дата:", w.get("created"));
            }
            case "widget/order_list" -> {
                if (w.get("orders") instanceof List<?> rows) {
                    for (Object r : rows) {
                        if (r instanceof Map<?, ?> o) {
                            String link = o.get("url") instanceof String u && !u.isEmpty() ? "  → " + u : "";
                            System.out.printf("%s│%s #%s  %s — %s (%s %s, %s)%s%n", CYAN, RESET,
                                    o.get("id"), o.get("item"), o.get("status_label"),
                                    o.get("amount"), o.get("currency"), o.get("created"), link);
                        }
                    }
                }
            }
            default -> { // widget/confirmation and any future dialog-style widget
                String message = str(w.get("message"));
                if (!message.isEmpty()) {
                    System.out.printf("%s│%s %s%n", CYAN, RESET, message);
                }
                String labels = actionLabels(w.get("actions"));
                if (!labels.isEmpty()) {
                    System.out.printf("%s│%s %s%n", CYAN, RESET, labels);
                }
            }
        }
        System.out.printf("%s└─%s%n", CYAN, RESET);
    }

    private static void field(String label, Object v) {
        if (v != null && !String.valueOf(v).isEmpty()) {
            System.out.printf("%s│%s %-9s %s%n", CYAN, RESET, label, v);
        }
    }

    /** Renders a widget's action buttons as "[Label]  [Label]". */
    static String actionLabels(Object v) {
        if (!(v instanceof List<?> actions)) {
            return "";
        }
        StringBuilder b = new StringBuilder();
        for (Object a : actions) {
            if (a instanceof Map<?, ?> m && m.get("label") instanceof String l) {
                if (!b.isEmpty()) {
                    b.append("  ");
                }
                b.append("[").append(l).append("]");
            }
        }
        return b.toString();
    }

    /**
     * Whether an assistant reply is a short lead-in worth keeping next to a
     * widget, versus a restatement of the widget's own data.
     */
    static boolean isBriefComment(String s) {
        s = s.trim();
        if (s.isEmpty()) {
            return false;
        }
        if (s.codePointCount(0, s.length()) > 200) {
            return false;
        }
        if (s.chars().filter(c -> c == '|').count() >= 2) { // markdown table
            return false;
        }
        return s.chars().filter(c -> c == '\n').count() <= 2;
    }

    private static String str(Object o) {
        return o == null ? "" : String.valueOf(o);
    }
}
