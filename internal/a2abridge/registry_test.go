package a2abridge

import (
	"context"
	"testing"
	"time"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

func TestRegistryResolvesToolNameCollisions(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID, a.Description = "one", "Общий агент."
	b := ouroborosCfg(s.URL)
	b.ID, b.Description = "two", "Общий агент."
	// Оба вывели бы одно и то же имя, если бы имя бралось из карточки.
	a.Description, b.Description = "", ""

	g := NewRegistry([]config.AgentConfig{a, b}, nil)
	tools := g.Tools(context.Background())
	if len(tools) != 2 {
		t.Fatalf("tools: got %d, want 2", len(tools))
	}
	if tools[0].Name() == tools[1].Name() {
		t.Fatalf("tool names must be unique, both are %q", tools[0].Name())
	}
	if tools[1].Name() != "ask_two" {
		t.Errorf("collision must fall back to ask_<id>, got %q", tools[1].Name())
	}
}

func TestRegistryListMarksUnavailable(t *testing.T) {
	cfg := ouroborosCfg("http://127.0.0.1:1") // заведомо закрытый порт
	cfg.Timeout = "2s"
	g := NewRegistry([]config.AgentConfig{cfg}, nil)

	// Первый List отдаёт «ещё не проверяли» и уходит проверять в фоне: ждать
	// медленного агента эндпоинт не должен.
	infos := g.List(context.Background())
	if len(infos) != 1 {
		t.Fatalf("infos: got %d, want 1", len(infos))
	}
	if infos[0].Probed {
		t.Error("первый List не должен утверждать, что агент уже проверен")
	}
	// Дать фоновой проверке добежать до отказа.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if infos = g.List(context.Background()); infos[0].Probed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !infos[0].Probed {
		t.Fatal("фоновая проверка не отработала")
	}
	if infos[0].Available {
		t.Error("unreachable agent must be marked unavailable")
	}
	if infos[0].ID != "ouroboros" || infos[0].Name != "Ouroboros" {
		t.Errorf("info: %+v", infos[0])
	}
	if !infos[0].Verbatim {
		t.Error("verbatim flag lost")
	}
	// Недоступный агент не должен попадать в набор инструментов «Авто».
	if tools := g.Tools(context.Background()); len(tools) != 0 {
		t.Errorf("unavailable agent must not contribute a tool, got %d", len(tools))
	}
}

func TestRegistryKeepsConfigOrderAndCachesClients(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "first"
	b := ouroborosCfg(s.URL)
	b.ID = "second"

	g := NewRegistry([]config.AgentConfig{a, b}, nil)
	if got := g.IDs(); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("order: %v", got)
	}
	if first := g.First(); first == nil || first.ID() != "first" {
		t.Fatalf("First(): %v", first)
	}
	c1, _ := g.ClientFor("second")
	c2, _ := g.ClientFor("second")
	if c1 != c2 {
		t.Error("ClientFor must cache: two calls returned different clients")
	}
	if _, ok := g.ClientFor("missing"); ok {
		t.Error("ClientFor must report an unknown id")
	}
}
