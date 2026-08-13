package a2abridge

import (
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

// Здесь собрано всё знание о том, как A2UI выглядит частью A2A-сообщения.
// Раньше `part.MediaType = a2ui.MIMEType` стояло в четырёх местах, а проверка
// на него — в трёх; спека расширения при этом опознаёт часть по
// metadata.mimeType, а не по mediaType.
//
// Правило: принимаем либерально (обе пометки, оба типа — см. a2ui.IsA2UI),
// отдаём строго по спеке.

// newA2UIPart собирает часть с разметкой. `data` — массив сообщений, как
// требует спека («The data field ... MUST be an array of messages»), даже когда
// сообщение одно.
//
// Пометок ставится две: metadata.mimeType — та, по которой часть опознаёт
// стандартный клиент, и mediaType — родное поле A2A, по которому её узнаёт наш
// собственный код и живые агенты. Вторая ничему не мешает и не заменяет первую.
func newA2UIPart(msgs []map[string]any) *a2a.Part {
	data := make([]any, 0, len(msgs))
	for _, m := range msgs {
		data = append(data, m)
	}
	part := a2a.NewDataPart(data)
	part.MediaType = a2ui.MIMEType
	part.Metadata = map[string]any{a2ui.MIMEKey: a2ui.MIMEType}
	return part
}

// newActionPart собирает событие клиента (нажатие кнопки) по схеме
// client_to_server: тот же тип части, но полезная нагрузка — действие.
func newActionPart(name, surfaceID, sourceComponentID string, ctx map[string]any, at time.Time) *a2a.Part {
	msg := a2ui.NewAction(name, surfaceID, sourceComponentID, ctx, at.UTC().Format(time.RFC3339))
	return newA2UIPart([]map[string]any{msg})
}

// isA2UIPart отвечает, несёт ли часть разметку A2UI.
func isA2UIPart(p *a2a.Part) bool {
	return p != nil && a2ui.IsA2UI(p.MediaType, p.Metadata)
}

// a2uiParts переводит A2A-части в транспортно-независимый вид, понятный
// пакету a2ui.
func a2uiParts(parts []*a2a.Part) []a2ui.Part {
	out := make([]a2ui.Part, 0, len(parts))
	for _, p := range parts {
		if p == nil {
			continue
		}
		out = append(out, a2ui.Part{
			MediaType: p.MediaType,
			Metadata:  p.Metadata,
			Text:      p.Text(),
			Data:      p.Data(),
		})
	}
	return out
}
