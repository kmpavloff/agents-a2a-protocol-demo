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

func getOrderStatus(s *Store, id string) (string, error) {
	o, ok := s.Get(id)
	if !ok {
		return fmt.Sprintf("Заказ %s не найден.", id), nil
	}
	return fmt.Sprintf("Заказ %s (%s): статус — %s. Сумма: %.2f %s.", o.ID, o.Item, statusLabel(o.Status), o.Amount, o.Currency), nil
}

func listRecentOrders(s *Store, customer string) (string, error) {
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
type idArgs struct {
	OrderID string `json:"order_id" description:"Номер заказа, например 1041"`
}
type customerArgs struct {
	Customer string `json:"customer" description:"Имя клиента"`
}
type periodArgs struct {
	Period string `json:"period" description:"Период в формате ГГГГ-ММ"`
}
type queryArgs struct {
	Query string `json:"query" description:"Произвольный текст для поиска заказа"`
}

func mustTool(t tool.Tool, err error) tool.Tool {
	if err != nil {
		panic(err)
	}
	return t
}

// Tools returns the order tools bound to the given store.
func Tools(s *Store) []tool.Tool {
	return []tool.Tool{
		mustTool(functiontool.New(functiontool.Config{Name: "find_order", Description: "Найти заказ по номеру или тексту названия товара."},
			func(_ tool.Context, a queryArgs) (string, error) { return findOrder(s, a.Query) })),
		mustTool(functiontool.New(functiontool.Config{Name: "get_order_status", Description: "Узнать статус заказа по его номеру."},
			func(_ tool.Context, a idArgs) (string, error) { return getOrderStatus(s, a.OrderID) })),
		mustTool(functiontool.New(functiontool.Config{Name: "list_recent_orders", Description: "Показать последние заказы клиента, новые сверху."},
			func(_ tool.Context, a customerArgs) (string, error) { return listRecentOrders(s, a.Customer) })),
		mustTool(functiontool.New(functiontool.Config{Name: "get_sales_stats", Description: "Получить статистику продаж за период (ГГГГ-ММ)."},
			func(_ tool.Context, a periodArgs) (string, error) { return getSalesStats(s, a.Period) })),
		mustTool(functiontool.New(functiontool.Config{Name: "initiate_refund", Description: "Оформить возврат по заказу (по его номеру)."},
			func(_ tool.Context, a idArgs) (string, error) { return initiateRefund(s, a.OrderID) })),
	}
}
