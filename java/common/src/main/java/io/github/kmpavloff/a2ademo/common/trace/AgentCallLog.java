package io.github.kmpavloff.a2ademo.common.trace;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.fasterxml.jackson.databind.node.TextNode;
import java.io.IOException;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.HashMap;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Последние JSON-RPC-обмены оркестратора с удалёнными агентами по сессиям
 * (= contextId браузерного разговора), порт Go a2abridge.CallLog. Нужен панели
 * «A2A-протокол»: браузер видит только свой обмен с оркестратором, а запрос к
 * агенту (с metadata.skill и прочим) оставался невидимым. Память ограничена и
 * по записям на сессию, и по числу сессий. Потокобезопасен.
 */
public final class AgentCallLog {

    static final int MAX_CALLS_PER_SESSION = 50;
    static final int MAX_SESSIONS = 100;
    /** Предел на одно тело; обрезанное уходит JSON-строкой, чтобы не ломать ответ. */
    static final int MAX_BODY = 256 * 1024;

    private static final ObjectMapper MAPPER = new ObjectMapper();

    /**
     * Один обмен, как он прошёл по проводу. Тела — с замаскированными номерами
     * карт; response пуст, если ответа не было (error).
     */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    public record Call(long seq, String agentId, String agentName, String url, String method,
                       JsonNode request, JsonNode response, Integer status, String error, long tookMs) {

        Call withSeq(long s) {
            return new Call(s, agentId, agentName, url, method, request, response, status, error, tookMs);
        }
    }

    /** К какой сессии и какому агенту относится запрос — клиент видит байты, но не знает, чей это ход. */
    public record Tag(String session, String agentId, String agentName) {
    }

    private long seq;
    private final Map<String, Deque<Call>> sessions = new HashMap<>();
    private final Map<String, Boolean> order = new LinkedHashMap<>(); // для вытеснения старейшей сессии

    public synchronized void add(String session, Call c) {
        Deque<Call> calls = sessions.get(session);
        if (calls == null) {
            calls = new ArrayDeque<>();
            sessions.put(session, calls);
            order.put(session, Boolean.TRUE);
            if (order.size() > MAX_SESSIONS) {
                Iterator<String> it = order.keySet().iterator();
                sessions.remove(it.next());
                it.remove();
            }
        }
        calls.addLast(c.withSeq(++seq));
        while (calls.size() > MAX_CALLS_PER_SESSION) {
            calls.removeFirst();
        }
    }

    /** Обмены сессии с номером больше after — браузер передаёт последний уже показанный. */
    public synchronized List<Call> since(String session, long after) {
        List<Call> out = new ArrayList<>();
        Deque<Call> calls = sessions.get(session);
        if (calls != null) {
            for (Call c : calls) {
                if (c.seq() > after) {
                    out.add(c);
                }
            }
        }
        return out;
    }

    /**
     * Готовит тело для панели: номера карт маскируются (ответ на форму возврата
     * — это сам номер), не-JSON и слишком большое уходят строкой. Маскируются
     * только строки: число той же длины (метка времени в миллисекундах) картой
     * не бывает.
     */
    public static JsonNode body(String raw) {
        if (raw == null) {
            return null;
        }
        if (raw.length() <= MAX_BODY) {
            try {
                JsonNode n = MAPPER.readTree(raw);
                if (n != null) {
                    return mask(n);
                }
            } catch (IOException ignored) {
                // не JSON — уйдёт строкой ниже
            }
        }
        String s = Tracer.maskCardLike(raw);
        if (s.length() > MAX_BODY) {
            s = s.substring(0, MAX_BODY) + "… обрезано";
        }
        return TextNode.valueOf(s);
    }

    private static JsonNode mask(JsonNode n) {
        if (n.isTextual()) {
            return TextNode.valueOf(Tracer.maskCardLike(n.asText()));
        }
        if (n instanceof ObjectNode o) {
            List<String> names = new ArrayList<>();
            o.fieldNames().forEachRemaining(names::add);
            for (String k : names) {
                o.set(k, mask(o.get(k)));
            }
        } else if (n instanceof ArrayNode a) {
            for (int i = 0; i < a.size(); i++) {
                a.set(i, mask(a.get(i)));
            }
        }
        return n;
    }
}
