package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * OrdersClient as a thin delegating tool: it must route every layer of a
 * {@link Remote.Reply} to its handler and hand the model only the text — the
 * protocol work behind that reply is Remote's, and is tested in RemoteTest.
 */
class OrdersClientTest {

    /** Remote.ask returns a canned reply instead of talking to the network. */
    private static class StubRemote extends Remote {
        private final Reply reply;

        StubRemote(Reply reply) {
            super(new AgentConfig("stub", "", "http://127.0.0.1:1", "", List.of(), false, "", "", AuthConfig.NONE),
                    Tracer.noop());
            this.reply = reply;
        }

        @Override
        public Reply ask(String sessionId, String text, boolean wantA2ui) {
            return reply;
        }
    }

    // Виджеты, разметка, файлы и собственный текст агента уходят в UI мимо
    // модели; модели достаётся только текстовый результат инструмента.
    @Test
    void forwardsEveryLayerToItsHandler() {
        Remote remote = new StubRemote(new Remote.Reply(
                "Заказ 1041 доставлен", false,
                List.of(Map.of("version", A2ui.VERSION)),
                List.of(Map.of("_kind", "widget/order")),
                List.of(new Remote.AttachedFile("receipt.html", "text/html", new byte[]{1}))));

        List<Map<String, Object>> widgets = new ArrayList<>();
        List<Map<String, Object>> a2ui = new ArrayList<>();
        List<String> files = new ArrayList<>();
        List<String> texts = new ArrayList<>();

        OrdersClient c = new OrdersClient(remote, Tracer.noop());
        c.setWidgetHandler((s, w) -> widgets.add(w));
        c.setA2uiHandler((s, msgs) -> a2ui.addAll(msgs));
        c.setFileHandler((s, name, mime, data) -> files.add(name));
        c.setTextHandler((s, t) -> texts.add(t));

        assertEquals("Заказ 1041 доставлен", c.ask("s1", "статус 1041", true));
        assertEquals(1, widgets.size());
        assertEquals(1, a2ui.size());
        assertEquals(List.of("receipt.html"), files);
        assertEquals(List.of("Заказ 1041 доставлен"), texts);
    }

    // Задачу, вставшую в input-required, модель обязана увидеть как вопрос.
    @Test
    void marksAPendingAnswerForTheModel() {
        Remote remote = new StubRemote(new Remote.Reply(
                "Подтвердите возврат", true, List.of(), List.of(), List.of()));
        assertEquals("NEEDS_USER_INPUT: Подтвердите возврат",
                new OrdersClient(remote, Tracer.noop()).ask("s1", "верни 1041", false));
    }

    @Test
    void emptyMessageRepliesEscalateToStop() {
        OrdersClient c = new OrdersClient(
                new StubRemote(new Remote.Reply("", false, List.of(), List.of(), List.of())), Tracer.noop());
        OrdersClient.EmptyReply first = c.emptyMessageReply("s1");
        assertTrue(!first.stop());
        OrdersClient.EmptyReply second = c.emptyMessageReply("s1");
        assertTrue(second.stop());
        c.clearEmpty("s1");
        assertTrue(!c.emptyMessageReply("s1").stop(), "counter resets after a real message");
    }
}
