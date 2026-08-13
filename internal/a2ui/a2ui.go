// Package a2ui maps the demo's domain widgets to Google's A2UI v0.9 generative-UI
// JSON and parses A2UI action events. It is the only place that knows the A2UI
// wire format; the domain (orders) and transport (a2abridge) packages stay
// A2UI-agnostic.
package a2ui

import (
	"fmt"
	"strings"
)

const (
	// ExtensionURI — ревизия A2UI, на которой мы говорим. У 0.9.1 свой URI;
	// v1.0 существует, но это релиз-кандидат, и мы его пока не берём.
	ExtensionURI = "https://a2ui.org/a2a-extension/a2ui/v0.9.1"

	// LegacyExtensionURI — та же поддержка под именем предыдущей ревизии.
	// Отправляем и принимаем оба: агент, знающий только 0.9, иначе не поймёт,
	// что клиент умеет рисовать, и ответит одним текстом.
	LegacyExtensionURI = "https://a2ui.org/a2a-extension/a2ui/v0.9"

	MIMEType = "application/a2ui+json"

	// Version — значение поля version в сообщениях. Схемы 0.9.1 объявляют его
	// как enum ["v0.9", "v0.9.1"], схемы 0.9 — как const "v0.9". Полезная
	// нагрузка у ревизий одна и та же: 0.9.1 лишь стандартизировал MIME и
	// ослабил требование к уникальности surfaceId.
	Version = "v0.9.1"

	// CapabilitiesKey — ключ внутри a2uiClientCapabilities. Именно "v0.9", а не
	// Version: схема client_capabilities.json в наборе 0.9.1 объявляет ровно
	// это свойство и никакого "v0.9.1" не знает.
	CapabilitiesKey = "v0.9"

	// CatalogID — каталог компонентов. В 0.9.1 он свой не заводился, ссылка
	// по-прежнему на набор v0_9.
	CatalogID = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"

	// LegacyMIMEType — тип из ранних сборок A2UI (0.8 и часть 0.9). Мы его
	// только принимаем: отдаём всегда MIMEType.
	LegacyMIMEType = "application/json+a2ui"

	// MIMEKey — ключ, под которым тип части лежит в её метаданных. Спека
	// расширения опознаёт A2UI-часть именно так, а не по полю mediaType.
	MIMEKey = "mimeType"
)

// IsA2UI отвечает, несёт ли часть разметку A2UI. Спека помечает её
// metadata.mimeType; поле mediaType — второй, менее формальный способ, которым
// пользуемся мы сами и живые агенты. Принимаем оба и оба типа, отдаём всегда
// metadata.mimeType + MIMEType (см. ClientCapabilities и сборку частей в
// a2abridge).
//
// Сигнатура намеренно транспортно-независима: пакет a2ui не знает про a2a.
func IsA2UI(mediaType string, metadata map[string]any) bool {
	if known(mediaType) {
		return true
	}
	m, _ := metadata[MIMEKey].(string)
	return known(m)
}

func known(mime string) bool {
	return mime == MIMEType || mime == LegacyMIMEType
}

// ClientCapabilities возвращает значение metadata.a2uiClientCapabilities —
// объявление рендерера о том, какие каталоги он умеет. Вместе с заголовком
// расширения это штатный признак «клиент умеет A2UI»; acceptedOutputModes
// таким признаком по спеке не является.
func ClientCapabilities() map[string]any {
	return map[string]any{
		CapabilitiesKey: map[string]any{"supportedCatalogIds": []any{CatalogID}},
	}
}

// NewAction собирает событие клиента по схеме client_to_server.
//
// Все пять полей действия обязательны (`required` в client_to_server.json), а
// сам конверт — ровно два свойства: version и action. Поэтому пустые surfaceID
// и sourceComponentID именно пустеют, а не исчезают: без них payload не пройдёт
// валидацию у агента, который её делает.
func NewAction(name, surfaceID, sourceComponentID string, ctx map[string]any, timestamp string) map[string]any {
	if ctx == nil {
		ctx = map[string]any{}
	}
	return map[string]any{"version": Version, "action": map[string]any{
		"name":              name,
		"surfaceId":         surfaceID,
		"sourceComponentId": sourceComponentID,
		"timestamp":         timestamp,
		"context":           ctx,
	}}
}

// surfaceCounter makes surface ids unique within a process without needing a
// random source (unavailable in some sandboxes). It is not concurrency-critical:
// ids only need to be distinct per emitted widget, and the executor emits from a
// single goroutine per request.
var surfaceCounter int

func nextSurfaceID(kind string) string {
	surfaceCounter++
	return fmt.Sprintf("%s-%d", kind, surfaceCounter)
}

// text builds a Text component.
func text(id, s, variant string) map[string]any {
	return map[string]any{"id": id, "component": "Text", "text": s, "variant": variant}
}

// button builds a Button whose child is a Text label and whose click emits an
// A2UI action event {name, context}. The label is copied into a per-button
// context (never the shared ctx) so a client can echo a human-readable action
// instead of the raw action name.
func button(id, labelID, label, variant, actionName string, ctx map[string]any) []map[string]any {
	bctx := map[string]any{"label": label}
	for k, v := range ctx {
		bctx[k] = v
	}
	return []map[string]any{
		{
			"id": id, "component": "Button", "child": labelID, "variant": variant,
			"action": map[string]any{"event": map[string]any{"name": actionName, "context": bctx}},
		},
		text(labelID, label, "body"),
	}
}

// surface wraps components into the standard createSurface + updateComponents pair.
func surface(surfaceID string, components []map[string]any) []map[string]any {
	return []map[string]any{
		{"version": Version, "createSurface": map[string]any{"surfaceId": surfaceID, "catalogId": CatalogID}},
		{"version": Version, "updateComponents": map[string]any{"surfaceId": surfaceID, "components": components}},
	}
}

// ParseAction extracts an incoming A2UI action event from a DataPart payload.
// Shape: {"version":"v0.9","action":{"name":"...","context":{...}}}. По спеке
// data — массив таких сообщений, поэтому принимается и он: берётся первое
// сообщение-действие. Returns ok=false when the payload carries no action.
func ParseAction(payload any) (string, map[string]any, bool) {
	if list, ok := payload.([]any); ok {
		for _, item := range list {
			if name, ctx, ok := ParseAction(item); ok {
				return name, ctx, true
			}
		}
		return "", nil, false
	}
	data, ok := payload.(map[string]any)
	if !ok || data == nil {
		return "", nil, false
	}
	action, ok := data["action"].(map[string]any)
	if !ok {
		return "", nil, false
	}
	name, ok := action["name"].(string)
	if !ok || name == "" {
		return "", nil, false
	}
	ctx, _ := action["context"].(map[string]any)
	if ctx == nil {
		ctx = map[string]any{}
	}
	return name, ctx, true
}

// ActionOrigin возвращает поверхность и компонент, откуда пришло действие.
// Схема их не требует, поэтому оба значения могут быть пустыми; нужны, чтобы
// передать событие дальше, не потеряв, какая кнопка какой карточки нажата.
func ActionOrigin(payload any) (surfaceID, sourceComponentID string) {
	if list, ok := payload.([]any); ok {
		for _, item := range list {
			if s, c := ActionOrigin(item); s != "" || c != "" {
				return s, c
			}
		}
		return "", ""
	}
	data, _ := payload.(map[string]any)
	action, _ := data["action"].(map[string]any)
	surfaceID, _ = action["surfaceId"].(string)
	sourceComponentID, _ = action["sourceComponentId"].(string)
	return surfaceID, sourceComponentID
}

// FromWidget converts a widget map (keyed by "_kind" plus payload) into the
// ordered A2UI messages to emit. Returns ok=false for an unknown kind.
func FromWidget(w map[string]any) ([]map[string]any, bool) {
	kind, _ := w["_kind"].(string)
	title, _ := w["title"].(string)
	switch kind {
	case "widget/confirmation":
		sid := nextSurfaceID("confirmation")
		msg, _ := w["message"].(string)
		// Drop the "(да/нет)" hint: redundant in the widget, where the buttons
		// already offer the choice. The text-fallback part keeps it.
		msg = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(msg), "(да/нет)"))
		orderID, _ := w["order_id"].(string)
		ctx := map[string]any{"order_id": orderID}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": []any{"title", "msg", "actions"}},
			text("title", title, "h3"),
			text("msg", msg, "body"),
			{"id": "actions", "component": "Row", "children": []any{"approve", "decline"}},
		}
		comps = append(comps, button("approve", "approve_lbl", "Оформить возврат", "primary", "approve_refund", ctx)...)
		comps = append(comps, button("decline", "decline_lbl", "Отмена", "default", "decline_refund", ctx)...)
		return surface(sid, comps), true

	case "widget/refund_form":
		sid := nextSurfaceID("refund_form")
		msg, _ := w["message"].(string)
		orderID, _ := w["order_id"].(string)
		severity, _ := w["severity"].(string)
		msgVariant := "body"
		if severity == "error" {
			msgVariant = "h3" // make a validation error stand out
		}
		// The card number is typed into a TextField bound to the surface data
		// model; the submit button's context resolves {path} bindings at click
		// time, so the value reaches the agent inside the action event.
		ctxSubmit := map[string]any{"order_id": orderID, "card_number": map[string]any{"path": "/card_number"}}
		ctxDecline := map[string]any{"order_id": orderID}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": []any{"title", "msg", "card", "actions"}},
			text("title", title, "h3"),
			text("msg", msg, msgVariant),
			{"id": "card", "component": "TextField", "label": "Номер карты",
				"value": map[string]any{"path": "/card_number"}, "variant": "shortText"},
			{"id": "actions", "component": "Row", "children": []any{"submit", "decline"}},
		}
		comps = append(comps, button("submit", "submit_lbl", "Вернуть на карту", "primary", "submit_refund_details", ctxSubmit)...)
		comps = append(comps, button("decline", "decline_lbl", "Отмена", "default", "decline_refund", ctxDecline)...)
		return surface(sid, comps), true

	case "widget/refund_receipt":
		sid := nextSurfaceID("refund_receipt")
		children := []any{"title"}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": children},
			text("title", title, "h3"),
		}
		add := func(id, line string) {
			children = append(children, id)
			comps = append(comps, text(id, line, "body"))
		}
		if v, ok := w["receipt_id"]; ok {
			add("rid", fmt.Sprintf("Квитанция №: %v", v))
		}
		add("order", fmt.Sprintf("Заказ: #%v — %v", w["order_id"], w["item"]))
		add("amount", fmt.Sprintf("Сумма возврата: %v %v", w["amount"], w["currency"]))
		if v, _ := w["card_last4"].(string); v != "" {
			add("card", fmt.Sprintf("Карта получателя: •••• %s", v))
		}
		if v, ok := w["created"]; ok {
			add("created", fmt.Sprintf("Дата: %v", v))
		}
		add("note", "Файл квитанции можно скачать ниже.")
		comps[0]["children"] = children
		return surface(sid, comps), true

	case "widget/order":
		sid := nextSurfaceID("order")
		o, _ := w["order"].(map[string]any)
		children := []any{"title"}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": children},
			text("title", title, "h3"),
		}
		add := func(id, label string, key string) {
			if v, ok := o[key]; ok && v != nil && v != "" {
				children = append(children, id)
				comps = append(comps, text(id, fmt.Sprintf("%s %v", label, v), "body"))
			}
		}
		add("item", "Товар:", "item")
		add("status", "Статус:", "status_label")
		if amt, ok := o["amount"]; ok {
			children = append(children, "amount")
			comps = append(comps, text("amount", fmt.Sprintf("Сумма: %v %v", amt, o["currency"]), "body"))
		}
		add("customer", "Клиент:", "customer")
		add("created", "Дата:", "created")
		// Text renders markdown in the browser, so the order-card link is a
		// plain markdown link. The URL comes from the widget (built by code).
		if url, _ := o["url"].(string); url != "" {
			children = append(children, "link")
			comps = append(comps, text("link", fmt.Sprintf("[Открыть карточку заказа →](%s)", url), "body"))
		}
		comps[0]["children"] = children // refresh root Column children after appends
		return surface(sid, comps), true

	case "widget/order_list":
		sid := nextSurfaceID("order_list")
		rows, _ := w["orders"].([]any)
		children := []any{"title"}
		comps := []map[string]any{
			{"id": "root", "component": "Column", "children": children},
			text("title", title, "h3"),
		}
		for i, r := range rows {
			o, ok := r.(map[string]any)
			if !ok {
				continue
			}
			id := fmt.Sprintf("row%d", i)
			children = append(children, id)
			// The order number becomes a markdown link when the widget carries
			// a per-row url, so each row opens its order card.
			num := fmt.Sprintf("#%v", o["id"])
			if url, _ := o["url"].(string); url != "" {
				num = fmt.Sprintf("[%s](%s)", num, url)
			}
			line := fmt.Sprintf("%s  %v — %v (%v %v, %v)",
				num, o["item"], o["status_label"], o["amount"], o["currency"], o["created"])
			comps = append(comps, text(id, line, "body"))
		}
		comps[0]["children"] = children
		return surface(sid, comps), true
	}
	return nil, false
}
