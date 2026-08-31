package a2ui

import (
	"encoding/json"
	"testing"
)

// Привязка поля к модели данных — единственный способ вернуть агенту то, что
// набрал пользователь: рендерер пишет введённое в модель, только если value
// пришло объектом {path}, и подставляет его в контекст действия при клике.
//
// Тест сторожит нашу нормализацию: Ingest переписывает чужие компоненты под
// basic-каталог, и потеря привязки где-то там выглядела бы как «агент прислал
// пустое поле» — то есть чужой виной.
func TestIngestKeepsValueBindingAndActionContext(t *testing.T) {
	raw := `[
	  {"version":"v0.9","createSurface":{"surfaceId":"s1"}},
	  {"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[
	    {"id":"root","component":"Column","children":["reason","submit"]},
	    {"id":"reason","component":"TextField","label":"Причина",
	     "value":{"path":"/reason"},"variant":"longText"},
	    {"id":"submit","component":"Button","child":"submit-lbl","variant":"primary",
	     "action":{"event":{"name":"submit_return","context":{
	        "order_id":"ORD-002","reason":{"path":"/reason"}}}}},
	    {"id":"submit-lbl","component":"Text","text":"Отправить"}
	  ]}}
	]`
	var data any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}

	msgs := Ingest([]Part{{MediaType: MIMEType, Data: data}})

	var comps []map[string]any
	for _, m := range msgs {
		if uc, ok := m["updateComponents"].(map[string]any); ok {
			comps, _ = uc["components"].([]map[string]any)
		}
	}
	if len(comps) == 0 {
		t.Fatal("компоненты потерялись целиком")
	}

	byID := map[string]map[string]any{}
	for _, c := range comps {
		id, _ := c["id"].(string)
		byID[id] = c
	}

	// 1. Привязка поля должна остаться объектом: строка "" сделала бы сеттер
	//    рендерера пустышкой, и набранное никуда не записалось бы.
	value := byID["reason"]["value"]
	binding, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value поля стало %T (%v) — привязка потеряна", value, value)
	}
	if binding["path"] != "/reason" {
		t.Errorf("путь привязки = %v", binding["path"])
	}

	// 2. Привязка в контексте действия — то, что рендерер разрешит при клике.
	action, _ := byID["submit"]["action"].(map[string]any)
	event, _ := action["event"].(map[string]any)
	ctx, _ := event["context"].(map[string]any)
	ref, ok := ctx["reason"].(map[string]any)
	if !ok {
		t.Fatalf("в контексте действия reason стало %T (%v)", ctx["reason"], ctx["reason"])
	}
	if ref["path"] != "/reason" {
		t.Errorf("путь в контексте = %v", ref["path"])
	}
	if ctx["order_id"] != "ORD-002" {
		t.Errorf("обычное значение контекста потеряно: %v", ctx["order_id"])
	}
}
