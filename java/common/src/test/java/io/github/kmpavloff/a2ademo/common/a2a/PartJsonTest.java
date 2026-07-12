package io.github.kmpavloff.a2ademo.common.a2a;

import io.github.kmpavloff.a2ademo.common.Json;
import org.junit.jupiter.api.Test;

import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;

/**
 * Pins the wire format to the a2a-go v2 JSON fixtures (a2a/json_test.go), so
 * the Java agents stay interoperable with the Go ones.
 */
class PartJsonTest {

    @Test
    void textPartMatchesGoFixture() throws Exception {
        assertEquals("{\"text\":\"hello, world\"}", Json.MAPPER.writeValueAsString(Part.text("hello, world")));
    }

    @Test
    void dataPartMatchesGoFixture() throws Exception {
        Part p = Part.data(Map.of("foo", "bar"), null);
        assertEquals("{\"data\":{\"foo\":\"bar\"}}", Json.MAPPER.writeValueAsString(p));
    }

    @Test
    void textPartWithMetadataMatchesGoFixture() throws Exception {
        Part p = Part.text("42");
        p.metadata = Map.of("foo", "bar");
        assertEquals("{\"text\":\"42\",\"metadata\":{\"foo\":\"bar\"}}", Json.MAPPER.writeValueAsString(p));
    }

    @Test
    void roundTripsGoTaskJson() throws Exception {
        String goTask = """
                {"id":"t1","contextId":"c1",
                 "status":{"state":"TASK_STATE_INPUT_REQUIRED",
                           "message":{"messageId":"m1","role":"ROLE_AGENT","parts":[{"text":"Какой заказ?"}]},
                           "timestamp":"2026-07-09T21:03:43.123456789Z"},
                 "artifacts":[{"artifactId":"a1","parts":[{"text":"Готово."},
                   {"data":{"order":{"id":"1041"}},"metadata":{"kind":"widget/order","version":1}}]}],
                 "history":[{"messageId":"m0","role":"ROLE_USER","parts":[{"text":"статус заказа 1041"}]}]}
                """;
        A2aTask t = Json.MAPPER.readValue(goTask, A2aTask.class);
        assertEquals("t1", t.id);
        assertEquals(TaskState.INPUT_REQUIRED, t.status.state);
        assertEquals("Какой заказ?", t.status.message.firstText());
        assertEquals("widget/order", t.artifacts.getFirst().parts.get(1).metadata.get("kind"));
        assertEquals("статус заказа 1041", t.history.getFirst().firstText());
    }

    @Test
    void messageSerializesGoFieldNames() throws Exception {
        A2aMessage m = A2aMessage.forTask(A2aMessage.ROLE_USER, "t1", "c1", Part.text("да"));
        m.messageId = "m2";
        assertEquals(
                "{\"messageId\":\"m2\",\"contextId\":\"c1\",\"taskId\":\"t1\",\"role\":\"ROLE_USER\",\"parts\":[{\"text\":\"да\"}]}",
                Json.MAPPER.writeValueAsString(m));
    }
}
