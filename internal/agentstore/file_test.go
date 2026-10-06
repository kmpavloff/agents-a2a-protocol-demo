package agentstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// Отсутствие overlay — обычное состояние свежего клона, а не ошибка.
func TestLoadOverlayMissingFile(t *testing.T) {
	over, err := loadOverlay(filepath.Join(t.TempDir(), "нет-такого.yaml"))
	if err != nil {
		t.Fatalf("loadOverlay: %v", err)
	}
	if over != nil {
		t.Errorf("overlay: got %+v, want nil", over)
	}
}

func TestSaveLoadOverlayRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	want := []Override{
		{AgentConfig: config.AgentConfig{
			ID: "ouroboros", Name: "Ouroboros", URL: "http://192.168.1.68:18800",
			Verbatim: true, Timeout: "240s", Skills: []string{"shop", "support"},
			Auth: config.AuthConfig{Type: "basic", Username: "ouroboros", Password: "test"},
		}},
		{AgentConfig: config.AgentConfig{ID: "orders"}, Hidden: true},
	}
	if err := saveOverlay(p, want); err != nil {
		t.Fatalf("saveOverlay: %v", err)
	}
	got, err := loadOverlay(p)
	if err != nil {
		t.Fatalf("loadOverlay: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("overlay: got %d записей, want 2", len(got))
	}
	if !got[0].AgentConfig.Equal(want[0].AgentConfig) || got[0].Hidden != want[0].Hidden {
		t.Errorf("первая запись:\n got %+v\nwant %+v", got[0], want[0])
	}
	if !got[1].Hidden || got[1].ID != "orders" {
		t.Errorf("вторая запись: %+v", got[1])
	}
}

// Шапка объясняет тому, кто откроет файл руками, что его перезапишут.
func TestSaveOverlayWritesHeader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	if err := saveOverlay(p, nil); err != nil {
		t.Fatalf("saveOverlay: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "#") {
		t.Errorf("файл должен начинаться с комментария-предупреждения:\n%s", b)
	}
}

// Запись идёт через временный файл: оборванная запись не должна оставить
// половину конфига — и не должна оставить мусор рядом.
func TestSaveOverlayLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agents.local.yaml")
	if err := saveOverlay(p, []Override{{AgentConfig: config.AgentConfig{ID: "a", URL: "http://x"}}}); err != nil {
		t.Fatalf("saveOverlay: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "agents.local.yaml" {
		t.Errorf("в каталоге остался мусор: %v", entries)
	}
}

// Пароль лежит в файле открытым текстом — права должны это учитывать.
func TestSaveOverlayFilePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	if err := saveOverlay(p, nil); err != nil {
		t.Fatalf("saveOverlay: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("права: got %o, want 600", fi.Mode().Perm())
	}
}

// Overlay, записанный до появления списка навыков, несёт одиночный skill: —
// он читается как список из одного навыка.
func TestLoadOverlayMigratesLegacySkill(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	if err := os.WriteFile(p, []byte("agents:\n  - id: o\n    url: http://x\n    skill: shop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadOverlay(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Skills) != 1 || got[0].Skills[0] != "shop" || got[0].LegacySkill != "" {
		t.Errorf("миграция skill: %+v", got[0].AgentConfig)
	}
}
