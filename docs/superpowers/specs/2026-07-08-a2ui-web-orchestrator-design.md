# Дизайн: A2UI веб-UI для оркестратора (generative UI поверх A2A)

Дата: 2026-07-08

## Контекст и проблема

Демо показывает протокол A2A: пользователь общается с **оркестратором**, тот делегирует
работу **воркеру** (`orders-agent`) по A2A. В предыдущей итерации воркер начал возвращать
структурированные виджеты (подтверждение возврата, карточка заказа, список заказов) в A2A
`DataPart`, а терминальный TUI рисует их псевдографикой и подавляет дублирующее текстовое эхо.

Теперь нужно **нормально продемонстрировать виджеты в браузере** через
[A2UI](https://a2ui.org/) — открытый протокол Google для «generative UI»: агент шлёт
декларативный JSON, описывающий интерфейс, а клиент-рендерер превращает его в нативные виджеты.
A2UI оформлен как **расширение A2A** (`X-A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9`),
а UI-сообщения едут как `DataPart` с `mimeType: application/a2ui+json` — то есть ложатся на уже
существующий у нас механизм `DataPart`.

Разрыв: оркестратор сегодня — только терминальный REPL, он не A2A-**сервер** и ничего не знает
про A2UI. Браузерный рендерер A2UI (`@a2ui/lit`) — это A2A-клиент, которому нужен A2A-агент с
AgentCard.

## Цели

- Оркестратор умеет режим `--web`: поднимает **A2A-сервер** (a2asrv) + AgentCard, объявляющий
  A2UI-расширение, и отдаёт статику браузерного фронтенда.
- Браузер (официальный `@a2ui/lit` рендерер + `@a2a-js/sdk` клиент) подключается к оркестратору
  по A2A, шлёт запросы, получает A2UI-виджеты и **интерактивно** нажимает кнопки.
- Оркестратор — **A2UI-шлюз**: маппит доменные виджеты воркера (наш текущий `DataPart`-формат) в
  A2UI v0.9 JSON. Воркер не меняется.
- Клик по кнопке в виджете шлёт A2UI-`action` обратно по A2A; оркестратор транслирует его в
  resume A2A-задачи воркера (`approve_refund→«да»`, `decline_refund→«нет»`), переиспользуя всю
  существующую логику pending/`parseAffirmative`.
- Терминальный REPL сохраняется как отдельный режим (по умолчанию).

## Не-цели (YAGNI)

- Генерация A2UI **самой LLM** — A2UI-JSON строит код оркестратора из доменных виджетов
  (в духе «Pattern A»: схему виджета владеет код, не модель).
- Изменения воркера: он продолжает эмитить текущий кастомный widget-`DataPart`; A2UI-знание живёт
  только в оркестраторе.
- Полный каталог A2UI-компонентов и произвольные surface-обновления/`updateDataModel`/streaming —
  берём минимально необходимое подмножество (`Card`, `Row`, `Column`, `Text`, `Button`, `List`).
- Переписывание фронтенд-shell из `google/a2ui` целиком — пишем минимальное собственное Lit-app,
  переиспользуя **опубликованные npm-пакеты** рендерера.
- Продакшн-безопасность (полный CSP/sandbox/санитизация) — только отметки и разумные дефолты.
- Аутентификация, многопользовательские сессии, персист.

---

## Архитектура

```
browser (Vite + Lit; @a2ui/lit + @a2ui/web_core + @a2a-js/sdk)
   │  A2A JSON-RPC (message/send), header X-A2A-Extensions: .../a2ui/v0.9
   │  ← A2UI DataPart (application/a2ui+json): createSurface + updateComponents
   │  → action DataPart: { version:"v0.9", action:{ name, context } }
orchestrator (Go, режим --web):
   ├─ a2asrv JSON-RPC handler на /invoke  + AgentCard (well-known) c A2UI-расширением
   ├─ статика web/dist через go:embed
   ├─ adk LLM-раннер (как сейчас) + OrdersClient (A2A-клиент к воркеру)
   └─ A2UI-шлюз: widget-map → A2UI JSON; action → resume-текст
   │  A2A (как сейчас)
worker (orders-agent, без изменений): эмитит доменные widget-DataPart'ы
```

Оркестратор одновременно A2A-**сервер** (для браузера) и A2A-**клиент** (к воркеру) —
симметрично воркеру, у которого уже есть `a2abridge` executor.

---

## Компоненты (Go)

### `internal/a2ui` (новый пакет)

Единственное место, где живёт знание про A2UI. Держит его отдельно от доменного `orders` и от
транспортного `a2abridge`, чтобы каждый модуль имел одну ответственность.

- **Константы**: `ExtensionURI = "https://a2ui.org/a2a-extension/a2ui/v0.9"`, `MIMEType =
  "application/a2ui+json"`, `Version = "v0.9"`.
- **Типы сообщений v0.9** (минимум): `createSurface`, `updateComponents` с плоским списком
  компонентов и ID-ссылками. Точные поля пинуются на этапе имплементации против
  `specification/v0_9/json` и `@a2ui/web_core/v0_9`.
- **Билдеры компонентов** каталога `basic`: `Card`, `Row`, `Column`, `Text`, `Button`, `List`.
  Правила каталога: `Text` требует `text`, `Button` требует `action`.
- **`FromWidget(w map[string]any) (surfaceMsgs, ok)`** — конвертер нашего widget-формата
  (`_kind` + payload) в A2UI-surface:
  - `widget/confirmation` → `Card`{ `Text`(message), `Row`[ `Button`«Оформить возврат»
    action=`approve_refund`, `Button`«Отмена» action=`decline_refund` ] }; `context` кнопок несёт
    `order_id`.
  - `widget/order` → `Card`{ `Column`[ `Text` по полям заказа ] }.
  - `widget/order_list` → `Column`/`List`[ по одной `Card` на заказ ].
- **`ParseAction(parts) (name string, ctx map[string]any, ok)`** — вытаскивает из входящего
  `DataPart` payload `{ version:"v0.9", action:{ name, context } }`.

Юнит-тестируемо без сети и без браузера.

### Executor оркестратора (в `internal/a2abridge`, новый файл `orchserver.go`)

A2A-серверная обёртка вокруг раннера оркестратора — по образцу воркерского `executor`
(`server.go`).

- Активирует A2UI-расширение: если во входящем запросе есть заголовок расширения и AgentCard его
  объявляет — режим A2UI; иначе — текстовый фолбэк (без A2UI-частей).
- Разбирает вход: обычный `TextPart` (чат) **или** `action`-`DataPart` (клик). Для `action`
  транслирует `name`→синтетический пользовательский текст (`approve_refund→«да»`,
  `decline_refund→«нет»`, прочее→описательный текст) и дальше обрабатывает как обычный ввод.
- Гоняет раннер оркестратора; ловит доменный виджет через **уже существующий** side-channel
  `OrdersClient.SetWidgetHandler`; конвертит его `a2ui.FromWidget` → A2UI-`DataPart`
  (`application/a2ui+json`); эмитит A2UI-часть **вместе** с текстом (текст — фолбэк для
  не-A2UI клиентов).
- Резюме A2A-задачи воркера работает как сейчас: синтетическое «да» проходит через раннер →
  `ask_orders_agent("да")` → воркер резюмит ту же задачу.

### Маршрутизация виджетов по сессиям (рефайн side-channel)

Текущий `OrdersClient.SetWidgetHandler(func(map[string]any))` — **глобальный** (один колбэк).
В TUI это ок (одна сессия), но A2A-сервер может обслуживать несколько браузеров одновременно, и
виджет обязан попасть в поток ответа **своего** запроса. Виджет рождается синхронно внутри
`OrdersClient.ask(ctx, sessionID, text)`, где `sessionID` (= A2A contextID) известен.

Решение: сделать хендлер **сессионным** — расширить сигнатуру до
`func(sessionID string, w map[string]any)`. Executor оркестратора держит per-request сток
(канал/срез), выбираемый по `sessionID`, и колбэк кладёт виджет в нужный. TUI-обёртка игнорирует
`sessionID`. Так глобальный хендлер перестаёт быть точкой гонки между сессиями.

### AgentCard оркестратора

Новый AgentCard (аналог воркерского `a2abridge.AgentCard`), объявляющий A2UI-расширение в
`capabilities`/`extensions` и JSONRPC-интерфейс на `publicURL/invoke`.

### HTTP-сервер и статика

В `cmd/orchestrator/main.go` — флаг `--web` (и/или `mode`): монтирует `/invoke`
(`a2asrv.NewJSONRPCHandler`), well-known AgentCard (`a2asrv.NewStaticAgentCardHandler`) и отдаёт
собранный фронт из встроенного `web/dist` (`go:embed`, пакет `internal/webui`). Без `--web` —
текущий REPL.

---

## Компонент (фронтенд)

### `web/` (новый, Vite + Lit + TypeScript)

Минимальное приложение, переиспущее **официальные** пакеты:

- `@a2ui/lit` (`v0.10.x`) + `@a2ui/web_core` — рендер A2UI-surface (`basicCatalog`, `Context`).
- `@a2a-js/sdk` — A2A-клиент (по образцу `samples/client/lit/shell/client.ts`):
  `A2AClient.fromCardUrl(baseURL + '/.well-known/agent-card.json')`, заголовок
  `X-A2A-Extensions: .../a2ui/v0.9`, отправка текстовых и `action`-`DataPart`.
- UI: поле ввода чата + область, куда монтируется A2UI-рендерер; обработчик A2UI-событий кнопок
  формирует `action`-`DataPart` и шлёт его тем же клиентом.

Сборка `vite build` → `web/dist` (статические ассеты). Node/Vite нужны только для сборки;
в рантайме Go отдаёт `dist` из бинаря.

---

## Поток данных (интерактивный возврат)

1. Браузер: `A2AClient.sendMessage(TextPart "верни деньги за 1055")` → оркестратор `/invoke`.
2. Executor оркестратора → раннер LLM → `ask_orders_agent` → воркер: `initiate_refund` → HITL →
   `input-required` + наш confirmation-widget (`DataPart`).
3. `onWidget` ловит виджет → `a2ui.FromWidget` → `createSurface`+`updateComponents` (`Card` +
   2×`Button`) → эмит A2UI-`DataPart` + текстовый вопрос.
4. Браузер: `@a2ui/lit` рисует `Card` с кнопками `[Оформить возврат] [Отмена]`.
5. Клик «Оформить возврат» → `sendMessage(DataPart {version:"v0.9",
   action:{name:"approve_refund", context:{order_id:"1055"}}})`.
6. Executor: `ParseAction` → «да» → раннер → `ask_orders_agent("да")` → воркер резюмит ту же
   задачу → возврат исполняется.
7. Оркестратор эмитит результат (текст; при желании — order-widget → A2UI).

Не-A2UI путь (заголовок не прислан): шаги 3/7 эмитят только текст — A2A-сервер остаётся рабочим
для текстовых клиентов.

---

## Обработка ошибок

- Расширение не активировано → текстовый режим без A2UI-частей (как референс-агент).
- Неизвестный `action.name` → лог + мягкий фолбэк: передать как описательный текст, не падать.
- Пустой/битый `action`-payload → трактуем как отсутствие действия, фолбэк на текст.
- Входящие от агента данные — untrusted (security-note A2UI): полагаемся на whitelisting
  официального рендерера (рендерит только известные компоненты, не выполняет код) + отметка про
  CSP в README. Полный sandbox — вне объёма.

---

## Тестирование

- **Go, юнит** (`internal/a2ui`): `FromWidget` для трёх видов виджетов даёт валидный A2UI-JSON с
  ожидаемыми компонентами/actions; `ParseAction` корректно достаёт `name`/`context` и мягко
  фейлит на мусоре.
- **Go, e2e** (`internal/a2abridge`, по образцу существующих со stub-LLM): (а) текстовый запрос
  через executor оркестратора → в ответе есть A2UI-`DataPart` нужного вида; (б) `action`-`DataPart`
  `approve_refund` → задача воркера резюмится, возврат исполняется в сторе; `decline_refund` →
  возврат не исполняется.
- **Фронтенд**: smoke-компиляция (`vite build`/`tsc`) в CI-смысле; функциональная проверка —
  **ручная в браузере** (рендер трёх виджетов + сквозной клик). Явно фиксируем, что браузерная
  часть не покрыта авто-тестами в этом окружении.

---

## Предпосылки и пины на этап имплементации

- **Node/Vite тулчейн** доступен (подтверждено) — нужен только для сборки `web/`.
- Публичность и точные подпути npm-пакетов `@a2ui/lit` / `@a2ui/web_core` (`/v0_9`), а также
  версия `@a2a-js/sdk` — фиксируются `npm install` на старте имплементации.
- Точная JSON-схема сообщений v0.9 (`createSurface`/`updateComponents`) и обязательные поля
  компонентов — пинуются против `specification/v0_9/json` и `catalogs/basic/catalog.json`
  (+ `rules.txt`).
- Точный способ активации расширения в `a2asrv` (Go) — уточняется против API a2a-go v2.3.1
  (`capabilities.extensions` в AgentCard + чтение `X-A2A-Extensions`).

---

## Файлы

| Файл | Изменение |
|---|---|
| `internal/a2ui/*.go` | **новый** — типы/билдеры A2UI, `FromWidget`, `ParseAction`, константы |
| `internal/a2abridge/orchserver.go` | **новый** — A2A-executor оркестратора + AgentCard оркестратора |
| `internal/webui/embed.go` | **новый** — `go:embed web/dist`, http.FileServer |
| `internal/a2abridge/client.go` | `SetWidgetHandler` → сессионная сигнатура `func(sessionID, w)` |
| `internal/tui/repl.go` | адаптация под новую сигнатуру хендлера (игнорирует `sessionID`) |
| `cmd/orchestrator/main.go` | флаг `--web`: A2A-сервер + статика; иначе REPL (как сейчас) |
| `web/**` | **новый** — Vite+Lit приложение (client, app, index.html, package.json, vite.config) |
| `internal/a2ui/*_test.go`, `internal/a2abridge/*_test.go` | тесты маппинга/парсинга/e2e |
| `README.md` | архитектура + инструкция запуска веб-режима |
