package io.github.kmpavloff.a2ademo.worker.agent;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Port of the Go parseAffirmative table test: refunds fail CLOSED. */
class ParseAffirmativeTest {

    @Test
    void affirmativeReplies() {
        for (String s : new String[]{
                "да", "Да!", "да, давай", "конечно", "ок", "ok", "yes", "yep",
                "подтверждаю", "оформляй", "да, подтверждаю", "давай оформляй"}) {
            assertTrue(WorkerAgent.parseAffirmative(s), s);
        }
    }

    @Test
    void negativeOrHedgedRepliesAreRefusals() {
        for (String s : new String[]{
                "нет", "не надо", "отмена", "отмени", "потом", "наверное",
                "да нет", "не подтверждаю", "", "   ", "123", "да, но позже"}) {
            assertFalse(WorkerAgent.parseAffirmative(s), "\"" + s + "\"");
        }
    }
}
