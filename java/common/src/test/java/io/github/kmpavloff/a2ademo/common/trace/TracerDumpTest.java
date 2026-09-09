package io.github.kmpavloff.a2ademo.common.trace;

import org.junit.jupiter.api.Test;

import java.io.StringWriter;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Полный дамп тел A2A-обмена по A2A_DEBUG. Через форму возврата уезжает
 * настоящий номер карты, поэтому ни трейс, ни дамп не должны превращаться в
 * утечку.
 */
class TracerDumpTest {

    @Test
    void masksCardLikeDigitRuns() {
        assertEquals("оплата «номер карты скрыт» принята",
                Tracer.maskCardLike("оплата 4111 1111 1111 1111 принята"));
        assertEquals("«номер карты скрыт»", Tracer.maskCardLike("4111-1111-1111-1111"));
        assertEquals("заказ 1041", Tracer.maskCardLike("заказ 1041"), "короткие числа не трогаем");
    }

    @Test
    void dumpStaysSilentWithoutDebug() {
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", false, new Object[]{out}).dump("запрос", "{\"a\":1}");
        assertEquals("", out.toString());
    }

    @Test
    void dumpPrettyPrintsAndMasks() {
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", true, new Object[]{out}).dump("запрос агенту", "{\"card\":\"4111111111111111\"}");
        String s = out.toString();
        assertTrue(s.contains("⇄ запрос агенту:"), s);
        assertTrue(s.contains("«номер карты скрыт»"), s);
        assertTrue(!s.contains("4111111111111111"), "номер карты не должен попасть в лог");
        assertTrue(s.contains("\n"), "тело печатается с отступами: " + s);
    }

    @Test
    void dumpPrintsUnparseableBodiesAsIs() {
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", true, new Object[]{out}).dump("ответ", "не JSON");
        assertTrue(out.toString().contains("не JSON"));
    }

    @Test
    void dumpTruncatesHugeBodies() {
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", true, new Object[]{out}).dump("ответ", "x".repeat(200_000));
        assertTrue(out.toString().contains("… обрезано"), "длинное тело обязано обрезаться");
    }

    @Test
    void dumpTruncatesBasedOnUtf8Bytes() {
        // Cyrillic characters are 2 bytes in UTF-8 each. Create 33K Cyrillic chars
        // = 66KB in UTF-8, which exceeds 64KB limit. With char-based truncation,
        // this would NOT truncate (only 33K chars). With byte-based truncation,
        // it will truncate correctly.
        String cyrillicBody = "й".repeat(33_000); // Cyrillic letter й = 2 bytes in UTF-8
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", true, new Object[]{out}).dump("тест", cyrillicBody);
        String output = out.toString();
        assertTrue(output.contains("… обрезано"),
            "Cyrillic body with 66KB UTF-8 size must truncate at 64KB byte limit");
        // Verify the output is actually truncated (not the full body)
        assertTrue(output.length() < 100_000,
            "Output should be significantly smaller than input body");
    }
}
