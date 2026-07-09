package a2abridge

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"
)

// TestEndToEndRefundWithClarification exercises the full orchestrator→worker
// A2A flow in-process: turn 1 triggers input-required, turn 2 resumes and
// completes the same worker task.
func TestEndToEndRefundWithClarification(t *testing.T) {
	// Worker stub: turn 1 asks for clarification, turn 2 completes the refund.
	workerModel := llm.NewStub(
		llm.StubTurn{Text: "NEED_INPUT: Какой заказ вернуть?"},
		llm.StubTurn{Text: "Возврат по заказу 1041 оформлен."},
	)

	// Reuse the existing in-process worker helper from client_test.go.
	url := startWorker(t, workerModel)

	oc, err := NewOrdersClient(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}

	sess := "e2e"

	// Turn 1: worker needs clarification.
	r1, err := oc.ask(context.Background(), sess, "хочу вернуть деньги за последний заказ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r1, "NEEDS_USER_INPUT:") {
		t.Fatalf("turn 1 should need input, got %q", r1)
	}

	// Turn 2: provide order id; same worker task is resumed and completes.
	r2, err := oc.ask(context.Background(), sess, "заказ 1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r2, "оформлен") {
		t.Fatalf("turn 2 should complete refund, got %q", r2)
	}
}

// e2eStore seeds a refundable order 1041 for the confirmation flow.
func e2eStore(t *testing.T) *orders.Store {
	t.Helper()
	p := t.TempDir() + "/o.json"
	body := `{"orders":[{"id":"1041","customer":"alice","item":"Хаб","amount":34.5,"currency":"EUR","status":"delivered","created":"2026-06-10","refundable":true}],"sales_stats":[]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := orders.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEndToEndRefundConfirmed(t *testing.T) {
	store := e2eStore(t)
	// Turn 1: LLM calls initiate_refund → adk requests confirmation.
	// Turn 2 (after "да"): adk executes the refund → LLM summarizes.
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
		llm.StubTurn{Text: "Возврат по заказу 1041 оформлен."},
	)
	url := startWorkerWithTools(t, model, store)

	oc, err := NewOrdersClient(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := "conf-yes"

	r1, err := oc.ask(context.Background(), sess, "оформи возврат по заказу 1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r1, "NEEDS_USER_INPUT:") || !strings.Contains(r1, "1041") {
		t.Fatalf("turn 1 should ask to confirm order 1041, got %q", r1)
	}

	// The refund must NOT be applied until the user actually confirms.
	if o, ok := store.Get("1041"); !ok || o.Status == "refunded" {
		t.Fatalf("order 1041 must be present and NOT yet refunded before confirmation; ok=%v status=%q", ok, o.Status)
	}

	r2, err := oc.ask(context.Background(), sess, "да")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r2, "оформлен") {
		t.Fatalf("turn 2 should complete the refund, got %q", r2)
	}
	o, ok := store.Get("1041")
	if !ok {
		t.Fatal("order 1041 missing from store")
	}
	if o.Status != "refunded" {
		t.Errorf("store should show order 1041 refunded, got status %q", o.Status)
	}
}

func TestEndToEndRefundDeclined(t *testing.T) {
	store := e2eStore(t)
	model := llm.NewStub(
		llm.StubTurn{Call: &genai.FunctionCall{Name: "initiate_refund", Args: map[string]any{"order_id": "1041"}}},
	)
	url := startWorkerWithTools(t, model, store)

	oc, err := NewOrdersClient(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := "conf-no"

	r1, err := oc.ask(context.Background(), sess, "оформи возврат по заказу 1041")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r1, "NEEDS_USER_INPUT:") || !strings.Contains(r1, "1041") {
		t.Fatalf("turn 1 should ask to confirm order 1041, got %q", r1)
	}

	r2, err := oc.ask(context.Background(), sess, "нет, не надо")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(r2), "отмен") {
		t.Fatalf("declining should report the refund was cancelled, got %q", r2)
	}
	o, ok := store.Get("1041")
	if !ok {
		t.Fatal("order 1041 missing from store")
	}
	if o.Status == "refunded" {
		t.Error("refund must NOT have executed after the user declined")
	}
}
