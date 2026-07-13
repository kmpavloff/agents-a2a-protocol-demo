package orders

import (
	"strings"

	"google.golang.org/adk/tool"
)

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

// OrderURL builds the customer-facing order-card link from the configured
// base URL, or "" when links are disabled (empty base). Built by CODE so the
// LLM can never invent or mangle a link.
func OrderURL(base, id string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + id
}

// orderWidget builds the single-order display widget from an Order. url is the
// order-card link ("" omits it).
func orderWidget(o Order, url string) map[string]any {
	order := map[string]any{
		"id":           o.ID,
		"item":         o.Item,
		"status":       o.Status,
		"status_label": statusLabel(o.Status),
		"amount":       o.Amount,
		"currency":     o.Currency,
		"customer":     o.Customer,
		"created":      o.Created,
		"refundable":   o.Refundable,
	}
	if url != "" {
		order["url"] = url
	}
	return map[string]any{
		"kind":  "widget/order",
		"title": "Заказ " + o.ID,
		"order": order,
	}
}

// refundReceiptWidget builds the refund-receipt widget from a just-refunded
// order. The payment context (masked card, receipt id, timestamp) is appended
// later by the A2A layer, which owns the card-collection step.
func refundReceiptWidget(o Order) map[string]any {
	return map[string]any{
		"kind":     "widget/refund_receipt",
		"title":    "Квитанция о возврате",
		"order_id": o.ID,
		"item":     o.Item,
		"amount":   o.Amount,
		"currency": o.Currency,
	}
}

// orderListWidget builds the order-list widget for a customer's orders.
// linkBase is the order-card base URL ("" omits per-row links).
func orderListWidget(customer string, list []Order, linkBase string) map[string]any {
	rows := make([]map[string]any, 0, len(list))
	for _, o := range list {
		row := map[string]any{
			"id":           o.ID,
			"item":         o.Item,
			"status_label": statusLabel(o.Status),
			"amount":       o.Amount,
			"currency":     o.Currency,
			"created":      o.Created,
		}
		if url := OrderURL(linkBase, o.ID); url != "" {
			row["url"] = url
		}
		rows = append(rows, row)
	}
	return map[string]any{
		"kind":     "widget/order_list",
		"title":    "Последние заказы: " + customer,
		"customer": customer,
		"orders":   rows,
	}
}
