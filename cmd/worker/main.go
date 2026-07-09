package main

import (
	"log"
	"net/http"
	"os"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2abridge"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/llm"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/orders"
)

func main() {
	cfg, err := config.LoadWorker("configs/worker.yaml")
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store, err := orders.Load(cfg.DataPath)
	if err != nil {
		log.Fatalf("orders: %v", err)
	}
	model := llm.New(cfg.LLM)
	log.Printf("orders-agent | LLM=%s model=%q | data=%s | listen=%s",
		cfg.LLM.BaseURL, cfg.LLM.Model, cfg.DataPath, cfg.ListenAddr)
	tools := orders.Tools(store)
	log.Printf("orders-agent tools (%d):", len(tools))
	for _, t := range tools {
		log.Printf("  - %s: %s", t.Name(), t.Description())
	}
	ag, err := agent.NewWorker(model, tools)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	r, err := runner.New(runner.Config{
		AppName:           "orders",
		Agent:             ag,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		log.Fatalf("runner: %v", err)
	}

	// Trace the A2A protocol exchange to stdout so the worker's activity is
	// visible (it was previously silent between requests).
	trace := a2abridge.NewTracer(os.Stdout, "[A2A worker] ")

	card := a2abridge.AgentCard(cfg.PublicURL)
	handler := a2asrv.NewHandler(a2abridge.NewExecutor(r, trace))

	mux := http.NewServeMux()
	// JSON-RPC endpoint — matches the URL advertised in the agent card (publicURL/invoke).
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
	// Well-known agent card path — used by a2aclient resolver.
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	log.Printf("orders-agent listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
}
