package agentstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// overlayHeader объясняет тому, кто откроет файл руками, почему его правки
// проживут до первого сохранения из UI.
const overlayHeader = "# Управляется из веб-интерфейса (экран «Настройки»).\n" +
	"# Правки руками будут перезаписаны при следующем сохранении.\n"

// overlayDoc — форма файла. Отдельный тип, а не голый список: так overlay
// выглядит как orchestrator.yaml, и в него позже можно добавить поля, не ломая
// уже написанные файлы.
type overlayDoc struct {
	Agents []Override `yaml:"agents"`
}

// loadOverlay читает overlay. Отсутствие файла — обычное состояние свежего
// клона, а не ошибка: правок из UI просто ещё не было.
func loadOverlay(path string) ([]Override, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agents overlay %s: %w", path, err)
	}
	var doc overlayDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse agents overlay %s: %w", path, err)
	}
	return doc.Agents, nil
}

// saveOverlay пишет overlay целиком, атомарно: сначала временный файл рядом,
// потом переименование. Оборванная запись иначе оставила бы половину списка
// агентов, и оркестратор не поднялся бы вовсе.
func saveOverlay(path string, over []Override) error {
	body, err := yaml.Marshal(overlayDoc{Agents: over})
	if err != nil {
		return fmt.Errorf("marshal agents overlay: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Временный файл — в том же каталоге: переименование атомарно только в
	// пределах одной файловой системы.
	tmp, err := os.CreateTemp(dir, ".agents-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp overlay: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op после удачного Rename
	// Пароли лежат открытым текстом, как и в orchestrator.yaml.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp overlay: %w", err)
	}
	if _, err := tmp.WriteString(overlayHeader); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp overlay: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp overlay: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp overlay: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp overlay to %s: %w", path, err)
	}
	return nil
}
