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
