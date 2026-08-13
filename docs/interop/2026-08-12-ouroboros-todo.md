# Что доделать на стороне агента Ouroboros

Дата: 2026-08-12. Адресат: владелец агента `192.168.1.68:18800`.

Наш клиент приведён к стандарту A2UI — ревизия **v0.9.1**, целиком и без
поблажек (v1.0 существует, но это релиз-кандидат, и мы его пока не берём):
разметку отдаём и читаем в частях сообщения, помечаем `metadata.mimeType`,
объявляем `a2uiClientCapabilities`, а нажатие кнопки уезжает штатным событием
`action`. Ниже — что из-за этого нужно поменять у вас и что стоит поменять
сверх того.

Документ самодостаточный: часть I описывает формы, на которые нужно прийти,
часть II — список работ. Всё, что относится к транспорту, снято с живого обмена
2026-08-12.

Нормативные источники:
[A2UI extension spec](https://github.com/google/A2UI/blob/main/specification/v0_9_1/docs/a2ui_extension_specification.md),
[A2A 1.0](https://a2a-protocol.org/), conformance-набор
`agent_sdks/conformance/suites/a2a_integration.yaml` в репозитории google/A2UI.

---

# Часть I. Формы обмена

## I.1 Что мы присылаем

Заголовки на каждом JSON-RPC `POST` (на `GET` карточки идёт только
`Authorization`):

| Заголовок | Значение |
|---|---|
| `Content-Type` | `application/json` |
| `Authorization` | `Basic …` |
| `A2A-Version` | `1.0` |
| `A2A-Extensions` | `https://a2ui.org/a2a-extension/a2ui/v0.9.1` и `…/a2ui/v0.9` |
| `X-A2A-Extensions` | те же два значения |

Оба имени заголовка — потому что спека A2UI писалась под ранний A2A и знает
`X-A2A-Extensions`, а в A2A 1.0 префикс `X-` убран. Оба URI — потому что у
ревизий 0.9 и 0.9.1 они разные. Значения идут **отдельными** заголовками, не
через запятую: `a2a-go` сравнивает значение целиком и списка из него не делает.
Узнавать достаточно любое из имён и любой из URI.

Само сообщение:

```jsonc
{
  "jsonrpc": "2.0", "id": "1", "method": "SendMessage",
  "params": {
    "message": {
      "messageId": "m1",
      "role": "ROLE_USER",
      "parts": [ { "text": "покажи детали заказа ORD-002", "mediaType": "text/plain" } ],
      "metadata": {
        "skill": "shop",
        "a2uiClientCapabilities": {
          "v0.9": { "supportedCatalogIds": [
            "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json" ] }
        }
      },
      "contextId": "b77f97970d2c4a158d2fb99e00679360"  // со второго хода — id из вашего ответа
    },
    "configuration": { "acceptedOutputModes": ["text/plain", "application/a2ui+json"] }
  }
}
```

* `metadata.skill` — идентификатор навыка; о нём отдельный разговор в §7.
* `metadata.a2uiClientCapabilities` — штатный признак «клиент отрисует UI»,
  вместе с заголовком расширения. Ключ внутри именно `v0.9`: другого свойства
  схема возможностей клиента не знает даже в наборе 0.9.1. В
  `supportedCatalogIds` — каталог, который рендерер гарантированно отрисует;
  компоненты вне его лучше не присылать.
* `acceptedOutputModes` мы шлём, но триггером A2UI он не является. Спека
  предупреждает: *«You should not use `accepted_output_modes: ['a2ui']` (which
  is not an A2UI standard) to trigger A2UI»*.
* `contextId` — нить разговора. Со второго хода мы возвращаем ваш; повторять
  номер заказа не нужно.

## I.2 Форма части в A2A 1.0

Часть плоская, и её тип определяется тем, какое из полей присутствует —
`text`, `data`, `raw` или `url`; рядом могут стоять `filename`, `mediaType` и
`metadata`. Отдельного поля-дискриминатора **нет**:

```jsonc
{ "text": "Заказ ORD-002 — отгружен.", "mediaType": "text/plain" }
{ "data": [ { "version": "v0.9.1",
              "createSurface": { "surfaceId": "surface_b77f9797",
                "catalogId": "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json" } } ],
  "metadata": { "mimeType": "application/a2ui+json" } }
```

Поле `kind` (`{"kind": "text", …}`) — из A2A до 1.0; именно в таком виде части
показаны в примерах спеки A2UI, писавшейся под ранний A2A. Реализации обычно
его игнорируют, но опираться на него нельзя.

Разбор при чтении: ровно одно из четырёх полей с содержимым. Ни одного —
ошибка, больше одного — тоже.

## I.3 Каким должен стать ваш ответ

Ответ завёрнут: `result` → `{"task": {...}}`, состояние
`TASK_STATE_COMPLETED`, содержимое — в `status.message.parts`. Частей две:
текст и `DataPart` с разметкой. Артефакты не нужны.

```jsonc
{
  "jsonrpc": "2.0", "id": "1",
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
                      "children": ["title", "status-row", "divider1", "btn-row"] },
                    { "id": "title",        "component": "Text",    "text": "Заказ ORD-002", "variant": "h3" },
                    { "id": "status-row",   "component": "Row",     "children": ["status-label", "status-val"] },
                    { "id": "status-label", "component": "Text",    "text": "Статус", "variant": "caption" },
                    { "id": "status-val",   "component": "Text",    "text": "📦 Отгружен", "variant": "body" },
                    { "id": "divider1",     "component": "Divider", "axis": "horizontal" },
                    { "id": "btn-row",      "component": "Row",     "children": ["return-btn"] },
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

Требования к самой разметке (всё это вы уже соблюдаете, здесь — чтобы не
потерять при переносе):

* компоненты **плоские**: `child` — строка-ссылка на `id`, `children` — массив
  таких строк; вложенных объектов быть не должно;
* ровно один компонент с `id: "root"` — с него рендерер начинает обход, без
  него поверхность не рисуется;
* `catalogId` — из числа тех, что клиент прислал в `a2uiClientCapabilities`;
* `action.event.context` — объект `{ключ: значение}`, не массив пар;
* `version` — `"v0.9"` либо `"v0.9.1"`: схемы 0.9.1 объявляют поле как enum из
  этих двух. Полезная нагрузка у ревизий одна и та же — 0.9.1 лишь
  стандартизировал MIME и ослабил требование к уникальности `surfaceId`.

MIME-тип — `application/a2ui+json`. Легаси-тип `application/json+a2ui`
референсный SDK ставит для релизов 0.8 и 0.9: принимать стоит оба, отдавать
только первый.

---

# Часть II. Список работ

## 1. Разбирать нажатие кнопки как событие A2UI ⛔ ломает демо

**Что сломалось.** Раньше мы пересказывали нажатие текстом — «Пользователь
нажал кнопку «Оформить возврат» (order_id: ORD-001)» — и это разбирала ваша
модель. Теперь уезжает штатное событие, и до правки кнопка в связке не
работает.

**Что приходит.** Обычный `SendMessage` в том же `contextId`, единственная
часть — `DataPart`. Ниже запрос целиком; сама часть — снимок с провода, а не
пересказ:

```json
{
  "jsonrpc": "2.0",
  "id": "3",
  "method": "SendMessage",
  "params": {
    "message": {
      "messageId": "019ff530-9070-764b-b630-93fb30c31203",
      "role": "ROLE_USER",
      "contextId": "b77f97970d2c4a158d2fb99e00679360",
      "parts": [
        {
          "data": [
            {
              "action": {
                "context": {
                  "label": "Оформить возврат",
                  "order_id": "ORD-002"
                },
                "name": "return_order",
                "sourceComponentId": "return-btn",
                "surfaceId": "surface_b77f9797",
                "timestamp": "2026-08-12T09:54:06Z"
              },
              "version": "v0.9.1"
            }
          ],
          "mediaType": "application/a2ui+json",
          "metadata": {
            "mimeType": "application/a2ui+json"
          }
        }
      ],
      "metadata": {
        "skill": "shop",
        "a2uiClientCapabilities": {
          "v0.9": {
            "supportedCatalogIds": [
              "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
            ]
          }
        }
      }
    },
    "configuration": {
      "acceptedOutputModes": ["text/plain", "application/a2ui+json"]
    }
  }
}
```

Часть помечена дважды: `metadata.mimeType` — то, по чему её опознаёт спека, `mediaType` — родное поле
A2A, оставлено рядом и ничему не мешает.

`name` и `context` — те самые, что вы положили в `Button.action.event`;
`context.label` дописываем мы, чтобы в ленте было что показать вместо
служебного имени. `surfaceId` возвращается ваш собственный: у себя в ленте мы
переименовываем поверхности по ходам (см. §8), а перед отправкой снимаем
суффикс. `timestamp` — момент, когда ход ушёл к вам, а не когда пользователь
нажал кнопку.

Полезная нагрузка проверяется схемой `client_to_server_list.json` — это массив,
элементы которого описаны в `client_to_server.json`. Обратите внимание: там
**все пять** полей действия объявлены обязательными (`name`, `surfaceId`,
`sourceComponentId`, `timestamp`, `context`), а само сообщение ограничено ровно
двумя свойствами — `version` и `action`.

**Что сделать.** На входе: если часть помечена `application/a2ui+json` и её
`data` содержит `action` — обработать это как нажатие, не отдавая модели сырой
JSON. Текстовую форму можно продолжать принимать, но полагаться на неё больше
нельзя.

**Проверка.** Показать карточку заказа, нажать в нашем UI «Оформить возврат» и
убедиться, что агент ответил сводкой возврата, а не переспросил, что от него
хотят.

## 2. Класть A2UI в части сообщения, а не в артефакт

**Почему.** Спека расширения знает только `DataPart` и не упоминает артефакты;
референсный клиент из репозитория google/A2UI
(`samples/client/lit/shell/middleware/a2a.ts`) читает ровно
`result.status.message?.parts`. Агент, кладущий разметку в артефакт, для такого
клиента отвечает пустотой.

**Сейчас.** `result.task.artifacts[0].parts[0]`.
**Надо.** `result.task.status.message.parts` — рядом с текстовой частью, см.
§I.3.

**Проверка.** В ответе на «покажи детали заказа ORD-002»
`status.message.parts` содержит две части: текст и данные.

Мы артефакт читать не перестали — он остался запасным путём, поэтому пункт не
ломающий. Но пока он нужен, любой другой клиент вас не увидит.

## 3. Помечать часть `metadata.mimeType`

**Почему.** Спека: *«To identify a `DataPart` as containing A2UI data, it must
have the following metadata»* — и дальше `mimeType = "application/a2ui+json"`.
Поле `mediaType`, которым пользуемся мы оба, — родное поле A2A и как признак
A2UI стандартом не назначено.

Осторожно с формулировкой в самой спеке: в тексте путь записан как
`DataPart.data.metadata["mimeType"]`, но её же пример кладёт `metadata` **рядом**
с `data`, а не внутрь него — и так же делают референсный SDK
(`DataPart(data=…, metadata={"mimeType": …})`) и conformance-набор. То есть
метаданные принадлежат части, а лишний `data` в пути — ошибка документа.

```jsonc
{ "metadata": { "mimeType": "application/a2ui+json" }, "data": [ /* сообщения */ ] }
```

`mediaType` рядом оставить можно — он никому не мешает.

## 4. `data` — всегда массив сообщений

Спека: *«The `data` field of the `DataPart` contains a list of A2UI JSON
messages … It MUST be an array of messages»* — даже когда сообщение одно. Здесь
вы уже правы, пункт только для полноты: не потеряйте это при переносе разметки
в сообщение.

## 5. Объявить A2UI в карточке

Сейчас в `capabilities` только `streaming` и `pushNotifications`. Клиент по
карточке решает, слать ли `a2uiClientCapabilities` и ждать ли поверхность;
единственный намёк на поддержку — `application/a2ui+json` в
`defaultOutputModes`.

```json
"capabilities": {
  "streaming": false,
  "pushNotifications": false,
  "extensions": [
    { "uri": "https://a2ui.org/a2a-extension/a2ui/v0.9.1",
      "description": "Ability to render A2UI v0.9.1",
      "required": false,
      "params": {
        "supportedCatalogIds": [
          "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
        ],
        "acceptsInlineCatalogs": false
      } }
  ]
}
```

## 6. Не отдавать A2UI тем, кто его не просил

Сейчас карточка приезжает даже клиенту, приславшему
`acceptedOutputModes: ["text/plain"]` и не объявившему расширение. Такому
клиенту разметка не нужна: он не знает, что с ней делать, и вынужден
отбрасывать её сам.

Признаки, по которым видно, что клиент её ждёт, перечислены в §I.1: заголовок
расширения (любое из двух имён, любой из двух URI) и
`metadata.a2uiClientCapabilities`. Нет ни того ни другого — достаточно текста.

## 7. Навыки в карточке: объявить `shop`

**Что изменилось за день.** Утром `skills[]` был дампом из 97 внутренних
инструментов (`mcp_shop__get_order_status`, `browser_action`,
`compact_context`, …) со служебными описаниями вида «External MCP tool from
configured server 'shop'». К вечеру список сократился до одного навыка:

```json
{ "id": "ouroboros", "name": "Ouroboros",
  "description": "Ouroboros A2A peer", "tags": ["ouroboros", "agent"] }
```

Сотню строк из промпта это убрало — спасибо. Но задача была не «сократить
список», а «объявить то, к чему обращается клиент», и она осталась.

**Почему это мешает.** Значение, которое мы кладём в `metadata.skill`
(`"shop"`), в карточке как навык не объявлено — сопоставить одно с другим
по-прежнему нельзя. А `description` вида «Ouroboros A2A peer» не говорит
клиенту ничего: карточку читает не только человек, но и языковая модель на
стороне клиента, когда выбирает, кому адресовать запрос. Из-за этого нам
пришлось задать описание агента у себя в конфиге и не выводить его из
карточки.

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

**Надо.** Один навык — одна пользовательская задача, `id` совпадает с тем, что
приезжает в `metadata.skill`:

```json
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
  "outputModes": ["text/plain", "application/a2ui+json"]
}
```

Как формировать список:

* **`id` — это контракт.** Клиент кладёт его в `metadata.skill`, поэтому
  идентификатор должен быть стабильным: переименование навыка ломает всех, кто
  уже к нему обращается. Значение в `metadata.skill` и `id` в карточке обязаны
  совпадать буквально.
* **Навык — это задача пользователя, а не внутренний инструмент.** «Заказы
  магазина» — навык; `get_order_status`, `get_order_details`, `create_return` —
  его внутренняя кухня, в карточку они не выносятся. Список из сотни строк
  утопит промпт и сделает выбор навыка хуже, а не лучше.
* **`description` — единственное, по чему навык выбирают.** Пишите, что навык
  делает **и чего не делает**: границы отсекают неверную маршрутизацию лучше,
  чем перечисление возможностей. Без служебных префиксов вроде «External MCP
  tool from server …» — это шум для читателя.
* **`examples` — настоящие фразы пользователя**, в тех формулировках, в которых
  их пишут: они дают клиенту образцы для сопоставления. Три-пять достаточно.
* **`tags`** — для поиска и группировки; хотя бы один общий тег на домен.
* **`inputModes` / `outputModes`** указывайте только когда они отличаются от
  умолчаний агента. Здесь же появляется `application/a2ui+json` — если UI умеет
  не весь агент, а конкретный навык, это единственное место, где клиент может
  это увидеть.
* **`description` и `name` — недоверенные данные для клиента.** Они попадают в
  чужой промпт, поэтому в них не должно быть ни инструкций, ни секретов.

## 8. Мелочи

* **`supportedInterfaces[0].url` = `http://0.0.0.0:18800/`** — это адрес
  прослушивания, а не адрес для обращения. Мы берём схему и хост из своей
  конфигурации, а путь из карточки, но клиент, доверяющий карточке буквально,
  никуда не попадёт.
* **`surfaceId` выводится из `contextId`** и потому повторяется от хода к ходу.
  Даже по ослабленному правилу v0.9.1 («уникален среди активных поверхностей»,
  вместо прежнего «глобально уникален на время жизни рендерера») повторный
  `createSurface` для существующей поверхности остаётся ошибкой, пока она не
  удалена. В ленте каждый ход — отдельная карточка, и предыдущую мы не удаляем,
  поэтому переименовываем поверхности у себя. Достаточно либо добавлять к
  идентификатору номер хода, либо слать `deleteSurface` перед повторным
  созданием.
* **Карточка не по каноническому пути.** Она отдаётся с
  `/.well-known/agent.json`, тогда как A2A определяет
  `/.well-known/agent-card.json`; последний отвечает `404`. Клиент, идущий по
  спецификации, агента просто не найдёт — нам путь пришлось прописать в
  конфиге. Проверено 2026-08-12 вечером, три попытки подряд.
* **Карточка отдаётся ~24 секунды.** Это не запрос к модели, а статический
  документ; клиенты обычно берут его с коротким таймаутом и на таком времени
  отваливаются, считая агента недоступным.
* **Внутренний сбой** — исправлено, спасибо: ход с
  `Client error '403 Forbidden' for url 'http://127.0.0.1:8767/chat/allocate-internal'`
  теперь приезжает в состоянии `TASK_STATE_FAILED`, а не `TASK_STATE_COMPLETED`,
  и клиент видит в нём отказ, а не успешный ответ. Текст ошибки при этом лежит
  в `status.message` — если сможете отдавать такие случаи JSON-RPC-ошибкой,
  будет ещё чище, но и текущего достаточно.

---

## Порядок

| # | Пункт | Последствие, пока не сделано |
|---|---|---|
| 1 | разбор события `action` | кнопка в связке не работает |
| 7 | навыки в карточке | `metadata.skill` не сопоставляется с карточкой, выбор навыка вслепую |
| 2, 3 | A2UI в части сообщения, `metadata.mimeType` | сторонний A2UI-клиент видит пустой ответ |
| 5, 6 | объявление и признаки | клиент не знает, ждать ли UI; UI приезжает тем, кто его не просил |
| 8 | путь и адрес карточки, `surfaceId` | клиент по спецификации агента не найдёт и не подключится; вторая карточка затирает первую |

## Как проверить

Пункты 1 и 4 — автоматически, с нашей стороны есть тест против живого агента.
Он делает текстовый ход, требует, чтобы вся присланная разметка была
рендерабельной (у каждого компонента есть `id` и тип), а затем, если в ответе
нашлась кнопка, нажимает её штатным событием и ждёт осмысленного ответа:

```bash
LIVE_AGENT_URL=http://192.168.1.68:18800 \
LIVE_AGENT_CARD_PATH=/.well-known/agent-card.json \
LIVE_AGENT_SKILL=shop \
LIVE_AGENT_USER=ouroboros LIVE_AGENT_PASSWORD=testpass \
go test ./internal/a2abridge/ -run TestLiveRemoteAgent -v -timeout 600s
```

Пункты 2, 3 и 4 видны в ответе на обычный запрос — где лежит разметка, чем
помечена и массив ли в `data`:

```bash
curl -s -X POST http://192.168.1.68:18800/ -u ouroboros:testpass \
  -H 'Content-Type: application/json' \
  -H 'A2A-Version: 1.0' \
  -H 'A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9.1' \
  -H 'A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9' \
  --max-time 280 \
  -d '{"jsonrpc":"2.0","id":"1","method":"SendMessage","params":{
        "message":{"messageId":"m1","role":"ROLE_USER",
          "parts":[{"text":"покажи детали заказа ORD-002","mediaType":"text/plain"}],
          "metadata":{"skill":"shop","a2uiClientCapabilities":{"v0.9":{
            "supportedCatalogIds":["https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"]}}}},
        "configuration":{"acceptedOutputModes":["text/plain","application/a2ui+json"]}}}' \
| python3 -c '
import json,sys
r=json.load(sys.stdin)["result"]
t=r.get("task", r)
print("артефактов:", len(t.get("artifacts") or []), "— должно быть 0")
parts=((t.get("status") or {}).get("message") or {}).get("parts") or []
data=[p for p in parts if "data" in p]
print("частей в сообщении:", len(parts), "| из них с разметкой:", len(data), "— ожидается 1")
for p in data:
    print("mimeType:", (p.get("metadata") or {}).get("mimeType"), "— ожидается application/a2ui+json")
    print("data:", type(p["data"]).__name__, "— ожидается list")'
```

Пункт 6 — тот же запрос без заголовка расширения и с
`acceptedOutputModes: ["text/plain"]`: в ответе не должно быть частей с
разметкой.

Пункты 5, 7 и 8 — это карточка: `curl -s
http://192.168.1.68:18800/.well-known/agent-card.json -u ouroboros:testpass`.
