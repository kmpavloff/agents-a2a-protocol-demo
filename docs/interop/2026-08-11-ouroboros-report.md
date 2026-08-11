# Отчёт по интеропу: Ouroboros ↔ клиент на официальном A2A/A2UI SDK

Дата: 2026-08-11. Адресат: владелец агента Ouroboros (`192.168.1.68:18800`).

Этот документ — сопровождение к заплаткам совместимости в `internal/a2abridge`
и `internal/a2ui`. Каждая из них обходит конкретное расхождение живого агента
со спекой; когда расхождение будет устранено на его стороне, соответствующую
заплатку можно снимать. Ниже по каждому пункту: цитата из спеки или
референсной реализации, реальный ответ демона и команда для проверки.

Все ответы сняты 2026-08-11, после обновления контракта на стороне агента.
Базовые параметры: `http://192.168.1.68:18800/`, Basic `ouroboros:testpass`,
`metadata.skill = "shop"`.

Наши реализации-эталоны: `github.com/a2aproject/a2a-go v2.3.1` (A2A) и
`@a2ui/web_core` / `@a2ui/lit` 0.10.4 (рендеринг A2UI).

---

## 1. В поверхности нет компонента `root` — это блокирует рендеринг

Схема A2UI v0.9, `server_to_client.json`, описание `updateComponents`:

> One of the components in one of the components lists **MUST** have an 'id' of
> 'root' to serve as the root of the component tree.

Референсный рендерер на это и опирается — `@a2ui/lit@0.10.4`,
`src/v0_9/surface/a2ui-surface.js:125`:

```js
this._hasRoot = !!this.surface?.componentsModel.get('root');
```

Нет `root` → `render()` навсегда отдаёт `<div>Loading surface...</div>`.

```bash
curl -s -X POST http://192.168.1.68:18800/ -u ouroboros:testpass \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":"1","method":"SendMessage","params":{"message":{
       "messageId":"m1","role":"ROLE_USER",
       "parts":[{"text":"покажи заказ ORD-003","mediaType":"text/plain"}],
       "metadata":{"skill":"shop"}}}}' \
| python3 -c 'import json,sys
d=json.load(sys.stdin)
c=[m for m in d["result"]["artifacts"][0]["parts"][0]["data"] if "updateComponents" in m][0]["updateComponents"]["components"]
print("root:", any(x["id"]=="root" for x in c), "| ids:", [x["id"] for x in c][:5])'
```

Вывод: `root: False | ids: ['card', 'card-inner', 'title', 'divider1', 'status-row']`

**Наша заплатка:** `a2ui.ensureRoot` оборачивает верхние компоненты в `Column`
с `id: "root"` (`internal/a2ui/ingest.go`).

---

## 2. Контракт показывает вложенные компоненты, демон отдаёт плоские

В контракте агента приведён пример с вложением:

```json
{"id": "card", "component": "Card", "child": {
  "id": "card-inner", "component": "Column", "children": [ {...}, {...} ]}}
```

Строкой ниже там же написано «Формат компонента (flat, с дискриминатором
`component`)» — права вторая формулировка. Демон отдаёт:

```json
{"id": "card",       "component": "Card",   "child": "card-inner"}
{"id": "card-inner", "component": "Column", "children": ["title", "divider1", "status-row"]}
```

`child`/`children` — строковые ссылки на id. Того же требует схема каталога:
`anyComponent` с `"discriminator": {"propertyName": "component"}` и
`unevaluatedProperties: false`.

Ошибка документа, код верен. Заплатка не нужна.

---

## 3. Раздел «SendMessage vs GetTask» не совпадает с поведением демона

`GetTask` по id из свежего `SendMessage` (`d3453172216d422eb47e930fbb73140c`):

| Строка контракта | Заявлено | Реально |
|---|---|---|
| обёртка | `result: {"task": {...}}` | `result: {id, contextId, status, artifacts}` — без обёртки |
| `status.message` | нет | **есть** |
| `history` | есть | **нет** |
| `artifacts` | есть | есть (совпало) |

```bash
curl -s -X POST http://192.168.1.68:18800/ -u ouroboros:testpass \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":"g","method":"GetTask","params":{"id":"<TASK_ID>"}}' \
| python3 -c 'import json,sys
r=json.load(sys.stdin)["result"]
print("ключи:", list(r.keys()))
t=r.get("task", r)
print("status:", list(t["status"].keys()), "| history:", bool(t.get("history")))'
```

Вывод: `ключи: ['id', 'contextId', 'status', 'artifacts']`,
`status: ['state', 'message'] | history: False`

Отдельно: из этого раздела был сделан вывод, что наш клиент «вызывает не тот
метод». Показанный агенту JSON был ответом **нашего** оркестратора браузеру.
Признаки: поле `metadata.agentId` — наше изобретение для участка
браузер→оркестратор, агент его не видит и вернуть не может; id задачи
`019fef7c-d974-7e25-…` — UUIDv7 с дефисами в формате `a2a-go`, тогда как id
агента это 32 hex-символа без дефисов. `GetTask` мы не вызывали ни разу:
`grep -c "polling GetTask" a2a-orchestrator.log` → `0` (опрос идёт только при
`WORKING`/`SUBMITTED`).

---

## 4. Конвенция обёрток перевёрнута относительно A2A 1.0

В `a2a-go v2.3.1`:

- `a2aclient/jsonrpc.go:194` — результат **`SendMessage`** декодируется как
  `a2a.StreamResponse`, то есть oneof-обёртка
  `{"task"|"message"|"statusUpdate"|"artifactUpdate": {...}}` (`a2a/core.go:95`).
  Без обёртки — ошибка `result violates A2A spec - could not determine type`
  (`jsonrpc.go:196`).
- `a2aclient/jsonrpc.go:258` — результат **`GetTask`** декодируется как
  **голый** `a2a.Task`.

То есть ровно наоборот относительно контракта агента. По факту демон отдаёт
голый объект в обоих методах, так что своему контракту он тоже не
соответствует — с `GetTask` это случайно совпало с A2A 1.0.

**Наша заплатка:** `envelopeTransport` доклеивает обёртку на лету, только для
`SendMessage` и только когда её нет (`internal/a2abridge/remote.go`).

---

## 5. `securitySchemes` ломают разбор карточки целиком

Карточка отдаёт OpenAPI-стиль:

```json
"securitySchemes": {"basic": {"type": "http", "scheme": "basic", "description": "..."}}
```

`a2a-go` ждёт форму A2A (oneof по имени схемы: `httpAuth`, `apiKey`,
`openIdConnect`, `mutualTls`, `oauth2`) и падает — `a2a/auth.go:152`:

```
unknown security scheme type for basic: [type scheme description]
```

Падает не поле, а **вся** карточка: `Resolver.Resolve` возвращает ошибку, и
клиент не может подключиться. Ожидаемая форма:
`{"basic": {"httpAuth": {"scheme": "basic"}}}`.

**Наша заплатка:** `tolerantCardParser` выкидывает `securitySchemes`/`security`
и повторяет разбор (`internal/a2abridge/remote.go`).

---

## 6. Контекст события у кнопок — массив вместо объекта

Приходит:

```json
"action": {"event": {"name": "return_order",
                     "context": [{"key": "order_id", "value": "ORD-001"}]}}
```

Схема A2UI v0.9, `common_types.json`, `$defs.Action.event.context`:
`"type": "object"` — «A JSON object containing the key-value pairs for the
action context». Ожидается `{"order_id": "ORD-001"}`. На массиве рендерер
соберёт пустой контекст, и нажатие кнопки приедет к агенту без параметров.

**Наша заплатка:** `a2ui.normalizeAction` сводит массив пар к объекту.

---

## 7. Главное: A2UI приходит примерно в половине случаев

Это не про формат, а про поведение, и именно из-за этого виджет в UI появляется
через раз.

Замер за 2026-08-11 по протокольному логу: **28 ходов, 15 с A2UI-артефактом,
13 без**. Один и тот же запрос символ в символ даёт разный результат:

```
2/3 с виджетом — "верни детали по заказу ORD-003 в формате A2UI без лишних деталей"
1/2 с виджетом — "а что с заказом ORD-002?"
```

В ходах без артефакта текст агента нередко **описывает карточку словами** —
например «A2UI-карточка пришла артефактом — `Card` с `Column`, пять строк:
заголовок, статус, товар, сумма, доставка», — при том что артефакта в ответе
нет: модель рассказала про UI вместо того, чтобы его отдать.

Клиент просит UI всем, что даёт протокол:

- `configuration.acceptedOutputModes: ["text/plain", "application/a2ui+json"]`
- заголовок `A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9`

Больше рычагов у клиента нет — заставить агента вернуть артефакт протокол не
позволяет. Лечится детерминизмом на уровне скилла: если запрос про заказ и
данные получены, карточка прикрепляется всегда, без участия модели в этом
решении.

Наша сторона отмечает такие ходы в трейсе строкой:

```
ⓘ агент объявляет A2UI и получил запрос на него, но в этом ответе прислал только текст — виджета не будет
```

---

## Приоритеты

| # | Проблема | Последствие | Когда чинить | Заплатка у нас |
|---|---|---|---|---|
| 1 | нет `root` | ни один A2UI-клиент не отрисует карточку | в первую очередь | `a2ui.ensureRoot` |
| 7 | A2UI через раз | «иногда работает» — худший вид бага | в первую очередь | нет, невозможна |
| 4 | нет oneof-обёртки на `SendMessage` | клиент на официальном SDK не стартует | во вторую | `envelopeTransport` |
| 5 | `securitySchemes` в стиле OpenAPI | клиент не разберёт карточку | во вторую | `tolerantCardParser` |
| 6 | `context` массивом | кнопки приходят без параметров | в третью | `a2ui.normalizeAction` |
| 2, 3 | пример компонентов и раздел `GetTask` | ошибки документа, код верен | в третью | не нужна |

Каждая заплатка — место, где мы подменяем ответ агента своим представлением о
нём. Чем меньше их останется, тем меньше шансов разойтись в следующий раз.

Проверить связку после любых правок на стороне агента:

```bash
LIVE_AGENT_URL=http://192.168.1.68:18800 \
LIVE_AGENT_CARD_PATH=/.well-known/agent.json \
LIVE_AGENT_SKILL=shop \
LIVE_AGENT_USER=ouroboros LIVE_AGENT_PASSWORD=testpass \
go test ./internal/a2abridge/ -run TestLiveRemoteAgent -v -timeout 300s
```
