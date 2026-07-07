package agent

import (
	"strings"
	"testing"
)

func TestBuildOrchestratorInstruction(t *testing.T) {
	got := buildOrchestratorInstruction("ask_orders_agent", "Агент по имени «orders-agent» умеет: возвраты")
	if !strings.Contains(got, "ask_orders_agent") {
		t.Errorf("instruction must reference the derived tool name; got %q", got)
	}
	if !strings.Contains(got, "Агент по имени «orders-agent» умеет") {
		t.Errorf("instruction must embed the worker summary; got %q", got)
	}
	if strings.Contains(got, "статистику продаж или возвраты вызывайте") {
		t.Errorf("instruction must not hardcode the orders domain routing anymore")
	}
	if !strings.Contains(got, "NEEDS_USER_INPUT") {
		t.Errorf("instruction must keep the NEEDS_USER_INPUT behavioral rule; got %q", got)
	}
}
