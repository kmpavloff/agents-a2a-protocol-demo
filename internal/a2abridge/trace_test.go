package a2abridge

import (
	"bytes"
	"strings"
	"testing"
)

func debugTracer(w *bytes.Buffer) *Tracer {
	t := NewTracer(w, "")
	t.debug = true
	return t
}

// Через DataPart формы возврата уезжает настоящий номер карты. Дамп включают,
// чтобы разобраться в чужой проблеме, и он не должен превращаться в утечку.
func TestDumpMasksCardNumbers(t *testing.T) {
	var buf bytes.Buffer
	body := []byte(`{"parts":[{"data":[{"action":{"context":{"card_number":"4111 1111 1111 1111","order_id":"1041"}}}]}]}`)
	debugTracer(&buf).Dump("запрос агенту", body)

	out := buf.String()
	if strings.Contains(out, "4111") {
		t.Errorf("номер карты утёк в дамп:\n%s", out)
	}
	if !strings.Contains(out, "номер карты скрыт") {
		t.Errorf("нет пометки о маскировании:\n%s", out)
	}
	// Остальное должно остаться читаемым, иначе дамп бесполезен.
	if !strings.Contains(out, "1041") || !strings.Contains(out, "action") {
		t.Errorf("маскирование съело полезное содержимое:\n%s", out)
	}
}

// Номер карты не должен попадать в лог и без debug-режима: ответом на форму
// возврата служит сам номер, и обычная строка трейса печатала бы его целиком.
func TestMaskCardLike(t *testing.T) {
	for _, in := range []string{"4111 1111 1111 1111", "4111111111111111", "4111-1111-1111-1111"} {
		if got := MaskCardLike(in); strings.Contains(got, "4111") {
			t.Errorf("номер %q не замаскирован: %q", in, got)
		}
	}
	// Короткие числа — не карты: номер заказа обязан остаться читаемым.
	if got := MaskCardLike("заказ 1041"); got != "заказ 1041" {
		t.Errorf("номер заказа пострадал: %q", got)
	}
}

// Выключенный дамп обязан молчать: иначе каждый ход тащил бы в лог десятки
// килобайт разметки.
func TestDumpSilentWithoutDebug(t *testing.T) {
	var buf bytes.Buffer
	NewTracer(&buf, "").Dump("запрос агенту", []byte(`{"a":1}`))
	if buf.Len() != 0 {
		t.Errorf("дамп напечатался без A2A_DEBUG: %q", buf.String())
	}
}

// Однострочный ответ с разметкой A2UI читать невозможно — ради этого дамп и
// включают, поэтому JSON переформатируется.
func TestDumpPrettyPrintsJSON(t *testing.T) {
	var buf bytes.Buffer
	debugTracer(&buf).Dump("ответ агента", []byte(`{"a":{"b":1}}`))
	if !strings.Contains(buf.String(), "\n") {
		t.Errorf("JSON не переформатирован: %q", buf.String())
	}
}

// Одна поверхность бывает под два десятка килобайт; без предела один ход
// способен утопить лог целиком.
func TestDumpTruncatesHugeBodies(t *testing.T) {
	var buf bytes.Buffer
	huge := []byte(`{"text":"` + strings.Repeat("щ", maxDumpBytes) + `"}`)
	debugTracer(&buf).Dump("ответ агента", huge)
	if buf.Len() > maxDumpBytes+512 {
		t.Errorf("дамп не обрезан: %d байт", buf.Len())
	}
	if !strings.Contains(buf.String(), "обрезано") {
		t.Error("нет пометки об усечении")
	}
}
