package a2ui

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Part — минимальный вид A2A-части, нужный Ingest. Пакет a2ui намеренно не
// импортирует a2a: транспорт остаётся снаружи, здесь живёт только знание о
// формате A2UI.
type Part struct {
	MediaType string
	Text      string
	Data      any
}

// knownMessages — сообщения A2UI, которые понимает наш рендерер. Всё остальное
// отбрасывается, чтобы не смущать процессор на стороне браузера.
var knownMessages = []string{"createSurface", "updateComponents", "updateDataModel", "deleteSurface"}

// basicComponents — 18 компонентов basic-каталога v0.9. Ingest обязан выдавать
// только их: объявленный агентами каталог именно этот, и наш рендерер отвергает
// всё, чего в нём нет.
var basicComponents = map[string]bool{
	"Text": true, "Image": true, "Icon": true, "Video": true, "AudioPlayer": true,
	"Row": true, "Column": true, "List": true, "Card": true, "Tabs": true,
	"Modal": true, "Divider": true, "Button": true, "TextField": true,
	"CheckBox": true, "ChoicePicker": true, "Slider": true, "DateTimeInput": true,
}

// Ingest достаёт A2UI-сообщения из частей ответа удалённого агента и приводит
// их к форме, которую понимает наш рендерер: плоские компоненты basic-каталога
// v0.9.
//
// Толерантен по входу, потому что живой агент отдаёт не то, что обещает его
// контракт: A2UI приезжает и как DataPart с массивом сообщений, и как одиночный
// объект, и как ТЕКСТОВАЯ часть с mediaType application/a2ui+json, внутри
// которой JSON-строка. Компоненты приезжают и плоскими, и обёрнутыми
// ({"Text": {...}}), и типов, которых нет в объявленном самим агентом каталоге.
//
// Инвариант: Ingest никогда не паникует и не возвращает ошибку. Худший исход —
// блёклый, но валидный набор компонентов: чужой агент не должен ронять наш UI.
func Ingest(parts []Part) []map[string]any {
	var out []map[string]any
	st := &ingestState{rooted: map[string]bool{}}
	for _, p := range parts {
		if p.MediaType != MIMEType {
			continue
		}
		for _, raw := range messagesFrom(p) {
			if m, ok := normalizeMessage(raw, st); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// ingestState — состояние одного вызова Ingest: счётчик id для компонентов,
// приехавших без него, и поверхности, которым корень уже назначен.
type ingestState struct {
	gen    int
	rooted map[string]bool
}

// messagesFrom разбирает полезную нагрузку части в список сырых A2UI-сообщений,
// принимая все три встреченные раскладки.
func messagesFrom(p Part) []map[string]any {
	payload := p.Data
	if payload == nil {
		if strings.TrimSpace(p.Text) == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(p.Text), &payload); err != nil {
			return nil
		}
	}
	switch v := payload.(type) {
	case []any:
		var msgs []map[string]any
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				msgs = append(msgs, m)
			}
		}
		return msgs
	case []map[string]any:
		return v
	case map[string]any:
		return []map[string]any{v}
	}
	return nil
}

// normalizeMessage переписывает версию сообщения на нашу и нормализует
// компоненты. Возвращает ok=false для сообщений неизвестного вида.
func normalizeMessage(msg map[string]any, st *ingestState) (map[string]any, bool) {
	kind := ""
	for _, k := range knownMessages {
		if _, ok := msg[k]; ok {
			kind = k
			break
		}
	}
	if kind == "" {
		return nil, false
	}
	out := make(map[string]any, len(msg))
	for k, v := range msg {
		out[k] = v
	}
	out["version"] = Version

	if kind == "createSurface" {
		// Компоненты мы приводим к basic-каталогу, поэтому и объявлять надо
		// его: иначе клиент отвергнет поверхность, которую Ingest уже сделал
		// пригодной к отрисовке.
		if payload, ok := out["createSurface"].(map[string]any); ok {
			copied := make(map[string]any, len(payload))
			for k, v := range payload {
				copied[k] = v
			}
			copied["catalogId"] = CatalogID
			out["createSurface"] = copied
		}
	}
	if kind != "updateComponents" {
		return out, true
	}
	payload, ok := msg["updateComponents"].(map[string]any)
	if !ok {
		return nil, false
	}
	raw := componentList(payload["components"])
	if len(raw) == 0 {
		return nil, false
	}
	comps := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		comps = append(comps, normalizeComponent(c, st)...)
	}
	if len(comps) == 0 {
		return nil, false
	}
	labelButtonActions(comps)
	// Рендерер строит дерево от компонента с id ровно "root"; без него
	// поверхность навсегда остаётся в состоянии «Loading surface…». Чужие
	// агенты называют корень как угодно — назначаем его сами.
	if surfaceID, _ := payload["surfaceId"].(string); !st.rooted[surfaceID] {
		comps = ensureRoot(comps)
		st.rooted[surfaceID] = true
	}
	next := make(map[string]any, len(payload))
	for k, v := range payload {
		next[k] = v
	}
	next["components"] = comps
	out["updateComponents"] = next
	return out, true
}

// labelButtonActions дописывает в контекст действия человекочитаемую подпись
// кнопки. Без неё клиенту нечего показать в ленте, кроме служебного имени
// действия («return_order» вместо «Подтвердить возврат»): в самом событии
// подписи нет, она живёт в дочернем Text.
func labelButtonActions(comps []map[string]any) {
	byID := make(map[string]map[string]any, len(comps))
	for _, c := range comps {
		if id, ok := c["id"].(string); ok {
			byID[id] = c
		}
	}
	for _, c := range comps {
		if typ, _ := c["component"].(string); typ != "Button" {
			continue
		}
		action, ok := c["action"].(map[string]any)
		if !ok {
			continue
		}
		event, ok := action["event"].(map[string]any)
		if !ok {
			continue
		}
		ctx, ok := event["context"].(map[string]any)
		if !ok {
			ctx = map[string]any{}
			event["context"] = ctx
		}
		if _, exists := ctx["label"]; exists {
			continue
		}
		child, _ := c["child"].(string)
		if label, _ := byID[child]["text"].(string); label != "" {
			ctx["label"] = label
		}
	}
}

// componentList приводит поле components к списку объектов, переживая и
// []any, и уже типизированный []map[string]any.
func componentList(v any) []map[string]any {
	switch list := v.(type) {
	case []map[string]any:
		return list
	case []any:
		out := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// unwrap разбирает обёрнутую форму компонента ({"Text": {...}}), которую шлёт
// внешний агент, в пару «тип, свойства». Плоская форма возвращается как есть.
func unwrap(c map[string]any) (string, map[string]any) {
	if t, ok := c["component"].(string); ok && t != "" {
		props := make(map[string]any, len(c))
		for k, v := range c {
			if k != "component" {
				props[k] = v
			}
		}
		return t, props
	}
	if len(c) == 1 {
		for name, v := range c {
			if props, ok := v.(map[string]any); ok {
				return name, props
			}
		}
	}
	return "", nil
}

// normalizeComponent превращает один входящий компонент в один или несколько
// компонентов basic-каталога. Id контейнера всегда сохраняется: на него
// ссылается children родителя, и потеря id разорвала бы дерево.
func normalizeComponent(c map[string]any, st *ingestState) []map[string]any {
	typ, props := unwrap(c)
	if typ == "" {
		return nil
	}
	id, _ := props["id"].(string)
	if id == "" {
		st.gen++
		id = fmt.Sprintf("gen%d", st.gen)
	}
	delete(props, "id")

	switch typ {
	case "Button":
		return normalizeButton(id, props)
	case "Heading":
		txt, _ := props["text"].(string)
		return []map[string]any{text(id, txt, "h3")}
	case "Metric":
		label, _ := props["label"].(string)
		value := fmt.Sprintf("%v", props["value"])
		line := value
		if label != "" {
			line = "**" + label + ":** " + value
		}
		return []map[string]any{text(id, line, "body")}
	case "Callout":
		return normalizeCallout(id, props)
	case "Table":
		return []map[string]any{text(id, markdownTable(props), "body")}
	}

	if !basicComponents[typ] {
		// Незнакомый компонент лучше показать дампом, чем потерять молча.
		return []map[string]any{text(id, unknownDump(typ, props), "body")}
	}
	out := map[string]any{"id": id, "component": typ}
	for k, v := range props {
		out[k] = v
	}
	normalizeAction(out)
	return []map[string]any{out}
}

// normalizeAction приводит контекст события к объекту: схема A2UI требует
// «ключ → значение», а агент присылает массив пар {key, value}, на котором
// рендерер соберёт бессмысленный контекст.
func normalizeAction(c map[string]any) {
	action, ok := c["action"].(map[string]any)
	if !ok {
		return
	}
	event, ok := action["event"].(map[string]any)
	if !ok {
		return
	}
	pairs, ok := event["context"].([]any)
	if !ok {
		return
	}
	ctx := make(map[string]any, len(pairs))
	for _, p := range pairs {
		pair, ok := p.(map[string]any)
		if !ok {
			continue
		}
		key, ok := pair["key"].(string)
		if !ok || key == "" {
			continue
		}
		ctx[key] = pair["value"]
	}
	event["context"] = ctx
}

// normalizeButton приводит кнопку к форме каталога: у него нет свойства label,
// подпись живёт отдельным дочерним Text.
func normalizeButton(id string, props map[string]any) []map[string]any {
	btn := map[string]any{"id": id, "component": "Button"}
	for k, v := range props {
		if k == "label" {
			continue
		}
		btn[k] = v
	}
	normalizeAction(btn)
	if _, hasChild := btn["child"]; hasChild {
		return []map[string]any{btn}
	}
	label, _ := props["label"].(string)
	if label == "" {
		label = "OK"
	}
	labelID := id + "__lbl"
	btn["child"] = labelID
	return []map[string]any{btn, text(labelID, label, "body")}
}

// normalizeCallout разворачивает плашку в колонку из заголовка и текста,
// сохраняя id самой плашки за колонкой.
func normalizeCallout(id string, props map[string]any) []map[string]any {
	titleID, bodyID := id+"__t", id+"__b"
	title, _ := props["title"].(string)
	body, _ := props["text"].(string)
	children := []any{}
	comps := []map[string]any{nil} // место под колонку
	if title != "" {
		children = append(children, titleID)
		comps = append(comps, text(titleID, title, "h3"))
	}
	if body != "" {
		children = append(children, bodyID)
		comps = append(comps, text(bodyID, body, "body"))
	}
	if len(children) == 0 {
		return []map[string]any{text(id, "", "body")}
	}
	comps[0] = map[string]any{"id": id, "component": "Column", "children": children}
	return comps
}

// markdownTable рисует таблицу разметкой: Text в браузере рендерится через
// markdown-it, поэтому она станет настоящей <table>.
func markdownTable(props map[string]any) string {
	cols := componentList(props["columns"])
	rows := componentList(props["rows"])
	if len(cols) == 0 {
		return unknownDump("Table", props)
	}
	var b strings.Builder
	keys := make([]string, 0, len(cols))
	b.WriteString("|")
	for _, c := range cols {
		key, _ := c["key"].(string)
		label, _ := c["label"].(string)
		if label == "" {
			label = key
		}
		keys = append(keys, key)
		fmt.Fprintf(&b, " %s |", label)
	}
	b.WriteString("\n|")
	for range keys {
		b.WriteString(" --- |")
	}
	for _, r := range rows {
		b.WriteString("\n|")
		for _, k := range keys {
			v, ok := r[k]
			if !ok || v == nil {
				v = ""
			}
			fmt.Fprintf(&b, " %v |", v)
		}
	}
	return b.String()
}

// ensureRoot гарантирует, что у набора есть компонент с id "root": рендерер
// строит дерево именно от него, и без него поверхность навсегда остаётся в
// состоянии «Loading surface…». Чужие агенты называют корень как угодно,
// поэтому недостающий корень добавляется отдельной колонкой поверх верхних
// компонентов — переименовывать чужие id нельзя, на них могут ссылаться
// последующие обновления той же поверхности.
func ensureRoot(comps []map[string]any) []map[string]any {
	referenced := make(map[string]bool, len(comps))
	for _, c := range comps {
		if child, ok := c["child"].(string); ok {
			referenced[child] = true
		}
		if children, ok := c["children"].([]any); ok {
			for _, ch := range children {
				if name, ok := ch.(string); ok {
					referenced[name] = true
				}
			}
		}
	}
	var tops []any
	for _, c := range comps {
		id, _ := c["id"].(string)
		if id == "root" {
			return comps // корень уже на месте
		}
		if !referenced[id] {
			tops = append(tops, id)
		}
	}
	if len(tops) == 0 {
		// Дерево замкнуто само на себя — вешаем корень на первый компонент.
		if len(comps) == 0 {
			return comps
		}
		first, _ := comps[0]["id"].(string)
		tops = []any{first}
	}
	root := map[string]any{"id": "root", "component": "Column", "children": tops}
	return append([]map[string]any{root}, comps...)
}

// unknownDump компактно показывает то, что мы не умеем отрисовать, вместо того
// чтобы выбросить компонент.
func unknownDump(typ string, props map[string]any) string {
	raw, err := json.Marshal(props)
	if err != nil {
		return typ
	}
	return "`" + typ + "` " + string(raw)
}

// surfaceIDKeys — сообщения, у которых есть surfaceId.
var surfaceIDKeys = []string{"createSurface", "updateComponents", "updateDataModel", "deleteSurface"}

// RetagSurfaces приписывает суффикс ко всем surfaceId в наборе сообщений.
//
// Нужно потому, что агент нередко выводит surfaceId из идентификатора
// контекста, а контекст живёт всю сессию: второй ход присылает createSurface с
// тем же id, и рендерер падает с «Surface … already exists». В ленте каждый ход
// — отдельная карточка, поэтому и поверхность у него должна быть своя.
//
// Ссылки внутри набора переписываются согласованно, так что updateComponents
// по-прежнему попадает в свою поверхность.
func RetagSurfaces(msgs []map[string]any, suffix string) []map[string]any {
	if suffix == "" {
		return msgs
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		next := make(map[string]any, len(m))
		for k, v := range m {
			next[k] = v
		}
		for _, key := range surfaceIDKeys {
			payload, ok := next[key].(map[string]any)
			if !ok {
				continue
			}
			id, ok := payload["surfaceId"].(string)
			if !ok || id == "" {
				continue
			}
			copied := make(map[string]any, len(payload))
			for k, v := range payload {
				copied[k] = v
			}
			copied["surfaceId"] = id + suffix
			next[key] = copied
		}
		out = append(out, next)
	}
	return out
}
