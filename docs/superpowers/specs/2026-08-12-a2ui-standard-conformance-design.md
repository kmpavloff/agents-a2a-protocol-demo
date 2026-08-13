# Приведение A2UI к стандарту v0.9

Дата: 2026-08-12.

## Задача

Наш обмен A2UI держится на договорённостях, а не на спеке расширения. Три
расхождения, каждое из которых делает нас невидимыми для стороннего
A2UI-клиента или агента:

1. **Место.** Разметка едет в `artifacts`. Спека знает только `DataPart`, а
   референсный клиент (`samples/client/lit/shell/middleware/a2a.ts`) читает
   ровно `result.status.message?.parts` и артефакты не смотрит вовсе.
2. **Пометка.** Часть помечается полем `mediaType`. Спека требует
   `metadata["mimeType"] = "application/a2ui+json"`.
3. **Триггер.** Мы включаем A2UI через `acceptedOutputModes`. Спека прямо
   запрещает считать это триггером: включают заголовок расширения и
   `message.metadata["a2uiClientCapabilities"]`, которого мы не шлём.

Плюс мелочи: заголовок расширения в A2UI v0.9 называется `X-A2A-Extensions`
(A2A 1.0 убрал префикс, мы шлём только новое имя), а событие кнопки уезжает
пересказом на русском вместо штатного `action`-DataPart.

Источники: [A2UI extension spec
v0.9](https://github.com/google/A2UI/blob/main/specification/v0_9/docs/a2ui_extension_specification.md),
conformance-набор `agent_sdks/conformance/suites/a2a_integration.yaml`,
референсный клиент из репозитория google/A2UI.

## Границы

Правим обе стороны, на которых говорим A2UI:

* **клиент → внешний агент** — `internal/a2abridge/remote.go`;
* **наш A2A-сервер → браузер** — `internal/a2abridge/orchserver.go` и
  `web/src`.

Не трогаем: локальный воркер заказов (он отдаёт свой `widget/order` DataPart, а
в A2UI его превращает оркестратор) и HITL-возврат.

## Устройство

### Формат части — в одном месте

Сейчас `part.MediaType = a2ui.MIMEType` стоит в четырёх местах `orchserver.go`,
проверка `p.MediaType == a2ui.MIMEType` — в трёх местах `remote.go`, и своя
копия знания живёт в браузере. Знание переезжает в `internal/a2ui`:

```go
// NewPart собирает A2UI-часть по спеке: data — всегда массив сообщений,
// тип — в metadata.mimeType.
func NewPart(msgs []any) *a2a.Part

// IsPart узнаёт A2UI-часть по metadata.mimeType или по mediaType,
// включая легаси-тип application/json+a2ui.
func IsPart(p *a2a.Part) bool
```

`NewPart` ставит **обе** пометки — `Metadata["mimeType"]` и `MediaType`.
`mediaType` определён в A2A для любых частей и стандартному клиенту не мешает, а
нам оставляет работающими существующие читатели. Отдаём строго по спеке,
принимаем либерально: `IsPart` знает все три способа пометки.

`Ingest` уже принимает и массив, и одиночный объект — правится только место, где
он решает, что часть относится к A2UI.

### Клиент → внешний агент

| | Было | Стало |
|---|---|---|
| чтение A2UI | `artifacts`, по `mediaType` | сначала `status.message.parts`, артефакты — запасной путь; узнаётся через `IsPart` |
| триггер | `acceptedOutputModes` | `metadata.a2uiClientCapabilities` + заголовок; `acceptedOutputModes` остаётся, но больше ни на что не влияет |
| заголовок | `A2A-Extensions` | `A2A-Extensions` и `X-A2A-Extensions` |

Артефакты из чтения не убираем: Ouroboros сегодня кладёт разметку только туда, и
без запасного пути демо умирает целиком. Для отдачи запасных путей нет.

`a2uiClientCapabilities` заполняется каталогом, который умеет наш рендерер:

```json
{"v0.9": {"supportedCatalogIds": ["https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"]}}
```

### Наш сервер → браузер

A2UI переезжает из артефакта в части завершающего сообщения задачи, с
`metadata.mimeType`. Браузер читает `status.message.parts` и отбирает части по
`metadata.mimeType`, а не «любая data-часть подряд», как сейчас. Обе стороны
едут одним изменением, поэтому совместимость со старой формой не нужна.

### Действия

Штатное событие по спеке:

```json
{"version": "v0.9",
 "action": {"name": "return_order", "surfaceId": "surface_1",
            "sourceComponentId": "return-btn", "timestamp": "2026-08-12T09:15:00Z",
            "context": {"order_id": "ORD-002"}}}
```

* **браузер → оркестратор**: `data` становится массивом, добавляются
  `metadata.mimeType`, `surfaceId`, `sourceComponentId`, `timestamp`.
* **оркестратор → verbatim-агент**: вместо `actionToPrompt` уезжает такой
  DataPart.

Это единственное место, где мы сознательно ломаем работающее демо: кнопка в
связке с Ouroboros перестанет работать, пока владелец агента не научит его
разбирать `action`. Решение принято сознательно — поблажек не держим.

HITL-возврат (`approve_refund`, `decline_refund`, `submit_refund_details`)
остаётся текстовым: это ответ на `input-required` нашему воркеру, а не
A2UI-действие чужому агенту, и в режиме «Авто» он проходит через LLM, где
DataPart взяться неоткуда.

## Ошибки

Поведение при расхождениях не меняется: часть, не опознанная как A2UI, едет
дальше как обычная data-часть; сообщение A2UI, не прошедшее разбор, пропускается
с записью в трейс, остальные обрабатываются (спека требует именно так —
последовательно и без транзакционности).

## Тестирование

* `IsPart` узнаёт `metadata.mimeType`, `mediaType` и легаси
  `application/json+a2ui`; не узнаёт чужую data-часть.
* `NewPart` кладёт массив и обе пометки.
* `remote` шлёт `a2uiClientCapabilities` и оба имени заголовка.
* `remote` читает A2UI из `status.message.parts`, при их отсутствии — из
  артефакта.
* Действие к verbatim-агенту уезжает `action`-DataPart с полным составом полей.
* Браузер: `yarn build`, плюс существующие тесты `go test ./...`.
* Живой стенд: `TestLiveRemoteAgent` при поднятом агенте.

## Итог выполнения (2026-08-12)

Сделано целиком; `go build`, `go vet`, `go test ./...` и `yarn build` проходят,
связка проверена вживую (JSON-RPC + браузер со скриншотами). Четыре отклонения
от того, что написано выше:

1. **Хелперы частей живут не в `a2ui`, а в `a2abridge`**
   (`internal/a2abridge/a2uipart.go`: `newA2UIPart`, `newActionPart`,
   `isA2UIPart`). Подпись `NewPart(...) *a2a.Part` из раздела «Устройство»
   потребовала бы импорта `a2a` в пакет `a2ui`, который намеренно не знает про
   транспорт. В `a2ui` уехало только знание о формате: `IsA2UI(mediaType,
   metadata)`, `ClientCapabilities()`, `NewAction(...)`.
2. **Браузер остался либеральным на чтении.** План предполагал, что обе стороны
   едут одним изменением и совместимость не нужна; на деле `web/dist`
   обслуживает ещё и Java-оркестратор, который отдаёт A2UI артефактом. Браузер
   читает `status.message.parts`, а при их отсутствии — артефакт.
3. **Все пять полей действия обязательны.** Схема `client_to_server.json`
   объявляет `required` для `name`, `surfaceId`, `sourceComponentId`,
   `timestamp`, `context`, а конверт ограничивает двумя свойствами. Первая
   редакция `NewAction` пропускала пустые поля — payload не прошёл бы валидацию;
   исправлено, пустое значение остаётся полем.
4. **Понадобилась обратная операция к `RetagSurfaces`.** Поверхности агента
   переименовываются по ходам для ленты, и вместе с событием наружу уезжал
   чужой `surfaceId`, которого агент никогда не выдавал. Добавлен
   `a2ui.UntagSurface`; заодно `actionToPrompt`, оставшийся без вызова после
   перехода на события, подключён на пути делегирования через модель — там
   текст неизбежен, и контекст кнопки в нём терять нельзя.

## Результат для владельца агента

Отдельным документом — `docs/interop/2026-08-12-ouroboros-todo.md`: чеклист
правок на его стороне (`skills[]`, `capabilities.extensions`, A2UI в
`message.parts` с `metadata.mimeType`, разбор входящего `action`), по каждому
пункту — зачем, как выглядит правильно и чем проверить. Ссылается на уже
написанный контракт `docs/interop/ouroboros-contract.md`.
