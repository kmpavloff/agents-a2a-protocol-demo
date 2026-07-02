package a2abridge

import (
	"context"
	"strings"
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
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
