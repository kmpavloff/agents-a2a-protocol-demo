// internal/a2abridge/profile_test.go
package a2abridge

import (
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func sampleCard() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "orders-agent",
		Description: "Управляет заказами, статусами, статистикой и возвратами.",
		Skills: []a2a.AgentSkill{{
			ID:          "manage_orders",
			Name:        "Управление заказами",
			Description: "Поиск заказов, статусы, статистика продаж и оформление возвратов.",
			Tags:        []string{"заказы", "возвраты"},
			Examples:    []string{"верни деньги за заказ 1041", "статус заказа 1041"},
		}},
	}
}

func TestSanitizeToolName(t *testing.T) {
	cases := map[string]string{
		"orders-agent": "ask_orders_agent",
		"Orders Agent": "ask_Orders_Agent",
		"":             "ask_agent",
		"---":          "ask_agent",
	}
	for in, want := range cases {
		if got := sanitizeToolName(in); got != want {
			t.Errorf("sanitizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProfileFromCard(t *testing.T) {
	p := ProfileFromCard(sampleCard())
	if p.ToolName != "ask_orders_agent" {
		t.Errorf("ToolName = %q, want ask_orders_agent", p.ToolName)
	}
	for _, want := range []string{"Управление заказами", "верни деньги за заказ 1041", "NEEDS_USER_INPUT"} {
		if !strings.Contains(p.ToolDesc, want) {
			t.Errorf("ToolDesc missing %q; got %q", want, p.ToolDesc)
		}
	}
	for _, want := range []string{"orders-agent", "Управление заказами"} {
		if !strings.Contains(p.Summary, want) {
			t.Errorf("Summary missing %q; got %q", want, p.Summary)
		}
	}
}

func TestProfileFromCardNilSafe(t *testing.T) {
	p := ProfileFromCard(nil)
	if p.ToolName != "ask_agent" {
		t.Errorf("nil card ToolName = %q, want ask_agent", p.ToolName)
	}
	if !strings.Contains(p.ToolDesc, "NEEDS_USER_INPUT") {
		t.Errorf("nil card ToolDesc should still carry the NEEDS_USER_INPUT tail; got %q", p.ToolDesc)
	}
}
