// Package a2ui maps the demo's domain widgets to Google's A2UI v0.9 generative-UI
// JSON and parses A2UI action events. It is the only place that knows the A2UI
// wire format; the domain (orders) and transport (a2abridge) packages stay
// A2UI-agnostic.
package a2ui

import (
	"fmt"
	"strings"
)

const (
	ExtensionURI = "https://a2ui.org/a2a-extension/a2ui/v0.9"
	MIMEType     = "application/a2ui+json"
	Version      = "v0.9"
	CatalogID    = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
)

// surfaceCounter makes surface ids unique within a process without needing a
// random source (unavailable in some sandboxes). It is not concurrency-critical:
// ids only need to be distinct per emitted widget, and the executor emits from a
// single goroutine per request.
var surfaceCounter int

func nextSurfaceID(kind string) string {
	surfaceCounter++
	return fmt.Sprintf("%s-%d", kind, surfaceCounter)
}

// text builds a Text component.
func text(id, s, variant string) map[string]any {
	return map[string]any{"id": id, "component": "Text", "text": s, "variant": variant}
}

// button builds a Button whose child is a Text label and whose click emits an
// A2UI action event {name, context}. The label is copied into a per-button
// context (never the shared ctx) so a client can echo a human-readable action
// instead of the raw action name.
func button(id, labelID, label, variant, actionName string, ctx map[string]any) []map[string]any {
	bctx := map[string]any{"label": label}
	for k, v := range ctx {
		bctx[k] = v
	}
	return []map[string]any{
		{
			"id": id, "component": "Button", "child": labelID, "variant": variant,
			"action": map[string]any{"event": map[string]any{"name": actionName, "context": bctx}},
		},
		text(labelID, label, "body"),
	}
}

// surface wraps components into the standard createSurface + updateComponents pair.
func surface(surfaceID string, components []map[string]any) []map[string]any {
	return []map[string]any{
		{"version": Version, "createSurface": map[string]any{"surfaceId": surfaceID, "catalogId": CatalogID}},
		{"version": Version, "updateComponents": map[string]any{"surfaceId": surfaceID, "components": components}},
	}
}

// ParseAction extracts an incoming A2UI action event from a DataPart's data map.
// Shape: {"version":"v0.9","action":{"name":"...","context":{...}}}. Returns
// ok=false when the map is not an action payload.
func ParseAction(data map[string]any) (string, map[string]any, bool) {
	if data == nil {
		return "", nil, false
	}
	action, ok := data["action"].(map[string]any)
	if !ok {
		return "", nil, false
	}
	name, ok := action["name"].(string)
	if !ok || name == "" {
		return "", nil, false
	}
	ctx, _ := action["context"].(map[string]any)
	if ctx == nil {
		ctx = map[string]any{}
	}
	return name, ctx, true
}

// FromWidget converts a widget map (keyed by "_kind" plus payload) into the
// ordered A2UI messages to emit. Returns ok=false for an unknown kind.
func FromWidget(w map[string]any) ([]map[string]any, bool) {
	kind, _ := w["_kind"].(string)
	title, _ := w["title"].(string)
	switch kind {
	case "widget/confirmation":
		sid := nextSurfaceID("confirmation")
		msg, _ := w["message"].(string)
		// Drop the "(да/нет)" hint: redundant in the widget, where the buttons
		// already offer the choice. The text-fallback part keeps it.
		msg = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(msg), "(да/нет)"))
		orderID, _ := w["order_id"].(string)
		ctx := map[string]any{"order_id": orderID}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": []any{"title", "msg", "actions"}},
			text("title", title, "h3"),
			text("msg", msg, "body"),
			{"id": "actions", "component": "Row", "children": []any{"approve", "decline"}},
		}
		comps = append(comps, button("approve", "approve_lbl", "Оформить возврат", "primary", "approve_refund", ctx)...)
		comps = append(comps, button("decline", "decline_lbl", "Отмена", "default", "decline_refund", ctx)...)
		return surface(sid, comps), true

	case "widget/order":
		sid := nextSurfaceID("order")
		o, _ := w["order"].(map[string]any)
		children := []any{"title"}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": children},
			text("title", title, "h3"),
		}
		add := func(id, label string, key string) {
			if v, ok := o[key]; ok && v != nil && v != "" {
				children = append(children, id)
				comps = append(comps, text(id, fmt.Sprintf("%s %v", label, v), "body"))
			}
		}
		add("item", "Товар:", "item")
		add("status", "Статус:", "status_label")
		if amt, ok := o["amount"]; ok {
			children = append(children, "amount")
			comps = append(comps, text("amount", fmt.Sprintf("Сумма: %v %v", amt, o["currency"]), "body"))
		}
		add("customer", "Клиент:", "customer")
		add("created", "Дата:", "created")
		comps[0]["children"] = children // refresh root Column children after appends
		return surface(sid, comps), true

	case "widget/order_list":
		sid := nextSurfaceID("order_list")
		rows, _ := w["orders"].([]any)
		children := []any{"title"}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": children},
			text("title", title, "h3"),
		}
		for i, r := range rows {
			o, ok := r.(map[string]any)
			if !ok {
				continue
			}
			id := fmt.Sprintf("row%d", i)
			children = append(children, id)
			line := fmt.Sprintf("#%v  %v — %v (%v %v, %v)",
				o["id"], o["item"], o["status_label"], o["amount"], o["currency"], o["created"])
			comps = append(comps, text(id, line, "body"))
		}
		comps[0]["children"] = children
		return surface(sid, comps), true
	}
	return nil, false
}
