package a2abridge

import (
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

// Отдаём строго по спеке: data — массив сообщений, тип — в metadata.mimeType.
// mediaType ставится вдобавок и не заменяет метаданные.
func TestNewA2UIPartFollowsSpec(t *testing.T) {
	part := newA2UIPart([]map[string]any{
		{"version": "v0.9", "createSurface": map[string]any{"surfaceId": "s1"}},
	})
	data, ok := part.Data().([]any)
	if !ok {
		t.Fatalf("data должна быть массивом сообщений, получено %T", part.Data())
	}
	if len(data) != 1 {
		t.Errorf("ожидалось одно сообщение, получено %d", len(data))
	}
	if got, _ := part.Metadata[a2ui.MIMEKey].(string); got != a2ui.MIMEType {
		t.Errorf("metadata.mimeType = %q, ожидалось %q", got, a2ui.MIMEType)
	}
	if part.MediaType != a2ui.MIMEType {
		t.Errorf("mediaType = %q, ожидалось %q", part.MediaType, a2ui.MIMEType)
	}
}

// Приём либерален: часть узнаётся по любой из двух пометок и по обоим типам,
// но чужая data-часть за A2UI не выдаётся.
func TestIsA2UIPartAcceptsBothMarks(t *testing.T) {
	byMetadata := a2a.NewDataPart([]any{})
	byMetadata.Metadata = map[string]any{a2ui.MIMEKey: a2ui.MIMEType}

	byMediaType := a2a.NewDataPart([]any{})
	byMediaType.MediaType = a2ui.MIMEType

	legacy := a2a.NewDataPart([]any{})
	legacy.Metadata = map[string]any{a2ui.MIMEKey: a2ui.LegacyMIMEType}

	widget := a2a.NewDataPart(map[string]any{"order_id": "1041"})
	widget.Metadata = map[string]any{"kind": "widget/order"}

	for name, tc := range map[string]struct {
		part *a2a.Part
		want bool
	}{
		"metadata.mimeType": {byMetadata, true},
		"mediaType":         {byMediaType, true},
		"легаси-тип":        {legacy, true},
		"доменный виджет":   {widget, false},
		"nil":               {nil, false},
	} {
		if got := isA2UIPart(tc.part); got != tc.want {
			t.Errorf("%s: isA2UIPart = %v, ожидалось %v", name, got, tc.want)
		}
	}
}

// Ревизия A2UI, на которой мы говорим, — 0.9.1. Две тонкости перехода, которые
// легко потерять: ключ возможностей клиента остаётся "v0.9" (другого свойства
// схема не знает даже в наборе 0.9.1), а поддержка объявляется под обоими URI,
// иначе агент, знающий только 0.9, не поймёт, что клиент умеет рисовать.
func TestA2UIRevisionWiring(t *testing.T) {
	if a2ui.Version != "v0.9.1" {
		t.Errorf("версия сообщений = %q, ожидалась v0.9.1", a2ui.Version)
	}
	caps := a2ui.ClientCapabilities()
	if _, ok := caps["v0.9"]; !ok || len(caps) != 1 {
		t.Errorf("ключ a2uiClientCapabilities должен быть ровно \"v0.9\", получено %v", caps)
	}
	if a2ui.ExtensionURI == a2ui.LegacyExtensionURI {
		t.Error("URI ревизий должны различаться")
	}

	card := OrchestratorCard("http://localhost:8080")
	announced := map[string]bool{}
	for _, ext := range card.Capabilities.Extensions {
		announced[ext.URI] = true
	}
	for _, uri := range []string{a2ui.ExtensionURI, a2ui.LegacyExtensionURI} {
		if !announced[uri] {
			t.Errorf("карточка не объявляет %q", uri)
		}
	}
}

// Поверхность в ленте переименована по ходам, а агент знает её под своим
// именем: вернуть ему чужой идентификатор значит лишить его возможности
// сопоставить событие со своей поверхностью.
func TestActionSurfaceReturnsToAgentUntagged(t *testing.T) {
	msgs := a2ui.RetagSurfaces([]map[string]any{
		{"version": "v0.9", "createSurface": map[string]any{"surfaceId": "s1"}},
	}, "-t3")
	cs, _ := msgs[0]["createSurface"].(map[string]any)
	inFeed, _ := cs["surfaceId"].(string)
	if inFeed != "s1-t3" {
		t.Fatalf("в ленте поверхность должна быть переименована, получено %q", inFeed)
	}
	if got := a2ui.UntagSurface(inFeed); got != "s1" {
		t.Errorf("агенту уходит %q, ожидалось %q", got, "s1")
	}
	// Идентификатор без суффикса не должен пострадать.
	if got := a2ui.UntagSurface("confirmation-1"); got != "confirmation-1" {
		t.Errorf("чужой id укорочен: %q", got)
	}
}

// Событие кнопки собирается по схеме client_to_server и читается обратно теми
// же разборщиками, которыми оркестратор читает событие из браузера.
func TestNewActionPartRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 12, 9, 15, 0, 0, time.UTC)
	part := newActionPart("return_order", "surface-1", "return-btn",
		map[string]any{"order_id": "ORD-002"}, at)

	if !isA2UIPart(part) {
		t.Fatal("событие должно быть помечено как A2UI")
	}
	name, ctx, ok := a2ui.ParseAction(part.Data())
	if !ok {
		t.Fatalf("действие не разобралось: %#v", part.Data())
	}
	if name != "return_order" || ctx["order_id"] != "ORD-002" {
		t.Errorf("получено name=%q ctx=%v", name, ctx)
	}
	surface, source := a2ui.ActionOrigin(part.Data())
	if surface != "surface-1" || source != "return-btn" {
		t.Errorf("происхождение потеряно: surface=%q source=%q", surface, source)
	}
	msgs, _ := part.Data().([]any)
	msg, _ := msgs[0].(map[string]any)
	if len(msg) != 2 {
		t.Errorf("конверт должен нести ровно version и action, получено %v", msg)
	}
	action, _ := msg["action"].(map[string]any)
	if action["timestamp"] != "2026-08-12T09:15:00Z" {
		t.Errorf("timestamp = %v", action["timestamp"])
	}
	// Схема client_to_server.json объявляет все пять полей обязательными:
	// пустое значение остаётся полем, а не исчезает.
	for _, f := range []string{"name", "surfaceId", "sourceComponentId", "timestamp", "context"} {
		if _, ok := action[f]; !ok {
			t.Errorf("в действии нет обязательного поля %q: %v", f, action)
		}
	}
	blank := a2ui.NewAction("ping", "", "", nil, "")
	ba, _ := blank["action"].(map[string]any)
	if len(ba) != 5 {
		t.Errorf("пустые поля должны остаться в действии, получено %v", ba)
	}
}
