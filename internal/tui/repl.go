// Package tui is a minimal REPL front-end for the orchestrator agent.
package tui

import (
	"bufio"
	"context"
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

// Run starts a REPL that reads user input, runs it through the orchestrator
// runner, and prints assistant responses. Returns nil on "exit"/"quit" or EOF.
func Run(ctx context.Context, r *runner.Runner) error {
	fmt.Printf("%sOrders Assistant.%s Type your request, or 'exit' to quit.\n", cyan, reset)
	in := bufio.NewScanner(os.Stdin)
	const userID, sessID = "tui-user", "tui-session"
	for {
		fmt.Printf("%syou>%s ", cyan, reset)
		if !in.Scan() {
			return in.Err()
		}
		line := in.Text()
		if line == "exit" || line == "quit" {
			return nil
		}
		if line == "" {
			continue
		}
		msg := genai.NewContentFromText(line, genai.RoleUser)
		for event, err := range r.Run(ctx, userID, sessID, msg, agent.RunConfig{}) {
			if err != nil {
				fmt.Printf("%s[error] %v%s\n", gray, err, reset)
				break
			}
			if event != nil && event.Content != nil {
				for _, p := range event.Content.Parts {
					if p.Text != "" {
						fmt.Printf("%sassistant>%s %s\n", cyan, reset, p.Text)
					}
				}
			}
		}
	}
}
