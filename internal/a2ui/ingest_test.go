package a2ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertRenderable проверяет инвариант, ради которого Ingest существует: каждый
// компонент плоский, с id и типом из basic-каталога.
func assertRenderable(t *testing.T, msgs []map[string]any) {
	t.Helper()
	if len(msgs) == 0 {
		t.Fatal("no A2UI messages")
	}
	seenUpdate := false
	for _, m := range msgs {
		if m["version"] != Version {
			t.Errorf("version not normalised: %v", m["version"])
		}
		uc, ok := m["updateComponents"].(map[string]any)
		if !ok {
			continue
		}
		seenUpdate = true
		comps, _ := uc["components"].([]map[string]any)
		if len(comps) == 0 {
			t.Fatal("updateComponents without components")
		}
		for _, c := range comps {
			id, _ := c["id"].(string)
			typ, _ := c["component"].(string)
			if id == "" {
				t.Errorf("component without id: %v", c)
			}
			if !basicComponents[typ] {
				t.Errorf("component %q is not in the basic catalog: %v", typ, c)
			}
		}
	}
	if !seenUpdate {
		t.Fatal("no updateComponents message")
	}
	// Без компонента с id "root" рендерер не начнёт обход дерева и навсегда
	// останется в состоянии «Loading surface…».
	if _, ok := componentsByID(msgs)["root"]; !ok {
		t.Error("no component with id \"root\": the surface will never render")
	}
}

// componentsByID собирает плоский индекс компонентов из всех сообщений.
func componentsByID(msgs []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, m := range msgs {
		uc, ok := m["updateComponents"].(map[string]any)
		if !ok {
			continue
		}
		comps, _ := uc["components"].([]map[string]any)
		for _, c := range comps {
			id, _ := c["id"].(string)
			out[id] = c
		}
	}
	return out
}

// TestIngestRealOuroborosResponse гоняет Ingest на живом ответе внешнего
// агента, снятом 2026-08-10: текстовая часть с mediaType a2ui+json, обёрнутые
// компоненты и четыре типа вне объявленного им же каталога.
func TestIngestRealOuroborosResponse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "ouroboros_order_card.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Artifacts []struct {
				Parts []struct {
					Text      string `json:"text"`
					Data      any    `json:"data"`
					MediaType string `json:"mediaType"`
				} `json:"parts"`
			} `json:"artifacts"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	var parts []Part
	for _, p := range resp.Result.Artifacts[0].Parts {
		parts = append(parts, Part{MediaType: p.MediaType, Text: p.Text, Data: p.Data})
	}
	msgs := Ingest(parts)
	assertRenderable(t, msgs)

	// Дерево должно остаться связным: каждый id из children существует.
	byID := componentsByID(msgs)
	for id, c := range byID {
		children, ok := c["children"].([]any)
		if !ok {
			continue
		}
		for _, ch := range children {
			name, ok := ch.(string)
			if !ok {
				continue
			}
			if _, exists := byID[name]; !exists {
				t.Errorf("component %q references missing child %q", id, name)
			}
		}
	}
}

// TestIngestContractShape — форма из контракта: DataPart с массивом сообщений и
// уже плоскими компонентами. Должна проходить насквозь.
func TestIngestContractShape(t *testing.T) {
	data := []any{
		map[string]any{"version": "v0.9.1", "createSurface": map[string]any{"surfaceId": "s1", "catalogId": CatalogID}},
		map[string]any{"version": "v0.9.1", "updateComponents": map[string]any{
			"surfaceId": "s1",
			"components": []any{
				map[string]any{"id": "title", "component": "Text", "text": "Order", "variant": "h2"},
			},
		}},
	}
	msgs := Ingest([]Part{{MediaType: MIMEType, Data: data}})
	assertRenderable(t, msgs)
	if len(msgs) != 2 {
		t.Fatalf("messages: got %d, want 2", len(msgs))
	}
	title := componentsByID(msgs)["title"]
	if title["text"] != "Order" || title["variant"] != "h2" {
		t.Errorf("flat component altered: %v", title)
	}
}

func TestIngestDegradesUnknownComponents(t *testing.T) {
	comps := []any{
		map[string]any{"Heading": map[string]any{"id": "h", "text": "Заказ", "level": 2}},
		map[string]any{"Metric": map[string]any{"id": "m", "label": "Итого", "value": "3 499 ₽"}},
		map[string]any{"Callout": map[string]any{"id": "c", "variant": "success", "title": "Доставлен", "text": "Иван"}},
		map[string]any{"Table": map[string]any{"id": "t",
			"columns": []any{map[string]any{"key": "sku", "label": "Артикул"}},
			"rows":    []any{map[string]any{"sku": "WIDGET-RED"}}}},
		map[string]any{"Button": map[string]any{"id": "b", "label": "Отследить", "variant": "secondary"}},
		map[string]any{"Sparkline": map[string]any{"id": "x", "points": []any{1, 2}}},
	}
	data := []any{
		map[string]any{"createSurface": map[string]any{"surfaceId": "s1", "catalogId": CatalogID}},
		map[string]any{"updateComponents": map[string]any{"surfaceId": "s1", "components": comps}},
	}
	msgs := Ingest([]Part{{MediaType: MIMEType, Data: data}})
	assertRenderable(t, msgs)

	byID := componentsByID(msgs)
	if byID["h"]["component"] != "Text" || byID["h"]["variant"] != "h3" {
		t.Errorf("Heading → %v", byID["h"])
	}
	if txt, _ := byID["m"]["text"].(string); !strings.Contains(txt, "Итого") || !strings.Contains(txt, "3 499") {
		t.Errorf("Metric → %v", byID["m"])
	}
	if byID["c"]["component"] != "Column" {
		t.Errorf("Callout → %v", byID["c"])
	}
	if byID["c__t"] == nil || byID["c__b"] == nil {
		t.Errorf("Callout children missing: %v", byID)
	}
	if txt, _ := byID["t"]["text"].(string); !strings.Contains(txt, "| Артикул |") || !strings.Contains(txt, "WIDGET-RED") {
		t.Errorf("Table → %v", byID["t"])
	}
	if byID["b"]["component"] != "Button" || byID["b"]["child"] != "b__lbl" {
		t.Errorf("Button → %v", byID["b"])
	}
	if byID["b__lbl"]["text"] != "Отследить" {
		t.Errorf("Button label → %v", byID["b__lbl"])
	}
	if byID["x"]["component"] != "Text" {
		t.Errorf("unknown component → %v", byID["x"])
	}
}

func TestIngestIgnoresGarbage(t *testing.T) {
	cases := [][]Part{
		nil,
		{{MediaType: "text/plain", Text: "просто текст"}},
		{{MediaType: MIMEType, Text: "не json"}},
		{{MediaType: MIMEType, Data: 42}},
		{{MediaType: MIMEType, Data: []any{"строка вместо объекта"}}},
		{{MediaType: MIMEType, Data: map[string]any{"updateComponents": "не объект"}}},
		{{MediaType: MIMEType, Data: map[string]any{"чтоТоСвоё": map[string]any{}}}},
	}
	for i, parts := range cases {
		if got := Ingest(parts); len(got) != 0 {
			t.Errorf("case %d: expected no messages, got %v", i, got)
		}
	}
}

// Контекст события по схеме — объект «ключ → значение»; внешний агент шлёт
// массив пар, на котором рендерер собрал бы бессмысленный контекст.
func TestIngestNormalisesActionContext(t *testing.T) {
	comps := []any{
		map[string]any{"id": "btn", "component": "Button", "child": "lbl",
			"action": map[string]any{"event": map[string]any{
				"name":    "return_order",
				"context": []any{map[string]any{"key": "order_id", "value": "ORD-001"}},
			}}},
		map[string]any{"id": "lbl", "component": "Text", "text": "Возврат"},
	}
	data := []any{
		map[string]any{"createSurface": map[string]any{"surfaceId": "s1", "catalogId": CatalogID}},
		map[string]any{"updateComponents": map[string]any{"surfaceId": "s1", "components": comps}},
	}
	msgs := Ingest([]Part{{MediaType: MIMEType, Data: data}})
	assertRenderable(t, msgs)

	btn := componentsByID(msgs)["btn"]
	event := btn["action"].(map[string]any)["event"].(map[string]any)
	ctx, ok := event["context"].(map[string]any)
	if !ok {
		t.Fatalf("context is not an object: %#v", event["context"])
	}
	if ctx["order_id"] != "ORD-001" {
		t.Errorf("context: %v", ctx)
	}
}

func TestEnsureRootWrapsForeignTree(t *testing.T) {
	// Чужой корень называется как угодно — рендереру нужен именно "root".
	comps := []map[string]any{
		{"id": "order_card", "component": "Column", "children": []any{"title"}},
		{"id": "title", "component": "Text", "text": "Заказ"},
	}
	got := ensureRoot(comps)
	root := got[0]
	if root["id"] != "root" || root["component"] != "Column" {
		t.Fatalf("root: %v", root)
	}
	if children, _ := root["children"].([]any); len(children) != 1 || children[0] != "order_card" {
		t.Errorf("root children: %v", root["children"])
	}
	// Исходные id не трогаем: на них ссылаются последующие обновления.
	if got[1]["id"] != "order_card" {
		t.Errorf("original id renamed: %v", got[1])
	}
}

func TestEnsureRootKeepsExistingRoot(t *testing.T) {
	comps := []map[string]any{
		{"id": "root", "component": "Column", "children": []any{"title"}},
		{"id": "title", "component": "Text", "text": "Заказ"},
	}
	if got := ensureRoot(comps); len(got) != 2 {
		t.Errorf("existing root must not be wrapped again: %v", got)
	}
}

func TestEnsureRootCollectsSeveralTops(t *testing.T) {
	comps := []map[string]any{
		{"id": "a", "component": "Text", "text": "раз"},
		{"id": "b", "component": "Text", "text": "два"},
	}
	got := ensureRoot(comps)
	children, _ := got[0]["children"].([]any)
	if len(children) != 2 {
		t.Errorf("both tops must hang under root: %v", got[0])
	}
}
