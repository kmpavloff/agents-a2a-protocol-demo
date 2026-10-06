package a2abridge

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// Ход к агенту оставляет в журнале своей сессии ровно то, что ушло по
// проводу: SendMessage с metadata.skill и опрос GetTask, номер карты скрыт.
func TestRemoteRecordsAgentCallsPerSession(t *testing.T) {
	s := startOuroborosStub(t, true) // первый ответ WORKING → будет GetTask
	trace := NewTracer(io.Discard, "")
	r := NewRemote(ouroborosCfg(s.URL), trace)
	r.SetSessionSkill("sess-1", "shop")

	if _, err := r.Ask(context.Background(), "sess-1", "карта 4111 1111 1111 1111"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	calls := trace.Calls().Since("sess-1", 0)
	if len(calls) != 2 || calls[0].Method != "SendMessage" || calls[1].Method != "GetTask" {
		t.Fatalf("calls: %+v", calls)
	}
	c := calls[0]
	if c.AgentID != "ouroboros" || c.AgentName != "Ouroboros" || c.Status != 200 || len(c.Response) == 0 {
		t.Errorf("call: %+v", c)
	}
	var req struct {
		Params struct {
			Message struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(c.Request, &req); err != nil {
		t.Fatalf("request не JSON: %v\n%s", err, c.Request)
	}
	if req.Params.Message.Metadata["skill"] != "shop" {
		t.Errorf("metadata.skill: %v", req.Params.Message.Metadata)
	}
	if strings.Contains(string(c.Request), "4111") {
		t.Errorf("номер карты попал в журнал: %s", c.Request)
	}
	if calls[1].Seq <= c.Seq {
		t.Errorf("seq не растёт: %d, %d", c.Seq, calls[1].Seq)
	}

	if other := trace.Calls().Since("sess-2", 0); len(other) != 0 {
		t.Errorf("чужая сессия видит обмены: %+v", other)
	}
	if rest := trace.Calls().Since("sess-1", c.Seq); len(rest) != 1 {
		t.Errorf("Since(after) должен отдавать только новые: %+v", rest)
	}
}

// Маскируются строки, а не числа: метка времени в миллисекундах той же длины,
// что и номер карты, и её замена сломала бы JSON.
func TestCallBodyMasksOnlyStrings(t *testing.T) {
	got := string(callBody([]byte(`{"ts":1728200000000,"text":"4111-1111-1111-1111"}`)))
	if !json.Valid([]byte(got)) || !strings.Contains(got, "1728200000000") || strings.Contains(got, "4111") {
		t.Errorf("callBody: %s", got)
	}
	if s := string(callBody([]byte("не json"))); s != `"не json"` {
		t.Errorf("не-JSON должен уйти строкой: %s", s)
	}
}

// Память журнала ограничена и по записям, и по сессиям.
func TestCallLogIsBounded(t *testing.T) {
	l := NewCallLog()
	for i := 0; i < maxCallsPerSession+5; i++ {
		l.add("a", AgentCall{})
	}
	if n := len(l.Since("a", 0)); n != maxCallsPerSession {
		t.Errorf("записей на сессию: %d", n)
	}
	for i := 0; i < maxCallSessions; i++ {
		l.add(strings.Repeat("s", i+1), AgentCall{})
	}
	if n := len(l.Since("a", 0)); n != 0 {
		t.Errorf("старейшая сессия должна быть вытеснена, осталось %d", n)
	}
}
