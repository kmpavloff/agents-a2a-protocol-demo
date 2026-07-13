package a2abridge

import "strings"

// cardDigits extracts the digit sequence from a free-text card-number reply,
// tolerating spaces, dashes and surrounding words. Returns "" when the reply
// carries no digits at all (treated as a cancellation at the card step).
func cardDigits(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// validCard reports whether digits looks like a real card number: 13–19 digits
// passing the Luhn checksum. Validation is done by CODE — the LLM never judges
// payment details.
func validCard(digits string) bool {
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// maskCard renders a card number as its last four digits for traces and user
// echoes; the full number is never logged or repeated back.
func maskCard(digits string) string {
	if len(digits) < 4 {
		return "••••"
	}
	return "•••• " + digits[len(digits)-4:]
}
