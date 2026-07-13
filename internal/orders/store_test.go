package orders

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func seed(t *testing.T) *Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "orders.json")
	body := `{"orders":[
		{"id":"1023","customer":"alice","item":"Клавиатура","amount":89.9,"currency":"EUR","status":"delivered","created":"2026-06-01","refundable":true},
		{"id":"1041","customer":"alice","item":"Хаб","amount":34.5,"currency":"EUR","status":"delivered","created":"2026-06-10","refundable":true},
		{"id":"1055","customer":"alice","item":"Подставка","amount":45,"currency":"EUR","status":"shipped","created":"2026-06-18","refundable":false}
	],"sales_stats":[{"period":"2026-06","orders":198,"revenue":9120.1,"currency":"EUR"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func TestByCustomerSortedDesc(t *testing.T) {
	s := seed(t)
	got := s.ByCustomer("alice")
	if len(got) != 3 {
		t.Fatalf("want 3 orders, got %d", len(got))
	}
	if got[0].ID != "1055" {
		t.Errorf("want newest (1055) first, got %s", got[0].ID)
	}
}

func TestRefundHappyPath(t *testing.T) {
	s := seed(t)
	o, err := s.Refund("1041")
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if o.Status != "refunded" {
		t.Errorf("status: want refunded, got %s", o.Status)
	}
}

func TestRefundNotRefundable(t *testing.T) {
	s := seed(t)
	if _, err := s.Refund("1055"); !errors.Is(err, ErrNotRefundable) {
		t.Fatalf("want ErrNotRefundable, got %v", err)
	}
}

func TestRefundNotFound(t *testing.T) {
	s := seed(t)
	if _, err := s.Refund("9999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestStats(t *testing.T) {
	s := seed(t)
	st, ok := s.Stats("2026-06")
	if !ok || st.Orders != 198 {
		t.Fatalf("stats lookup failed: %+v ok=%v", st, ok)
	}
}

func TestOrderURL(t *testing.T) {
	if got := OrderURL("https://shop.test/orders", "1041"); got != "https://shop.test/orders/1041" {
		t.Errorf("OrderURL = %q", got)
	}
	if got := OrderURL("https://shop.test/orders/", "1041"); got != "https://shop.test/orders/1041" {
		t.Errorf("trailing slash: OrderURL = %q", got)
	}
	if got := OrderURL("", "1041"); got != "" {
		t.Errorf("empty base must disable links, got %q", got)
	}
}
