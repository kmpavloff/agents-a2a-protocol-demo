package agentstore

import (
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
