package orders

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// statusLabel maps the internal status enum to a human-readable Russian label.
func statusLabel(status string) string {
	switch status {
	case "delivered":
		return "доставлен"
	case "shipped":
		return "отправлен"
	case "processing":
		return "в обработке"
	case "refunded":
		return "возврат оформлен"
	default:
		return status
	}
}

// missingOrderID guards against an empty/whitespace order_id. Some models emit
// a probing tool call without the id even when the user gave one; instead of
// looking up an empty id, return a hint (as a normal result, not an error) so
// the model re-calls the tool with a concrete number.
func missingOrderID(id string) (string, bool) {
	if strings.TrimSpace(id) == "" {
		return "Не указан номер заказа. Передайте order_id (например, 1041) и вызовите инструмент снова.", true
	}
	return "", false
}

func getOrderStatus(s *Store, id string) (string, error) {
	if hint, missing := missingOrderID(id); missing {
		return hint, nil
	}
	id = strings.TrimSpace(id)
	o, ok := s.Get(id)
	if !ok {
		return fmt.Sprintf("Заказ %s не найден.", id), nil
	}
	return fmt.Sprintf("Заказ %s (%s): статус — %s. Сумма: %.2f %s.", o.ID, o.Item, statusLabel(o.Status), o.Amount, o.Currency), nil
}

func listRecentOrders(s *Store, customer string) (string, error) {
	if strings.TrimSpace(customer) == "" {
		return "Не указано имя клиента. Передайте customer (например, alice) и вызовите инструмент снова.", nil
	}
	customer = strings.TrimSpace(customer)
	list := s.ByCustomer(customer)
	if len(list) == 0 {
		return fmt.Sprintf("Заказы для клиента %q не найдены.", customer), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Последние заказы клиента %s:\n", customer)
	for _, o := range list {
		fmt.Fprintf(&b, "- #%s %s (%s, %.2f %s, %s)\n", o.ID, o.Item, statusLabel(o.Status), o.Amount, o.Currency, o.Created)
	}
	return b.String(), nil
}

func getSalesStats(s *Store, period string) (string, error) {
	st, ok := s.Stats(period)
	if !ok {
		return fmt.Sprintf("Нет статистики продаж за период %q.", period), nil
	}
	return fmt.Sprintf("Продажи за %s: %d заказов, выручка %.2f %s.", st.Period, st.Orders, st.Revenue, st.Currency), nil
}

func initiateRefund(s *Store, id string) (string, error) {
	if hint, missing := missingOrderID(id); missing {
		return hint, nil
	}
	id = strings.TrimSpace(id)
	o, err := s.Refund(id)
	switch {
	case errors.Is(err, ErrNotFound):
		return fmt.Sprintf("Невозможно оформить возврат: заказ %s не найден.", id), nil
	case errors.Is(err, ErrNotRefundable):
		return fmt.Sprintf("Невозможно оформить возврат: заказ %s не подлежит возврату.", id), nil
	case err != nil:
		return "", err
	}
	return fmt.Sprintf("Возврат по заказу %s оформлен (%.2f %s).", o.ID, o.Amount, o.Currency), nil
}

func findOrder(s *Store, query string) (string, error) {
	q := strings.TrimSpace(strings.TrimPrefix(query, "#"))
	if o, ok := s.Get(q); ok {
		return getOrderStatus(s, o.ID)
	}
	// fall back to item substring match across all orders
	var hits []Order
	for _, o := range s.AllOrders() {
		if strings.Contains(strings.ToLower(o.Item), strings.ToLower(query)) {
			hits = append(hits, o)
		}
	}
	if len(hits) == 0 {
		return fmt.Sprintf("По запросу %q ничего не найдено.", query), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Совпадения по запросу %q:\n", query)
	for _, o := range hits {
		fmt.Fprintf(&b, "- #%s %s (%s)\n", o.ID, o.Item, statusLabel(o.Status))
	}
	return b.String(), nil
}

// argument structs (adk derives the tool JSON schema from these)

// idArgs accepts the order number under several key names because small models
// often invent synonyms (order_number, number, id) instead of the documented
// order_id. All fields are optional (omitempty) so none is schema-required; the
// runtime guard reports a missing id with a helpful message instead.
type idArgs struct {
	OrderID     string `json:"order_id,omitempty" description:"Номер заказа, например 1041"`
	OrderNumber string `json:"order_number,omitempty" description:"Синоним order_id (номер заказа)"`
	Number      string `json:"number,omitempty" description:"Синоним order_id (номер заказа)"`
	ID          string `json:"id,omitempty" description:"Синоним order_id (номер заказа)"`
}

// orderID returns the first non-empty, trimmed id-like field.
func (a idArgs) orderID() string {
	for _, v := range []string{a.OrderID, a.OrderNumber, a.Number, a.ID} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
// customerArgs, like idArgs, accepts the client name under several key names
// because small models often invent synonyms instead of the documented
// customer. All fields are optional; the runtime guard reports a missing name.
type customerArgs struct {
	Customer     string `json:"customer,omitempty" description:"Имя клиента, например alice"`
	CustomerName string `json:"customer_name,omitempty" description:"Синоним customer (имя клиента)"`
	Name         string `json:"name,omitempty" description:"Синоним customer (имя клиента)"`
	Client       string `json:"client,omitempty" description:"Синоним customer (имя клиента)"`
}

// customer returns the first non-empty, trimmed client-name field.
func (a customerArgs) customer() string {
	for _, v := range []string{a.Customer, a.CustomerName, a.Name, a.Client} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
type periodArgs struct {
	Period string `json:"period" description:"Период в формате ГГГГ-ММ"`
}
type queryArgs struct {
	Query string `json:"query" description:"Произвольный текст для поиска заказа"`
}

// refundNeedsConfirmation gates the HITL confirmation for initiate_refund: only
// a call that carries a concrete order id triggers a confirmation request, so a
// model's empty probing call does not spam the user with confirmations.
func refundNeedsConfirmation(a idArgs) bool { return a.orderID() != "" }

func mustTool(t tool.Tool, err error) tool.Tool {
	if err != nil {
		panic(err)
	}
	return t
}

// Tools returns the order tools bound to the given store. orderLinkBase is the
// base URL for customer-facing order-card links in widgets ("" disables links).
func Tools(s *Store, orderLinkBase string) []tool.Tool {
	return []tool.Tool{
		mustTool(functiontool.New(functiontool.Config{Name: "find_order", Description: "Найти заказ по номеру или тексту названия товара."},
			func(_ tool.Context, a queryArgs) (string, error) { return findOrder(s, a.Query) })),
		mustTool(functiontool.New(functiontool.Config{Name: "get_order_status", Description: "Узнать статус заказа по его номеру."},
			func(tc tool.Context, a idArgs) (string, error) {
				text, err := getOrderStatus(s, a.orderID())
				if err == nil {
					if o, ok := s.Get(a.orderID()); ok {
						stashWidget(tc, orderWidget(o, OrderURL(orderLinkBase, o.ID)))
					}
				}
				return text, err
			})),
		mustTool(functiontool.New(functiontool.Config{Name: "list_recent_orders", Description: "Показать последние заказы клиента по его имени (например, alice), новые сверху."},
			func(tc tool.Context, a customerArgs) (string, error) {
				text, err := listRecentOrders(s, a.customer())
				if err == nil {
					if list := s.ByCustomer(a.customer()); len(list) > 0 {
						stashWidget(tc, orderListWidget(a.customer(), list, orderLinkBase))
					}
				}
				return text, err
			})),
		mustTool(functiontool.New(functiontool.Config{Name: "get_sales_stats", Description: "Получить статистику продаж за период (ГГГГ-ММ)."},
			func(_ tool.Context, a periodArgs) (string, error) { return getSalesStats(s, a.Period) })),
		mustTool(functiontool.New(functiontool.Config{
			Name:                        "initiate_refund",
			Description:                 "Оформить возврат по заказу (по его номеру).",
			RequireConfirmationProvider: refundNeedsConfirmation,
		},
			func(tc tool.Context, a idArgs) (string, error) {
				text, err := initiateRefund(s, a.orderID())
				if err == nil {
					// A successful refund leaves the order in "refunded" —
					// stash the receipt widget for the A2A layer to enrich.
					if o, ok := s.Get(strings.TrimSpace(a.orderID())); ok && o.Status == "refunded" {
						stashWidget(tc, refundReceiptWidget(o))
					}
				}
				return text, err
			})),
	}
}
