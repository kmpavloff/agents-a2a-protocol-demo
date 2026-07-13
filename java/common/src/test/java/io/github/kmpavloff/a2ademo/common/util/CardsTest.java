package io.github.kmpavloff.a2ademo.common.util;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Port of the Go a2abridge card tests. */
class CardsTest {

    @Test
    void digitsExtraction() {
        assertEquals("4111111111111111", Cards.digits("4111 1111 1111 1111"));
        assertEquals("4111111111111111", Cards.digits("карта 4111-1111-1111-1111"));
        assertEquals("", Cards.digits("нет, передумал"));
        assertEquals("", Cards.digits(null));
    }

    @Test
    void luhnValidation() {
        assertTrue(Cards.valid("4111111111111111"), "Visa test number");
        assertTrue(Cards.valid("5555555555554444"), "Mastercard test number");
        assertFalse(Cards.valid("4111111111111112"), "Luhn fails");
        assertFalse(Cards.valid("1234567890123456"), "Luhn fails");
        assertFalse(Cards.valid("411111111111"), "too short");
        assertFalse(Cards.valid("41111111111111111111"), "too long");
        assertFalse(Cards.valid(""));
    }

    @Test
    void masking() {
        assertEquals("•••• 1111", Cards.mask("4111111111111111"));
        assertEquals("••••", Cards.mask("12"));
    }
}
