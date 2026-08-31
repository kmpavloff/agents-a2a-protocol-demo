// Package agentstore хранит правки списка агентов, сделанные из веб-интерфейса.
//
// Базовый список приезжает из configs/orchestrator.yaml и остаётся рукописным:
// в нём живут комментарии, объясняющие каждого агента, и переписывать его
// машинно значило бы их потерять. Правки UI ложатся отдельным overlay-файлом
// поверх базового списка.
package agentstore

import (
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// Override — одна overlay-запись: агент целиком плюс признак «скрыт».
//
// Hidden живёт здесь, а не в config.AgentConfig: он описывает не агента, а
// решение UI убрать его из списка. Агент, заведённый в YAML, иначе не
// удаляется — базовый файл мы не трогаем.
type Override struct {
	config.AgentConfig `yaml:",inline"`
	Hidden             bool `yaml:"hidden,omitempty"`
}

// merge накладывает overlay на базовый список.
//
// Запись с совпавшим id заменяет базовую целиком и остаётся на её месте:
// порядок задаёт пункты селектора в браузере и выбор агента для терминального
// REPL, и менять его из-за правки адреса нельзя. Записи с незнакомыми id — это
// заведённые через UI агенты, они уходят в конец в порядке overlay.
func merge(base []config.AgentConfig, over []Override) []config.AgentConfig {
	byID := make(map[string]Override, len(over))
	for _, o := range over {
		byID[o.ID] = o
	}
	used := make(map[string]bool, len(over))
	out := make([]config.AgentConfig, 0, len(base)+len(over))
	for _, b := range base {
		o, ok := byID[b.ID]
		if !ok {
			out = append(out, b)
			continue
		}
		used[o.ID] = true
		if o.Hidden {
			continue
		}
		out = append(out, o.AgentConfig)
	}
	for _, o := range over {
		if used[o.ID] || o.Hidden {
			continue
		}
		out = append(out, o.AgentConfig)
	}
	return out
}
