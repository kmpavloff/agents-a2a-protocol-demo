package io.github.kmpavloff.a2ademo.common.util;

/**
 * Card-number helpers for the refund HITL flow (port of a2abridge/card.go).
 * Validation and masking are done by CODE — the LLM never judges or sees
 * payment details in full.
 */
public final class Cards {

    private Cards() {}

    /**
     * Extracts the digit sequence from a free-text card-number reply,
     * tolerating spaces, dashes and surrounding words. Returns "" when the
     * reply carries no digits at all (treated as a cancellation).
     */
    public static String digits(String text) {
        if (text == null) {
            return "";
        }
        StringBuilder b = new StringBuilder();
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (c >= '0' && c <= '9') {
                b.append(c);
            }
        }
        return b.toString();
    }

    /** Whether digits looks like a real card number: 13–19 digits passing Luhn. */
    public static boolean valid(String digits) {
        if (digits == null || digits.length() < 13 || digits.length() > 19) {
            return false;
        }
        int sum = 0;
        boolean doubleIt = false;
        for (int i = digits.length() - 1; i >= 0; i--) {
            int d = digits.charAt(i) - '0';
            if (doubleIt) {
                d *= 2;
                if (d > 9) {
                    d -= 9;
                }
            }
            sum += d;
            doubleIt = !doubleIt;
        }
        return sum % 10 == 0;
    }

    /** Last four digits for traces and user echoes; never the full number. */
    public static String mask(String digits) {
        if (digits == null || digits.length() < 4) {
            return "••••";
        }
        return "•••• " + digits.substring(digits.length() - 4);
    }
}
