// Package tui is a minimal REPL front-end for the orchestrator agent.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

const (
	cyan  = "\033[36m"
	gray  = "\033[90m"
	reset = "\033[0m"
)

// compactArgs renders function-call arguments as compact JSON for a one-line
// trace, falling back to Go's default formatting if marshaling fails.
func compactArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	if b, err := json.Marshal(args); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", args)
}

// Run starts a REPL that reads user input, runs it through the orchestrator
// runner, and prints assistant responses. Returns nil on "exit"/"quit" or EOF.
func Run(ctx context.Context, r *runner.Runner) error {
	fmt.Printf("%sАссистент службы заказов.%s Введите запрос или 'exit' для выхода.\n", cyan, reset)
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	const userID, sessID = "tui-user", "tui-session"
	for {
		fmt.Printf("%sвы>%s ", cyan, reset)
		if !in.Scan() {
			return in.Err()
		}
		line := in.Text()
		if line == "exit" || line == "quit" || line == "выход" {
			return nil
		}
		if line == "" {
			continue
		}
		msg := genai.NewContentFromText(line, genai.RoleUser)
		fmt.Printf("%s  · агент → LLM: запрос%s\n", gray, reset)
		for event, err := range r.Run(ctx, userID, sessID, msg, agent.RunConfig{}) {
			if err != nil {
				fmt.Printf("%s[ошибка] %v%s\n", gray, err, reset)
				break
			}
			if event == nil || event.Content == nil {
				continue
			}
			var combined string
			for _, p := range event.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					// LLM решило, какой инструмент вызвать.
					fmt.Printf("%s  · LLM → агент: вызвать %s(%s)%s\n",
						gray, p.FunctionCall.Name, compactArgs(p.FunctionCall.Args), reset)
				case p.FunctionResponse != nil:
					// Результат инструмента уходит обратно в LLM.
					fmt.Printf("%s  · инструмент → LLM: результат %s, снова спрашиваю LLM%s\n",
						gray, p.FunctionResponse.Name, reset)
				case p.Text != "":
					combined += p.Text
				}
			}
			if combined != "" {
				fmt.Printf("%sассистент>%s %s\n", cyan, reset, combined)
			}
		}
	}
}
