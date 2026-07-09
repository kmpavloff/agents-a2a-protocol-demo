package a2ui

import "testing"

func TestParseAction(t *testing.T) {
	name, ctx, ok := ParseAction(map[string]any{
		"version": "v0.9",
		"action":  map[string]any{"name": "approve_refund", "context": map[string]any{"order_id": "1055"}},
	})
	if !ok || name != "approve_refund" || ctx["order_id"] != "1055" {
		t.Fatalf("got name=%q ctx=%v ok=%v", name, ctx, ok)
	}
}

func TestParseActionRejectsNonAction(t *testing.T) {
	if _, _, ok := ParseAction(map[string]any{"useStreaming": false}); ok {
		t.Error("non-action data must return ok=false")
	}
	if _, _, ok := ParseAction(nil); ok {
		t.Error("nil data must return ok=false")
	}
}
