package orders

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

func getOrderStatus(s *Store, id string) (string, error) {
	o, ok := s.Get(id)
	if !ok {
		return fmt.Sprintf("Order %s not found.", id), nil
	}
	return fmt.Sprintf("Order %s (%s) is %s. Amount: %.2f %s.", o.ID, o.Item, o.Status, o.Amount, o.Currency), nil
}

func listRecentOrders(s *Store, customer string) (string, error) {
	list := s.ByCustomer(customer)
	if len(list) == 0 {
		return fmt.Sprintf("No orders found for customer %q.", customer), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Recent orders for %s:\n", customer)
	for _, o := range list {
		fmt.Fprintf(&b, "- #%s %s (%s, %.2f %s, %s)\n", o.ID, o.Item, o.Status, o.Amount, o.Currency, o.Created)
	}
	return b.String(), nil
}

func getSalesStats(s *Store, period string) (string, error) {
	st, ok := s.Stats(period)
	if !ok {
		return fmt.Sprintf("No sales statistics for period %q.", period), nil
	}
	return fmt.Sprintf("Sales for %s: %d orders, revenue %.2f %s.", st.Period, st.Orders, st.Revenue, st.Currency), nil
}

func initiateRefund(s *Store, id string) (string, error) {
	o, err := s.Refund(id)
	switch {
	case errors.Is(err, ErrNotFound):
		return fmt.Sprintf("Cannot refund: order %s not found.", id), nil
	case errors.Is(err, ErrNotRefundable):
		return fmt.Sprintf("Cannot refund: order %s is not refundable.", id), nil
	case err != nil:
		return "", err
	}
	return fmt.Sprintf("Refund initiated for order %s (%.2f %s).", o.ID, o.Amount, o.Currency), nil
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
		return fmt.Sprintf("No order matched %q.", query), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Matches for %q:\n", query)
	for _, o := range hits {
		fmt.Fprintf(&b, "- #%s %s (%s)\n", o.ID, o.Item, o.Status)
	}
	return b.String(), nil
}

// argument structs (adk derives the tool JSON schema from these)
type idArgs struct {
	OrderID string `json:"order_id" description:"The order identifier, e.g. 1041"`
}
type customerArgs struct {
	Customer string `json:"customer" description:"The customer name"`
}
type periodArgs struct {
	Period string `json:"period" description:"Period in YYYY-MM format"`
}
type queryArgs struct {
	Query string `json:"query" description:"Free-text order search query"`
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
		mustTool(functiontool.New(functiontool.Config{Name: "find_order", Description: "Find an order by id or item text."},
			func(_ tool.Context, a queryArgs) (string, error) { return findOrder(s, a.Query) })),
		mustTool(functiontool.New(functiontool.Config{Name: "get_order_status", Description: "Get the status of an order by id."},
			func(_ tool.Context, a idArgs) (string, error) { return getOrderStatus(s, a.OrderID) })),
		mustTool(functiontool.New(functiontool.Config{Name: "list_recent_orders", Description: "List a customer's recent orders, newest first."},
			func(_ tool.Context, a customerArgs) (string, error) { return listRecentOrders(s, a.Customer) })),
		mustTool(functiontool.New(functiontool.Config{Name: "get_sales_stats", Description: "Get sales statistics for a period (YYYY-MM)."},
			func(_ tool.Context, a periodArgs) (string, error) { return getSalesStats(s, a.Period) })),
		mustTool(functiontool.New(functiontool.Config{Name: "initiate_refund", Description: "Initiate a refund for an order by id."},
			func(_ tool.Context, a idArgs) (string, error) { return initiateRefund(s, a.OrderID) })),
	}
}
