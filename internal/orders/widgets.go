package orders

import "google.golang.org/adk/tool"

// WidgetStateKey is the session-state key a read tool uses to hand a structured
// widget to the A2A bridge. The executor drains it from the tool's event
// (event.Actions.StateDelta) and re-emits it as an A2A DataPart. The value is a
// plain map (not a typed struct) so it survives any session-state serialization
// unchanged, and carries its widget kind under the "kind" entry.
const WidgetStateKey = "a2a_widget"

// stashWidget records w on the tool context so the bridge can turn it into a
// DataPart after the run. It is deliberately built by CODE from the store's
// structured data, never by the LLM — the model only phrases the text answer.
func stashWidget(tc tool.Context, w map[string]any) {
	if a := tc.Actions(); a != nil && a.StateDelta != nil {
		a.StateDelta[WidgetStateKey] = w
	}
}

// orderWidget builds the single-order display widget from an Order.
func orderWidget(o Order) map[string]any {
	return map[string]any{
		"kind":  "widget/order",
		"title": "Заказ " + o.ID,
		"order": map[string]any{
			"id":           o.ID,
			"item":         o.Item,
			"status":       o.Status,
			"status_label": statusLabel(o.Status),
			"amount":       o.Amount,
			"currency":     o.Currency,
			"customer":     o.Customer,
			"created":      o.Created,
			"refundable":   o.Refundable,
		},
	}
}

// orderListWidget builds the order-list widget for a customer's orders.
func orderListWidget(customer string, list []Order) map[string]any {
	rows := make([]map[string]any, 0, len(list))
	for _, o := range list {
		rows = append(rows, map[string]any{
			"id":           o.ID,
			"item":         o.Item,
			"status_label": statusLabel(o.Status),
			"amount":       o.Amount,
			"currency":     o.Currency,
			"created":      o.Created,
		})
	}
	return map[string]any{
		"kind":     "widget/order_list",
		"title":    "Последние заказы: " + customer,
		"customer": customer,
		"orders":   rows,
	}
}
