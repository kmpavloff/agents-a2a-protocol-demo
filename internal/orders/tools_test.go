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
	if !strings.Contains(out, "delivered") {
		t.Errorf("want status in output, got %q", out)
	}
}

func TestGetOrderStatusNotFound(t *testing.T) {
	s := seed(t)
	out, err := getOrderStatus(s, "9999")
	if err != nil {
		t.Fatalf("domain miss must not be a Go error, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "not found") {
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
	if !strings.Contains(strings.ToLower(out), "not refundable") {
		t.Errorf("want not-refundable message, got %q", out)
	}
}

func TestToolsCount(t *testing.T) {
	s := seed(t)
	if got := len(Tools(s)); got != 5 {
		t.Errorf("want 5 tools, got %d", got)
	}
}
