package main

import (
	"context"
	"flag"
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
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/tui"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/webui"
)

func main() {
	web := flag.Bool("web", false, "serve the A2UI web UI + A2A server instead of the terminal REPL")
	flag.Parse()

	ctx := context.Background()
	cfg, err := config.LoadOrchestrator("configs/orchestrator.yaml")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// A2A protocol trace goes to a file so it does not clutter the REPL on stdout.
	logFile, err := os.OpenFile(cfg.A2ALogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("open a2a log %s: %v", cfg.A2ALogPath, err)
	}
	defer logFile.Close()
	trace := a2abridge.NewTracer(logFile, "[A2A client] ")
	log.Printf("A2A protocol trace → %s", cfg.A2ALogPath)

	oc, err := a2abridge.NewOrdersClient(ctx, cfg.WorkerURL, trace)
	if err != nil {
		log.Fatalf("orders client (is the worker running at %s?): %v", cfg.WorkerURL, err)
	}
	model := llm.New(cfg.LLM)
	ordersTool := oc.Tool()
	log.Printf("orchestrator tools (1):")
	log.Printf("  - %s: %s", ordersTool.Name(), ordersTool.Description())
	ag, err := agent.NewOrchestrator(model, ordersTool, oc.Profile().Summary)
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
	if *web {
		exec := a2abridge.NewOrchestratorExecutor(r, oc, trace)
		handler := a2asrv.NewHandler(exec)
		mux := http.NewServeMux()
		// JSON-RPC endpoint — matches the URL advertised in the agent card (publicURL/invoke).
		mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
		// Well-known agent card path — used by a2aclient resolver / A2UI-aware browsers.
		mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(a2abridge.OrchestratorCard(cfg.PublicURL)))
		// Embedded frontend.
		mux.Handle("/", webui.Handler())
		log.Printf("orchestrator web UI on %s", cfg.ListenAddr)
		log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
		return
	}

	// Widgets the worker returns in DataParts render directly in the terminal,
	// bypassing the orchestrator LLM (Run registers the handler on oc).
	if err := tui.Run(ctx, r, oc); err != nil {
		log.Fatalf("tui: %v", err)
	}
}
