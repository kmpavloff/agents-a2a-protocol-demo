package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2abridge"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agent"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agentstore"
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

	// A2A protocol trace goes to a file so it does not clutter the REPL on
	// stdout. In --web mode there is no REPL, so mirror the trace to stdout too
	// where it is easy to watch.
	logFile, err := os.OpenFile(cfg.A2ALogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("open a2a log %s: %v", cfg.A2ALogPath, err)
	}
	defer logFile.Close()
	var traceW io.Writer = logFile
	traceDst := cfg.A2ALogPath
	if *web {
		traceW = io.MultiWriter(os.Stdout, logFile)
		traceDst = cfg.A2ALogPath + " + stdout"
	}
	trace := a2abridge.NewTracer(traceW, "[A2A client] ")
	log.Printf("A2A protocol trace → %s", traceDst)

	// Правки из UI лежат отдельным файлом поверх рукописного конфига.
	store, err := agentstore.New(cfg.Agents, cfg.AgentsOverlayPath)
	if err != nil {
		log.Fatalf("agents overlay: %v", err)
	}
	agents := store.Agents()
	reg := a2abridge.NewRegistry(agents, trace)
	// Правка из UI доезжает до живого реестра: новый агент появляется в
	// селекторе и в режиме «Авто» без перезапуска.
	store.OnChange(reg.Apply)
	model := llm.New(cfg.LLM)
	log.Printf("orchestrator | LLM=%s model=%q", cfg.LLM.BaseURL, cfg.LLM.Model)
	log.Printf("agents (%d), overlay %s:", len(agents), cfg.AgentsOverlayPath)
	for _, a := range agents {
		// Пароль сюда не попадает намеренно: логи демо показывают целиком.
		log.Printf("  - %s → %s (card %s, skill=%q, verbatim=%v, timeout=%s)",
			a.ID, a.URL, a.CardPath, a.Skill, a.Verbatim, a.TimeoutDuration())
	}

	// Сессионная служба общая для всех runner'ов: набор инструментов меняется
	// при переключении агента в селекторе, но история разговора привязана к
	// contextId и переезжать вместе с ним не должна.
	sessions := session.InMemoryService()

	// buildRunner собирает runner под набор инструментов выбранного агента:
	// в режиме «Авто» их несколько, при явном выборе — ровно один.
	buildRunner := func(tools []tool.Tool, summary string) (*runner.Runner, error) {
		ag, err := agent.NewOrchestrator(model, tools, summary)
		if err != nil {
			return nil, err
		}
		return runner.New(runner.Config{
			AppName:           "orchestrator",
			Agent:             ag,
			SessionService:    sessions,
			AutoCreateSession: true,
		})
	}

	if *web {
		exec := a2abridge.NewOrchestratorExecutor(reg, buildRunner, trace)
		handler := a2asrv.NewHandler(exec)
		mux := http.NewServeMux()
		// JSON-RPC endpoint — matches the URL advertised in the agent card (publicURL/invoke).
		mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
		// Well-known agent card path — used by a2aclient resolver / A2UI-aware browsers.
		mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(a2abridge.OrchestratorCard(cfg.PublicURL)))
		// Список агентов для селектора в браузере.
		mux.Handle("/api/agents", webui.AgentsHandler(reg.List))
		// Правка списка агентов из браузера.
		webui.RegisterAgentConfig(mux, store)
		// Embedded frontend.
		mux.Handle("/", webui.Handler())
		log.Printf("orchestrator web UI on %s", cfg.ListenAddr)
		log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
		return
	}

	// Терминальный REPL выбора агента не даёт и работает с первым агентом
	// списка — то есть после миграции с worker_url ведёт себя как раньше.
	// LoadOrchestrator гарантирует минимум одного агента в БАЗОВОМ списке, но
	// overlay может скрыть их все — тогда reg.First() вернёт nil, и без этой
	// проверки первое же обращение к nil-агенту падало бы нил-паникой.
	first := reg.First()
	if first == nil {
		log.Fatalf("нет ни одного агента: все скрыты в %s — удалите файл или верните агента через UI", cfg.AgentsOverlayPath)
	}
	if err := first.Connect(ctx); err != nil {
		log.Fatalf("agent %q (запущен ли он?): %v", first.ID(), err)
	}
	oc, ok := reg.ClientFor(first.ID())
	if !ok {
		log.Fatalf("agent %q not in registry", first.ID())
	}
	ordersTool := oc.Tool()
	log.Printf("orchestrator tools (1):")
	log.Printf("  - %s: %s", ordersTool.Name(), ordersTool.Description())
	r, err := buildRunner([]tool.Tool{ordersTool}, oc.Profile().Summary)
	if err != nil {
		log.Fatalf("runner: %v", err)
	}

	// Widgets the worker returns in DataParts render directly in the terminal,
	// bypassing the orchestrator LLM (Run registers the handler on oc).
	if err := tui.Run(ctx, r, oc); err != nil {
		log.Fatalf("tui: %v", err)
	}
}
