package a2abridge

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
)

// clientCapturingWidgets returns a client whose widget handler appends every
// widget the worker emits over A2A, so tests can assert on them.
func clientCapturingWidgets(t *testing.T, url string) (*OrdersClient, *[]map[string]any) {
	t.Helper()
	oc, err := NewOrdersClient(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	oc.SetWidgetHandler(func(_ string, w map[string]any) { got = append(got, w) })
	return oc, &got
}

// TestWorkerEmitsOrderWidget: get_order_status stashes structured order data,
// the executor emits it as a DataPart in the completed artifact, and the client
// forwards it to the widget handler (bypassing the LLM string return).
func TestWorkerEmitsOrderWidget(t *testing.T) {
	store := e2eStore(t)
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "get_order_status", Args: map[string]any{"order_id": "1041"}}},
		llm.StubTurn{Text: "Заказ 1041: статус — доставлен."},
	)
	url := startWorkerWithTools(t, model, store)
	oc, got := clientCapturingWidgets(t, url)

	if _, err := oc.ask(context.Background(), "s", "статус заказа 1041"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("want exactly 1 widget, got %d: %#v", len(*got), *got)
	}
	w := (*got)[0]
	if w["_kind"] != "widget/order" {
		t.Errorf("kind = %v, want widget/order", w["_kind"])
	}
	o, ok := w["order"].(map[string]any)
	if !ok {
		t.Fatalf("widget carries no order payload: %#v", w)
	}
	if o["id"] != "1041" {
		t.Errorf("order id = %v, want 1041", o["id"])
	}
	if o["status_label"] != "доставлен" {
		t.Errorf("status_label = %v, want доставлен", o["status_label"])
	}
}

// TestWorkerEmitsOrderListWidget: list_recent_orders emits a widget/order_list
// DataPart whose rows mirror the store.
func TestWorkerEmitsOrderListWidget(t *testing.T) {
	store := e2eStore(t)
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "list_recent_orders", Args: map[string]any{"customer": "alice"}}},
		llm.StubTurn{Text: "Последние заказы alice: #1041."},
	)
	url := startWorkerWithTools(t, model, store)
	oc, got := clientCapturingWidgets(t, url)

	if _, err := oc.ask(context.Background(), "s", "последние заказы alice"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || (*got)[0]["_kind"] != "widget/order_list" {
		t.Fatalf("want one widget/order_list, got %#v", *got)
	}
	rows, ok := (*got)[0]["orders"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("want 1 order row, got %#v", (*got)[0]["orders"])
	}
	row, _ := rows[0].(map[string]any)
	if row["id"] != "1041" {
		t.Errorf("row id = %v, want 1041", row["id"])
	}
}

// TestWorkerEmitsConfirmationWidget: the HITL refund confirmation ships a
// widget/confirmation DataPart alongside the text prompt in the input-required
// status message.
func TestWorkerEmitsConfirmationWidget(t *testing.T) {
	store := e2eStore(t)
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
	)
	url := startWorkerWithTools(t, model, store)
	oc, got := clientCapturingWidgets(t, url)

	r1, err := oc.ask(context.Background(), "s", "верни деньги за 1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r1, "NEEDS_USER_INPUT:") {
		t.Fatalf("want confirmation prompt, got %q", r1)
	}
	if len(*got) != 1 || (*got)[0]["_kind"] != "widget/confirmation" {
		t.Fatalf("want one widget/confirmation, got %#v", *got)
	}
	w := (*got)[0]
	if w["order_id"] != "1041" {
		t.Errorf("confirmation order_id = %v, want 1041", w["order_id"])
	}
	if _, ok := w["actions"].([]any); !ok {
		t.Errorf("confirmation widget should carry actions, got %#v", w["actions"])
	}
}
