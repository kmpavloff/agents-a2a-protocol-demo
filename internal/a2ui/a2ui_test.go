package a2ui

import "testing"

func TestFromWidgetConfirmation(t *testing.T) {
	msgs, ok := FromWidget(map[string]any{
		"_kind":    "widget/confirmation",
		"title":    "Подтверждение возврата",
		"message":  "Оформить возврат по заказу 1055?",
		"order_id": "1055",
		"actions": []any{
			map[string]any{"id": "approve", "label": "Оформить возврат"},
			map[string]any{"id": "decline", "label": "Отмена"},
		},
	})
	if !ok {
		t.Fatal("confirmation widget should map")
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages (createSurface, updateComponents), got %d", len(msgs))
	}
	if msgs[0]["version"] != "v0.9" || msgs[0]["createSurface"] == nil {
		t.Errorf("msg0 must be a v0.9 createSurface, got %#v", msgs[0])
	}
	uc, _ := msgs[1]["updateComponents"].(map[string]any)
	if uc == nil {
		t.Fatalf("msg1 must be updateComponents, got %#v", msgs[1])
	}
	comps, _ := uc["components"].([]map[string]any)
	// Expect at least: root, message text, two buttons, two button labels.
	var buttons, actions int
	for _, c := range comps {
		if c["component"] == "Button" {
			buttons++
			if a, ok := c["action"].(map[string]any); ok {
				if _, ok := a["event"].(map[string]any); ok {
					actions++
				}
			}
		}
	}
	if buttons != 2 || actions != 2 {
		t.Errorf("want 2 buttons each with an action.event, got buttons=%d actions=%d", buttons, actions)
	}
}

func TestFromWidgetUnknownKind(t *testing.T) {
	if _, ok := FromWidget(map[string]any{"_kind": "widget/nope"}); ok {
		t.Error("unknown kind must return ok=false")
	}
}

// collectTexts returns all Text component texts from a FromWidget result.
func collectTexts(t *testing.T, msgs []map[string]any) []string {
	t.Helper()
	uc, _ := msgs[1]["updateComponents"].(map[string]any)
	comps, _ := uc["components"].([]map[string]any)
	var out []string
	for _, c := range comps {
		if c["component"] == "Text" {
			if s, ok := c["text"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func TestFromWidgetOrderIncludesLink(t *testing.T) {
	msgs, ok := FromWidget(map[string]any{
		"_kind": "widget/order",
		"title": "Заказ 1041",
		"order": map[string]any{
			"id": "1041", "item": "USB-C хаб", "status_label": "доставлен",
			"amount": 34.5, "currency": "EUR",
			"url": "https://shop.test/orders/1041",
		},
	})
	if !ok {
		t.Fatal("order widget should map")
	}
	want := "[Открыть карточку заказа →](https://shop.test/orders/1041)"
	for _, s := range collectTexts(t, msgs) {
		if s == want {
			return
		}
	}
	t.Errorf("no markdown link text %q in components: %v", want, collectTexts(t, msgs))
}

func TestFromWidgetOrderListRowsLinkTheNumber(t *testing.T) {
	msgs, ok := FromWidget(map[string]any{
		"_kind": "widget/order_list",
		"title": "Последние заказы: alice",
		"orders": []any{map[string]any{
			"id": "1041", "item": "USB-C хаб", "status_label": "доставлен",
			"amount": 34.5, "currency": "EUR", "created": "2026-06-10",
			"url": "https://shop.test/orders/1041",
		}},
	})
	if !ok {
		t.Fatal("order_list widget should map")
	}
	texts := collectTexts(t, msgs)
	for _, s := range texts {
		if s == "[#1041](https://shop.test/orders/1041)  USB-C хаб — доставлен (34.5 EUR, 2026-06-10)" {
			return
		}
	}
	t.Errorf("no linked row in components: %v", texts)
}

func TestFromWidgetOrderWithoutURLHasNoLink(t *testing.T) {
	msgs, ok := FromWidget(map[string]any{
		"_kind": "widget/order",
		"title": "Заказ 1041",
		"order": map[string]any{"id": "1041", "item": "USB-C хаб"},
	})
	if !ok {
		t.Fatal("order widget should map")
	}
	for _, s := range collectTexts(t, msgs) {
		if len(s) > 0 && s[0] == '[' {
			t.Errorf("unexpected link text %q for widget without url", s)
		}
	}
}
