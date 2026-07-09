package orders

import (
	"strings"
	"testing"
)

func TestGetOrderStatus(t *testing.T) {
	s := seed(t)
	out, err := getOrderStatus(s, "1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "доставлен") {
		t.Errorf("want status in output, got %q", out)
	}
}

func TestGetOrderStatusNotFound(t *testing.T) {
	s := seed(t)
	out, err := getOrderStatus(s, "9999")
	if err != nil {
		t.Fatalf("domain miss must not be a Go error, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "не найден") {
		t.Errorf("want 'not found' message, got %q", out)
	}
}

func TestOrderIDToolsGuardEmptyID(t *testing.T) {
	s := seed(t)
	for _, tc := range []struct {
		name string
		fn   func(*Store, string) (string, error)
	}{
		{"get_order_status", getOrderStatus},
		{"initiate_refund", initiateRefund},
	} {
		for _, id := range []string{"", "   ", "\t"} {
			out, err := tc.fn(s, id)
			if err != nil {
				t.Fatalf("%s(%q): unexpected error: %v", tc.name, id, err)
			}
			if !strings.Contains(out, "Не указан номер заказа") {
				t.Errorf("%s(%q): want missing-id hint, got %q", tc.name, id, out)
			}
		}
	}
}

func TestIDArgsOrderIDAliases(t *testing.T) {
	cases := []struct {
		name string
		args idArgs
		want string
	}{
		{"order_id", idArgs{OrderID: "1041"}, "1041"},
		{"order_number", idArgs{OrderNumber: "1041"}, "1041"},
		{"number", idArgs{Number: "1041"}, "1041"},
		{"id", idArgs{ID: "1041"}, "1041"},
		{"trimmed", idArgs{OrderNumber: "  1041 "}, "1041"},
		{"precedence_order_id_first", idArgs{OrderID: "1041", Number: "9999"}, "1041"},
		{"all_empty", idArgs{}, ""},
	}
	for _, tc := range cases {
		if got := tc.args.orderID(); got != tc.want {
			t.Errorf("%s: orderID()=%q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestListRecentOrders(t *testing.T) {
	s := seed(t)
	out, err := listRecentOrders(s, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1055") || !strings.Contains(out, "1023") {
		t.Errorf("want all alice orders listed, got %q", out)
	}
}

func TestListRecentOrdersGuardsEmptyCustomer(t *testing.T) {
	s := seed(t)
	for _, c := range []string{"", "   ", "\t"} {
		out, err := listRecentOrders(s, c)
		if err != nil {
			t.Fatalf("customer %q: unexpected error: %v", c, err)
		}
		if !strings.Contains(out, "Не указано имя клиента") {
			t.Errorf("customer %q: want missing-customer hint, got %q", c, out)
		}
	}
}

func TestCustomerArgsAliases(t *testing.T) {
	cases := []struct {
		name string
		args customerArgs
		want string
	}{
		{"customer", customerArgs{Customer: "alice"}, "alice"},
		{"customer_name", customerArgs{CustomerName: "alice"}, "alice"},
		{"name", customerArgs{Name: "alice"}, "alice"},
		{"client", customerArgs{Client: "alice"}, "alice"},
		{"trimmed", customerArgs{Name: "  alice "}, "alice"},
		{"precedence_customer_first", customerArgs{Customer: "alice", Name: "bob"}, "alice"},
		{"all_empty", customerArgs{}, ""},
	}
	for _, tc := range cases {
		if got := tc.args.customer(); got != tc.want {
			t.Errorf("%s: customer()=%q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestInitiateRefundNotRefundable(t *testing.T) {
	s := seed(t)
	out, err := initiateRefund(s, "1055")
	if err != nil {
		t.Fatalf("must not be a Go error, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "не подлежит возврату") {
		t.Errorf("want not-refundable message, got %q", out)
	}
}

func TestFindOrderByID(t *testing.T) {
	s := seed(t)
	out, err := findOrder(s, "1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1041") || !strings.Contains(out, "Хаб") {
		t.Errorf("want order 1041 with item Хаб, got %q", out)
	}
}

func TestFindOrderByIDWithHash(t *testing.T) {
	s := seed(t)
	out, err := findOrder(s, "#1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1041") || !strings.Contains(out, "Хаб") {
		t.Errorf("want order 1041 with item Хаб, got %q", out)
	}
}

func TestFindOrderBySubstring(t *testing.T) {
	s := seed(t)
	out, err := findOrder(s, "Хаб")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1041") {
		t.Errorf("want Хаб order in matches, got %q", out)
	}
}

func TestFindOrderNoMatch(t *testing.T) {
	s := seed(t)
	out, err := findOrder(s, "zzzzz")
	if err != nil {
		t.Fatalf("domain miss must not be a Go error, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "ничего не найдено") {
		t.Errorf("want 'no order matched' message, got %q", out)
	}
}

func TestInitiateRefundNotFound(t *testing.T) {
	s := seed(t)
	out, err := initiateRefund(s, "9999")
	if err != nil {
		t.Fatalf("domain miss must not be a Go error, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "не найден") {
		t.Errorf("want 'not found' message, got %q", out)
	}
}

func TestToolsCount(t *testing.T) {
	s := seed(t)
	if got := len(Tools(s)); got != 5 {
		t.Errorf("want 5 tools, got %d", got)
	}
}

func TestRefundNeedsConfirmation(t *testing.T) {
	if !refundNeedsConfirmation(idArgs{OrderID: "1041"}) {
		t.Error("refund with a concrete order id must require confirmation")
	}
	if refundNeedsConfirmation(idArgs{}) {
		t.Error("empty/probing refund call must NOT require confirmation")
	}
}
