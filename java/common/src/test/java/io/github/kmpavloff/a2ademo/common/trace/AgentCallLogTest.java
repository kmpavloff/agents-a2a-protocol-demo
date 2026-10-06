package io.github.kmpavloff.a2ademo.common.trace;

import com.fasterxml.jackson.databind.JsonNode;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Журнал обменов с агентами для панели «A2A-протокол» — то же, что Go CallLog. */
class AgentCallLogTest {

    private static AgentCallLog.Call call() {
        return new AgentCallLog.Call(0, "a", "A", "http://x", "SendMessage", null, null, null, null, 0);
    }

    // Маскируются строки, а не числа: метка времени в миллисекундах той же
    // длины, что и номер карты, и её замена сломала бы JSON.
    @Test
    void masksOnlyStrings() {
        JsonNode n = AgentCallLog.body("{\"ts\":1728200000000,\"text\":\"4111-1111-1111-1111\"}");
        assertEquals(1728200000000L, n.path("ts").asLong());
        assertFalse(n.path("text").asText().contains("4111"), n.toString());
        assertTrue(AgentCallLog.body("не json").isTextual());
    }

    // Память журнала ограничена и по записям, и по сессиям.
    @Test
    void isBounded() {
        AgentCallLog l = new AgentCallLog();
        for (int i = 0; i < AgentCallLog.MAX_CALLS_PER_SESSION + 5; i++) {
            l.add("a", call());
        }
        assertEquals(AgentCallLog.MAX_CALLS_PER_SESSION, l.since("a", 0).size());
        for (int i = 0; i < AgentCallLog.MAX_SESSIONS; i++) {
            l.add("s" + i, call());
        }
        assertEquals(0, l.since("a", 0).size(), "старейшая сессия должна быть вытеснена");
    }
}
