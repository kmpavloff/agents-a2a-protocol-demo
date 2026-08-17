# Контракт с агентом Ouroboros

Как разговаривать с внешним A2A-агентом Ouroboros: адрес, заголовки, карточка,
формы запросов и ответов. Реализации клиента документ не касается — подходит
любой HTTP-клиент, умеющий JSON-RPC.

Адреса, заголовки и текстовые ходы сняты с живого демона `192.168.1.68:18800`
2026-08-12; разделы про A2UI и про состав `skills[]` описывают целевую форму по
спекам, то есть являются требованием, а не слепком текущего поведения.

**Если пишете клиента прямо сейчас**, учтите: часть требований агент уже
выполнил (канонический путь карточки, объявленное расширение A2UI), а вот где
он размещает разметку — в `artifacts` или в частях сообщения — и чем её
помечает, на 2026-08-12 подтвердить не удалось: агент перестал обслуживать
ходы, отвечая `TASK_STATE_FAILED` с текстом «timed out». Пока не проверено,
читайте оба места и обе пометки (`metadata.mimeType` и `mediaType`) — так
делает наш клиент. Список запрошенных правок лежит рядом:
`2026-08-12-ouroboros-todo.md`.

Работа разбита на два этапа: сначала навык, который отвечает текстом, потом
поверх него — A2UI. Второй этап ничего не ломает в первом: клиент, не умеющий
A2UI, продолжает читать текст.

Нормативные источники: [A2A 1.0](https://a2a-protocol.org/) и
[A2UI extension spec v0.9](https://github.com/google/A2UI/blob/main/specification/v0_9/docs/a2ui_extension_specification.md).

---

## 1. Транспорт

| | |
|---|---|
| Адрес | `http://192.168.1.68:18800/` — единственный эндпоинт, JSON-RPC 2.0 поверх `POST` |
| Карточка | `GET /.well-known/agent-card.json` — канонический путь; старый `/.well-known/agent.json` с 2026-08-12 отдаёт `404` |
| Аутентификация | HTTP Basic, `ouroboros:test` |
| Методы | `SendMessage`, `GetTask` |
| Стриминг | нет — ответ приходит одним куском |
| Время хода | от 30 с до 3 мин; таймаут клиента стоит ставить от 180 с |

## 2. Заголовки

| Заголовок | Значение | |
|---|---|---|
| `Content-Type` | `application/json` | обязателен |
| `Authorization` | `Basic b3Vyb2Jvcm9zOnRlc3Q=` | обязателен, иначе `401` |
| `A2A-Version` | `1.0` | версия протокола, на которой говорит клиент |
| `A2A-Extensions` | `https://a2ui.org/a2a-extension/a2ui/v0.9.1` и `…/v0.9` | этап 2: клиент умеет рендерить A2UI |
| `X-A2A-Extensions` | те же значения | этап 2: те же URI под старым именем заголовка |
| `Accept` | `application/json` | необязателен, ответ всегда JSON |

`A2A-Version` и `A2A-Extensions` — служебные параметры A2A; идут на каждый
JSON-RPC `POST` и **не** идут на `GET` карточки (её тянут только с
`Authorization`). Значение `A2A-Version` — версия из `protocolVersion` карточки,
здесь `1.0`. Официальные SDK проставляют его сами; в `curl`-примерах ниже он
написан явно, потому что там SDK нет.

Заголовок расширения на этапе 2 стоит слать **обоими** именами: A2UI-спека v0.9
писалась под ранний A2A и называет его `X-A2A-Extensions`, в A2A 1.0 префикс
`X-` убран. Агенту достаточно принимать любое из двух.

У ревизий A2UI **разные URI**: `…/v0.9.1` и `…/v0.9`. Мы отправляем оба —
отдельными значениями заголовка, а не через запятую: `a2a-go` сравнивает
значение целиком и списка из него не делает. Агенту достаточно узнавать любой
из двух.

Ответ приходит с `Content-Type: application/json` и служебных заголовков A2A не
содержит — `A2A-Extensions` в ответе не возвращается, так что о принятом
расширении клиент судит по самому ответу. Тип полезной нагрузки указывается
**внутри** тела, в метаданных части; отдельного заголовка на A2UI нет.

## 3. Два этапа

| | Этап 1 — текстовый навык | Этап 2 — A2UI |
|---|---|---|
| Карточка: `skills[]` | навык с `outputModes: ["text/plain"]` | в `outputModes` добавляется `application/a2ui+json` |
| Карточка: `capabilities.extensions` | не нужно | объявляется расширение A2UI и поддерживаемые каталоги |
| Запрос клиента | текст + `metadata.skill` | плюс заголовок расширения и `metadata.a2uiClientCapabilities` |
| Ответ агента | текст в `status.message.parts` | плюс `DataPart` с A2UI там же |
| Нажатие кнопки | нет кнопок | `DataPart` с `action` от клиента к агенту |
| Клиент | показывает текст | разбирает `DataPart`, рисует поверхность, шлёт действия |

Готовность этапа 1: на запрос со `skill` приходит осмысленный текст, разговор
держится по `contextId`. Готовность этапа 2: клиент рисует карточку и её кнопка
доезжает обратно к агенту.

## 4. Карточка агента

```bash
curl -s http://192.168.1.68:18800/.well-known/agent-card.json -u ouroboros:test
```

Карточка отдаётся медленно — от 6 до 60 секунд в зависимости от загрузки
агента. Таймаут на её загрузку стоит брать с запасом, иначе агент будет
выглядеть недоступным.

`supportedInterfaces[0].url` содержит `127.0.0.1` (до 2026-08-12 там было
`0.0.0.0`) — это адрес, по которому агент слышит сам себя, а не адрес для
обращения снаружи. Схему и хост клиент берёт из своей конфигурации, из карточки
осмысленно брать только путь.

### 4.1 Этап 1 — текстовый агент

```jsonc
{
  "name": "Ouroboros",
  "version": "1.5.0",
  "protocolVersion": "1.0",
  "capabilities": { "streaming": false, "pushNotifications": false },
  "supportedInterfaces": [
    { "url": "http://127.0.0.1:18800/", "protocolBinding": "JSONRPC", "protocolVersion": "1.0" }
  ],
  "defaultInputModes":  ["text/plain"],
  "defaultOutputModes": ["text/plain"],
  "securitySchemes": { "basic": { "httpAuthSecurityScheme": { "scheme": "basic" } } },
  "security": [ { "basic": [] } ],
  "skills": [
    {
      "id": "shop",
      "name": "Заказы интернет-магазина",
      "description": "Статус и детали заказа по его номеру: состав, суммы, доставка, трек-номер; оформление возврата. Работает с заказами вида ORD-001. Не меняет состав заказа и не проводит оплату.",
      "tags": ["orders", "shop", "returns"],
      "examples": [
        "какой статус у заказа ORD-002?",
        "покажи детали заказа ORD-001",
        "хочу оформить возврат по ORD-001"
      ],
      "inputModes": ["text/plain"],
      "outputModes": ["text/plain"]
    }
  ]
}
```

### 4.2 Этап 2 — агент отдаёт A2UI

Та же карточка целиком. Относительно §4.1 меняются три места, они помечены
комментариями; всё остальное совпадает буква в букву.

```jsonc
{
  "name": "Ouroboros",
  "version": "1.5.0",
  "protocolVersion": "1.0",
  // 1. Агент объявляет, что умеет отдавать разметку, и какие каталоги знает.
  "capabilities": {
    "streaming": false,
    "pushNotifications": false,
    "extensions": [
      {
        "uri": "https://a2ui.org/a2a-extension/a2ui/v0.9.1",
        "description": "Ability to render A2UI v0.9.1",
        "required": false,
        "params": {
          "supportedCatalogIds": [
            "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
          ],
          "acceptsInlineCatalogs": false
        }
      }
    ]
  },
  "supportedInterfaces": [
    { "url": "http://127.0.0.1:18800/", "protocolBinding": "JSONRPC", "protocolVersion": "1.0" }
  ],
  "defaultInputModes":  ["text/plain"],
  // 2. Тип появляется в умолчаниях агента…
  "defaultOutputModes": ["text/plain", "application/a2ui+json"],
  "securitySchemes": { "basic": { "httpAuthSecurityScheme": { "scheme": "basic" } } },
  "security": [ { "basic": [] } ],
  "skills": [
    {
      "id": "shop",
      "name": "Заказы интернет-магазина",
      "description": "Статус и детали заказа по его номеру: состав, суммы, доставка, трек-номер; оформление возврата. Работает с заказами вида ORD-001. Не меняет состав заказа и не проводит оплату.",
      "tags": ["orders", "shop", "returns"],
      "examples": [
        "какой статус у заказа ORD-002?",
        "покажи детали заказа ORD-001",
        "хочу оформить возврат по ORD-001"
      ],
      "inputModes": ["text/plain"],
      // 3. …и в самом навыке, если UI умеет именно он, а не весь агент.
      "outputModes": ["text/plain", "application/a2ui+json"]
    }
  ]
}
```

Спека называет объявление расширения необязательным, но именно по нему клиент
решает, слать ли `a2uiClientCapabilities` и ждать ли поверхность. Без него
единственный намёк на поддержку — `application/a2ui+json` в `outputModes`.

### 4.3 Как формировать `skills[]`

Поля навыка по A2A 1.0:

| Поле | | |
|---|---|---|
| `id` | обязательно | машинный идентификатор, уникален в пределах карточки |
| `name` | обязательно | человекочитаемое имя |
| `description` | обязательно | что навык делает |
| `tags` | обязательно | ключевые слова; может быть пустым массивом, но само поле должно быть |
| `examples` | нет | примеры пользовательских фраз |
| `inputModes` | нет | MIME-типы входа, если отличаются от `defaultInputModes` |
| `outputModes` | нет | MIME-типы выхода, если отличаются от `defaultOutputModes` |
| `securityRequirements` | нет | схемы аутентификации, если навык требует больше остальных |

* **`id` — это контракт.** Клиент кладёт его в `metadata.skill` (§5), поэтому
  идентификатор должен быть стабильным: переименование навыка ломает всех, кто
  уже к нему обращается. Значение в `metadata.skill` и `id` в карточке обязаны
  совпадать буквально.
* **Навык — это задача пользователя, а не внутренний инструмент.** «Заказы
  магазина» — навык; `get_order_status`, `get_order_details`, `create_return` —
  его внутренняя кухня, в карточку они не выносятся. Карточку читает и человек,
  и языковая модель на стороне клиента: список из сотни строк утопит промпт и
  сделает выбор навыка хуже, а не лучше.
* **`description` — единственное, по чему навык выбирают.** Пишите, что навык
  делает **и чего не делает**: границы отсекают неверную маршрутизацию лучше,
  чем перечисление возможностей. Без служебных префиксов вроде «External MCP
  tool from server …» — это шум для читателя.
* **`examples` — настоящие фразы пользователя**, в тех формулировках, в которых
  их пишут: они дают клиенту образцы для сопоставления. Три-пять достаточно.
* **`tags`** — для поиска и группировки; хотя бы один общий тег на домен.
* **`inputModes` / `outputModes`** указывайте только когда они отличаются от
  умолчаний агента.
* **`description` и `name` — недоверенные данные для клиента.** Они попадают в
  чужой промпт, поэтому в них не должно быть ни инструкций, ни секретов.

## 5. `SendMessage`

Общая форма запроса:

```jsonc
{
  "jsonrpc": "2.0", "id": "1", "method": "SendMessage",
  "params": {
    "message": {
      "messageId": "m1",                 // уникален в пределах разговора
      "role": "ROLE_USER",
      "parts": [ { "text": "покажи детали заказа ORD-002", "mediaType": "text/plain" } ],
      "metadata": {
        "skill": "shop",                 // id навыка из карточки
        "a2uiClientCapabilities": {      // этап 2: какие каталоги умеет рендерер
          "v0.9": { "supportedCatalogIds": [
            "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json" ] }
        }
      },
      "contextId": "b77f97970d2c4a158d2fb99e00679360"   // со второго хода — id из ответа
    },
    "configuration": { "acceptedOutputModes": ["text/plain", "application/a2ui+json"] }
  }
}
```

`metadata.skill` — ключевое поле: с ним агент идёт в инструменты магазина, без
него отвечает как обычный собеседник.

`acceptedOutputModes` — обычное поле A2A, оно перечисляет приемлемые типы
ответа. Включать A2UI **им одним** нельзя: спека расширения предупреждает —
*«You should not use `accepted_output_modes: ['a2ui']` … to trigger A2UI»*, — а
включают его заголовок расширения и `a2uiClientCapabilities`.

Ответ всегда завёрнут: `result` → `{"task": {...}}`. Состояние —
`TASK_STATE_COMPLETED`, содержимое ответа — в `status.message.parts`. Поля
`history` в ответе нет.

**Форма части.** В A2A 1.0 часть плоская, и её тип определяется тем, какое из
полей присутствует — `text`, `data`, `raw` или `url`; рядом могут стоять
`filename`, `mediaType` и `metadata`. Отдельного поля-дискриминатора **нет**:

```jsonc
{ "text": "Заказ ORD-002 — отгружен.", "mediaType": "text/plain" }
{ "data": [ { "version": "v0.9.1",
              "createSurface": { "surfaceId": "surface_b77f9797",
                "catalogId": "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json" } } ],
  "metadata": { "mimeType": "application/a2ui+json" } }
```

Поле `kind` (`{"kind": "text", …}`) — из A2A до 1.0; именно в таком виде части
показаны в примерах спеки A2UI v0.9, писавшейся под ранний A2A. Реализации
обычно его игнорируют, но опираться на него нельзя: тип читается по наличию
поля с содержимым.

### 5.1 Ход без A2UI (этап 1)

```bash
curl -s -X POST http://192.168.1.68:18800/ -u ouroboros:test \
  -H 'Content-Type: application/json' \
  -H 'A2A-Version: 1.0' \
  -d '{"jsonrpc":"2.0","id":"1","method":"SendMessage","params":{
        "message":{"messageId":"m-chat","role":"ROLE_USER",
          "parts":[{"text":"Сколько будет 2+2? Ответь одним числом.","mediaType":"text/plain"}]},
        "configuration":{"acceptedOutputModes":["text/plain"]}}}'
```

```json
{
  "jsonrpc": "2.0", "id": "1",
  "result": { "task": {
    "id": "cdc31a5fe1364031b4cc29d36bf4e8b3",
    "contextId": "cdc31a5fe1364031b4cc29d36bf4e8b3",
    "status": {
      "state": "TASK_STATE_COMPLETED",
      "message": {
        "messageId": "4b9cf1f3956d4841b8901356041ddfcc",
        "role": "ROLE_AGENT",
        "parts": [ { "text": "4", "mediaType": "text/plain" } ]
      }
    }
  } }
}
```

Одна текстовая часть, `artifacts` нет — весь этап 1 выглядит так.

### 5.2 Ход с A2UI (этап 2)

```bash
curl -s -X POST http://192.168.1.68:18800/ -u ouroboros:test \
  -H 'Content-Type: application/json' \
  -H 'A2A-Version: 1.0' \
  -H 'A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9.1' \
  -H 'A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9' \
  -H 'X-A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9.1' \
  --max-time 200 \
  -d '{"jsonrpc":"2.0","id":"2","method":"SendMessage","params":{
        "message":{"messageId":"m-order","role":"ROLE_USER",
          "parts":[{"text":"покажи детали заказа ORD-002","mediaType":"text/plain"}],
          "metadata":{"skill":"shop","a2uiClientCapabilities":{"v0.9":{
            "supportedCatalogIds":["https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"]}}}},
        "configuration":{"acceptedOutputModes":["text/plain","application/a2ui+json"]}}}'
```

Ответ — те же `status.message.parts`, но частей две: текст и `DataPart` с
разметкой. **Где лежит A2UI:** в `parts` сообщения, рядом с текстом. Тип
указывается в метаданных части — `metadata.mimeType`; `data` — всегда
**массив** A2UI-сообщений, даже если оно одно.

```jsonc
{
  "jsonrpc": "2.0", "id": "2",
  "result": { "task": {
    "id": "b77f97970d2c4a158d2fb99e00679360",
    "contextId": "b77f97970d2c4a158d2fb99e00679360",
    "status": {
      "state": "TASK_STATE_COMPLETED",
      "message": {
        "messageId": "ea678786a4c941f2b19f2f7cb9a5d1b9",
        "role": "ROLE_AGENT",
        "parts": [
          { "text": "Заказ **ORD-002** — отгружен. Итого 6 798 ₽.", "mediaType": "text/plain" },
          { "metadata": { "mimeType": "application/a2ui+json" },
            "data": [
              { "version": "v0.9.1",
                "createSurface": {
                  "surfaceId": "surface_b77f9797",
                  "catalogId": "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
                } },
              { "version": "v0.9.1",
                "updateComponents": {
                  "surfaceId": "surface_b77f9797",
                  "components": [
                    { "id": "root",      "component": "Card",   "child": "root-body" },
                    { "id": "root-body", "component": "Column",
                      "children": ["title", "status-row", "divider1", "total-row", "btn-row"] },
                    { "id": "title",        "component": "Text",    "text": "Заказ ORD-002", "variant": "h3" },
                    { "id": "status-row",   "component": "Row",     "children": ["status-label", "status-val"] },
                    { "id": "status-label", "component": "Text",    "text": "Статус", "variant": "caption" },
                    { "id": "status-val",   "component": "Text",    "text": "📦 Отгружен", "variant": "body" },
                    { "id": "divider1",     "component": "Divider", "axis": "horizontal" },
                    { "id": "total-row",    "component": "Row", "justify": "spaceBetween",
                      "children": ["total-label", "total-val"] },
                    { "id": "total-label",  "component": "Text", "text": "**Итого**",  "variant": "body" },
                    { "id": "total-val",    "component": "Text", "text": "**6 798 ₽**", "variant": "body" },
                    { "id": "btn-row",      "component": "Row",  "children": ["return-btn"] },
                    { "id": "return-btn",   "component": "Button", "child": "return-btn-text",
                      "variant": "primary",
                      "action": { "event": { "name": "return_order",
                                             "context": { "order_id": "ORD-002" } } } },
                    { "id": "return-btn-text", "component": "Text", "text": "Оформить возврат", "variant": "body" }
                  ],
                  "dataModel": { "context_id": "b77f97970d2c4a158d2fb99e00679360" }
                } }
            ] }
        ]
      }
    }
  } }
}
```

Требования к самой разметке:

* компоненты **плоские**: `child` — строка-ссылка на `id`, `children` — массив
  таких строк; вложенных объектов быть не должно;
* ровно один компонент с `id: "root"` — с него рендерер начинает обход, без
  него поверхность не рисуется;
* `catalogId` — из числа тех, что клиент прислал в `a2uiClientCapabilities`;
* `action.event.context` — объект `{ключ: значение}`, не массив пар;
* `surfaceId` уникален в пределах разговора: если он выводится из `contextId`,
  второй ход пришлёт `createSurface` с занятым идентификатором и затрёт первую
  карточку.

**Версия в сообщении** — `"v0.9"` либо `"v0.9.1"`. Схемы набора v0.9.1
объявляют поле как `enum ["v0.9", "v0.9.1"]`, схемы v0.9 — как `const "v0.9"`.
Сам релиз 0.9.1 меняет ровно две вещи и полезную нагрузку не трогает:
стандартизирует MIME на `application/a2ui+json` и ослабляет требование к
`surfaceId` (уникальность среди активных поверхностей вместо глобальной на
время жизни рендерера). Отсюда и обещание спеки: *«v0.9.1 is fully compatible
with v0.9 payloads … clients and servers can upgrade seamlessly»*.

Мы говорим на 0.9.1 и стамп ставим тот же — `"v0.9.1"`, как и живой агент.
Отдельного модуля `v0_9_1` в референсном рендерере нет (`@a2ui/web_core` везёт
`v0_8`, `v0_9`, `v1_0`), но модуль `v0_9` значение поля в рантайме не проверяет
— поверхность с `"v0.9.1"` он рисует; проверено в браузере. v1.0 существует, но
это релиз-кандидат, и мы его пока не берём.

MIME-тип: `application/a2ui+json`. Легаси-тип `application/json+a2ui`
референсный SDK ставит для релизов 0.8 и 0.9, поэтому принимать стоит оба, а
отдавать только первый.

### 5.3 Продолжение разговора

Разговор держит агент: во втором и последующих запросах достаточно передать
`contextId` из предыдущего ответа — повторять номер заказа не нужно.

```jsonc
// Ход 1 — запрос без contextId: начинается новый разговор.
{ "jsonrpc": "2.0", "id": "1", "method": "SendMessage",
  "params": { "message": {
    "messageId": "m1", "role": "ROLE_USER",
    "parts": [ { "text": "покажи заказ ORD-001", "mediaType": "text/plain" } ],
    "metadata": { "skill": "shop" } } } }
```

```jsonc
// Ход 1 — ответ. Здесь агент и выдаёт идентификатор разговора.
{ "jsonrpc": "2.0", "id": "1",
  "result": { "task": {
    "id": "f914291408b5491a86c41d4cde925688",
    "contextId": "f914291408b5491a86c41d4cde925688",
    "status": {
      "state": "TASK_STATE_COMPLETED",
      "message": {
        "messageId": "8a1c0f24c7bf4a51b0d1e6c3a7f92b45",
        "role": "ROLE_AGENT",
        "parts": [ { "text": "Заказ ORD-001 — доставлен 20 июля, трек RU123456789.",
                     "mediaType": "text/plain" } ]
      }
    }
  } }
}
```

```jsonc
// Ход 2 — тот же contextId, номер заказа не повторяется.
{ "jsonrpc": "2.0", "id": "2", "method": "SendMessage",
  "params": { "message": {
    "messageId": "m2", "role": "ROLE_USER",
    "contextId": "f914291408b5491a86c41d4cde925688",
    "parts": [ { "text": "а трек-номер какой?", "mediaType": "text/plain" } ],
    "metadata": { "skill": "shop" } } } }
```

```jsonc
// Ход 2 — ответ: contextId тот же, id задачи новый.
{ "jsonrpc": "2.0", "id": "2",
  "result": { "task": {
    "id": "4fa8a88fd89643dcb890e698e71c7ccc",
    "contextId": "f914291408b5491a86c41d4cde925688",
    "status": {
      "state": "TASK_STATE_COMPLETED",
      "message": {
        "messageId": "b3d90e17f5a24c86ae42c1d0fb7e5391",
        "role": "ROLE_AGENT",
        "parts": [ { "text": "RU123456789.", "mediaType": "text/plain" } ]
      }
    }
  } }
}
```

В ответе `contextId` тот же, а `id` задачи **новый**: контекст — это нить
разговора, задача — один ход в ней.

### 5.4 Нажатие кнопки (этап 2)

Действие уезжает обратно тем же `SendMessage` в том же `contextId`. Стандартная
форма — `DataPart` с тем же MIME-типом, где `data` — массив событий:

```jsonc
{ "jsonrpc": "2.0", "id": "3", "method": "SendMessage",
  "params": { "message": {
    "messageId": "m3", "role": "ROLE_USER",
    "contextId": "b77f97970d2c4a158d2fb99e00679360",
    "parts": [ {
      "mediaType": "application/a2ui+json",
      "metadata": { "mimeType": "application/a2ui+json" },
      "data": [ { "version": "v0.9",
        "action": {
          "name": "return_order",
          "surfaceId": "surface_b77f9797",
          "sourceComponentId": "return-btn",
          "timestamp": "2026-08-12T09:15:00Z",
          "context": { "order_id": "ORD-002" }
        } } ]
    } ],
    "metadata": { "skill": "shop" } } } }
```

Схема `client_to_server_list.json` объявляет обязательными **все пять** полей
действия — `name`, `surfaceId`, `sourceComponentId`, `timestamp`, `context`, — а
конверт ограничивает ровно двумя свойствами: `version` и `action`. Неизвестное
значение остаётся пустой строкой, но поле не пропадает.

Ответ агента на такой ход — обычный ход этапа 2: текст со сводкой возврата плюс
новая поверхность.

Пересказ действия человеческим текстом («Пользователь нажал кнопку …») — не
альтернатива, а тупик: его понимает только языковая модель на стороне агента, а
событие разберёт любая реализация. Наш клиент шлёт исключительно `DataPart`.

## 6. `GetTask`

Нужен, только если ход вернулся в состоянии `TASK_STATE_WORKING` или
`TASK_STATE_SUBMITTED` — тогда по `id` опрашивается результат.

```bash
curl -s -X POST http://192.168.1.68:18800/ -u ouroboros:test \
  -H 'Content-Type: application/json' \
  -H 'A2A-Version: 1.0' \
  -d '{"jsonrpc":"2.0","id":"g","method":"GetTask","params":{"id":"b77f97970d2c4a158d2fb99e00679360"}}'
```

Обёртки `{"task": …}` здесь **нет** — задача лежит в `result` напрямую. Это
единственное различие между двумя методами, и оно перевёрнуто относительно
привычного: обёртка есть у `SendMessage` и отсутствует у `GetTask`.

```jsonc
{
  "jsonrpc": "2.0", "id": "g",
  "result": {
    "id": "b77f97970d2c4a158d2fb99e00679360",
    "contextId": "b77f97970d2c4a158d2fb99e00679360",
    "status": {
      "state": "TASK_STATE_COMPLETED",
      "message": {
        "messageId": "ea678786a4c941f2b19f2f7cb9a5d1b9",
        "role": "ROLE_AGENT",
        "parts": [ { "text": "Заказ **ORD-002** — отгружен. Итого 6 798 ₽.",
                     "mediaType": "text/plain" } ]
      }
    },
    "artifacts": []
  }
}
```

Состав тот же, что у задачи из `SendMessage`: `history` не отдаётся и здесь.

## 7. Ошибки

Ошибки — стандартный JSON-RPC `error`, HTTP-код при этом `200`:

```json
{"jsonrpc":"2.0","id":"g","error":{"code":-32001,"message":"Task not found"}}
```

Отдельно стоит проверять `status.message` даже при `TASK_STATE_COMPLETED`:
внутренний сбой агента приезжает не как `error`, а как текст ответа.

Отсутствующий или неверный `Authorization` — это уже HTTP `401`, без тела
JSON-RPC.
