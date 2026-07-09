// Package tui is a minimal REPL front-end for the orchestrator agent.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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

// renderWidget prints a structured widget the worker returned in an A2A
// DataPart. Invoked from the widget handler Run registers, so widgets reach the
// terminal directly instead of being flattened into the LLM's text answer. It
// dispatches on the "_kind" the client injected from the part's metadata.
func renderWidget(w map[string]any) {
	title, _ := w["title"].(string)
	fmt.Printf("\n%s┌─ %s%s\n", cyan, title, reset)
	switch w["_kind"] {
	case "widget/order":
		if o, ok := w["order"].(map[string]any); ok {
			field := func(label, key string) {
				if v, ok := o[key]; ok && v != nil && v != "" {
					fmt.Printf("%s│%s %-9s %v\n", cyan, reset, label, v)
				}
			}
			field("Товар:", "item")
			field("Статус:", "status_label")
			if amt, ok := o["amount"]; ok {
				fmt.Printf("%s│%s %-9s %v %v\n", cyan, reset, "Сумма:", amt, o["currency"])
			}
			field("Клиент:", "customer")
			field("Дата:", "created")
		}
	case "widget/order_list":
		if rows, ok := w["orders"].([]any); ok {
			for _, r := range rows {
				o, ok := r.(map[string]any)
				if !ok {
					continue
				}
				fmt.Printf("%s│%s #%v  %v — %v (%v %v, %v)\n", cyan, reset,
					o["id"], o["item"], o["status_label"], o["amount"], o["currency"], o["created"])
			}
		}
	default: // widget/confirmation and any future dialog-style widget
		if message, _ := w["message"].(string); message != "" {
			fmt.Printf("%s│%s %s\n", cyan, reset, message)
		}
		if labels := actionLabels(w["actions"]); labels != "" {
			fmt.Printf("%s│%s %s\n", cyan, reset, labels)
		}
	}
	fmt.Printf("%s└─%s\n", cyan, reset)
}

// actionLabels renders a widget's action buttons as "[Label]  [Label]".
func actionLabels(v any) string {
	actions, ok := v.([]any)
	if !ok {
		return ""
	}
	var labels []string
	for _, a := range actions {
		if m, ok := a.(map[string]any); ok {
			if l, ok := m["label"].(string); ok {
				labels = append(labels, "["+l+"]")
			}
		}
	}
	return strings.Join(labels, "  ")
}

// WidgetSource is anything that can push structured widgets to a handler —
// implemented by the A2A orders client. The TUI registers a handler so widgets
// render directly here instead of passing through the LLM.
type WidgetSource interface {
	SetWidgetHandler(func(sessionID string, w map[string]any))
}

// Run starts a REPL that reads user input, runs it through the orchestrator
// runner, and prints assistant responses. Widgets from ws render inline; when a
// widget is shown during a turn, the assistant's text echo is suppressed so the
// same prompt/result isn't displayed twice (widget + text). Returns nil on
// "exit"/"quit" or EOF.
func Run(ctx context.Context, r *runner.Runner, ws WidgetSource) error {
	fmt.Printf("%sАссистент службы заказов.%s Введите запрос или 'exit' для выхода.\n", cyan, reset)
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	const userID, sessID = "tui-user", "tui-session"

	// widgetShown flips when a widget renders during the current turn. Safe as a
	// plain bool: the handler fires synchronously on this goroutine (the tool
	// call runs inside r.Run's iteration), never concurrently.
	widgetShown := false
	if ws != nil {
		ws.SetWidgetHandler(func(_ string, w map[string]any) {
			renderWidget(w)
			widgetShown = true
		})
	}

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
		widgetShown = false
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
			// Skip the text echo when a widget already conveyed this turn's result.
			if combined != "" && !widgetShown {
				fmt.Printf("%sассистент>%s %s\n", cyan, reset, combined)
			}
		}
	}
}
