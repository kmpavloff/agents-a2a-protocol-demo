package main

import (
	"context"
	"log"

	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2abridge"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/tui"
)

func main() {
	ctx := context.Background()
	cfg, err := config.LoadOrchestrator("configs/orchestrator.yaml")
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	oc, err := a2abridge.NewOrdersClient(ctx, cfg.WorkerURL)
	if err != nil {
		log.Fatalf("orders client (is the worker running at %s?): %v", cfg.WorkerURL, err)
	}
	model := llm.New(cfg.LLM)
	ag, err := agent.NewOrchestrator(model, oc.Tool())
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	r, err := runner.New(runner.Config{
		AppName:           "orchestrator",
		Agent:             ag,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		log.Fatalf("runner: %v", err)
	}
	if err := tui.Run(ctx, r); err != nil {
		log.Fatalf("tui: %v", err)
	}
}
