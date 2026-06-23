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
