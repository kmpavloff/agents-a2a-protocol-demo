package agentstore

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

func base() []config.AgentConfig {
	return []config.AgentConfig{
		{ID: "orders", Name: "Агент заказов", URL: "http://localhost:8081"},
		{ID: "ouroboros", Name: "Ouroboros", URL: "http://192.168.1.68:18800"},
	}
}

// Перекрытие идёт целой записью и не двигает агента по списку: порядок задаёт
// пункты селектора, и агент, которому поменяли адрес, не должен прыгать в конец.
func TestMergeOverridesInPlace(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "orders", Name: "Мой воркер", URL: "http://127.0.0.1:9000"}},
	})
	if len(got) != 2 {
		t.Fatalf("agents: got %d, want 2", len(got))
	}
	if got[0].ID != "orders" || got[0].URL != "http://127.0.0.1:9000" || got[0].Name != "Мой воркер" {
		t.Errorf("перекрытая запись: %+v", got[0])
	}
	if got[1].ID != "ouroboros" || got[1].URL != "http://192.168.1.68:18800" {
		t.Errorf("нетронутая запись изменилась: %+v", got[1])
	}
}

// Поля, которых нет в overlay-записи, не подтягиваются из базовой: запись
// перекрывает агента целиком.
func TestMergeOverrideReplacesWholeRecord(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "ouroboros", URL: "http://192.168.1.68:18800"}},
	})
	if got[1].Name != "" {
		t.Errorf("имя должно исчезнуть вместе с перекрытой записью, got %q", got[1].Name)
	}
}

func TestMergeHiddenDropsAgent(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "orders"}, Hidden: true},
	})
	if len(got) != 1 || got[0].ID != "ouroboros" {
		t.Fatalf("скрытый агент не убран: %+v", got)
	}
}

func TestMergeAppendsUnknownIDs(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}},
		{AgentConfig: config.AgentConfig{ID: "billing", URL: "http://localhost:9200"}},
	})
	if len(got) != 4 {
		t.Fatalf("agents: got %d, want 4", len(got))
	}
	if got[2].ID != "shop" || got[3].ID != "billing" {
		t.Errorf("новые агенты должны идти в конец в порядке overlay: %+v", got)
	}
}

// Пустой overlay ничего не меняет: без файла демо работает ровно как раньше.
func TestMergeWithoutOverrides(t *testing.T) {
	got := merge(base(), nil)
	if len(got) != 2 || got[0].ID != "orders" || got[1].ID != "ouroboros" {
		t.Errorf("базовый список изменился: %+v", got)
	}
}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	s, err := New(base(), p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, p
}

func TestStoreCreateAppendsAndPersists(t *testing.T) {
	s, p := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	agents := s.Agents()
	if len(agents) != 3 || agents[2].ID != "shop" {
		t.Fatalf("agents: %+v", agents)
	}
	// Пережило перезапуск: новый Store с тем же файлом видит агента.
	again, err := New(base(), p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(again.Agents()) != 3 {
		t.Errorf("после перечитывания: %+v", again.Agents())
	}
}

func TestStoreCreateRejectsDuplicateAndInvalid(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "orders", URL: "http://x"}); !errors.Is(err, ErrExists) {
		t.Errorf("дубликат id: got %v, want ErrExists", err)
	}
	if err := s.Create(config.AgentConfig{ID: "БОЛЬШИЕ", URL: "http://x"}); err == nil {
		t.Error("невалидный id должен быть отвергнут")
	}
	// Отвергнутая запись не должна осесть в состоянии.
	if len(s.Agents()) != 2 {
		t.Errorf("список изменился после неудачных Create: %+v", s.Agents())
	}
}

func TestStoreUpdateOverridesFileAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("orders", config.AgentConfig{ID: "orders", Name: "Свой", URL: "http://127.0.0.1:9000"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	agents := s.Agents()
	if agents[0].URL != "http://127.0.0.1:9000" || agents[0].Name != "Свой" {
		t.Errorf("правка не применилась: %+v", agents[0])
	}
	recs := s.Records()
	if recs[0].Source != "ui" {
		t.Errorf("источник: got %q, want ui", recs[0].Source)
	}
}

// Пустой пароль в запросе означает «не менять»: наружу мы его не отдаём, и
// форма присылает пустое поле каждый раз, когда пароль не трогали.
func TestStoreUpdateKeepsPasswordWhenEmpty(t *testing.T) {
	s, _ := newStore(t)
	withPass := config.AgentConfig{
		ID: "ouroboros", URL: "http://192.168.1.68:18800",
		Auth: config.AuthConfig{Type: "basic", Username: "ouroboros", Password: "секрет"},
	}
	if err := s.Update("ouroboros", withPass); err != nil {
		t.Fatalf("Update: %v", err)
	}
	noPass := withPass
	noPass.Auth.Password = ""
	noPass.Name = "Новое имя"
	if err := s.Update("ouroboros", noPass); err != nil {
		t.Fatalf("Update: %v", err)
	}
	agents := s.Agents()
	if agents[1].Auth.Password != "секрет" {
		t.Errorf("пароль потерян: %q", agents[1].Auth.Password)
	}
	if agents[1].Name != "Новое имя" {
		t.Errorf("остальные поля не применились: %+v", agents[1])
	}
}

func TestStoreUpdateUnknownAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("нет-такого", config.AgentConfig{ID: "нет-такого", URL: "http://x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// Файлового агента удалить из YAML мы не можем — помечаем скрытым. В списке
// настроек он остаётся, иначе вернуть его можно было бы только правкой файла.
func TestStoreDeleteHidesFileAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Delete("orders"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if agents := s.Agents(); len(agents) != 1 || agents[0].ID != "ouroboros" {
		t.Fatalf("agents: %+v", agents)
	}
	recs := s.Records()
	if len(recs) != 2 || recs[0].ID != "orders" || !recs[0].Hidden {
		t.Fatalf("скрытый агент должен остаться в Records: %+v", recs)
	}
}

// Агент, заведённый через UI, удаляется насовсем: скрывать нечего.
func TestStoreDeleteRemovesUIAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("shop"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, r := range s.Records() {
		if r.ID == "shop" {
			t.Errorf("UI-агент должен исчезнуть целиком: %+v", r)
		}
	}
}

func TestStoreResetRestoresFileVersion(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("orders", config.AgentConfig{ID: "orders", URL: "http://127.0.0.1:9000"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("ouroboros"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset("orders"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := s.Reset("ouroboros"); err != nil {
		t.Fatalf("Reset скрытого: %v", err)
	}
	agents := s.Agents()
	if len(agents) != 2 || agents[0].URL != "http://localhost:8081" {
		t.Errorf("сброс не вернул версию из файла: %+v", agents)
	}
	if s.Records()[0].Source != "file" {
		t.Errorf("источник после сброса: %q", s.Records()[0].Source)
	}
	if err := s.Reset("orders"); !errors.Is(err, ErrNotFound) {
		t.Errorf("сброс без overlay-записи: got %v, want ErrNotFound", err)
	}
}

// Правка адреса, заданного переменной окружения, ничего не изменит — UI обязан
// об этом сказать, иначе пользователь чинит несуществующую поломку.
func TestStoreRecordsMarksEnvLocked(t *testing.T) {
	t.Setenv("A2A_AGENT_ORDERS_URL", "http://from-env:8081")
	s, _ := newStore(t)
	recs := s.Records()
	if recs[0].URL != "http://from-env:8081" {
		t.Errorf("показан адрес из файла, а не действующий: %q", recs[0].URL)
	}
	if len(recs[0].EnvLocked) != 1 || recs[0].EnvLocked[0] != "url" {
		t.Errorf("envLocked: %+v", recs[0].EnvLocked)
	}
}

func TestStoreRecordsReportPasswordWithoutRevealing(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("ouroboros", config.AgentConfig{
		ID: "ouroboros", URL: "http://192.168.1.68:18800",
		Auth: config.AuthConfig{Type: "basic", Username: "u", Password: "секрет"},
	}); err != nil {
		t.Fatal(err)
	}
	r := s.Records()[1]
	if !r.HasPassword {
		t.Error("HasPassword должен быть true")
	}
	if r.Auth.Password != "" {
		t.Errorf("Records не должны нести пароль: %q", r.Auth.Password)
	}
}

func TestStoreNotifiesOnChange(t *testing.T) {
	s, _ := newStore(t)
	var got [][]config.AgentConfig
	s.OnChange(func(agents []config.AgentConfig) { got = append(got, agents) })
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(config.AgentConfig{ID: "orders", URL: "http://x"}); err == nil {
		t.Fatal("ожидался отказ на дубликате")
	}
	if len(got) != 1 {
		t.Fatalf("подписчик вызван %d раз(а), want 1 — только на удачных правках", len(got))
	}
	if len(got[0]) != 3 {
		t.Errorf("подписчику приехал не тот список: %+v", got[0])
	}
}
