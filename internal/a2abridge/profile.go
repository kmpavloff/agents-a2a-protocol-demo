package a2abridge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// WorkerProfile is everything the orchestrator learns about the worker from its
// AgentCard: the delegating tool's name and description, plus a human-readable
// capabilities block injected into the orchestrator's system prompt.
type WorkerProfile struct {
	ToolName string
	ToolDesc string
	Summary  string
}

// needsInputTail is appended to every derived tool description so the orchestrator
// LLM knows how to handle a clarification/confirmation bounce.
const needsInputTail = "Если он вернёт NEEDS_USER_INPUT, задайте пользователю этот вопрос, затем снова вызовите инструмент с его ответом."

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// sanitizeToolName turns a card name into a valid function-tool name of the form
// ask_<slug>. Non-alphanumeric runs collapse to "_"; an empty result falls back
// to "ask_agent".
func sanitizeToolName(name string) string {
	slug := strings.Trim(nonAlnum.ReplaceAllString(name, "_"), "_")
	if slug == "" {
		return "ask_agent"
	}
	return "ask_" + slug
}

// quoteExamples wraps each example in guillemets and joins them.
func quoteExamples(ex []string, sep string) string {
	q := make([]string, len(ex))
	for i, e := range ex {
		q[i] = "«" + e + "»"
	}
	return strings.Join(q, sep)
}

// ProfileFromCard derives the delegating tool + prompt block from a worker card.
// A nil card yields safe fallbacks and never panics.
func ProfileFromCard(card *a2a.AgentCard) WorkerProfile {
	var name, desc string
	var skills []a2a.AgentSkill
	if card != nil {
		name, desc, skills = card.Name, card.Description, card.Skills
	}

	// Tool description: agent description + each skill + examples + fixed tail.
	var td strings.Builder
	td.WriteString("Делегировать запрос удалённому агенту.")
	if desc != "" {
		td.WriteString(" " + desc)
	}
	for _, s := range skills {
		fmt.Fprintf(&td, " Навык «%s»: %s", s.Name, s.Description)
		if len(s.Examples) > 0 {
			fmt.Fprintf(&td, " Примеры: %s.", quoteExamples(s.Examples, ", "))
		}
	}
	td.WriteString(" " + needsInputTail)

	// Summary block for the orchestrator prompt.
	agentName := name
	if agentName == "" {
		agentName = "агент"
	}
	var sm strings.Builder
	fmt.Fprintf(&sm, "Агент по имени «%s» умеет:", agentName)
	for _, s := range skills {
		fmt.Fprintf(&sm, "\n- %s — %s", s.Name, s.Description)
		if len(s.Examples) > 0 {
			fmt.Fprintf(&sm, " (примеры: %s)", quoteExamples(s.Examples, "; "))
		}
	}

	return WorkerProfile{
		ToolName: sanitizeToolName(name),
		ToolDesc: td.String(),
		Summary:  sm.String(),
	}
}
