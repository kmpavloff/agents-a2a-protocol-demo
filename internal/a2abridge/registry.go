package a2abridge

import (
	"context"
	"sync"

	"google.golang.org/adk/tool"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// AgentInfo — то, что оркестратор рассказывает браузеру про доступных агентов.
type AgentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Verbatim    bool   `json:"verbatim"`
	Available   bool   `json:"available"`
}

// Registry — набор удалённых агентов из конфига, в порядке объявления.
// Порядок важен: первый агент обслуживает терминальный REPL, и он же задаёт
// стабильный порядок пунктов в селекторе UI.
type Registry struct {
	trace *Tracer

	mu      sync.Mutex
	order   []string
	remotes map[string]*Remote
	clients map[string]*OrdersClient
}

// NewRegistry создаёт реестр по конфигу. Соединения не открываются: каждый
// агент подключается лениво, при первом обращении.
func NewRegistry(agents []config.AgentConfig, trace *Tracer) *Registry {
	g := &Registry{
		trace:   trace,
		remotes: make(map[string]*Remote, len(agents)),
		clients: make(map[string]*OrdersClient, len(agents)),
	}
	for _, cfg := range agents {
		g.order = append(g.order, cfg.ID)
		g.remotes[cfg.ID] = NewRemote(cfg, trace)
	}
	return g
}

// IDs возвращает идентификаторы агентов в порядке конфига.
func (g *Registry) IDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.order...)
}

// Get возвращает агента по идентификатору.
func (g *Registry) Get(id string) (*Remote, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.remotes[id]
	return r, ok
}

// First возвращает первого агента из конфига — того, с кем работает REPL.
func (g *Registry) First() *Remote {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.order) == 0 {
		return nil
	}
	return g.remotes[g.order[0]]
}

// ClientFor возвращает делегирующий клиент агента, создавая его при первом
// обращении.
func (g *Registry) ClientFor(id string) (*OrdersClient, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.clients[id]; ok {
		return c, true
	}
	r, ok := g.remotes[id]
	if !ok {
		return nil, false
	}
	c := NewOrdersClientFromRemote(r)
	g.clients[id] = c
	return c, true
}

// Clients возвращает клиентов всех агентов в порядке конфига.
func (g *Registry) Clients() []*OrdersClient {
	out := make([]*OrdersClient, 0, len(g.IDs()))
	for _, id := range g.IDs() {
		if c, ok := g.ClientFor(id); ok {
			out = append(out, c)
		}
	}
	return out
}

// Tools собирает делегирующие инструменты доступных агентов — набор для
// режима «Авто», где модель сама выбирает, к кому обратиться. Недоступные
// агенты пропускаются: инструмент, который заведомо не работает, только сбивает
// модель с толку.
func (g *Registry) Tools(ctx context.Context) []tool.Tool {
	var tools []tool.Tool
	seen := make(map[string]bool)
	for _, id := range g.IDs() {
		r, ok := g.Get(id)
		if !ok {
			continue
		}
		if err := r.Connect(ctx); err != nil {
			g.trace.Logf("agent %q unavailable, skipping its tool: %v", id, err)
			continue
		}
		// Имена инструментов выводятся из карточек и могут совпасть; коллизию
		// разрешаем детерминированно, в порядке конфига.
		if name := r.Profile().ToolName; seen[name] {
			fallback := defaultToolName(id)
			g.trace.Logf("⚠ tool name %q is already taken — renaming agent %q tool to %q", name, id, fallback)
			r.SetToolName(fallback)
		}
		c, ok := g.ClientFor(id)
		if !ok {
			continue
		}
		t := c.Tool()
		seen[t.Name()] = true
		tools = append(tools, t)
	}
	return tools
}

// Summaries собирает блоки возможностей доступных агентов для промпта.
func (g *Registry) Summaries(ctx context.Context) []string {
	var out []string
	for _, id := range g.IDs() {
		r, ok := g.Get(id)
		if !ok {
			continue
		}
		if err := r.Connect(ctx); err != nil {
			continue
		}
		if s := r.Profile().Summary; s != "" {
			out = append(out, s)
		}
	}
	return out
}

// List описывает агентов для UI, попутно проверяя доступность каждого.
func (g *Registry) List(ctx context.Context) []AgentInfo {
	ids := g.IDs()
	out := make([]AgentInfo, 0, len(ids))
	for _, id := range ids {
		r, ok := g.Get(id)
		if !ok {
			continue
		}
		if err := r.Connect(ctx); err != nil {
			g.trace.Logf("agent %q unavailable: %v", id, err)
		}
		out = append(out, AgentInfo{
			ID:          r.ID(),
			Name:        r.Name(),
			Description: r.Description(),
			Verbatim:    r.Verbatim(),
			Available:   r.Available(),
		})
	}
	return out
}
