package a2abridge

import "testing"

func TestCardDigits(t *testing.T) {
	cases := map[string]string{
		"4111 1111 1111 1111":       "4111111111111111",
		"карта 4111-1111-1111-1111": "4111111111111111",
		"нет, передумал":            "",
		"":                          "",
	}
	for in, want := range cases {
		if got := cardDigits(in); got != want {
			t.Errorf("cardDigits(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidCard(t *testing.T) {
	valid := []string{
		"4111111111111111", // Visa test number
		"5555555555554444", // Mastercard test number
		"2200000000000053", // 16-digit Luhn-valid
	}
	for _, d := range valid {
		if !validCard(d) {
			t.Errorf("validCard(%q) = false, want true", d)
		}
	}
	invalid := []string{
		"4111111111111112", // Luhn fails
		"1234567890123456", // Luhn fails
		"411111111111",     // too short (12)
		"41111111111111111111", // too long (20)
		"",
	}
	for _, d := range invalid {
		if validCard(d) {
			t.Errorf("validCard(%q) = true, want false", d)
		}
	}
}

func TestMaskCard(t *testing.T) {
	if got := maskCard("4111111111111111"); got != "•••• 1111" {
		t.Errorf("maskCard = %q", got)
	}
	if got := maskCard("12"); got != "••••" {
		t.Errorf("maskCard short = %q", got)
	}
}
