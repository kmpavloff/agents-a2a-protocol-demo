package a2abridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	neturl "net/url"
	"strings"
	"sync"
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

// Нетронутый агент должен пережить правку соседа: в его *Remote лежат живое
// соединение и зависшая input-required задача, и терять их из-за чужой правки
// нельзя.
func TestApplyKeepsUnchangedRemotes(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	b := ouroborosCfg(s.URL)
	b.ID = "two"
	g := NewRegistry([]config.AgentConfig{a, b}, nil)

	before, _ := g.Get("one")
	changed := b
	changed.Name = "Другое имя"
	g.Apply([]config.AgentConfig{a, changed})

	after, ok := g.Get("one")
	if !ok || before != after {
		t.Error("нетронутый агент пересоздан")
	}
	two, ok := g.Get("two")
	if !ok || two.Name() != "Другое имя" {
		t.Errorf("изменённый агент не обновлён: %v", ok)
	}
}

func TestApplyAddsAndRemoves(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	g := NewRegistry([]config.AgentConfig{a}, nil)

	b := ouroborosCfg(s.URL)
	b.ID = "two"
	g.Apply([]config.AgentConfig{b})

	if _, ok := g.Get("one"); ok {
		t.Error("удалённый агент остался в реестре")
	}
	if _, ok := g.Get("two"); !ok {
		t.Error("новый агент не появился")
	}
	if ids := g.IDs(); len(ids) != 1 || ids[0] != "two" {
		t.Errorf("порядок: %v", ids)
	}
}

// Изменённый агент теряет и кэшированного клиента: тот держит ссылку на старый
// *Remote и продолжал бы ходить по прежнему адресу.
func TestApplyDropsClientOfChangedAgent(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	g := NewRegistry([]config.AgentConfig{a}, nil)
	before, _ := g.ClientFor("one")

	changed := a
	changed.Name = "Другое"
	g.Apply([]config.AgentConfig{changed})

	after, _ := g.ClientFor("one")
	if before == after {
		t.Error("клиент изменённого агента должен быть пересоздан")
	}
}

func TestApplyBumpsGeneration(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	g := NewRegistry([]config.AgentConfig{a}, nil)
	gen := g.Generation()

	same := a
	g.Apply([]config.AgentConfig{same})
	if g.Generation() != gen {
		t.Error("поколение не должно расти, когда ничего не изменилось")
	}

	changed := a
	changed.URL = "http://127.0.0.1:1"
	g.Apply([]config.AgentConfig{changed})
	if g.Generation() == gen {
		t.Error("поколение должно вырасти после правки")
	}
}

// Клиент, созданный уже после старта, обязан получить те же обработчики, что и
// созданные в конструкторе исполнителя, — иначе виджеты нового агента молча
// пропадут.
func TestClientInitRunsForLaterClients(t *testing.T) {
	s := startOuroborosStub(t, false)
	a := ouroborosCfg(s.URL)
	a.ID = "one"
	g := NewRegistry([]config.AgentConfig{a}, nil)

	var inited []string
	g.SetClientInit(func(c *OrdersClient) { inited = append(inited, c.Profile().ToolName) })

	b := ouroborosCfg(s.URL)
	b.ID = "two"
	g.Apply([]config.AgentConfig{a, b})
	if _, ok := g.ClientFor("two"); !ok {
		t.Fatal("клиент нового агента не создан")
	}
	if len(inited) != 1 {
		t.Fatalf("SetClientInit вызван %d раз(а), want 1", len(inited))
	}
}

// gatedAgent ставит перед заглушкой задержку на запрос карточки: так первое
// подключение «висит», пока тест не откроет ворота, — как у живого агента,
// чья карточка отвечает секундами.
func gatedAgent(t *testing.T) (url string, arrived <-chan struct{}, open func()) {
	t.Helper()
	s := startOuroborosStub(t, false)
	target, err := neturl.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	gate, hit := make(chan struct{}), make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/") {
			select {
			case hit <- struct{}{}:
			default:
			}
			<-gate
		}
		proxy.ServeHTTP(w, r)
	}))
	var once sync.Once
	opener := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(func() { opener(); srv.Close() })
	return srv.URL, hit, opener
}

// Пока первое подключение в пути, агент ещё не проверен: «не отвечает» на
// этом месте — домысел, а не факт.
func TestRegistryListIsNotProbedWhileConnecting(t *testing.T) {
	url, arrived, open := gatedAgent(t)
	g := NewRegistry([]config.AgentConfig{ouroborosCfg(url)}, nil)

	g.List(context.Background()) // запускает фоновую проверку
	<-arrived
	if info := g.List(context.Background())[0]; info.Probed {
		t.Errorf("подключение ещё идёт, а агент уже помечен проверенным: %+v", info)
	}

	open()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if info := g.List(context.Background())[0]; info.Probed {
			if !info.Available {
				t.Errorf("живой агент помечен недоступным: %+v", info)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("фоновая проверка не завершилась")
}

// Ход, пришедший во время первого подключения, должен его дождаться, а не
// счесть агента лежащим: иначе запрос сразу после загрузки страницы падает
// с «not responding» у вполне живого агента.
func TestRegistryReadyWaitsForInFlightConnect(t *testing.T) {
	url, arrived, open := gatedAgent(t)
	g := NewRegistry([]config.AgentConfig{ouroborosCfg(url)}, nil)
	r, _ := g.Get("ouroboros")

	g.List(context.Background())
	<-arrived
	done := make(chan error, 1)
	go func() { done <- g.ready(context.Background(), r) }()
	time.Sleep(100 * time.Millisecond) // ready уже внутри, пока ворота закрыты
	open()
	if err := <-done; err != nil {
		t.Fatalf("ready во время подключения: %v", err)
	}
}
