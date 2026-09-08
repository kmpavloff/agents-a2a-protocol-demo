# Java-оркестратор до паритета с Go — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Довести Java-порт оркестратора до функционального паритета с Go: A2UI v0.9.1 на проводе, приём чужой разметки, мультиагентность с селектором в браузере, правка списка агентов из UI и отладочный дамп протокола.

**Architecture:** Go-реализация — нормативный образец; Java повторяет её поведение, а не структуру пакетов. Пять слоёв ложатся друг на друга в порядке зависимостей: (1) формат A2UI на проводе, (2) приём и нормализация чужой разметки, (3) `Remote`/`Registry` вместо единственного `OrdersClient`, (4) `AgentStore` с overlay-файлом и HTTP-API настроек, (5) трейс и сборка. Ключевая перестройка внутри Java: история разговора переезжает из `OrchestratorAgent` в общий `SessionStore`, потому что агент теперь пересобирается при смене выбранного агента в селекторе, а история к contextId привязана и переезжать не должна.

**Tech Stack:** Java 21, Spring Boot 3.5.6 (только в `--web`), Jackson, SnakeYAML, JUnit 5. Никаких новых зависимостей.

**Spec:** нормативный источник — Go-реализация в корне репозитория плюс проектные документы:
- `docs/superpowers/specs/2026-08-10-multi-agent-selection-design.md` — мультиагентность и селектор
- `docs/superpowers/specs/2026-08-12-a2ui-standard-conformance-design.md` — A2UI v0.9.1
- `docs/superpowers/specs/2026-08-17-a2ui-mode-toggle-design.md` — режим «виджеты / только текст»
- `docs/superpowers/specs/2026-08-31-agent-settings-ui-design.md` — экран настройки агентов

Фронтенд `web/` общий: Java его же и раздаёт, менять его в этом плане не нужно.

## Global Constraints

- **Эталон — Go.** Любое расхождение в поведении, не оговорённое здесь явно, — ошибка. При сомнении читайте соответствующий файл в `internal/`.
- Комментарии — по-русски, объясняют «почему», а не «что». Javadoc у публичных типов — как в существующем Java-коде.
- Пароль агента **никогда** не уходит в HTTP-ответе. Наружу — только `auth.hasPassword: true|false`.
- Env-перекрытия (`A2A_AGENT_<ID>_URL`, `A2A_AGENT_<ID>_PASSWORD`) применяются **после** слияния overlay и остаются последним словом.
- Overlay-запись перекрывает агента **целиком**, а не по полям; позиция агента в списке сохраняется.
- Новых зависимостей в `pom.xml` не добавлять.
- Java-воркер уже в паритете — файлы под `java/worker/` в этом плане не трогаются вовсе.
- Каждая задача заканчивается зелёным `cd java && mvn -o test` и коммитом.
- Совместимость с Go-агентами обязана сохраниться в обе стороны: Go-оркестратор ↔ Java-воркер и Java-оркестратор ↔ Go-воркер.

## Структура файлов

Новое в `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/`:

| Файл | Ответственность | Образец в Go |
|---|---|---|
| `a2ui/A2ui.java` (правка) | Константы ревизий, `parseAction`, `actionOrigin`, `isA2UI`, `clientCapabilities`, `newAction`, `fromWidget` | `internal/a2ui/a2ui.go` |
| `a2ui/A2uiParts.java` | Сборка A2UI-части A2A: `metadata.mimeType` + массив сообщений | `internal/a2abridge/a2uipart.go` |
| `a2ui/A2uiIngest.java` | Приём и нормализация чужой разметки к basic-каталогу | `internal/a2ui/ingest.go` |
| `a2ui/Surfaces.java` | `retag`/`untag` идентификаторов поверхностей по ходам | хвост `internal/a2ui/ingest.go` |
| `a2a/Remote.java` | Одно соединение с одним удалённым агентом | `internal/a2abridge/remote.go` |
| `a2a/Registry.java` | Набор агентов, доступность, живая пересборка | `internal/a2abridge/registry.go` |
| `agent/SessionStore.java` | Общая история разговоров, независимая от выбранного агента | `session.InMemoryService()` в `cmd/orchestrator/main.go` |
| `store/AgentOverride.java` | Overlay-запись: агент + признак «скрыт» | `internal/agentstore/store.go` |
| `store/OverlayFile.java` | Чтение и атомарная запись overlay-файла | `internal/agentstore/file.go` |
| `store/AgentStore.java` | Слияние, CRUD, `Record` для экрана настроек | `internal/agentstore/store.go` |
| `web/AgentsController.java` | `GET /api/agents` | `internal/webui/agents.go` |
| `web/AgentConfigController.java` | Пять эндпоинтов `/api/agents/config…` | `internal/webui/agentconfig.go` |

Правки в `java/common/`: `config/ConfigLoader.java` (список агентов, overlay-путь), `config/AgentConfig.java` + `config/AuthConfig.java` (новые), `trace/Tracer.java` (дамп и маскирование).

---

## Фаза A — A2UI v0.9.1 на проводе

Чинит то, что сейчас сломано: кнопки виджетов в браузере не работают, потому что
фронтенд шлёт действие массивом, а Java принимает только объект.

### Task 1: Действие приходит массивом

**Files:**
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2ui.java:15-55`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiActionTest.java` (создать)

**Interfaces:**
- Produces: константы `A2ui.EXTENSION_URI` (теперь v0.9.1), `A2ui.LEGACY_EXTENSION_URI`, `A2ui.MIME_TYPE`, `A2ui.LEGACY_MIME_TYPE`, `A2ui.VERSION` (теперь `"v0.9.1"`), `A2ui.CAPABILITIES_KEY`, `A2ui.MIME_KEY`, `A2ui.CATALOG_ID`; методы `A2ui.parseAction(Object)`, `A2ui.actionOrigin(Object)`, `A2ui.isA2ui(String, Map)`, `A2ui.clientCapabilities()`, `A2ui.newAction(...)`; записи `A2ui.Action`, `A2ui.Origin`.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiActionTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Разбор входящего события A2UI. Полезная нагрузка приезжает массивом
 * сообщений — так её шлёт фронтенд и так требует спека 0.9.1.
 */
class A2uiActionTest {

    private static Object arrayPayload() {
        return List.of(Map.of(
                "version", "v0.9.1",
                "action", Map.of(
                        "name", "approve_refund",
                        "surfaceId", "confirmation-1-t3",
                        "sourceComponentId", "approve",
                        "timestamp", "2026-09-08T10:00:00Z",
                        "context", Map.of("order_id", "1041"))));
    }

    @Test
    void parsesActionFromArrayPayload() {
        A2ui.Action a = A2ui.parseAction(arrayPayload());
        assertNotNull(a, "действие в массиве должно разбираться");
        assertEquals("approve_refund", a.name());
        assertEquals("1041", a.context().get("order_id"));
    }

    // Одиночный объект принимаем по-прежнему: так шлют клиенты, писавшиеся до
    // 0.9.1, и наш собственный код в тестах.
    @Test
    void parsesActionFromBareObject() {
        A2ui.Action a = A2ui.parseAction(Map.of(
                "version", "v0.9",
                "action", Map.of("name", "decline_refund", "context", Map.of())));
        assertNotNull(a);
        assertEquals("decline_refund", a.name());
    }

    @Test
    void actionOriginReadsSurfaceAndComponent() {
        A2ui.Origin o = A2ui.actionOrigin(arrayPayload());
        assertEquals("confirmation-1-t3", o.surfaceId());
        assertEquals("approve", o.sourceComponentId());
    }

    @Test
    void returnsNullWhenPayloadCarriesNoAction() {
        assertNull(A2ui.parseAction(List.of(Map.of("version", "v0.9.1", "createSurface", Map.of()))));
        assertNull(A2ui.parseAction("не объект"));
        assertNull(A2ui.parseAction(null));
    }

    // Спека опознаёт A2UI-часть по metadata.mimeType; mediaType — второй, менее
    // формальный способ. Принимаем оба и оба типа.
    @Test
    void recognisesBothMarkersAndBothMimeTypes() {
        assertTrue(A2ui.isA2ui(A2ui.MIME_TYPE, null));
        assertTrue(A2ui.isA2ui(A2ui.LEGACY_MIME_TYPE, null));
        assertTrue(A2ui.isA2ui(null, Map.of(A2ui.MIME_KEY, A2ui.MIME_TYPE)));
        assertTrue(A2ui.isA2ui("text/plain", Map.of(A2ui.MIME_KEY, A2ui.LEGACY_MIME_TYPE)));
        assertTrue(!A2ui.isA2ui("text/plain", Map.of("kind", "widget/order")));
    }

    // Все пять полей действия обязательны по схеме client_to_server, а конверт —
    // ровно version + action.
    @Test
    void newActionCarriesAllRequiredFields() {
        Map<String, Object> msg = A2ui.newAction("return_order", "s1", "btn", Map.of("id", "7"), "2026-09-08T10:00:00Z");
        assertEquals(A2ui.VERSION, msg.get("version"));
        assertEquals(Map.of("version", A2ui.VERSION, "action", msg.get("action")), msg);
        @SuppressWarnings("unchecked")
        Map<String, Object> action = (Map<String, Object>) msg.get("action");
        assertEquals(
                java.util.Set.of("name", "surfaceId", "sourceComponentId", "timestamp", "context"),
                action.keySet());
        assertEquals("s1", action.get("surfaceId"));
    }

    @Test
    void clientCapabilitiesAnnounceTheBasicCatalogUnderTheV09Key() {
        Map<String, Object> caps = A2ui.clientCapabilities();
        assertEquals(java.util.Set.of(A2ui.CAPABILITIES_KEY), caps.keySet());
        assertEquals(Map.of("supportedCatalogIds", List.of(A2ui.CATALOG_ID)), caps.get(A2ui.CAPABILITIES_KEY));
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=A2uiActionTest test`
Expected: FAIL — компиляция не проходит, `A2ui.actionOrigin`/`isA2ui`/`newAction`/`clientCapabilities` не существуют.

- [ ] **Step 3: Переписать константы и разбор в `A2ui.java`**

Заменить блок констант (строки 15–19) на:

```java
    /**
     * Ревизия A2UI, на которой мы говорим. У 0.9.1 свой URI; v1.0 существует,
     * но это релиз-кандидат, и мы его пока не берём.
     */
    public static final String EXTENSION_URI = "https://a2ui.org/a2a-extension/a2ui/v0.9.1";

    /**
     * Та же поддержка под именем предыдущей ревизии. Объявляем и принимаем оба:
     * клиент, знающий только 0.9, иначе не поймёт, что мы умеем рисовать.
     */
    public static final String LEGACY_EXTENSION_URI = "https://a2ui.org/a2a-extension/a2ui/v0.9";

    public static final String MIME_TYPE = "application/a2ui+json";

    /** Тип из ранних сборок A2UI (0.8 и часть 0.9). Только принимаем. */
    public static final String LEGACY_MIME_TYPE = "application/json+a2ui";

    /**
     * Значение поля version в сообщениях. Схемы 0.9.1 объявляют его как enum
     * ["v0.9", "v0.9.1"], схемы 0.9 — как const "v0.9". Полезная нагрузка у
     * ревизий одна: 0.9.1 лишь стандартизировал MIME и ослабил требование к
     * уникальности surfaceId.
     */
    public static final String VERSION = "v0.9.1";

    /**
     * Ключ внутри a2uiClientCapabilities. Именно "v0.9", а не VERSION: схема
     * client_capabilities.json в наборе 0.9.1 объявляет ровно это свойство.
     */
    public static final String CAPABILITIES_KEY = "v0.9";

    /** Каталог компонентов. В 0.9.1 свой не заводился — ссылка на набор v0_9. */
    public static final String CATALOG_ID = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json";

    /**
     * Ключ, под которым тип части лежит в её метаданных. Спека расширения
     * опознаёт A2UI-часть именно так, а не по полю mediaType.
     */
    public static final String MIME_KEY = "mimeType";
```

Заменить `parseAction` (строки 32–55) и дописать соседей:

```java
    /** Поверхность и компонент, откуда пришло действие; оба поля могут пустеть. */
    public record Origin(String surfaceId, String sourceComponentId) {}

    /**
     * Достаёт событие A2UI из полезной нагрузки DataPart. Форма:
     * {@code {"version":"v0.9.1","action":{"name":"...","context":{...}}}}.
     * По спеке data — массив таких сообщений, поэтому принимается и он: берётся
     * первое сообщение-действие. Возвращает null, когда действия в нагрузке нет.
     */
    @SuppressWarnings("unchecked")
    public static Action parseAction(Object payload) {
        if (payload instanceof List<?> list) {
            for (Object item : list) {
                Action a = parseAction(item);
                if (a != null) {
                    return a;
                }
            }
            return null;
        }
        if (!(payload instanceof Map<?, ?> m)) {
            return null;
        }
        if (!(m.get("action") instanceof Map<?, ?> action)) {
            return null;
        }
        if (!(action.get("name") instanceof String name) || name.isEmpty()) {
            return null;
        }
        Map<String, Object> ctx = action.get("context") instanceof Map<?, ?> c
                ? new LinkedHashMap<>((Map<String, Object>) c)
                : new LinkedHashMap<>();
        return new Action(name, ctx);
    }

    /**
     * Поверхность и компонент источника события. Схема их не требует, поэтому
     * оба значения могут быть пустыми; нужны, чтобы передать событие дальше, не
     * потеряв, какая кнопка какой карточки нажата.
     */
    public static Origin actionOrigin(Object payload) {
        if (payload instanceof List<?> list) {
            for (Object item : list) {
                Origin o = actionOrigin(item);
                if (!o.surfaceId().isEmpty() || !o.sourceComponentId().isEmpty()) {
                    return o;
                }
            }
            return new Origin("", "");
        }
        if (!(payload instanceof Map<?, ?> m) || !(m.get("action") instanceof Map<?, ?> action)) {
            return new Origin("", "");
        }
        return new Origin(
                action.get("surfaceId") instanceof String s ? s : "",
                action.get("sourceComponentId") instanceof String s ? s : "");
    }

    /**
     * Несёт ли часть разметку A2UI. Спека помечает её metadata.mimeType;
     * mediaType — второй, менее формальный способ, которым пользуемся мы сами и
     * живые агенты. Принимаем оба и оба типа, отдаём всегда metadata.mimeType.
     */
    public static boolean isA2ui(String mediaType, Map<String, Object> metadata) {
        if (knownMime(mediaType)) {
            return true;
        }
        return metadata != null && metadata.get(MIME_KEY) instanceof String m && knownMime(m);
    }

    private static boolean knownMime(String mime) {
        return MIME_TYPE.equals(mime) || LEGACY_MIME_TYPE.equals(mime);
    }

    /**
     * Значение metadata.a2uiClientCapabilities — объявление рендерера о том,
     * какие каталоги он умеет. Вместе с заголовком расширения это штатный
     * признак «клиент умеет A2UI»; acceptedOutputModes спека им не считает.
     */
    public static Map<String, Object> clientCapabilities() {
        return Map.of(CAPABILITIES_KEY, Map.of("supportedCatalogIds", List.of(CATALOG_ID)));
    }

    /**
     * Событие клиента по схеме client_to_server. Все пять полей действия
     * обязательны, а конверт — ровно version и action; поэтому пустые surfaceId
     * и sourceComponentId именно пустеют, а не исчезают: без них payload не
     * пройдёт валидацию у агента, который её делает.
     */
    public static Map<String, Object> newAction(String name, String surfaceId, String sourceComponentId,
                                                Map<String, Object> ctx, String timestamp) {
        Map<String, Object> action = new LinkedHashMap<>();
        action.put("name", name);
        action.put("surfaceId", surfaceId == null ? "" : surfaceId);
        action.put("sourceComponentId", sourceComponentId == null ? "" : sourceComponentId);
        action.put("timestamp", timestamp);
        action.put("context", ctx == null ? Map.of() : ctx);
        Map<String, Object> msg = new LinkedHashMap<>();
        msg.put("version", VERSION);
        msg.put("action", action);
        return msg;
    }
```

- [ ] **Step 4: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=A2uiActionTest test`
Expected: PASS

- [ ] **Step 5: Починить существующий `A2uiTest`, ожидающий старую версию**

`A2uiTest.java:29` и `:105` сравнивают версию сообщения со строкой `"v0.9"`.
Заменить оба литерала на `A2ui.VERSION` — тест проверяет, что версия
проставлена, а не какая именно ревизия сейчас в ходу.

- [ ] **Step 6: Прогнать весь модуль**

Run: `cd java && mvn -o test`
Expected: PASS — все тесты зелёные.

- [ ] **Step 7: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2ui.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiActionTest.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiTest.java
git commit -m "fix(java/a2ui): действие приезжает массивом — принять обе раскладки"
```

---

### Task 2: A2UI-часть по спеке — metadata.mimeType и массив сообщений

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiParts.java`
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/OrchestratorWebExecutor.java:154-175`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiPartsTest.java` (создать)

**Interfaces:**
- Consumes: `A2ui.MIME_TYPE`, `A2ui.MIME_KEY`, `A2ui.newAction` из задачи 1; `common.a2a.Part`.
- Produces: `A2uiParts.message(List<Map<String,Object>>) -> Part`, `A2uiParts.action(String name, String surfaceId, String sourceComponentId, Map<String,Object> ctx, Instant at) -> Part`, `A2uiParts.isA2ui(Part) -> boolean`.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiPartsTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Сборка A2UI-части A2A-сообщения. Правило проекта: принимаем либерально,
 * отдаём строго по спеке — metadata.mimeType и массив сообщений.
 */
class A2uiPartsTest {

    @Test
    void marksThePartBothWaysAndWrapsMessagesInAnArray() throws Exception {
        Part p = A2uiParts.message(List.of(Map.of("version", A2ui.VERSION, "createSurface", Map.of("surfaceId", "s1"))));

        assertEquals(A2ui.MIME_TYPE, p.mediaType, "родное поле A2A — для нашего кода и живых агентов");
        assertEquals(A2ui.MIME_TYPE, p.metadata.get(A2ui.MIME_KEY), "по этой пометке часть опознаёт стандартный клиент");

        String json = Json.MAPPER.writeValueAsString(p);
        assertTrue(json.contains("\"data\":[{"), "data обязана быть массивом сообщений: " + json);
    }

    @Test
    void actionPartCarriesTheClientToServerEnvelope() {
        Part p = A2uiParts.action("approve_refund", "s1", "approve",
                Map.of("order_id", "1041"), Instant.parse("2026-09-08T10:00:00Z"));

        A2ui.Action a = A2ui.parseAction(p.data);
        assertEquals("approve_refund", a.name());
        assertEquals("1041", a.context().get("order_id"));
        assertEquals(new A2ui.Origin("s1", "approve"), A2ui.actionOrigin(p.data));

        @SuppressWarnings("unchecked")
        Map<String, Object> action = (Map<String, Object>) ((List<Map<String, Object>>) p.data).getFirst().get("action");
        assertEquals("2026-09-08T10:00:00Z", action.get("timestamp"), "RFC3339 в UTC, как в Go");
    }

    @Test
    void recognisesItsOwnPartsAndIgnoresWidgetParts() {
        assertTrue(A2uiParts.isA2ui(A2uiParts.message(List.of(Map.of("version", A2ui.VERSION)))));
        Part widget = Part.data(Map.of("id", "1041"), Map.of("kind", "widget/order"));
        assertTrue(!A2uiParts.isA2ui(widget));
        assertTrue(!A2uiParts.isA2ui(null));
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=A2uiPartsTest test`
Expected: FAIL — класса `A2uiParts` нет.

- [ ] **Step 3: Создать `A2uiParts.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import io.github.kmpavloff.a2ademo.common.a2a.Part;

import java.time.Instant;
import java.time.format.DateTimeFormatter;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * Всё знание о том, как A2UI выглядит частью A2A-сообщения (порт
 * a2abridge/a2uipart.go).
 *
 * <p>Правило: принимаем либерально (обе пометки, оба MIME — см.
 * {@link A2ui#isA2ui}), отдаём строго по спеке.
 */
public final class A2uiParts {

    private A2uiParts() {}

    /**
     * Часть с разметкой. Полезная нагрузка — массив сообщений, как требует
     * спека («The data field ... MUST be an array of messages»), даже когда
     * сообщение одно.
     *
     * <p>Пометок ставится две: metadata.mimeType — та, по которой часть опознаёт
     * стандартный клиент, и mediaType — родное поле A2A, по которому её узнаёт
     * наш собственный код и живые агенты. Вторая ничему не мешает и не заменяет
     * первую.
     */
    public static Part message(List<Map<String, Object>> msgs) {
        Part p = Part.data(new ArrayList<Object>(msgs), Map.of(A2ui.MIME_KEY, A2ui.MIME_TYPE));
        p.mediaType = A2ui.MIME_TYPE;
        return p;
    }

    /**
     * Событие клиента (нажатие кнопки) по схеме client_to_server: тот же тип
     * части, но полезная нагрузка — действие.
     */
    public static Part action(String name, String surfaceId, String sourceComponentId,
                              Map<String, Object> ctx, Instant at) {
        String ts = DateTimeFormatter.ISO_INSTANT.format(at.truncatedTo(ChronoUnit.SECONDS));
        return message(List.of(A2ui.newAction(name, surfaceId, sourceComponentId, ctx, ts)));
    }

    /** Несёт ли часть разметку A2UI. */
    public static boolean isA2ui(Part p) {
        return p != null && A2ui.isA2ui(p.mediaType, p.metadata);
    }
}
```

- [ ] **Step 4: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=A2uiPartsTest test`
Expected: PASS

- [ ] **Step 5: Перевести исполнитель на новую сборку части**

В `OrchestratorWebExecutor.a2uiParts` (строки 154–175) сейчас каждая
A2UI-сообщение уходит отдельной частью с одним лишь `mediaType`. Заменить тело
цикла так, чтобы виджет отдавался ОДНОЙ частью с массивом сообщений:

```java
        List<Part> parts = new ArrayList<>();
        for (Map<String, Object> w : ws) {
            List<Map<String, Object>> msgs = A2ui.fromWidget(w);
            if (msgs == null) {
                continue;
            }
            trace.logf("  A2UI: widget %s → %d message(s) (%s)", w.get("_kind"), msgs.size(), A2ui.MIME_TYPE);
            parts.add(A2uiParts.message(msgs));
        }
        return parts;
```

Добавить импорт `io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2uiParts`.

- [ ] **Step 6: Прогнать весь модуль**

Run: `cd java && mvn -o test`
Expected: PASS. Если `WebE2eTest` считает A2UI-части поштучно — поправить
подсчёт: одна часть на виджет вместо двух (createSurface + updateComponents
теперь едут в одном массиве).

- [ ] **Step 7: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiParts.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiPartsTest.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/OrchestratorWebExecutor.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/WebE2eTest.java
git commit -m "feat(java/a2ui): часть с разметкой по спеке — metadata.mimeType и массив сообщений"
```

---

### Task 3: Согласование расширения по обеим ревизиям

**Files:**
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/OrchestratorCards.java:22-26`
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/A2aWebController.java:57-70`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/ExtensionNegotiationTest.java` (создать)

**Interfaces:**
- Consumes: `A2ui.EXTENSION_URI`, `A2ui.LEGACY_EXTENSION_URI` из задачи 1.
- Produces: `A2aWebController.invoke` активирует A2UI по любому из двух URI и эхом возвращает тот, который запросили.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/ExtensionNegotiationTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Согласование расширения A2UI. Обе ревизии объявляются и обе принимаются:
 * полезная нагрузка у них общая, а клиент, знающий только 0.9, по одному лишь
 * URI 0.9.1 нас за A2UI-агента не примет. Фронтенд этого проекта как раз шлёт
 * предыдущую ревизию — см. комментарий в web/src/client.ts.
 */
class ExtensionNegotiationTest {

    @Test
    @SuppressWarnings("unchecked")
    void cardAdvertisesBothRevisions() {
        AgentCard card = OrchestratorCards.agentCard("http://localhost:8080");
        List<Map<String, Object>> exts = (List<Map<String, Object>>) card.capabilities.get("extensions");
        List<Object> uris = exts.stream().map(e -> e.get("uri")).toList();
        assertTrue(uris.contains(A2ui.EXTENSION_URI), "новая ревизия: " + uris);
        assertTrue(uris.contains(A2ui.LEGACY_EXTENSION_URI), "предыдущая ревизия: " + uris);
    }

    @Test
    void activatesOnEitherUriAndEchoesTheRequestedOne() {
        assertEquals(A2ui.EXTENSION_URI, A2aWebController.negotiate(List.of(A2ui.EXTENSION_URI)));
        assertEquals(A2ui.LEGACY_EXTENSION_URI, A2aWebController.negotiate(List.of(A2ui.LEGACY_EXTENSION_URI)));
    }

    // Spring разбивает значение заголовка по запятым, поэтому в списке может
    // приехать несколько URI сразу.
    @Test
    void activatesWhenTheHeaderCarriesSeveralUris() {
        assertEquals(A2ui.EXTENSION_URI,
                A2aWebController.negotiate(List.of("https://example.org/other", A2ui.EXTENSION_URI)));
    }

    @Test
    void staysInactiveWithoutTheHeader() {
        assertEquals("", A2aWebController.negotiate(null));
        assertEquals("", A2aWebController.negotiate(List.of("https://example.org/other")));
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=ExtensionNegotiationTest test`
Expected: FAIL — карточка объявляет один URI, метода `negotiate` нет.

- [ ] **Step 3: Объявить обе ревизии в карточке**

В `OrchestratorCards.agentCard` заменить блок `card.capabilities = ...` на:

```java
        // Обе ревизии: полезная нагрузка у них общая, а клиент, знающий только
        // 0.9, по одному лишь URI 0.9.1 нас за A2UI-агента не примет.
        card.capabilities = Map.of("extensions", List.of(
                Map.of("uri", A2ui.EXTENSION_URI,
                        "description", "Отдаёт интерфейс через A2UI (generative UI)."),
                Map.of("uri", A2ui.LEGACY_EXTENSION_URI,
                        "description", "То же в терминах предыдущей ревизии A2UI.")));
```

- [ ] **Step 4: Вынести согласование в проверяемый метод**

В `A2aWebController` заменить тело `invoke` (строки 57–70) на:

```java
    @PostMapping(value = "/invoke", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<JsonRpc.Response> invoke(
            @RequestBody String body,
            @RequestHeader(value = "A2A-Extensions", required = false) List<String> extensions,
            @RequestHeader(value = "X-A2A-Extensions", required = false) List<String> legacyExtensions) {
        // Заголовок приезжает под двумя именами: A2A 1.0 зовёт его
        // A2A-Extensions, а спека A2UI v0.9 писалась под ранний A2A и знает
        // только X-A2A-Extensions. Клиент шлёт оба — принимаем любой.
        String activated = negotiate(extensions);
        if (activated.isEmpty()) {
            activated = negotiate(legacyExtensions);
        }

        JsonRpc.Response response = handle(body, !activated.isEmpty());
        ResponseEntity.BodyBuilder builder = ResponseEntity.ok();
        if (!activated.isEmpty() && response.error == null) {
            // Эхом возвращается ИМЕННО запрошенный URI: клиент на 0.9 ждёт
            // подтверждения под своим именем ревизии.
            builder.header("A2A-Extensions", activated);
        }
        return builder.body(response);
    }

    /**
     * Какой URI расширения A2UI запросил клиент, или "" — если ни одного.
     * Значение заголовка бывает списком через запятую, поэтому оно ещё и
     * разбивается.
     */
    static String negotiate(List<String> headerValues) {
        if (headerValues == null) {
            return "";
        }
        for (String value : headerValues) {
            if (value == null) {
                continue;
            }
            for (String uri : value.split("[,\\s]+")) {
                if (uri.equals(A2ui.EXTENSION_URI) || uri.equals(A2ui.LEGACY_EXTENSION_URI)) {
                    return uri;
                }
            }
        }
        return "";
    }
```

- [ ] **Step 5: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=ExtensionNegotiationTest test`
Expected: PASS

- [ ] **Step 6: Прогнать весь модуль**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 7: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/OrchestratorCards.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/A2aWebController.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/ExtensionNegotiationTest.java
git commit -m "feat(java/a2ui): согласование расширения по обеим ревизиям 0.9 и 0.9.1"
```

---

## Фаза B — приём чужой разметки

Внешний агент вправе сам говорить на A2UI. Go принимает его разметку, нормализует
к basic-каталогу и передаёт в браузер; Java этого не умеет вовсе. Обе задачи фазы
дают проверенные библиотечные классы — в исполнитель они подключаются в фазе C
вместе с `Remote`.

### Task 4: Поверхности разводятся по ходам

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/Surfaces.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/SurfacesTest.java`

**Interfaces:**
- Produces: `Surfaces.retag(List<Map<String,Object>> msgs, String suffix) -> List<Map<String,Object>>`, `Surfaces.untag(String id) -> String`.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/SurfacesTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertSame;

/**
 * Суффикс хода у surfaceId. Агент нередко выводит идентификатор поверхности из
 * контекста, а контекст живёт всю сессию: второй ход прислал бы createSurface с
 * тем же id, и рендерер упал бы с «Surface … already exists».
 */
class SurfacesTest {

    @SuppressWarnings("unchecked")
    @Test
    void retagsEverySurfaceIdConsistently() {
        List<Map<String, Object>> msgs = List.of(
                Map.of("version", A2ui.VERSION, "createSurface", Map.of("surfaceId", "s1", "catalogId", A2ui.CATALOG_ID)),
                Map.of("version", A2ui.VERSION, "updateComponents", Map.of("surfaceId", "s1", "components", List.of())));

        List<Map<String, Object>> out = Surfaces.retag(msgs, "-t3");

        assertEquals("s1-t3", ((Map<String, Object>) out.get(0).get("createSurface")).get("surfaceId"));
        assertEquals("s1-t3", ((Map<String, Object>) out.get(1).get("updateComponents")).get("surfaceId"),
                "ссылка обновления обязана попасть в ту же поверхность");
        assertEquals("s1", ((Map<String, Object>) msgs.get(0).get("createSurface")).get("surfaceId"),
                "исходные сообщения не правятся на месте");
    }

    @Test
    void emptySuffixChangesNothing() {
        List<Map<String, Object>> msgs = List.of(Map.of("version", A2ui.VERSION));
        assertSame(msgs, Surfaces.retag(msgs, ""));
    }

    @Test
    void leavesMessagesWithoutASurfaceIdAlone() {
        List<Map<String, Object>> out = Surfaces.retag(
                List.of(Map.of("version", A2ui.VERSION, "createSurface", Map.of("catalogId", A2ui.CATALOG_ID))), "-t1");
        assertEquals(Map.of("catalogId", A2ui.CATALOG_ID), out.getFirst().get("createSurface"));
    }

    // На пути наружу суффикс снимается: в ленте поверхность зовётся s1-t3, а
    // агент знает её как s1, и сопоставить событие он может только со своим id.
    @Test
    void untagsBackToTheAgentsOwnId() {
        assertEquals("s1", Surfaces.untag("s1-t3"));
        assertEquals("s1", Surfaces.untag("s1"));
        assertEquals("confirmation-1", Surfaces.untag("confirmation-1-t42"));
        assertEquals("", Surfaces.untag(null));
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=SurfacesTest test`
Expected: FAIL — класса `Surfaces` нет.

- [ ] **Step 3: Создать `Surfaces.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * Идентификаторы поверхностей A2UI, разведённые по ходам (порт хвоста
 * internal/a2ui/ingest.go).
 */
public final class Surfaces {

    private Surfaces() {}

    /** Сообщения, у которых есть surfaceId. */
    private static final List<String> SURFACE_ID_KEYS =
            List.of("createSurface", "updateComponents", "updateDataModel", "deleteSurface");

    /** Суффикс, который навешивает {@link #retag}: «-t» и номер хода. */
    private static final Pattern TURN_SUFFIX = Pattern.compile("-t\\d+$");

    /**
     * Снимает суффикс хода — обратная операция к {@link #retag}.
     *
     * <p>Суффикс распознаётся по форме, а не запоминается: клик может прийти на
     * любом последующем ходу, и номер, под которым поверхность создавалась, к
     * этому моменту уже не тот. Агентский id, сам оканчивающийся на «-t» с
     * цифрами, будет укорочен ошибочно — но это ровно тот исход, который без
     * снятия суффикса получался бы всегда.
     */
    public static String untag(String id) {
        if (id == null) {
            return "";
        }
        return TURN_SUFFIX.matcher(id).replaceAll("");
    }

    /**
     * Приписывает суффикс ко всем surfaceId в наборе сообщений. Ссылки внутри
     * набора переписываются согласованно, так что updateComponents по-прежнему
     * попадает в свою поверхность. Входные сообщения не изменяются.
     */
    @SuppressWarnings("unchecked")
    public static List<Map<String, Object>> retag(List<Map<String, Object>> msgs, String suffix) {
        if (suffix == null || suffix.isEmpty() || msgs == null) {
            return msgs;
        }
        List<Map<String, Object>> out = new ArrayList<>(msgs.size());
        for (Map<String, Object> m : msgs) {
            Map<String, Object> next = new LinkedHashMap<>(m);
            for (String key : SURFACE_ID_KEYS) {
                if (!(next.get(key) instanceof Map<?, ?> raw)) {
                    continue;
                }
                Map<String, Object> payload = (Map<String, Object>) raw;
                if (!(payload.get("surfaceId") instanceof String id) || id.isEmpty()) {
                    continue;
                }
                Map<String, Object> copied = new LinkedHashMap<>(payload);
                copied.put("surfaceId", id + suffix);
                next.put(key, copied);
            }
            out.add(next);
        }
        return out;
    }
}
```

- [ ] **Step 4: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=SurfacesTest test`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/Surfaces.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/SurfacesTest.java
git commit -m "feat(java/a2ui): суффикс хода у surfaceId"
```

---

### Task 5: Нормализация чужой разметки к basic-каталогу

Самая большая задача плана. Порт `internal/a2ui/ingest.go` целиком.

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiIngest.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiIngestTest.java`

**Interfaces:**
- Consumes: `A2uiParts.isA2ui(Part)` из задачи 2, `common.a2a.Part`, `common.Json.MAPPER`.
- Produces: `A2uiIngest.ingest(List<Part>) -> List<Map<String,Object>>`.

**Инвариант:** `ingest` никогда не бросает исключение. Худший исход — блёклый, но
валидный набор компонентов: чужой агент не должен ронять наш UI.

**Отличие от Go, принятое сознательно:** Java-класс работает прямо с
`common.a2a.Part`, тогда как Go завёл собственный тип `a2ui.Part`, чтобы пакет не
зависел от SDK a2a. В Java `Part` — простой DTO своего же проекта, отдельный тип
дал бы переписывание без выигрыша.

**Расхождение в Go, которое НЕ переносим:** комментарий над `normalizeMessage`
(`internal/a2ui/ingest.go:100-106`) говорит о понижении версии до `v0.9`, а код
строкой ниже ставит `Version`, то есть `v0.9.1`. Портируем поведение кода.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiIngestTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import io.github.kmpavloff.a2ademo.common.a2a.Part;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Приём разметки, которую агент написал сам. Толерантен по входу, потому что
 * живой агент отдаёт не то, что обещает его контракт.
 */
class A2uiIngestTest {

    private static Part a2uiPart(Object payload) {
        Part p = Part.data(payload, Map.of(A2ui.MIME_KEY, A2ui.MIME_TYPE));
        p.mediaType = A2ui.MIME_TYPE;
        return p;
    }

    private static Map<String, Object> map(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put((String) kv[i], kv[i + 1]);
        }
        return m;
    }

    private static List<Map<String, Object>> mutable(Map<String, Object>... comps) {
        return new ArrayList<>(List.of(comps));
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> payloadOf(Map<String, Object> msg, String key) {
        return (Map<String, Object>) msg.get(key);
    }

    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> componentsOf(Map<String, Object> msg) {
        return (List<Map<String, Object>>) payloadOf(msg, "updateComponents").get("components");
    }

    @Test
    void ignoresPartsThatAreNotA2ui() {
        assertTrue(A2uiIngest.ingest(List.of(Part.text("просто текст"))).isEmpty());
        assertTrue(A2uiIngest.ingest(null).isEmpty());
    }

    // Версия переписывается на нашу, а каталог объявляется наш: компоненты мы
    // приводим к basic-каталогу, и объявлять надо именно его.
    @Test
    void normalisesVersionAndCatalog() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.8", "createSurface", map("surfaceId", "s1", "catalogId", "https://example.org/own"))))));

        assertEquals(1, out.size());
        assertEquals(A2ui.VERSION, out.getFirst().get("version"));
        assertEquals(A2ui.CATALOG_ID, payloadOf(out.getFirst(), "createSurface").get("catalogId"));
    }

    // Разметка приезжает и текстовой частью, внутри которой JSON-строка.
    @Test
    void readsMarkupFromATextPart() {
        Part p = Part.text("[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"s1\"}}]");
        p.mediaType = A2ui.LEGACY_MIME_TYPE;
        assertEquals(1, A2uiIngest.ingest(List.of(p)).size());
    }

    // Рендерер строит дерево от компонента с id ровно "root"; чужие агенты
    // называют корень как угодно — назначаем его сами.
    @Test
    void addsAMissingRootOverTheTopLevelComponents() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "hdr", "component", "Text", "text", "Заказ 1041"),
                        map("id", "body", "component", "Text", "text", "Доставлен"))))))));

        List<Map<String, Object>> comps = componentsOf(out.getFirst());
        assertEquals("root", comps.getFirst().get("id"));
        assertEquals("Column", comps.getFirst().get("component"));
        assertEquals(List.of("hdr", "body"), comps.getFirst().get("children"));
    }

    @Test
    void keepsAnExistingRootAsIs() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "root", "component", "Column", "children", List.of("hdr")),
                        map("id", "hdr", "component", "Text", "text", "Заказ"))))))));

        assertEquals(2, componentsOf(out.getFirst()).size());
    }

    // Корень назначается один раз на поверхность: второе обновление той же
    // поверхности ссылается на уже существующее дерево.
    @Test
    void rootsEachSurfaceOnlyOnce() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "a", "component", "Text", "text", "раз")))),
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "b", "component", "Text", "text", "два"))))))));

        assertEquals("root", componentsOf(out.get(0)).getFirst().get("id"));
        assertEquals("b", componentsOf(out.get(1)).getFirst().get("id"), "второй раз корень не навешивается");
    }

    // Обёрнутая форма компонента: {"Text": {...}}.
    @Test
    void unwrapsTheWrappedComponentForm() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("Text", map("id", "hdr", "text", "Заказ", "variant", "h3")))))))));

        Map<String, Object> hdr = componentsOf(out.getFirst()).get(1);
        assertEquals("Text", hdr.get("component"));
        assertEquals("Заказ", hdr.get("text"));
    }

    // У кнопки в каталоге нет свойства label — подпись живёт отдельным Text.
    @Test
    void splitsAButtonLabelIntoAChildText() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "ok", "component", "Button", "label", "Подтвердить возврат",
                                "action", map("event", map("name", "return_order")))))))));

        List<Map<String, Object>> comps = componentsOf(out.getFirst());
        Map<String, Object> btn = comps.stream().filter(c -> "ok".equals(c.get("id"))).findFirst().orElseThrow();
        Map<String, Object> label = comps.stream().filter(c -> "ok__lbl".equals(c.get("id"))).findFirst().orElseThrow();
        assertEquals("ok__lbl", btn.get("child"));
        assertEquals("Подтвердить возврат", label.get("text"));
        assertTrue(!btn.containsKey("label"), "label остаться в кнопке не должен");
    }

    // Подпись копируется в контекст действия: без неё клиенту нечего показать в
    // ленте, кроме служебного имени («return_order» вместо «Подтвердить возврат»).
    @SuppressWarnings("unchecked")
    @Test
    void copiesTheButtonLabelIntoTheActionContext() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "ok", "component", "Button", "label", "Подтвердить возврат",
                                "action", map("event", map("name", "return_order")))))))));

        Map<String, Object> btn = componentsOf(out.getFirst()).stream()
                .filter(c -> "ok".equals(c.get("id"))).findFirst().orElseThrow();
        Map<String, Object> event = (Map<String, Object>) ((Map<String, Object>) btn.get("action")).get("event");
        assertEquals("Подтвердить возврат", ((Map<String, Object>) event.get("context")).get("label"));
    }

    // Схема требует «ключ → значение», а агент присылает массив пар.
    @SuppressWarnings("unchecked")
    @Test
    void turnsAPairListContextIntoAnObject() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "ok", "component", "Button", "child", "lbl",
                                "action", map("event", map("name", "return_order", "context",
                                        List.of(map("key", "order_id", "value", "1041"))))),
                        map("id", "lbl", "component", "Text", "text", "Вернуть"))))))));

        Map<String, Object> btn = componentsOf(out.getFirst()).stream()
                .filter(c -> "ok".equals(c.get("id"))).findFirst().orElseThrow();
        Map<String, Object> event = (Map<String, Object>) ((Map<String, Object>) btn.get("action")).get("event");
        assertEquals("1041", ((Map<String, Object>) event.get("context")).get("order_id"));
    }

    @Test
    void rendersATableAsMarkdown() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "t", "component", "Table",
                                "columns", List.of(map("key", "id", "label", "№"), map("key", "item", "label", "Товар")),
                                "rows", List.of(map("id", "1041", "item", "Наушники"))))))))));

        Map<String, Object> t = componentsOf(out.getFirst()).stream()
                .filter(c -> "t".equals(c.get("id"))).findFirst().orElseThrow();
        assertEquals("Text", t.get("component"));
        assertEquals("| № | Товар |\n| --- | --- |\n| 1041 | Наушники |", t.get("text"));
    }

    @Test
    void mapsHeadingAndMetricToText() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "h", "component", "Heading", "text", "Заказ 1041"),
                        map("id", "m", "component", "Metric", "label", "Сумма", "value", 4990))))))));

        List<Map<String, Object>> comps = componentsOf(out.getFirst());
        Map<String, Object> h = comps.stream().filter(c -> "h".equals(c.get("id"))).findFirst().orElseThrow();
        Map<String, Object> m = comps.stream().filter(c -> "m".equals(c.get("id"))).findFirst().orElseThrow();
        assertEquals("h3", h.get("variant"));
        assertEquals("**Сумма:** 4990", m.get("text"));
    }

    // Незнакомый компонент лучше показать дампом, чем потерять молча.
    @Test
    void dumpsAnUnknownComponentInsteadOfDroppingIt() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("id", "x", "component", "Gauge", "percent", 42))))))));

        Map<String, Object> x = componentsOf(out.getFirst()).stream()
                .filter(c -> "x".equals(c.get("id"))).findFirst().orElseThrow();
        assertEquals("Text", x.get("component"));
        assertTrue(String.valueOf(x.get("text")).startsWith("`Gauge`"), "дамп: " + x.get("text"));
        assertTrue(String.valueOf(x.get("text")).contains("42"));
    }

    // Компонент без id получает сгенерированный: на id ссылается children
    // родителя, и потеря id разорвала бы дерево.
    @Test
    void generatesIdsForComponentsThatArrivedWithoutOne() {
        List<Map<String, Object>> out = A2uiIngest.ingest(List.of(a2uiPart(List.of(
                map("version", "v0.9", "updateComponents", map("surfaceId", "s1", "components", mutable(
                        map("component", "Text", "text", "раз"),
                        map("component", "Text", "text", "два"))))))));

        List<Map<String, Object>> comps = componentsOf(out.getFirst());
        assertEquals(List.of("gen1", "gen2"), comps.getFirst().get("children"));
    }

    @Test
    void dropsMessagesOfAnUnknownKind() {
        assertTrue(A2uiIngest.ingest(List.of(a2uiPart(List.of(map("version", "v0.9", "somethingElse", map()))))).isEmpty());
    }

    // Инвариант: мусор на входе не роняет UI.
    @Test
    void survivesMalformedPayloads() {
        assertTrue(A2uiIngest.ingest(List.of(a2uiPart("не JSON"))).isEmpty());
        assertTrue(A2uiIngest.ingest(List.of(a2uiPart(List.of("строка вместо сообщения")))).isEmpty());
        assertTrue(A2uiIngest.ingest(List.of(a2uiPart(map("version", "v0.9", "updateComponents",
                map("surfaceId", "s1", "components", "не список"))))).isEmpty());
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=A2uiIngestTest test`
Expected: FAIL — класса `A2uiIngest` нет.

- [ ] **Step 3: Создать `A2uiIngest.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2ui;

import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.Part;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Достаёт A2UI-сообщения из частей ответа удалённого агента и приводит их к
 * форме, которую понимает наш рендерер: плоские компоненты basic-каталога v0.9
 * (порт internal/a2ui/ingest.go).
 *
 * <p>Толерантен по входу, потому что живой агент отдаёт не то, что обещает его
 * контракт: A2UI приезжает и как DataPart с массивом сообщений, и как одиночный
 * объект, и как ТЕКСТОВАЯ часть с mediaType application/a2ui+json, внутри
 * которой JSON-строка. Компоненты приезжают и плоскими, и обёрнутыми
 * ({@code {"Text": {...}}}), и типов, которых нет в объявленном самим агентом
 * каталоге.
 *
 * <p>Инвариант: {@code ingest} никогда не бросает исключение. Худший исход —
 * блёклый, но валидный набор компонентов: чужой агент не должен ронять наш UI.
 */
public final class A2uiIngest {

    private A2uiIngest() {}

    /** Сообщения A2UI, которые понимает наш рендерер. Остальные отбрасываются. */
    private static final List<String> KNOWN_MESSAGES =
            List.of("createSurface", "updateComponents", "updateDataModel", "deleteSurface");

    /**
     * 18 компонентов basic-каталога v0.9. Ingest обязан выдавать только их:
     * объявленный агентами каталог именно этот, и наш рендерер отвергает всё,
     * чего в нём нет.
     */
    private static final Set<String> BASIC_COMPONENTS = Set.of(
            "Text", "Image", "Icon", "Video", "AudioPlayer",
            "Row", "Column", "List", "Card", "Tabs",
            "Modal", "Divider", "Button", "TextField",
            "CheckBox", "ChoicePicker", "Slider", "DateTimeInput");

    /** Состояние одного вызова: счётчик id и поверхности с назначенным корнем. */
    private static final class State {
        int gen;
        final Set<String> rooted = new HashSet<>();
    }

    public static List<Map<String, Object>> ingest(List<Part> parts) {
        List<Map<String, Object>> out = new ArrayList<>();
        if (parts == null) {
            return out;
        }
        State st = new State();
        for (Part p : parts) {
            if (!A2uiParts.isA2ui(p)) {
                continue;
            }
            for (Map<String, Object> raw : messagesFrom(p)) {
                Map<String, Object> m = normalizeMessage(raw, st);
                if (m != null) {
                    out.add(m);
                }
            }
        }
        return out;
    }

    /** Разбирает нагрузку части в список сырых сообщений, принимая все три раскладки. */
    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> messagesFrom(Part p) {
        Object payload = p.data;
        if (payload == null) {
            String text = p.textOrEmpty().trim();
            if (text.isEmpty()) {
                return List.of();
            }
            try {
                payload = Json.MAPPER.readValue(text, Object.class);
            } catch (Exception e) {
                return List.of();
            }
        }
        if (payload instanceof List<?> list) {
            List<Map<String, Object>> msgs = new ArrayList<>(list.size());
            for (Object item : list) {
                if (item instanceof Map<?, ?> m) {
                    msgs.add((Map<String, Object>) m);
                }
            }
            return msgs;
        }
        if (payload instanceof Map<?, ?> m) {
            return List.of((Map<String, Object>) m);
        }
        return List.of();
    }

    /**
     * Переписывает версию сообщения на нашу и нормализует компоненты. null —
     * для сообщений неизвестного вида и для набора, из которого ничего не
     * осталось.
     */
    @SuppressWarnings("unchecked")
    private static Map<String, Object> normalizeMessage(Map<String, Object> msg, State st) {
        String kind = "";
        for (String k : KNOWN_MESSAGES) {
            if (msg.containsKey(k)) {
                kind = k;
                break;
            }
        }
        if (kind.isEmpty()) {
            return null;
        }
        Map<String, Object> out = new LinkedHashMap<>(msg);
        out.put("version", A2ui.VERSION);

        if (kind.equals("createSurface") && out.get("createSurface") instanceof Map<?, ?> raw) {
            // Компоненты мы приводим к basic-каталогу, поэтому и объявлять надо
            // его: иначе клиент отвергнет поверхность, которую Ingest уже сделал
            // пригодной к отрисовке.
            Map<String, Object> copied = new LinkedHashMap<>((Map<String, Object>) raw);
            copied.put("catalogId", A2ui.CATALOG_ID);
            out.put("createSurface", copied);
        }
        if (!kind.equals("updateComponents")) {
            return out;
        }
        if (!(out.get("updateComponents") instanceof Map<?, ?> rawPayload)) {
            return null;
        }
        Map<String, Object> payload = (Map<String, Object>) rawPayload;
        List<Map<String, Object>> raw = componentList(payload.get("components"));
        if (raw.isEmpty()) {
            return null;
        }
        List<Map<String, Object>> comps = new ArrayList<>();
        for (Map<String, Object> c : raw) {
            comps.addAll(normalizeComponent(c, st));
        }
        if (comps.isEmpty()) {
            return null;
        }
        labelButtonActions(comps);
        // Рендерер строит дерево от компонента с id ровно "root"; без него
        // поверхность навсегда остаётся в состоянии «Loading surface…». Чужие
        // агенты называют корень как угодно — назначаем его сами, один раз на
        // поверхность.
        String surfaceId = payload.get("surfaceId") instanceof String s ? s : "";
        if (!st.rooted.contains(surfaceId)) {
            comps = ensureRoot(comps);
            st.rooted.add(surfaceId);
        }
        Map<String, Object> next = new LinkedHashMap<>(payload);
        next.put("components", comps);
        out.put("updateComponents", next);
        return out;
    }

    /** Приводит поле components к списку объектов. */
    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> componentList(Object v) {
        if (!(v instanceof List<?> list)) {
            return List.of();
        }
        List<Map<String, Object>> out = new ArrayList<>(list.size());
        for (Object item : list) {
            if (item instanceof Map<?, ?> m) {
                out.add((Map<String, Object>) m);
            }
        }
        return out;
    }

    /** Тип компонента и его свойства. */
    private record Unwrapped(String type, Map<String, Object> props) {}

    /**
     * Разбирает обёрнутую форму компонента ({@code {"Text": {...}}}), которую
     * шлёт внешний агент, в пару «тип, свойства». Плоская форма возвращается как
     * есть. Свойства всегда копируются: дальше их правят на месте.
     */
    @SuppressWarnings("unchecked")
    private static Unwrapped unwrap(Map<String, Object> c) {
        if (c.get("component") instanceof String t && !t.isEmpty()) {
            Map<String, Object> props = new LinkedHashMap<>(c);
            props.remove("component");
            return new Unwrapped(t, props);
        }
        if (c.size() == 1) {
            for (Map.Entry<String, Object> e : c.entrySet()) {
                if (e.getValue() instanceof Map<?, ?> props) {
                    return new Unwrapped(e.getKey(), new LinkedHashMap<>((Map<String, Object>) props));
                }
            }
        }
        return null;
    }

    /**
     * Превращает один входящий компонент в один или несколько компонентов
     * basic-каталога. Id контейнера всегда сохраняется: на него ссылается
     * children родителя, и потеря id разорвала бы дерево.
     */
    private static List<Map<String, Object>> normalizeComponent(Map<String, Object> c, State st) {
        Unwrapped u = unwrap(c);
        if (u == null) {
            return List.of();
        }
        String typ = u.type();
        Map<String, Object> props = u.props();
        String id = props.get("id") instanceof String s && !s.isEmpty() ? s : "gen" + (++st.gen);
        props.remove("id");

        switch (typ) {
            case "Button":
                return normalizeButton(id, props);
            case "Heading":
                return List.of(text(id, str(props.get("text")), "h3"));
            case "Metric": {
                String label = str(props.get("label"));
                String value = String.valueOf(props.get("value"));
                return List.of(text(id, label.isEmpty() ? value : "**" + label + ":** " + value, "body"));
            }
            case "Callout":
                return normalizeCallout(id, props);
            case "Table":
                return List.of(text(id, markdownTable(props), "body"));
            default:
                break;
        }
        if (!BASIC_COMPONENTS.contains(typ)) {
            // Незнакомый компонент лучше показать дампом, чем потерять молча.
            return List.of(text(id, unknownDump(typ, props), "body"));
        }
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("id", id);
        out.put("component", typ);
        out.putAll(props);
        normalizeAction(out);
        return List.of(out);
    }

    /**
     * Приводит контекст события к объекту: схема A2UI требует «ключ → значение»,
     * а агент присылает массив пар {@code {key, value}}, на котором рендерер
     * соберёт бессмысленный контекст.
     *
     * <p>Заодно action и event пересобираются в свои карты: дальше их правит
     * {@link #labelButtonActions}, а приехавшие из JSON вложенные карты
     * принадлежат сообщению агента, и править их на месте нельзя.
     */
    @SuppressWarnings("unchecked")
    private static void normalizeAction(Map<String, Object> c) {
        if (!(c.get("action") instanceof Map<?, ?> rawAction)) {
            return;
        }
        Map<String, Object> action = new LinkedHashMap<>((Map<String, Object>) rawAction);
        c.put("action", action);
        if (!(action.get("event") instanceof Map<?, ?> rawEvent)) {
            return;
        }
        Map<String, Object> event = new LinkedHashMap<>((Map<String, Object>) rawEvent);
        action.put("event", event);
        if (event.get("context") instanceof List<?> pairs) {
            Map<String, Object> ctx = new LinkedHashMap<>();
            for (Object p : pairs) {
                if (p instanceof Map<?, ?> pair && pair.get("key") instanceof String key && !key.isEmpty()) {
                    ctx.put(key, pair.get("value"));
                }
            }
            event.put("context", ctx);
        } else if (event.get("context") instanceof Map<?, ?> m) {
            event.put("context", new LinkedHashMap<>((Map<String, Object>) m));
        }
    }

    /**
     * Приводит кнопку к форме каталога: у неё нет свойства label, подпись живёт
     * отдельным дочерним Text.
     */
    private static List<Map<String, Object>> normalizeButton(String id, Map<String, Object> props) {
        Map<String, Object> btn = new LinkedHashMap<>();
        btn.put("id", id);
        btn.put("component", "Button");
        for (Map.Entry<String, Object> e : props.entrySet()) {
            if (!e.getKey().equals("label")) {
                btn.put(e.getKey(), e.getValue());
            }
        }
        normalizeAction(btn);
        if (btn.containsKey("child")) {
            return List.of(btn);
        }
        String label = str(props.get("label"));
        if (label.isEmpty()) {
            label = "OK";
        }
        String labelId = id + "__lbl";
        btn.put("child", labelId);
        return List.of(btn, text(labelId, label, "body"));
    }

    /**
     * Разворачивает плашку в колонку из заголовка и текста, сохраняя id самой
     * плашки за колонкой.
     */
    private static List<Map<String, Object>> normalizeCallout(String id, Map<String, Object> props) {
        String titleId = id + "__t";
        String bodyId = id + "__b";
        String title = str(props.get("title"));
        String body = str(props.get("text"));
        List<Object> children = new ArrayList<>();
        List<Map<String, Object>> comps = new ArrayList<>();
        comps.add(null); // место под колонку
        if (!title.isEmpty()) {
            children.add(titleId);
            comps.add(text(titleId, title, "h3"));
        }
        if (!body.isEmpty()) {
            children.add(bodyId);
            comps.add(text(bodyId, body, "body"));
        }
        if (children.isEmpty()) {
            return List.of(text(id, "", "body"));
        }
        Map<String, Object> column = new LinkedHashMap<>();
        column.put("id", id);
        column.put("component", "Column");
        column.put("children", children);
        comps.set(0, column);
        return comps;
    }

    /**
     * Дописывает в контекст действия человекочитаемую подпись кнопки. Без неё
     * клиенту нечего показать в ленте, кроме служебного имени действия
     * («return_order» вместо «Подтвердить возврат»): в самом событии подписи
     * нет, она живёт в дочернем Text.
     */
    @SuppressWarnings("unchecked")
    private static void labelButtonActions(List<Map<String, Object>> comps) {
        Map<String, Map<String, Object>> byId = new HashMap<>(comps.size());
        for (Map<String, Object> c : comps) {
            if (c.get("id") instanceof String id) {
                byId.put(id, c);
            }
        }
        for (Map<String, Object> c : comps) {
            if (!"Button".equals(c.get("component"))) {
                continue;
            }
            if (!(c.get("action") instanceof Map<?, ?> rawAction)) {
                continue;
            }
            Map<String, Object> action = (Map<String, Object>) rawAction;
            if (!(action.get("event") instanceof Map<?, ?> rawEvent)) {
                continue;
            }
            Map<String, Object> event = (Map<String, Object>) rawEvent;
            Map<String, Object> ctx;
            if (event.get("context") instanceof Map<?, ?> m) {
                ctx = (Map<String, Object>) m;
            } else {
                ctx = new LinkedHashMap<>();
                event.put("context", ctx);
            }
            if (ctx.containsKey("label")) {
                continue;
            }
            Map<String, Object> child = byId.get(c.get("child") instanceof String s ? s : "");
            if (child != null && child.get("text") instanceof String label && !label.isEmpty()) {
                ctx.put("label", label);
            }
        }
    }

    /**
     * Гарантирует, что у набора есть компонент с id "root". Недостающий корень
     * добавляется отдельной колонкой поверх верхних компонентов —
     * переименовывать чужие id нельзя, на них могут ссылаться последующие
     * обновления той же поверхности.
     */
    private static List<Map<String, Object>> ensureRoot(List<Map<String, Object>> comps) {
        Set<String> referenced = new HashSet<>();
        for (Map<String, Object> c : comps) {
            if (c.get("child") instanceof String child) {
                referenced.add(child);
            }
            if (c.get("children") instanceof List<?> children) {
                for (Object ch : children) {
                    if (ch instanceof String name) {
                        referenced.add(name);
                    }
                }
            }
        }
        List<Object> tops = new ArrayList<>();
        for (Map<String, Object> c : comps) {
            String id = c.get("id") instanceof String s ? s : "";
            if (id.equals("root")) {
                return comps; // корень уже на месте
            }
            if (!referenced.contains(id)) {
                tops.add(id);
            }
        }
        if (tops.isEmpty()) {
            // Дерево замкнуто само на себя — вешаем корень на первый компонент.
            if (comps.isEmpty()) {
                return comps;
            }
            tops.add(String.valueOf(comps.getFirst().get("id")));
        }
        Map<String, Object> root = new LinkedHashMap<>();
        root.put("id", "root");
        root.put("component", "Column");
        root.put("children", tops);
        List<Map<String, Object>> out = new ArrayList<>(comps.size() + 1);
        out.add(root);
        out.addAll(comps);
        return out;
    }

    /**
     * Рисует таблицу разметкой: Text в браузере рендерится через markdown-it,
     * поэтому она станет настоящей &lt;table&gt;.
     */
    private static String markdownTable(Map<String, Object> props) {
        List<Map<String, Object>> cols = componentList(props.get("columns"));
        List<Map<String, Object>> rows = componentList(props.get("rows"));
        if (cols.isEmpty()) {
            return unknownDump("Table", props);
        }
        List<String> keys = new ArrayList<>(cols.size());
        StringBuilder b = new StringBuilder("|");
        for (Map<String, Object> c : cols) {
            String key = str(c.get("key"));
            String label = str(c.get("label"));
            keys.add(key);
            b.append(' ').append(label.isEmpty() ? key : label).append(" |");
        }
        b.append("\n|");
        for (int i = 0; i < keys.size(); i++) {
            b.append(" --- |");
        }
        for (Map<String, Object> r : rows) {
            b.append("\n|");
            for (String k : keys) {
                Object v = r.get(k);
                b.append(' ').append(v == null ? "" : v).append(" |");
            }
        }
        return b.toString();
    }

    /** Компактно показывает то, что мы не умеем отрисовать. */
    private static String unknownDump(String typ, Map<String, Object> props) {
        try {
            return "`" + typ + "` " + Json.MAPPER.writeValueAsString(props);
        } catch (Exception e) {
            return typ;
        }
    }

    private static Map<String, Object> text(String id, String s, String variant) {
        Map<String, Object> c = new LinkedHashMap<>();
        c.put("id", id);
        c.put("component", "Text");
        c.put("text", s);
        c.put("variant", variant);
        return c;
    }

    private static String str(Object v) {
        return v instanceof String s ? s : "";
    }
}
```

- [ ] **Step 4: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=A2uiIngestTest test`
Expected: PASS

- [ ] **Step 5: Прогнать весь модуль**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 6: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiIngest.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2ui/A2uiIngestTest.java
git commit -m "feat(java/a2ui): приём и нормализация разметки чужого агента"
```

---

## Фаза C — мультиагентность

Java знает ровно одного агента по `worker_url`. Go давно работает со списком
`agents:`, показывает его в селекторе браузера и умеет отдавать ответ
verbatim-агента без локальной модели. Фаза меняет и конфиг, и весь путь
делегирования.

### Task 6: Список агентов в конфиге

**Files:**
- Create: `java/common/src/main/java/io/github/kmpavloff/a2ademo/common/config/AuthConfig.java`
- Create: `java/common/src/main/java/io/github/kmpavloff/a2ademo/common/config/AgentConfig.java`
- Create: `java/common/src/main/java/io/github/kmpavloff/a2ademo/common/config/GoDuration.java`
- Modify: `java/common/src/main/java/io/github/kmpavloff/a2ademo/common/config/ConfigLoader.java:33-62`
- Test: `java/common/src/test/java/io/github/kmpavloff/a2ademo/common/config/AgentsConfigTest.java`

**Interfaces:**
- Produces: `AgentConfig` (record: `id, name, url, cardPath, skill, verbatim, timeout, description, auth`) с методами `timeoutDuration()`, `withUrl`, `withPassword`, `withId`, `withCardPath`, статикой `AgentConfig.envVar(String id, String field)` и `AgentConfig.validate(AgentConfig)`; `AuthConfig` (record: `type, username, password`) с `basic()`; `GoDuration.parse(String) -> Duration`; `ConfigLoader.OrchestratorConfig` получает поля `agents()` (`List<AgentConfig>`) и `agentsOverlayPath()`; `ConfigLoader.normalizeAgents(List<AgentConfig>) -> List<AgentConfig>`.

**Почему свой разбор длительности.** Конфиг общий с Go, а там таймаут записан
как `"180s"`. `Duration.parse` из JDK ждёт ISO-8601 (`PT180S`) и такую строку не
примет.

**Расхождение с текущим Java-поведением, принятое ради паритета.** Сейчас
`workerUrl` по умолчанию равен `http://localhost:8081`. В Go умолчания нет:
пустые и `agents:`, и `worker_url` — ошибка запуска. Переходим на поведение Go,
потому что иначе переменная `WORKER_URL` и список агентов начинают спорить друг с
другом. `configs/orchestrator.example.yaml` уже содержит активную запись
`orders`, так что рабочие конфиги не ломаются.

- [ ] **Step 1: Написать падающий тест**

Создать `java/common/src/test/java/io/github/kmpavloff/a2ademo/common/config/AgentsConfigTest.java`:

```java
package io.github.kmpavloff.a2ademo.common.config;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Список агентов в конфиге оркестратора — та же форма, что читает Go. */
class AgentsConfigTest {

    private static Path write(Path dir, String yaml) throws IOException {
        Path p = dir.resolve("orchestrator.yaml");
        Files.writeString(p, yaml);
        return p;
    }

    @Test
    void readsTheAgentsListWithDefaults(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                listen_addr: ":8080"
                public_url: "http://localhost:8080"
                agents:
                  - id: orders
                    name: "Агент заказов"
                    url: "http://localhost:8081"
                  - id: ouroboros
                    url: "http://192.168.1.68:18800"
                    card_path: "/.well-known/agent.json"
                    skill: "shop"
                    verbatim: true
                    timeout: "180s"
                    description: "Заказы интернет-магазина."
                    auth:
                      type: basic
                      username: ouroboros
                      password: test
                llm:
                  base_url: "http://localhost:1234/v1"
                """);

        List<AgentConfig> agents = ConfigLoader.loadOrchestrator(cfg.toString()).agents();

        assertEquals(2, agents.size());
        assertEquals("orders", agents.getFirst().id());
        assertEquals(AgentConfig.DEFAULT_CARD_PATH, agents.getFirst().cardPath(), "путь карточки по умолчанию");
        assertEquals(Duration.ofSeconds(120), agents.getFirst().timeoutDuration());
        assertEquals(Duration.ofSeconds(180), agents.get(1).timeoutDuration());
        assertTrue(agents.get(1).verbatim());
        assertEquals("ouroboros", agents.get(1).auth().username());
    }

    // Исторический одиночный агент: из worker_url синтезируется запись orders,
    // и только когда список agents пуст.
    @Test
    void fallsBackToWorkerUrlWhenTheListIsEmpty(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                worker_url: "http://localhost:8081"
                llm:
                  base_url: "http://localhost:1234/v1"
                """);

        List<AgentConfig> agents = ConfigLoader.loadOrchestrator(cfg.toString()).agents();
        assertEquals(1, agents.size());
        assertEquals("orders", agents.getFirst().id());
        assertEquals("http://localhost:8081", agents.getFirst().url());
    }

    @Test
    void refusesAConfigWithNoAgentAtAll(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                llm:
                  base_url: "http://localhost:1234/v1"
                """);
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> ConfigLoader.loadOrchestrator(cfg.toString()));
        assertTrue(e.getMessage().contains("at least one agent"), e.getMessage());
    }

    @Test
    void overlayPathDefaultsToTheGoLocation(@TempDir Path dir) throws IOException {
        Path cfg = write(dir, """
                worker_url: "http://localhost:8081"
                llm:
                  base_url: "http://localhost:1234/v1"
                """);
        assertEquals("configs/agents.local.yaml", ConfigLoader.loadOrchestrator(cfg.toString()).agentsOverlayPath());
    }

    @Test
    void rejectsABadId() {
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("Orders", "", "http://x", "", "", false, "", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "", "", "", false, "", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "http://x", "", "", false, "нет", "", AuthConfig.NONE)));
        assertThrows(IllegalArgumentException.class, () -> AgentConfig.validate(
                new AgentConfig("orders", "", "http://x", "", "", false, "", "", new AuthConfig("bearer", "u", "p"))));
    }

    @Test
    void envVarNameMatchesGo() {
        assertEquals("A2A_AGENT_ORDERS_URL", AgentConfig.envVar("orders", "URL"));
        assertEquals("A2A_AGENT_MY_AGENT_PASSWORD", AgentConfig.envVar("my-agent", "PASSWORD"));
    }

    @Test
    void parsesGoDurationSyntax() {
        assertEquals(Duration.ofSeconds(180), GoDuration.parse("180s"));
        assertEquals(Duration.ofMinutes(3), GoDuration.parse("3m"));
        assertEquals(Duration.ofSeconds(90), GoDuration.parse("1m30s"));
        assertEquals(Duration.ofMillis(1500), GoDuration.parse("1500ms"));
        assertThrows(IllegalArgumentException.class, () -> GoDuration.parse("180"));
        assertThrows(IllegalArgumentException.class, () -> GoDuration.parse("PT180S"));
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl common -Dtest=AgentsConfigTest test`
Expected: FAIL — типов `AgentConfig`, `AuthConfig`, `GoDuration` нет.

- [ ] **Step 3: Создать `GoDuration.java`**

```java
package io.github.kmpavloff.a2ademo.common.config;

import java.time.Duration;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Разбор длительности в синтаксисе Go ("180s", "1m30s"). Нужен потому, что
 * конфиг общий с Go-реализацией, а {@link Duration#parse} ждёт ISO-8601
 * ("PT180S") и такую строку не примет.
 */
public final class GoDuration {

    private GoDuration() {}

    private static final Pattern UNIT = Pattern.compile("(\\d+(?:\\.\\d+)?)(ns|us|µs|ms|s|m|h)");

    /** @throws IllegalArgumentException если строка пуста или разобрана не целиком */
    public static Duration parse(String s) {
        String in = s == null ? "" : s.trim();
        if (in.isEmpty()) {
            throw new IllegalArgumentException("empty duration");
        }
        boolean negative = in.startsWith("-");
        if (negative || in.startsWith("+")) {
            in = in.substring(1);
        }
        Matcher m = UNIT.matcher(in);
        long nanos = 0;
        int consumed = 0;
        while (m.find() && m.start() == consumed) {
            double value = Double.parseDouble(m.group(1));
            nanos += Math.round(value * unitNanos(m.group(2)));
            consumed = m.end();
        }
        if (consumed != in.length()) {
            throw new IllegalArgumentException("bad duration: " + s);
        }
        return Duration.ofNanos(negative ? -nanos : nanos);
    }

    private static long unitNanos(String unit) {
        return switch (unit) {
            case "ns" -> 1L;
            case "us", "µs" -> 1_000L;
            case "ms" -> 1_000_000L;
            case "s" -> 1_000_000_000L;
            case "m" -> 60L * 1_000_000_000L;
            case "h" -> 3_600L * 1_000_000_000L;
            default -> throw new IllegalArgumentException("unknown unit: " + unit);
        };
    }
}
```

- [ ] **Step 4: Создать `AuthConfig.java` и `AgentConfig.java`**

`AuthConfig.java`:

```java
package io.github.kmpavloff.a2ademo.common.config;

/**
 * Как аутентифицироваться у удалённого агента. Поддерживается только HTTP
 * Basic — единственная схема, встреченная у живых агентов этого демо.
 */
public record AuthConfig(String type, String username, String password) {

    public static final AuthConfig NONE = new AuthConfig("", "", "");

    public AuthConfig {
        type = type == null ? "" : type;
        username = username == null ? "" : username;
        password = password == null ? "" : password;
    }

    /** Нужно ли подставлять заголовок Basic на каждый запрос. */
    public boolean basic() {
        return type.equalsIgnoreCase("basic") && !username.isEmpty();
    }

    public AuthConfig withPassword(String p) {
        return new AuthConfig(type, username, p);
    }
}
```

`AgentConfig.java`:

```java
package io.github.kmpavloff.a2ademo.common.config;

import java.time.Duration;
import java.util.regex.Pattern;

/**
 * Один удалённый A2A-агент, с которым умеет говорить оркестратор (порт
 * config.AgentConfig).
 *
 * <p>Запись состоит только из строк, bool и такой же записи {@link AuthConfig},
 * поэтому сравнивается обычным {@code equals} — на этом держится пересборка
 * реестра при правке конфига.
 *
 * @param name    подпись в селекторе UI; пусто → имя из AgentCard
 * @param url     вытесняет адрес, объявленный в самой карточке
 * @param cardPath путь к AgentCard: не все агенты кладут её в канонический
 *                 {@value #DEFAULT_CARD_PATH}
 * @param skill   уезжает в metadata.skill каждого сообщения — так внешний агент
 *                понимает, какой набор инструментов включать
 * @param verbatim при явном выборе этого агента в UI его ответ уходит в браузер
 *                 без локальной LLM — ни пересказа, ни лишней задержки
 * @param description вытесняет вывод из AgentCard: у агента может быть сотня
 *                    навыков, и промпт локальной модели такого не переживёт
 */
public record AgentConfig(String id, String name, String url, String cardPath, String skill,
                          boolean verbatim, String timeout, String description, AuthConfig auth) {

    public static final String DEFAULT_CARD_PATH = "/.well-known/agent-card.json";
    public static final Duration DEFAULT_TIMEOUT = Duration.ofSeconds(120);

    private static final Pattern ID_RE = Pattern.compile("^[a-z][a-z0-9_-]*$");

    public AgentConfig {
        id = id == null ? "" : id;
        name = name == null ? "" : name;
        url = url == null ? "" : url;
        cardPath = cardPath == null ? "" : cardPath;
        skill = skill == null ? "" : skill;
        timeout = timeout == null ? "" : timeout;
        description = description == null ? "" : description;
        auth = auth == null ? AuthConfig.NONE : auth;
    }

    /** Таймаут SendMessage с подстановкой умолчания. */
    public Duration timeoutDuration() {
        if (timeout.isEmpty()) {
            return DEFAULT_TIMEOUT;
        }
        try {
            Duration d = GoDuration.parse(timeout);
            return d.isZero() || d.isNegative() ? DEFAULT_TIMEOUT : d;
        } catch (IllegalArgumentException e) {
            return DEFAULT_TIMEOUT;
        }
    }

    /**
     * Имя переменной окружения для поля агента: пароль незачем держать в файле,
     * а адрес приходится подменять при запуске в контейнере.
     *
     * @param field "URL" или "PASSWORD"
     */
    public static String envVar(String id, String field) {
        return "A2A_AGENT_" + id.toUpperCase().replace('-', '_') + "_" + field;
    }

    /**
     * Проверяет одну запись — то же, что делает загрузка конфига, но без
     * env-перекрытий и без проверки на дубликаты. Отдельно нужна затем, что
     * запись из UI проверяется до того, как попадёт в список.
     */
    public static void validate(AgentConfig a) {
        if (!ID_RE.matcher(a.id()).matches()) {
            throw new IllegalArgumentException("agent id \"" + a.id() + "\" must match " + ID_RE.pattern());
        }
        if (a.url().isEmpty()) {
            throw new IllegalArgumentException("agent \"" + a.id() + "\": url is required");
        }
        if (!a.timeout().isEmpty()) {
            Duration d;
            try {
                d = GoDuration.parse(a.timeout());
            } catch (IllegalArgumentException e) {
                throw new IllegalArgumentException("agent \"" + a.id() + "\": bad timeout \"" + a.timeout() + "\"");
            }
            if (d.isZero() || d.isNegative()) {
                throw new IllegalArgumentException("agent \"" + a.id() + "\": bad timeout \"" + a.timeout() + "\"");
            }
        }
        String type = a.auth().type();
        if (!type.isEmpty() && !type.equalsIgnoreCase("basic")) {
            throw new IllegalArgumentException("agent \"" + a.id() + "\": unsupported auth type \"" + type + "\"");
        }
    }

    public AgentConfig withId(String v) {
        return new AgentConfig(v, name, url, cardPath, skill, verbatim, timeout, description, auth);
    }

    public AgentConfig withUrl(String v) {
        return new AgentConfig(id, name, v, cardPath, skill, verbatim, timeout, description, auth);
    }

    public AgentConfig withCardPath(String v) {
        return new AgentConfig(id, name, url, v, skill, verbatim, timeout, description, auth);
    }

    public AgentConfig withPassword(String v) {
        return new AgentConfig(id, name, url, cardPath, skill, verbatim, timeout, description, auth.withPassword(v));
    }
}
```

- [ ] **Step 5: Научить `ConfigLoader` читать список**

В `ConfigLoader` заменить запись `OrchestratorConfig` (строки 33–38) на:

```java
    /** listenAddr/publicUrl используются только в режиме --web (A2A-сервер + фронтенд). */
    public record OrchestratorConfig(String listenAddr, String publicUrl, String workerUrl,
                                     List<AgentConfig> agents, String agentsOverlayPath,
                                     String a2aLogPath, LlmConfig llm) {
        public int port() {
            return parsePort(listenAddr);
        }
    }
```

Заменить `loadOrchestrator` (строки 53–62) на:

```java
    public static OrchestratorConfig loadOrchestrator(String path) {
        Map<String, Object> y = readYaml(path);
        String listenAddr = env("ORCHESTRATOR_LISTEN_ADDR", str(y, "listen_addr", ":8080"));
        String publicUrl = env("ORCHESTRATOR_PUBLIC_URL", str(y, "public_url", "http://localhost:8080"));
        // Умолчания у worker_url нет намеренно, как и в Go: иначе переменная
        // WORKER_URL и список agents молча спорили бы друг с другом.
        String workerUrl = env("WORKER_URL", str(y, "worker_url", ""));
        String logPath = env("A2A_LOG_PATH", str(y, "a2a_log_path", "a2a-orchestrator.log"));
        String overlayPath = env("A2A_AGENTS_OVERLAY_PATH",
                str(y, "agents_overlay_path", "configs/agents.local.yaml"));
        LlmConfig llm = llm(y);
        require(llm.baseUrl(), "orchestrator config: llm.base_url is required (yaml or LLM_BASE_URL)");

        List<AgentConfig> agents = agents(y);
        // Совместимость: одиночный worker_url становится единственным агентом.
        if (agents.isEmpty() && !workerUrl.isBlank()) {
            agents = List.of(new AgentConfig("orders", "Агент заказов", workerUrl,
                    "", "", false, "", "", AuthConfig.NONE));
        }
        if (agents.isEmpty()) {
            throw new IllegalStateException(
                    "orchestrator config: at least one agent (agents: or worker_url:) is required");
        }
        return new OrchestratorConfig(listenAddr, publicUrl, workerUrl,
                normalizeAgents(agents), overlayPath, logPath, llm);
    }

    /** Записи agents: из YAML, без умолчаний и проверок — их делает normalizeAgents. */
    @SuppressWarnings("unchecked")
    private static List<AgentConfig> agents(Map<String, Object> y) {
        if (!(y.get("agents") instanceof List<?> list)) {
            return List.of();
        }
        List<AgentConfig> out = new ArrayList<>(list.size());
        for (Object item : list) {
            if (!(item instanceof Map<?, ?> raw)) {
                continue;
            }
            Map<String, Object> a = (Map<String, Object>) raw;
            Map<String, Object> auth = section(a, "auth");
            out.add(new AgentConfig(
                    str(a, "id", ""), str(a, "name", ""), str(a, "url", ""),
                    str(a, "card_path", ""), str(a, "skill", ""),
                    Boolean.TRUE.equals(a.get("verbatim")),
                    str(a, "timeout", ""), str(a, "description", ""),
                    new AuthConfig(str(auth, "type", ""), str(auth, "username", ""), str(auth, "password", ""))));
        }
        return out;
    }

    /**
     * Подставляет умолчания и env-перекрытия, затем валидирует список.
     * Применяется и к списку из YAML, и к слитому с overlay — поэтому env
     * остаётся последним словом в обоих случаях.
     */
    public static List<AgentConfig> normalizeAgents(List<AgentConfig> agents) {
        Set<String> seen = new HashSet<>(agents.size());
        List<AgentConfig> out = new ArrayList<>(agents.size());
        for (AgentConfig a : agents) {
            // Адрес перекрывается окружением: в контейнере агент живёт по
            // другому имени, чем на машине разработчика, а конфиг один и тот же.
            String urlEnv = System.getenv(AgentConfig.envVar(a.id(), "URL"));
            if (urlEnv != null && !urlEnv.isBlank()) {
                a = a.withUrl(urlEnv);
            }
            AgentConfig.validate(a);
            if (!seen.add(a.id())) {
                throw new IllegalStateException("orchestrator config: duplicate agent id \"" + a.id() + "\"");
            }
            if (a.cardPath().isEmpty()) {
                a = a.withCardPath(AgentConfig.DEFAULT_CARD_PATH);
            }
            String passEnv = System.getenv(AgentConfig.envVar(a.id(), "PASSWORD"));
            if (passEnv != null && !passEnv.isBlank()) {
                a = a.withPassword(passEnv);
            }
            out.add(a);
        }
        return List.copyOf(out);
    }
```

Дописать импорты `java.util.ArrayList`, `java.util.HashSet`, `java.util.List`, `java.util.Set`.

- [ ] **Step 6: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl common -Dtest=AgentsConfigTest test`
Expected: PASS

- [ ] **Step 7: Прогнать сборку целиком и починить вызовы `workerUrl()`**

Run: `cd java && mvn -o test`
Expected: FAIL при компиляции — `OrchestratorApplication` строит клиента по
`cfg.workerUrl()`. Временно заменить на `cfg.agents().getFirst().url()`, чтобы
модуль собирался; окончательная разводка — в задаче 12.

- [ ] **Step 8: Прогнать ещё раз**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 9: Коммит**

```bash
git add java/common/src/main/java/io/github/kmpavloff/a2ademo/common/config/ \
        java/common/src/test/java/io/github/kmpavloff/a2ademo/common/config/AgentsConfigTest.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/OrchestratorApplication.java
git commit -m "feat(java/config): список агентов, env-перекрытия и путь overlay-файла"
```

---

### Task 7: История разговора переезжает из агента в общее хранилище

Набор инструментов теперь меняется при переключении агента в селекторе, а история
привязана к contextId и переезжать вместе с ним не должна. В Go это общая
`session.InMemoryService()` на все runner'ы.

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/agent/SessionStore.java`
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/agent/OrchestratorAgent.java:30-70`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/agent/OrchestratorAgentToolsTest.java`

**Interfaces:**
- Consumes: `OrdersClient`, `ChatModel`.
- Produces: `SessionStore.history(String sessionId) -> List<ChatMessage>`; конструктор `OrchestratorAgent(ChatModel model, List<OrdersClient> tools, String summary, SessionStore sessions)`; `OrchestratorAgent.buildInstruction(String toolNames, String summary) -> String`.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/agent/OrchestratorAgentToolsTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.agent;

import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;
import io.github.kmpavloff.a2ademo.common.llm.ChatModel;
import io.github.kmpavloff.a2ademo.common.llm.ToolSpec;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Промпт и история при нескольких делегирующих инструментах. */
class OrchestratorAgentToolsTest {

    /** Модель, которая ничего не вызывает и запоминает последний запрос. */
    static class RecordingModel implements ChatModel {
        List<ChatMessage> lastRequest;
        List<ToolSpec> lastTools;

        @Override
        public Completion complete(List<ChatMessage> messages, List<ToolSpec> tools) {
            lastRequest = new ArrayList<>(messages);
            lastTools = tools;
            return new Completion("готово", null);
        }
    }

    @Test
    void listsEveryToolNameInThePrompt() {
        String prompt = OrchestratorAgent.buildInstruction("ask_orders, ask_ouroboros", "Агент умеет…");
        assertTrue(prompt.contains("инструментам: ask_orders, ask_ouroboros"), prompt);
        assertTrue(prompt.contains("Если инструментов несколько"),
                "правило выбора между инструментами обязано быть в промпте");
        assertTrue(prompt.contains("Агент умеет…"));
    }

    // История принадлежит сессии, а не агенту: при переключении агента в
    // селекторе собирается новый агент, а разговор продолжается тот же.
    @Test
    void historySurvivesRebuildingTheAgent() {
        SessionStore sessions = new SessionStore();
        RecordingModel model = new RecordingModel();

        new OrchestratorAgent(model, List.of(), "", sessions).runTurn("s1", "первый вопрос", new OrchestratorAgent.TurnListener() {});
        new OrchestratorAgent(model, List.of(), "", sessions).runTurn("s1", "второй вопрос", new OrchestratorAgent.TurnListener() {});

        List<String> texts = model.lastRequest.stream().map(ChatMessage::content).toList();
        assertTrue(texts.contains("первый вопрос"), "первый ход обязан остаться в истории: " + texts);
        assertTrue(texts.contains("второй вопрос"));
    }

    @Test
    void exposesOneToolSpecPerAgent() {
        RecordingModel model = new RecordingModel();
        new OrchestratorAgent(model, List.of(), "", new SessionStore())
                .runTurn("s1", "вопрос", new OrchestratorAgent.TurnListener() {});
        assertEquals(List.of(), model.lastTools, "без агентов инструментов нет");
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=OrchestratorAgentToolsTest test`
Expected: FAIL — `SessionStore` не существует, у `OrchestratorAgent` другой конструктор.

- [ ] **Step 3: Создать `SessionStore.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.agent;

import io.github.kmpavloff.a2ademo.common.llm.ChatMessage;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * История разговоров, общая для всех сборок агента.
 *
 * <p>Набор инструментов меняется при переключении агента в селекторе, и агент
 * из-за этого пересобирается; история же привязана к contextId разговора и
 * переезжать вместе с выбором не должна. В Go ту же роль играет одна
 * {@code session.InMemoryService()} на все runner'ы.
 */
public class SessionStore {

    private final Map<String, List<ChatMessage>> sessions = new ConcurrentHashMap<>();

    /** Изменяемая история сессии; создаётся при первом обращении. */
    public List<ChatMessage> history(String sessionId) {
        return sessions.computeIfAbsent(sessionId, k -> new ArrayList<>());
    }
}
```

- [ ] **Step 4: Перевести `OrchestratorAgent` на список инструментов**

Заменить блок правил в `INSTRUCTION_TEMPLATE` так, чтобы он совпадал с
`internal/agent/orchestrator.go`: первая строка — «делегируя её инструментам:
%1$s.», и сразу после первого правила добавлена строка

```
            - Если инструментов несколько, выбирайте тот, чей агент умеет нужное; не вызывайте два инструмента подряд с одним и тем же запросом.
```

Заменить поля и конструктор (строки 47–70) на:

```java
    private final ChatModel model;
    private final Map<String, OrdersClient> byTool = new LinkedHashMap<>();
    private final List<ToolSpec> toolSpecs = new ArrayList<>();
    private final String instruction;
    private final SessionStore sessions;

    /**
     * @param tools   делегирующие клиенты выбранных агентов: в режиме «Авто» их
     *                несколько, при явном выборе — ровно один
     * @param summary блок возможностей для промпта, собранный из карточек
     */
    public OrchestratorAgent(ChatModel model, List<OrdersClient> tools, String summary, SessionStore sessions) {
        this.model = model;
        this.sessions = sessions;
        List<String> names = new ArrayList<>(tools.size());
        for (OrdersClient c : tools) {
            String name = c.profile().toolName();
            byTool.put(name, c);
            names.add(name);
            toolSpecs.add(new ToolSpec(name, c.profile().toolDesc(), Map.of(
                    "type", "object",
                    "properties", Map.of("message", Map.of(
                            "type", "string",
                            "description", "Что спросить или сообщить удалённому агенту")),
                    "required", List.of("message"))));
        }
        this.instruction = buildInstruction(String.join(", ", names), summary);
    }

    /** Промпт под конкретный набор инструментов и блок возможностей. */
    public static String buildInstruction(String toolNames, String summary) {
        return String.format(INSTRUCTION_TEMPLATE, toolNames, summary);
    }
```

В `runTurn` заменить первую строку на `List<ChatMessage> history = sessions.history(sessionId);`,
подстановку `List.of(askTool)` — на `toolSpecs`, а выбор клиента — на поиск по
имени вызванного инструмента:

```java
            OrdersClient target = byTool.get(call.name());
            if (target == null) {
                // Модель выдумала имя инструмента. Отвечаем ей текстом, а не
                // молчанием: иначе она повторит вызов до упора в лимит.
                String reply = "Инструмента " + call.name() + " нет. Доступные: " + String.join(", ", byTool.keySet());
                history.add(ChatMessage.assistantToolCall(call));
                history.add(ChatMessage.tool(call.id(), reply));
                listener.onToolResult(call.name());
                continue;
            }
```

и далее использовать `target` вместо поля `orders`.

- [ ] **Step 5: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=OrchestratorAgentToolsTest test`
Expected: PASS

- [ ] **Step 6: Починить вызовы конструктора и прогнать модуль**

`OrchestratorApplication`, `Repl`, `WebE2eTest` строят `new OrchestratorAgent(model, orders)`.
Заменить на `new OrchestratorAgent(model, List.of(orders), orders.profile().summary(), new SessionStore())`.

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 7: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/agent/ \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/agent/OrchestratorAgentToolsTest.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/OrchestratorApplication.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/WebE2eTest.java
git commit -m "refactor(java/agent): несколько делегирующих инструментов и общая история сессий"
```

---

### Task 8: Трейс — дамп протокола и маскирование номера карты

Идёт раньше остальных задач фазы: на `dump` опирается транспорт из задачи 9.

**Files:**
- Modify: `java/common/src/main/java/io/github/kmpavloff/a2ademo/common/trace/Tracer.java`
- Test: `java/common/src/test/java/io/github/kmpavloff/a2ademo/common/trace/TracerDumpTest.java`

**Interfaces:**
- Produces: `Tracer.maskCardLike(String) -> String`, `Tracer.debug() -> boolean`, `Tracer.dump(String label, String body)`, константа `Tracer.DEBUG_ENV_VAR`, пакетный конструктор `Tracer(String prefix, boolean debug, Object... sinks)` для тестов.

- [ ] **Step 1: Написать падающий тест**

Создать `java/common/src/test/java/io/github/kmpavloff/a2ademo/common/trace/TracerDumpTest.java`:

```java
package io.github.kmpavloff.a2ademo.common.trace;

import org.junit.jupiter.api.Test;

import java.io.StringWriter;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Полный дамп тел A2A-обмена по A2A_DEBUG. Через форму возврата уезжает
 * настоящий номер карты, поэтому ни трейс, ни дамп не должны превращаться в
 * утечку.
 */
class TracerDumpTest {

    @Test
    void masksCardLikeDigitRuns() {
        assertEquals("оплата «номер карты скрыт» принята",
                Tracer.maskCardLike("оплата 4111 1111 1111 1111 принята"));
        assertEquals("«номер карты скрыт»", Tracer.maskCardLike("4111-1111-1111-1111"));
        assertEquals("заказ 1041", Tracer.maskCardLike("заказ 1041"), "короткие числа не трогаем");
    }

    @Test
    void dumpStaysSilentWithoutDebug() {
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", false, out).dump("запрос", "{\"a\":1}");
        assertEquals("", out.toString());
    }

    @Test
    void dumpPrettyPrintsAndMasks() {
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", true, out).dump("запрос агенту", "{\"card\":\"4111111111111111\"}");
        String s = out.toString();
        assertTrue(s.contains("⇄ запрос агенту:"), s);
        assertTrue(s.contains("«номер карты скрыт»"), s);
        assertTrue(!s.contains("4111111111111111"), "номер карты не должен попасть в лог");
        assertTrue(s.contains("\n"), "тело печатается с отступами: " + s);
    }

    @Test
    void dumpPrintsUnparseableBodiesAsIs() {
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", true, out).dump("ответ", "не JSON");
        assertTrue(out.toString().contains("не JSON"));
    }

    @Test
    void dumpTruncatesHugeBodies() {
        StringWriter out = new StringWriter();
        new Tracer("[A2A] ", true, out).dump("ответ", "x".repeat(200_000));
        assertTrue(out.toString().contains("… обрезано"), "длинное тело обязано обрезаться");
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl common -Dtest=TracerDumpTest test`
Expected: FAIL — методов `maskCardLike`/`dump` и конструктора с флагом нет.

- [ ] **Step 3: Дописать `Tracer.java`**

Добавить импорты `com.fasterxml.jackson.databind.ObjectMapper`, `java.util.regex.Pattern`
и вставить в класс:

```java
    /**
     * Включает дамп полных тел A2A-обмена. Отдельная переменная, а не поле
     * конфига: это инструмент разбирательства, который включают на один прогон.
     */
    public static final String DEBUG_ENV_VAR = "A2A_DEBUG";

    /**
     * Предел на одну запись. Разметка A2UI бывает под два десятка килобайт, и
     * без предела один ход способен утопить весь лог.
     */
    private static final int MAX_DUMP_BYTES = 64 * 1024;

    /**
     * Последовательность из 13–19 цифр, возможно разбитая пробелами или
     * дефисами. Маскируется везде, где текст попадает в лог.
     */
    private static final Pattern CARD_LIKE = Pattern.compile("\\b(?:\\d[ -]?){13,19}\\b");

    private static final String CARD_MASK = "«номер карты скрыт»";

    private static final ObjectMapper DUMP_MAPPER = new ObjectMapper();

    private final boolean debug;

    /** Прячет номера карт в произвольном тексте. */
    public static String maskCardLike(String s) {
        return s == null ? "" : CARD_LIKE.matcher(s).replaceAll(CARD_MASK);
    }

    /** Читает A2A_DEBUG. Пустое и «0»/«false» — выключено; любое иное значение включает. */
    private static boolean debugEnabled() {
        String v = System.getenv(DEBUG_ENV_VAR);
        if (v == null || v.isBlank()) {
            return false;
        }
        return !v.equalsIgnoreCase("0") && !v.equalsIgnoreCase("false");
    }

    /** Включён ли дамп тел. Нужен вызывающему, чтобы не собирать дорогой JSON впустую. */
    public boolean debug() {
        return debug;
    }

    /**
     * Печатает тело A2A-обмена целиком: label — что это и в какую сторону.
     * Молчит, когда debug выключен.
     *
     * <p>JSON переформатируется с отступами: читать однострочный ответ с
     * разметкой A2UI невозможно, а ради этого дамп и включают. Неразбираемое
     * тело печатается как есть — в разбирательстве важнее увидеть мусор, чем не
     * увидеть ничего.
     */
    public void dump(String label, String body) {
        if (!debug || body == null || body.isEmpty()) {
            return;
        }
        String out = body;
        try {
            out = DUMP_MAPPER.writerWithDefaultPrettyPrinter()
                    .writeValueAsString(DUMP_MAPPER.readTree(body));
        } catch (Exception ignored) {
            // тело не JSON — печатаем как есть
        }
        out = maskCardLike(out);
        String suffix = "";
        if (out.length() > MAX_DUMP_BYTES) {
            out = out.substring(0, MAX_DUMP_BYTES);
            suffix = System.lineSeparator() + "    … обрезано";
        }
        logf("⇄ %s:%n    %s%s", label, out.replace("\n", System.lineSeparator() + "    "), suffix);
    }
```

Заменить конструкторы на пару — публичный (читает окружение) и пакетный (флаг
задаётся явно, чтобы тест не зависел от переменных окружения):

```java
    public Tracer(String prefix, Object... sinks) {
        this(prefix, debugEnabled(), sinks);
    }

    Tracer(String prefix, boolean debug, Object... sinks) {
        this.prefix = prefix;
        this.debug = debug;
        for (Object s : sinks) {
            if (s != null) {
                this.sinks.add(s);
            }
        }
    }
```

Поле `prefix` перестаёт быть `final`? Нет — оба конструктора присваивают его
ровно один раз, публичный делегирует пакетному, так что `final` сохраняется.

- [ ] **Step 4: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl common -Dtest=TracerDumpTest test`
Expected: PASS

- [ ] **Step 5: Прогнать модуль и закоммитить**

Run: `cd java && mvn -o test`
Expected: PASS

```bash
git add java/common/src/main/java/io/github/kmpavloff/a2ademo/common/trace/Tracer.java \
        java/common/src/test/java/io/github/kmpavloff/a2ademo/common/trace/TracerDumpTest.java
git commit -m "feat(java/trace): дамп протокола по A2A_DEBUG с маскированием номера карты"
```

---

### Task 9: Транспорт — Basic-auth, свой путь карточки, GetTask, чужие ответы

**Files:**
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/A2aClient.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/A2aClientTest.java`

**Interfaces:**
- Consumes: `AgentConfig`, `AuthConfig`, `Tracer.dump`/`debug`.
- Produces: `A2aClient.resolve(AgentConfig cfg, Tracer trace) -> Resolved`, `A2aClient.resolve(String baseUrl) -> Resolved` (прежняя, для тестов), `A2aClient.sendMessage(A2aMessage msg, List<String> extensions) -> SendResult`, `A2aClient.getTask(String taskId) -> A2aTask`, `A2aClient.mergeEndpoint(String base, String declared) -> String`, `A2aClient.wrapBareResult(JsonNode) -> JsonNode`.

**Что портировать НЕ нужно.** Go пришлось писать снисходительный разбор карточки
(`tolerantCardParser`), потому что `a2a-go` падает на нераспознанном
`securitySchemes`. `Json.MAPPER` в этом проекте настроен с
`FAIL_ON_UNKNOWN_PROPERTIES=false`, поэтому такие блоки и так игнорируются.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/A2aClientTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpServer;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Транспорт до удалённого агента: авторизация, свой путь карточки, чужие ответы. */
class A2aClientTest {

    HttpServer server;
    String base;
    final List<String> seenAuth = new ArrayList<>();
    final List<String> seenExtensions = new ArrayList<>();
    String sendResult = "{\"task\":{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}";

    @BeforeEach
    void setUp() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        base = "http://127.0.0.1:" + server.getAddress().getPort();
        // Карточка лежит НЕ по каноническому пути и объявляет нерабочий 0.0.0.0.
        server.createContext("/.well-known/agent.json", ex -> respond(ex, """
                {"name":"ouroboros","description":"Магазин.","version":"1.0","capabilities":{},
                 "securitySchemes":{"basic":{"type":"http","scheme":"basic"}},
                 "supportedInterfaces":[{"url":"http://0.0.0.0:18800/invoke","protocolBinding":"JSONRPC"}],
                 "skills":[]}
                """));
        server.createContext("/invoke", ex -> {
            seenAuth.add(String.valueOf(ex.getRequestHeaders().getFirst("Authorization")));
            seenExtensions.add(String.valueOf(ex.getRequestHeaders().getFirst("A2A-Extensions")));
            JsonNode req = Json.MAPPER.readTree(ex.getRequestBody());
            respond(ex, "{\"jsonrpc\":\"2.0\",\"id\":" + req.path("id") + ",\"result\":" + sendResult + "}");
        });
        server.start();
    }

    @AfterEach
    void tearDown() {
        server.stop(0);
    }

    private static void respond(com.sun.net.httpserver.HttpExchange ex, String body) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().add("Content-Type", "application/json");
        ex.sendResponseHeaders(200, b.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(b);
        }
    }

    private A2aClient client() {
        AgentConfig cfg = new AgentConfig("ouroboros", "", base, "/.well-known/agent.json",
                "shop", true, "30s", "Магазин.", new AuthConfig("basic", "u", "p"));
        return A2aClient.resolve(cfg, Tracer.noop()).client();
    }

    @Test
    void resolvesACardAtANonCanonicalPathAndSurvivesSecuritySchemes() {
        assertNotNull(client());
    }

    // Хост из карточки бывает нерабочим (живой агент объявляет 0.0.0.0), поэтому
    // схему и хост берём из конфига — по нему мы карточку и получили.
    @Test
    void mergesTheDeclaredPathOntoTheConfiguredHost() {
        assertEquals("http://127.0.0.1:9000/invoke",
                A2aClient.mergeEndpoint("http://127.0.0.1:9000", "http://0.0.0.0:18800/invoke"));
        assertEquals("http://127.0.0.1:9000",
                A2aClient.mergeEndpoint("http://127.0.0.1:9000", "http://0.0.0.0:18800/"));
    }

    @Test
    void sendsBasicAuthAndTheExtensionHeader() {
        client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), List.of("https://a2ui.org/x"));
        assertEquals("Basic " + java.util.Base64.getEncoder().encodeToString("u:p".getBytes(StandardCharsets.UTF_8)),
                seenAuth.getFirst());
        assertEquals("https://a2ui.org/x", seenExtensions.getFirst());
    }

    // Агент кладёт объект задачи в result напрямую, без oneof-обёртки A2A 1.0.
    @Test
    void wrapsABareTaskResult() {
        sendResult = "{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}";
        A2aClient.SendResult res = client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), null);
        assertNotNull(res.task());
        assertEquals("t1", res.task().id);
    }

    @Test
    void wrapsABareMessageResult() {
        sendResult = "{\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"готово\"}]}";
        A2aClient.SendResult res = client().sendMessage(A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет")), null);
        assertNotNull(res.message());
        assertEquals("готово", res.message().firstText());
    }

    @Test
    void leavesAProperlyWrappedResultAlone() {
        JsonNode wrapped = Json.MAPPER.createObjectNode().set("task", Json.MAPPER.createObjectNode());
        assertTrue(A2aClient.wrapBareResult(wrapped).has("task"));
    }

    @Test
    void pollsGetTask() {
        sendResult = "{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}";
        assertEquals("t1", client().getTask("t1").id);
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=A2aClientTest test`
Expected: FAIL — у `A2aClient` нет ни `resolve(AgentConfig, Tracer)`, ни `getTask`, ни `mergeEndpoint`.

- [ ] **Step 3: Переписать `A2aClient.java`**

Заменить поля, конструктор и `resolve` на:

```java
    private final HttpClient http;
    private final AtomicLong nextId = new AtomicLong(1);
    private final String invokeUrl;
    private final Duration timeout;
    private final AuthConfig auth;
    private final Tracer trace;

    private A2aClient(String invokeUrl, Duration timeout, AuthConfig auth, Tracer trace) {
        this.invokeUrl = invokeUrl;
        this.timeout = timeout;
        this.auth = auth;
        this.trace = trace;
        this.http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();
    }

    /** Прежняя форма без конфига: анонимный агент по каноническому пути карточки. */
    public static Resolved resolve(String baseUrl) {
        return resolve(new AgentConfig("agent", "", baseUrl, "", "", false, "", "", AuthConfig.NONE), Tracer.noop());
    }

    /** Читает карточку по адресу и пути из конфига и строит клиента её JSONRPC-интерфейса. */
    public static Resolved resolve(AgentConfig cfg, Tracer trace) {
        String base = cfg.url().endsWith("/") ? cfg.url().substring(0, cfg.url().length() - 1) : cfg.url();
        String cardPath = cfg.cardPath().isEmpty() ? AgentConfig.DEFAULT_CARD_PATH : cfg.cardPath();
        AgentCard card;
        try {
            HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();
            HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(base + cardPath))
                    .timeout(Duration.ofSeconds(15))
                    .GET();
            authorize(b, cfg.auth());
            HttpResponse<String> resp = http.send(b.build(), HttpResponse.BodyHandlers.ofString());
            if (resp.statusCode() / 100 != 2) {
                throw new A2aException("agent card HTTP " + resp.statusCode() + " at " + base + cardPath);
            }
            trace.dump("карточка агента " + cfg.id(), resp.body());
            // Нераспознанные блоки (securitySchemes и прочее) Jackson молча
            // пропускает: аутентификацию мы ставим сами из конфига.
            card = Json.MAPPER.readValue(resp.body(), AgentCard.class);
        } catch (IOException | InterruptedException e) {
            if (e instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw new A2aException("resolve agent card at " + base + cardPath + ": " + e.getMessage(), e);
        }

        String url = null;
        if (card.supportedInterfaces != null) {
            for (AgentCard.AgentInterface i : card.supportedInterfaces) {
                if (AgentCard.TRANSPORT_JSONRPC.equals(i.protocolBinding)) {
                    url = mergeEndpoint(base, i.url);
                    break;
                }
            }
        }
        if (url == null) {
            // Карточка может не объявлять интерфейсов вовсе — тогда работаем по
            // адресу из конфига, как это делает Go.
            url = base;
        }
        return new Resolved(card, new A2aClient(url, cfg.timeoutDuration(), cfg.auth(), trace));
    }

    /**
     * Рабочий адрес транспорта: схема и хост из конфига (по нему карточка и была
     * получена), путь из карточки. Так и нерабочий 0.0.0.0 внутри чужой карточки
     * не мешает, и объявленный агентом путь не теряется.
     */
    static String mergeEndpoint(String base, String declared) {
        try {
            URI b = URI.create(base);
            URI d = URI.create(declared);
            String path = d.getPath();
            if (path == null || path.isEmpty() || path.equals("/")) {
                return base;
            }
            String basePath = b.getPath() == null ? "" : b.getPath();
            if (basePath.endsWith("/")) {
                basePath = basePath.substring(0, basePath.length() - 1);
            }
            return new URI(b.getScheme(), b.getAuthority(),
                    basePath + (path.startsWith("/") ? path : "/" + path), null, null).toString();
        } catch (Exception e) {
            return declared;
        }
    }

    /** Заголовок Basic на каждый запрос; встроенной поддержки схемы у HttpClient для нас мало. */
    private static void authorize(HttpRequest.Builder b, AuthConfig auth) {
        if (!auth.basic()) {
            return;
        }
        String token = java.util.Base64.getEncoder()
                .encodeToString((auth.username() + ":" + auth.password()).getBytes(StandardCharsets.UTF_8));
        b.header("Authorization", "Basic " + token);
    }
```

Заменить `sendMessage` и `call` на:

```java
    public SendResult sendMessage(A2aMessage message, List<String> extensions) {
        ObjectNode params = Json.MAPPER.createObjectNode();
        params.set("message", Json.MAPPER.valueToTree(message));

        JsonRpc.Request rpc = new JsonRpc.Request();
        rpc.id = Json.MAPPER.getNodeFactory().numberNode(nextId.getAndIncrement());
        rpc.method = JsonRpc.METHOD_SEND_MESSAGE;
        rpc.params = params;

        JsonNode result = wrapBareResult(call(rpc, extensions));
        try {
            if (result.has("task")) {
                return new SendResult(Json.MAPPER.treeToValue(result.get("task"), A2aTask.class), null);
            }
            if (result.has("message")) {
                return new SendResult(null, Json.MAPPER.treeToValue(result.get("message"), A2aMessage.class));
            }
        } catch (IOException e) {
            throw new A2aException("decode SendMessage result: " + e.getMessage(), e);
        }
        throw new A2aException("unexpected SendMessage result keys: " + result);
    }

    /** Опрос задачи, пока она в работе: контракт внешнего агента предписывает поллинг. */
    public A2aTask getTask(String taskId) {
        ObjectNode params = Json.MAPPER.createObjectNode();
        params.put("id", taskId);
        JsonRpc.Request rpc = new JsonRpc.Request();
        rpc.id = Json.MAPPER.getNodeFactory().numberNode(nextId.getAndIncrement());
        rpc.method = JsonRpc.METHOD_GET_TASK;
        rpc.params = params;
        JsonNode result = wrapBareResult(call(rpc, null));
        try {
            return Json.MAPPER.treeToValue(result.has("task") ? result.get("task") : result, A2aTask.class);
        } catch (IOException e) {
            throw new A2aException("decode GetTask result: " + e.getMessage(), e);
        }
    }

    /**
     * Чинит ответ агента, который отдаёт результат не по A2A 1.0: спека ждёт
     * oneof-обёртку {@code {"task": …}} либо {@code {"message": …}}, а агент
     * кладёт объект напрямую. Корректный ответ проходит насквозь.
     */
    static JsonNode wrapBareResult(JsonNode result) {
        if (result == null || !result.isObject()) {
            return result;
        }
        for (String k : List.of("task", "message", "statusUpdate", "artifactUpdate")) {
            if (result.has(k)) {
                return result;
            }
        }
        String key = null;
        if (result.has("status") || result.has("artifacts")) {
            key = "task";
        } else if (result.has("parts") || result.has("role")) {
            key = "message";
        }
        if (key == null) {
            return result;
        }
        return Json.MAPPER.createObjectNode().set(key, result);
    }

    private JsonNode call(JsonRpc.Request rpc, List<String> extensions) {
        HttpResponse<String> resp;
        String body;
        try {
            body = Json.MAPPER.writeValueAsString(rpc);
            HttpRequest.Builder b = HttpRequest.newBuilder(URI.create(invokeUrl))
                    .header("Content-Type", "application/json")
                    .timeout(timeout)
                    .POST(HttpRequest.BodyPublishers.ofString(body));
            authorize(b, auth);
            if (extensions != null && !extensions.isEmpty()) {
                String value = String.join(", ", extensions);
                // Под двумя именами: A2A 1.0 зовёт заголовок A2A-Extensions, а
                // спека A2UI v0.9 писалась под ранний A2A и знает только
                // X-A2A-Extensions — агент, собранный по ней, второго не увидит.
                b.header("A2A-Extensions", value);
                b.header("X-A2A-Extensions", value);
            }
            trace.dump("запрос агенту " + invokeUrl, body);
            resp = http.send(b.build(), HttpResponse.BodyHandlers.ofString());
        } catch (IOException | InterruptedException e) {
            if (e instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw new A2aException("A2A request to " + invokeUrl + " failed: " + e.getMessage(), e);
        }
        if (resp.statusCode() / 100 != 2) {
            throw new A2aException("A2A HTTP " + resp.statusCode() + " from " + invokeUrl);
        }
        trace.dump("ответ агента", resp.body());
        try {
            JsonNode root = Json.MAPPER.readTree(resp.body());
            JsonNode error = root.get("error");
            if (error != null && !error.isNull()) {
                throw new A2aException("A2A error " + error.path("code").asInt()
                        + ": " + error.path("message").asText());
            }
            JsonNode result = root.get("result");
            if (result == null || result.isNull()) {
                throw new A2aException("A2A response has no result");
            }
            return result;
        } catch (IOException e) {
            throw new A2aException("A2A response parse error: " + e.getMessage(), e);
        }
    }
```

Дописать импорты `AgentConfig`, `AuthConfig`, `Tracer`, `java.nio.charset.StandardCharsets`, `java.util.List`.

Существующие вызовы `sendMessage(msg)` из `OrdersClient` заменить на
`sendMessage(msg, null)`.

- [ ] **Step 4: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=A2aClientTest test`
Expected: PASS

- [ ] **Step 5: Прогнать весь модуль**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 6: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/A2aClient.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/OrdersClient.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/A2aClientTest.java
git commit -m "feat(java/a2a): basic-auth, свой путь карточки, GetTask и починка чужих ответов"
```

---

### Task 10: Remote — одно соединение с одним удалённым агентом

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/Remote.java`
- Modify: `java/common/src/main/java/io/github/kmpavloff/a2ademo/common/a2a/TaskState.java` (добавить `REJECTED`)
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/WorkerProfile.java` (профиль из конфига)
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/RemoteTest.java`

**Interfaces:**
- Consumes: `A2aClient` (задача 9), `AgentConfig`, `A2uiIngest` (задача 5), `A2uiParts` (задача 2), `Tracer`.
- Produces: `Remote(AgentConfig cfg, Tracer trace)`; методы `id()`, `name()`, `description()`, `verbatim()`, `available()`, `probed()`, `profile()`, `setToolName(String)`, `connect()`, `pendingTaskId(String)`, `ask(String sessionId, String text, boolean wantA2ui)`, `askAction(String sessionId, String name, String surfaceId, String sourceComponentId, Map<String,Object> ctx, boolean wantA2ui)`; записи `Remote.Reply`, `Remote.AttachedFile`; исключение `Remote.TurnFailedException` с `firstLine()`; статика `Remote.defaultToolName(String id)`.

**Адаптация к Java.** Go таскает режим «виджеты / только текст» значением
контекста, потому что вызов инструмента идёт через adk. В Java вызов идёт прямым
методом, поэтому режим — явный параметр `wantA2ui`. Поведение то же: не
запросив расширение, клиент выбрал текст, и просить разметку у внешнего агента
незачем.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/RemoteTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.github.kmpavloff.a2ademo.common.Json;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Один ход разговора с удалённым агентом. */
class RemoteTest {

    HttpServer server;
    String base;
    final Deque<String> results = new ArrayDeque<>();
    final List<JsonNode> seenRequests = new ArrayList<>();
    String extensions = "";
    String cardCapabilities = "{}";

    @BeforeEach
    void setUp() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        base = "http://127.0.0.1:" + server.getAddress().getPort();
        server.createContext("/.well-known/agent-card.json", ex -> respond(ex, """
                {"name":"orders-agent","description":"Управляет заказами.","version":"0.1.0",
                 "capabilities":%s,
                 "defaultInputModes":["text/plain"],"defaultOutputModes":["text/plain"],
                 "supportedInterfaces":[{"url":"%s/invoke","protocolBinding":"JSONRPC"}],
                 "skills":[]}
                """.formatted(cardCapabilities, base)));
        server.createContext("/invoke", ex -> {
            extensions = String.valueOf(ex.getRequestHeaders().getFirst("A2A-Extensions"));
            JsonNode req = Json.MAPPER.readTree(ex.getRequestBody());
            seenRequests.add(req);
            respond(ex, "{\"jsonrpc\":\"2.0\",\"id\":" + req.path("id") + ",\"result\":" + results.pop() + "}");
        });
        server.start();
    }

    @AfterEach
    void tearDown() {
        server.stop(0);
    }

    private static void respond(HttpExchange ex, String body) throws IOException {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().add("Content-Type", "application/json");
        ex.sendResponseHeaders(200, b.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(b);
        }
    }

    private Remote remote(AgentConfig cfg) {
        return new Remote(cfg, Tracer.noop());
    }

    private AgentConfig cfg() {
        return new AgentConfig("orders", "Агент заказов", base, "", "", false, "", "", AuthConfig.NONE);
    }

    // Соединение открывается лениво: выключенный агент не мешает оркестратору
    // стартовать, и «недоступен» до первой попытки — домысел, а не факт.
    @Test
    void connectsLazilyAndReportsProbeState() {
        Remote r = remote(cfg());
        assertFalse(r.probed(), "до первой попытки пробы не было");
        assertFalse(r.available());

        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"},"
                + "\"artifacts\":[{\"parts\":[{\"text\":\"готово\"}]}]}}");
        r.ask("s1", "статус 1041", false);

        assertTrue(r.probed());
        assertTrue(r.available());
        assertEquals("ask_orders_agent", r.profile().toolName());
    }

    @Test
    void unreachableAgentIsMarkedUnavailable() {
        AgentConfig broken = cfg().withUrl("http://127.0.0.1:1");
        Remote r = remote(broken);
        assertThrows(A2aClient.A2aException.class, r::connect);
        assertTrue(r.probed());
        assertFalse(r.available());
    }

    // metadata.skill включает у внешнего агента инструменты нужного навыка.
    @Test
    void sendsTheConfiguredSkillInMessageMetadata() {
        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}");
        remote(cfg().withUrl(base)).ask("s1", "привет", false);
        assertEquals(null, seenRequests.getFirst().path("params").path("message").get("metadata"));
    }

    // Задача, вставшая в input-required, запоминается и продолжается тем же
    // сообщением: агент ждёт ответа пользователя.
    @Test
    void storesAndResumesAPendingTask() {
        results.push("{\"task\":{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_INPUT_REQUIRED\","
                + "\"message\":{\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"Подтвердите возврат (да/нет)\"}]}}}}");
        Remote r = remote(cfg());
        Remote.Reply first = r.ask("s1", "верни 1041", false);

        assertTrue(first.needsInput());
        assertEquals("Подтвердите возврат (да/нет)", first.text());
        assertEquals("t1", r.pendingTaskId("s1"));

        results.push("{\"task\":{\"id\":\"t1\",\"contextId\":\"c1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"},"
                + "\"artifacts\":[{\"parts\":[{\"text\":\"Возврат оформлен\"}]}]}}");
        Remote.Reply second = r.ask("s1", "да", false);

        assertEquals("Возврат оформлен", second.text());
        assertEquals("", r.pendingTaskId("s1"), "терминальная задача забывается");
        JsonNode resume = seenRequests.get(1).path("params").path("message");
        assertEquals("t1", resume.path("taskId").asText());
        assertEquals("c1", resume.path("contextId").asText());
    }

    // Провалившаяся задача — ошибка, а не ответ: иначе сбой агента попал бы в
    // ленту как обычная реплика.
    @Test
    void turnFailureIsAnErrorNotAReply() {
        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_FAILED\","
                + "\"message\":{\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"timed out\\nсм. docs\"}]}}}}");
        Remote.TurnFailedException e = assertThrows(Remote.TurnFailedException.class,
                () -> remote(cfg()).ask("s1", "привет", false));
        assertEquals("timed out", e.firstLine(), "в ленту уходит одна строка");
    }

    // Задача в работе опрашивается через GetTask.
    @Test
    void pollsAWorkingTaskUntilItIsTerminal() {
        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}");
        results.addLast("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"},"
                + "\"artifacts\":[{\"parts\":[{\"text\":\"готово\"}]}]}}");
        assertEquals("готово", remote(cfg()).ask("s1", "привет", false).text());
        assertEquals("GetTask", seenRequests.get(1).path("method").asText());
    }

    // Разметку просим только у агента, который объявил её в карточке, и только
    // когда клиент выбрал режим виджетов.
    @Test
    void requestsA2uiOnlyFromAnAgentThatDeclaresIt() {
        results.push("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}");
        remote(cfg()).ask("s1", "привет", true);
        assertEquals("null", extensions, "карточка A2UI не объявляет — заголовка быть не должно");
    }

    @Test
    void ingestsAgentAuthoredMarkupFromTheStatusMessage() {
        results.push(("{\"task\":{\"id\":\"t1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\","
                + "\"message\":{\"role\":\"ROLE_AGENT\",\"parts\":["
                + "{\"text\":\"Вот заказ\"},"
                + "{\"data\":[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"s1\"}}],"
                + "\"metadata\":{\"mimeType\":\"%s\"}}]}}}}").formatted(A2ui.MIME_TYPE));
        Remote.Reply reply = remote(cfg()).ask("s1", "статус 1041", true);
        assertEquals("Вот заказ", reply.text(), "разметка не должна стать ответом пользователю");
        assertEquals(1, reply.a2ui().size());
    }

    @Test
    void defaultToolNameIsDerivedFromTheAgentId() {
        assertEquals("ask_ouroboros", Remote.defaultToolName("ouroboros"));
        assertEquals("ask_my_agent", Remote.defaultToolName("my-agent"));
        assertEquals("ask_agent", Remote.defaultToolName("—"));
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=RemoteTest test`
Expected: FAIL — класса `Remote` нет.

- [ ] **Step 3: Добавить состояние `REJECTED`**

В `TaskState.java` дописать рядом с `FAILED`:

```java
    /** Агент отказался брать запрос в работу. В Go это отдельное терминальное состояние. */
    public static final String REJECTED = "TASK_STATE_REJECTED";
```

- [ ] **Step 4: Научить `WorkerProfile` строиться из конфига**

Дописать в `WorkerProfile`:

```java
    /**
     * Профиль агента, описанного в конфиге. Конфиг вытесняет карточку целиком,
     * включая имя инструмента: у внешнего агента может быть сотня навыков и имя
     * вроде «Who I Am», непригодное ни для промпта, ни для идентификатора
     * функции.
     */
    public static WorkerProfile fromConfig(String agentId, String agentName, String description,
                                           AgentCard card, String toolNameOverride) {
        String name = agentName;
        if (name.isEmpty() && card != null && card.name != null) {
            name = card.name;
        }
        if (name.isEmpty()) {
            name = agentId;
        }
        String toolName = toolNameOverride.isEmpty() ? "ask_" + slug(agentId) : toolNameOverride;
        return new WorkerProfile(
                toolName,
                "Делегировать запрос удалённому агенту. " + description + " " + NEEDS_INPUT_TAIL,
                String.format("Агент по имени «%s» умеет: %s", name, description));
    }

    /** Идентификатор агента в форму, пригодную для имени функции. */
    static String slug(String id) {
        String s = id == null ? "" : id.replaceAll("[^a-zA-Z0-9]+", "_").replaceAll("^_+|_+$", "");
        return s.isEmpty() ? "agent" : s;
    }
```

- [ ] **Step 5: Создать `Remote.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.A2aTask;
import io.github.kmpavloff.a2ademo.common.a2a.AgentCard;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.a2a.TaskState;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2uiIngest;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2uiParts;

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Одно соединение с одним удалённым A2A-агентом: карточка, аутентификация,
 * сессии и один ход разговора (порт a2abridge/remote.go).
 *
 * <p>Знает про особенности чужих агентов (нестандартный путь карточки, нерабочий
 * адрес внутри неё, metadata.skill), но ничего не знает про LLM и про домен
 * заказов.
 */
public class Remote {

    /** Как часто опрашивать GetTask, пока задача в работе. Контракт предписывает 1–2 секунды. */
    private static final Duration POLL_INTERVAL = Duration.ofSeconds(2);

    /** Скачанный агентом файл, переданный дальше как есть. */
    public record AttachedFile(String name, String mediaType, byte[] data) {}

    /**
     * Что удалённый агент вернул за один ход.
     *
     * @param needsInput задача встала в input-required: агент ждёт ответа
     *                   пользователя и продолжит ТУ ЖЕ задачу
     * @param a2ui       готовые к рендеру сообщения A2UI (уже нормализованные)
     * @param widgets    доменные виджеты нашего воркера (DataPart с metadata.kind)
     */
    public record Reply(String text, boolean needsInput, List<Map<String, Object>> a2ui,
                        List<Map<String, Object>> widgets, List<AttachedFile> files) {}

    /**
     * Агент ответил, но ход не удался: задача пришла в FAILED/REJECTED/CANCELED,
     * а причина лежит в её статусном сообщении.
     *
     * <p>Отдельный тип нужен, чтобы интерфейс не путал это с недоступностью:
     * агент на связи и отвечает, просто не смог выполнить именно этот запрос.
     * Формулировка «агент недоступен» на таком сбое — неправда, а пользователь
     * по ней пойдёт проверять сеть вместо того, чтобы повторить ход.
     */
    public static class TurnFailedException extends RuntimeException {
        private final String reason;

        public TurnFailedException(String agentId, String state, String reason) {
            super("agent \"" + agentId + "\" turn failed (" + state + "): " + reason);
            this.reason = reason;
        }

        /**
         * Первая содержательная строка причины. Агенты кладут в неё
         * многострочный текст — сообщение об ошибке плюс ссылку на документацию,
         * — а в ленте нужна одна строка.
         */
        public String firstLine() {
            for (String line : reason.split("\n")) {
                String s = line.trim();
                if (!s.isEmpty()) {
                    return s;
                }
            }
            return reason;
        }
    }

    private record Pending(String taskId, String contextId) {}

    private final AgentConfig cfg;
    private final Tracer trace;
    private final Object connectLock = new Object();

    private volatile A2aClient client;
    private volatile AgentCard card;
    private volatile WorkerProfile profile = new WorkerProfile("", "", "");
    private volatile boolean available;
    private volatile boolean probed;
    private volatile String toolName = "";

    private final Map<String, Pending> pending = new ConcurrentHashMap<>();
    private final Map<String, String> contexts = new ConcurrentHashMap<>();

    /**
     * Создаёт соединение, но ещё не открывает его: карточка резолвится лениво
     * при первом обращении, чтобы выключенный агент в локальной сети не мешал
     * оркестратору стартовать.
     */
    public Remote(AgentConfig cfg, Tracer trace) {
        this.cfg = cfg;
        this.trace = trace;
    }

    public String id() {
        return cfg.id();
    }

    /** Подпись агента для UI: из конфига, иначе из карточки, иначе id. */
    public String name() {
        if (!cfg.name().isEmpty()) {
            return cfg.name();
        }
        AgentCard c = card;
        if (c != null && c.name != null && !c.name.isEmpty()) {
            return c.name;
        }
        return cfg.id();
    }

    public String description() {
        if (!cfg.description().isEmpty()) {
            return cfg.description();
        }
        AgentCard c = card;
        return c == null || c.description == null ? "" : c.description;
    }

    public boolean verbatim() {
        return cfg.verbatim();
    }

    /** Удалось ли последнее подключение. */
    public boolean available() {
        return available;
    }

    /**
     * Была ли вообще попытка подключиться. Пока её не было, «недоступен» — не
     * факт, а домысел, и показывать его пользователю нельзя.
     */
    public boolean probed() {
        return probed;
    }

    public WorkerProfile profile() {
        return profile;
    }

    /**
     * Перекрывает имя делегирующего инструмента. Нужен реестру для разрешения
     * коллизий, когда два агента вывели одно и то же имя.
     */
    public void setToolName(String name) {
        this.toolName = name;
        WorkerProfile p = profile;
        if (!p.toolName().isEmpty()) {
            profile = new WorkerProfile(name, p.toolDesc(), p.summary());
        }
    }

    /** Имя инструмента из id агента: ask_&lt;slug&gt;. */
    public static String defaultToolName(String id) {
        return "ask_" + WorkerProfile.slug(id);
    }

    /**
     * Резолвит AgentCard и создаёт клиента. Идемпотентен; после неудачи
     * следующий вызов пробует снова, поэтому выключенный агент оживает сам.
     */
    public void connect() {
        if (client != null) {
            return;
        }
        synchronized (connectLock) {
            if (client != null) {
                return;
            }
            probed = true;
            A2aClient.Resolved resolved;
            try {
                resolved = A2aClient.resolve(cfg, trace);
            } catch (RuntimeException e) {
                markUnavailable();
                throw e;
            }
            card = resolved.card();
            client = resolved.client();
            available = true;
            profile = buildProfile(resolved.card());
            trace.logf("resolved AgentCard \"%s\" of agent \"%s\" at %s (tool \"%s\")",
                    resolved.card().name, cfg.id(), cfg.url(), profile.toolName());
        }
    }

    /**
     * Роняет пометку доступности и заставляет следующий connect заново резолвить
     * карточку: соединение могло умереть вместе с агентом.
     */
    private void markUnavailable() {
        available = false;
        client = null;
    }

    /**
     * Выводит профиль агента. Описание из конфига вытесняет карточку целиком:
     * у внешнего агента может быть сотня навыков, и промпт локальной модели
     * такого не переживёт.
     */
    private WorkerProfile buildProfile(AgentCard c) {
        if (cfg.description().isEmpty()) {
            WorkerProfile p = WorkerProfile.fromCard(c);
            return toolName.isEmpty() ? p : new WorkerProfile(toolName, p.toolDesc(), p.summary());
        }
        return WorkerProfile.fromConfig(cfg.id(), cfg.name(), cfg.description(), c, toolName);
    }

    /**
     * Объявляет ли агент способность отдавать A2UI: только такому есть смысл
     * слать запрос на generative UI.
     */
    @SuppressWarnings("unchecked")
    private boolean acceptsA2ui() {
        AgentCard c = card;
        if (c == null) {
            return false;
        }
        if (c.defaultOutputModes != null && c.defaultOutputModes.contains(A2ui.MIME_TYPE)) {
            return true;
        }
        if (!(c.capabilities.get("extensions") instanceof List<?> exts)) {
            return false;
        }
        for (Object e : exts) {
            if (e instanceof Map<?, ?> ext && ext.get("uri") instanceof String uri
                    && (uri.equals(A2ui.EXTENSION_URI) || uri.equals(A2ui.LEGACY_EXTENSION_URI))) {
                return true;
            }
        }
        return false;
    }

    /** Id зависшей input-required задачи сессии, или "". */
    public String pendingTaskId(String sessionId) {
        Pending p = pending.get(sessionId);
        return p == null ? "" : p.taskId();
    }

    /** Одно текстовое сообщение агенту. */
    public Reply ask(String sessionId, String text, boolean wantA2ui) {
        return ask(sessionId, Part.text(text), text, wantA2ui);
    }

    /**
     * Нажатие кнопки — штатным событием A2UI, а не пересказом на человеческом
     * языке. Так это описывает схема client_to_server, и так его ждёт агент,
     * собранный на любом из референсных SDK.
     */
    public Reply askAction(String sessionId, String name, String surfaceId, String sourceComponentId,
                           Map<String, Object> ctx, boolean wantA2ui) {
        return ask(sessionId, A2uiParts.action(name, surfaceId, sourceComponentId, ctx, Instant.now()),
                "action " + name, wantA2ui);
    }

    /**
     * Общий путь обоих способов обратиться к агенту. {@code echo} попадает
     * только в трейс: части бывают не текстовые, а лог должен показывать, что
     * именно ушло.
     */
    private Reply ask(String sessionId, Part outgoing, String echo, boolean wantA2ui) {
        connect();
        A2aClient c = client;
        Pending p = pending.get(sessionId);
        String contextId = contexts.get(sessionId);
        boolean a2ui = wantA2ui && acceptsA2ui();

        trace.logf("──▶ delegating to agent \"%s\" | session=%s", cfg.id(), sessionId);

        A2aMessage msg;
        if (p != null) {
            trace.logf("    resuming input-required task | taskID=%s contextID=%s", p.taskId(), p.contextId());
            msg = A2aMessage.forTask(A2aMessage.ROLE_USER, p.taskId(), p.contextId(), outgoing);
        } else {
            msg = A2aMessage.of(A2aMessage.ROLE_USER, outgoing);
            if (contextId != null && !contextId.isEmpty()) {
                msg.contextId = contextId;
            }
        }
        Map<String, Object> meta = new LinkedHashMap<>();
        if (!cfg.skill().isEmpty()) {
            meta.put("skill", cfg.skill());
        }
        // Какие каталоги умеет наш рендерер. Штатный признак «клиент говорит на
        // A2UI»: acceptedOutputModes спека таким признаком не считает.
        if (a2ui) {
            meta.put("a2uiClientCapabilities", A2ui.clientCapabilities());
        }
        if (!meta.isEmpty()) {
            msg.metadata = meta;
        }
        // contextId в трейсе — потому что «агент забывает разговор» это первый
        // вопрос, который приходится проверять.
        String sentCtx = p != null ? p.contextId() : msg.contextId;
        trace.logf("    SendMessage role=user skill=\"%s\" contextId=%s text=\"%s\"",
                cfg.skill(), sentCtx == null || sentCtx.isEmpty() ? "(новый разговор)" : sentCtx,
                Tracer.maskCardLike(echo));

        A2aClient.SendResult res;
        try {
            res = c.sendMessage(msg, a2ui ? List.of(A2ui.EXTENSION_URI, A2ui.LEGACY_EXTENSION_URI) : null);
        } catch (A2aClient.A2aException e) {
            trace.logf("    ✖ SendMessage failed: %s", e.getMessage());
            // Сорвавшийся запрос — единственный честный признак, что агент лёг:
            // connect после первого успеха уже не переспрашивает карточку.
            markUnavailable();
            throw new A2aClient.A2aException("agent \"" + cfg.id() + "\" unreachable: " + e.getMessage(), e);
        }

        if (res.message() != null) {
            trace.logf("◀── response: Message (synchronous, no task) | parts=%d",
                    res.message().parts == null ? 0 : res.message().parts.size());
            pending.remove(sessionId);
            // Синхронный ответ тоже несёт контекст разговора: без этого агент,
            // отвечающий Message вместо Task, начинал бы беседу заново.
            if (res.message().contextId != null && !res.message().contextId.isEmpty()) {
                contexts.put(sessionId, res.message().contextId);
            }
            return replyFromParts(res.message().parts, null, "");
        }
        return replyFromTask(sessionId, awaitTerminal(c, res.task()));
    }

    /** Опрашивает GetTask, пока задача не выйдет из рабочего состояния. */
    private A2aTask awaitTerminal(A2aClient c, A2aTask task) {
        while (true) {
            String state = task.status == null ? "" : task.status.state;
            if (!TaskState.WORKING.equals(state) && !TaskState.SUBMITTED.equals(state)) {
                return task;
            }
            trace.logf("    … task %s is %s, polling GetTask in %s", task.id, state, POLL_INTERVAL);
            try {
                Thread.sleep(POLL_INTERVAL);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                throw new A2aClient.A2aException("agent \"" + cfg.id() + "\": interrupted while polling " + task.id);
            }
            task = c.getTask(task.id);
        }
    }

    /** Собирает ответ из терминальной задачи и запоминает состояние сессии. */
    private Reply replyFromTask(String sessionId, A2aTask task) {
        String state = task.status == null ? "" : task.status.state;
        trace.logf("◀── response: Task | id=%s contextID=%s state=%s", task.id, task.contextId, state);

        if (task.contextId != null && !task.contextId.isEmpty()) {
            contexts.put(sessionId, task.contextId);
        }
        if (TaskState.INPUT_REQUIRED.equals(state)) {
            pending.put(sessionId, new Pending(task.id, task.contextId));
            Reply reply = replyFromParts(statusParts(task), null, statusMessageText(task));
            reply = new Reply(reply.text(), true, reply.a2ui(), reply.widgets(), reply.files());
            trace.logf("    ⏸ input-required — stored pending task, asking user: \"%s\"", reply.text());
            return reply;
        }
        pending.remove(sessionId);

        if (TaskState.FAILED.equals(state) || TaskState.REJECTED.equals(state) || TaskState.CANCELED.equals(state)) {
            String why = statusMessageRaw(task).trim();
            trace.logf("    ✖ задача завершилась неуспехом: state=%s reason=\"%s\"", state, why);
            throw new TurnFailedException(cfg.id(), state, why);
        }

        // Текст берём из статусного сообщения, если агент положил его туда (так
        // предписывает контракт), иначе — из артефакта, как делает наш воркер.
        String fallback = taskResultText(task);
        String fromStatus = firstProseText(statusParts(task));
        if (!fromStatus.isEmpty()) {
            fallback = fromStatus;
        }
        Reply reply = replyFromParts(statusParts(task), artifactParts(task), fallback);
        trace.logf("    ✔ terminal state | text=\"%s\" a2ui=%d widgets=%d files=%d",
                reply.text(), reply.a2ui().size(), reply.widgets().size(), reply.files().size());
        return reply;
    }

    /**
     * Раскладывает части ответа по слоям: текст, A2UI, доменные виджеты, файлы.
     *
     * <p>statusP и artifactP разделены из-за A2UI: спека расширения кладёт
     * разметку в части сообщения, и оттуда её читает стандартный клиент.
     * Артефакт — второе место, куда её кладут живые агенты; берём его, только
     * если в сообщении разметки не было, иначе поверхность приехала бы дважды.
     */
    private Reply replyFromParts(List<Part> statusP, List<Part> artifactP, String fallback) {
        List<Part> parts = new ArrayList<>();
        if (artifactP != null) {
            parts.addAll(artifactP);
        }
        if (statusP != null) {
            parts.addAll(statusP);
        }
        String text = fallback == null ? "" : fallback;
        if (text.isEmpty()) {
            text = firstProseText(parts);
        }
        if (text.isBlank()) {
            text = "Готово.";
        }
        List<Map<String, Object>> a2ui = A2uiIngest.ingest(statusP);
        if (a2ui.isEmpty()) {
            a2ui = A2uiIngest.ingest(artifactP);
        }
        List<Map<String, Object>> widgets = new ArrayList<>();
        Map<String, Object> w = OrdersClient.firstWidget(parts);
        if (w != null) {
            widgets.add(w);
        }
        List<AttachedFile> files = new ArrayList<>();
        for (Part p : parts) {
            if (p == null || p.filename == null || p.filename.isEmpty() || p.raw == null) {
                continue;
            }
            try {
                files.add(new AttachedFile(p.filename, p.mediaType, Base64.getDecoder().decode(p.raw)));
            } catch (IllegalArgumentException ignored) {
                // битый base64 — часть пропускаем, ход не роняем
            }
        }
        return new Reply(text, false, a2ui, widgets, files);
    }

    private static List<Part> statusParts(A2aTask t) {
        return t.status == null || t.status.message == null ? null : t.status.message.parts;
    }

    private static List<Part> artifactParts(A2aTask t) {
        return t.artifacts == null || t.artifacts.isEmpty() ? null : t.artifacts.getLast().parts;
    }

    /**
     * Первая человекочитаемая текстовая часть. Разметка интерфейса пропускается:
     * A2UI приезжает и текстовой частью, и показывать её пользователю нельзя.
     */
    static String firstProseText(List<Part> parts) {
        if (parts == null) {
            return "";
        }
        for (Part p : parts) {
            if (p == null || A2uiParts.isA2ui(p)) {
                continue;
            }
            String txt = p.textOrEmpty();
            if (!txt.isEmpty()) {
                return txt;
            }
        }
        return "";
    }

    private static String statusMessageRaw(A2aTask t) {
        return t.status == null || t.status.message == null ? "" : firstProseText(t.status.message.parts);
    }

    private static String statusMessageText(A2aTask t) {
        String txt = statusMessageRaw(t);
        return txt.isEmpty() ? "Агенту по заказам нужны дополнительные данные." : txt;
    }

    private static String taskResultText(A2aTask t) {
        String txt = firstProseText(artifactParts(t));
        if (!txt.isEmpty()) {
            return txt;
        }
        if (t.history != null && !t.history.isEmpty()) {
            txt = firstProseText(t.history.getLast().parts);
            if (!txt.isEmpty()) {
                return txt;
            }
        }
        return "Готово.";
    }
}
```

`OrdersClient.firstWidget` сделать `public static` — им теперь пользуется `Remote`.

- [ ] **Step 6: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=RemoteTest test`
Expected: PASS

- [ ] **Step 7: Прогнать весь модуль**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 8: Коммит**

```bash
git add java/common/src/main/java/io/github/kmpavloff/a2ademo/common/a2a/TaskState.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/Remote.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/WorkerProfile.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/OrdersClient.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/RemoteTest.java
git commit -m "feat(java/a2a): Remote — соединение, доступность, ход разговора с удалённым агентом"
```

---

### Task 11: Registry — набор агентов, доступность, живая пересборка

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/Registry.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/RegistryTest.java`

**Interfaces:**
- Consumes: `Remote` (задача 10), `AgentConfig`, `Tracer`.
- Produces: `Registry(List<AgentConfig> agents, Tracer trace)`; методы `ids()`, `get(String) -> Optional<Remote>`, `first() -> Remote` (null, если агентов нет), `clientFor(String) -> OrdersClient`, `availableClients() -> List<OrdersClient>`, `summaries() -> List<String>`, `list() -> List<AgentInfo>`, `setClientInit(Consumer<OrdersClient>)`, `generation() -> long`, `apply(List<AgentConfig>)`; запись `Registry.AgentInfo(id, name, description, verbatim, available, probed)`.

**Адаптация к Java.** Go отводит на подключение прямо в ходе 20 секунд, а на
фоновую проверку — 30, задавая их контекстом. В Java бюджет задан таймаутами
самого `A2aClient` (10 с на соединение, 15 с на чтение карточки) — отдельного
слоя таймаутов не заводим. Смысл сохранён: ход не подвешивается на выключенном
агенте, а `/api/agents` вообще не ждёт — отдаёт последнее известное состояние.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/RegistryTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotSame;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Набор агентов: порядок, состояние для UI и пересборка при правке конфига. */
class RegistryTest {

    private static AgentConfig agent(String id, String url) {
        return new AgentConfig(id, "", url, "", "", false, "", "", AuthConfig.NONE);
    }

    // Порядок задаёт пункты селектора и выбор агента для терминального REPL.
    @Test
    void keepsTheConfiguredOrder() {
        Registry reg = new Registry(List.of(agent("orders", "http://a"), agent("shop", "http://b")), Tracer.noop());
        assertEquals(List.of("orders", "shop"), reg.ids());
        assertEquals("orders", reg.first().id());
    }

    // Пока попытки подключения не было, «недоступен» — домысел, и клиенту его
    // показывать нельзя.
    @Test
    void listReportsProbeStateSeparatelyFromAvailability() {
        Registry reg = new Registry(List.of(agent("orders", "http://127.0.0.1:1")), Tracer.noop());
        List<Registry.AgentInfo> before = reg.list();
        assertEquals(1, before.size());
        assertFalse(before.getFirst().available());
    }

    @Test
    void clientForIsCreatedOnceAndPrepared() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        boolean[] prepared = {false};
        reg.setClientInit(c -> prepared[0] = true);
        OrdersClient first = reg.clientFor("orders");
        assertSame(first, reg.clientFor("orders"), "клиент создаётся один раз");
        assertTrue(prepared[0], "новый клиент обязан пройти подготовку");
    }

    // Нетронутый агент остаётся тем же Remote: в нём живое соединение,
    // разобранная карточка и зависшие input-required задачи.
    @Test
    void applyKeepsUnchangedAgentsAndRebuildsChangedOnes() {
        Registry reg = new Registry(List.of(agent("orders", "http://a"), agent("shop", "http://b")), Tracer.noop());
        Remote orders = reg.get("orders").orElseThrow();
        Remote shop = reg.get("shop").orElseThrow();
        long gen = reg.generation();

        reg.apply(List.of(agent("orders", "http://a"), agent("shop", "http://CHANGED")));

        assertSame(orders, reg.get("orders").orElseThrow(), "нетронутый агент не пересоздаётся");
        assertNotSame(shop, reg.get("shop").orElseThrow(), "изменённый пересоздаётся целиком");
        assertTrue(reg.generation() > gen, "поколение обязано вырасти");
    }

    @Test
    void applyWithoutChangesLeavesTheGenerationAlone() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        long gen = reg.generation();
        reg.apply(List.of(agent("orders", "http://a")));
        assertEquals(gen, reg.generation());
    }

    @Test
    void applyCanRemoveAndAddAgents() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        reg.apply(List.of(agent("shop", "http://b")));
        assertEquals(List.of("shop"), reg.ids());
        assertTrue(reg.get("orders").isEmpty());
    }

    @Test
    void firstIsNullWhenEveryAgentIsGone() {
        Registry reg = new Registry(List.of(agent("orders", "http://a")), Tracer.noop());
        reg.apply(List.of());
        assertEquals(null, reg.first());
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=RegistryTest test`
Expected: FAIL — класса `Registry` нет.

- [ ] **Step 3: Создать `Registry.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.a2a;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicLong;
import java.util.function.Consumer;

/**
 * Набор удалённых агентов из конфига, в порядке объявления (порт
 * a2abridge/registry.go).
 *
 * <p>Порядок важен: первый агент обслуживает терминальный REPL, и он же задаёт
 * стабильный порядок пунктов в селекторе UI.
 */
public class Registry {

    /** То, что оркестратор рассказывает браузеру про доступных агентов. */
    public record AgentInfo(String id, String name, String description,
                            boolean verbatim, boolean available, boolean probed) {}

    private final Tracer trace;
    private final Object lock = new Object();

    private List<String> order = new ArrayList<>();
    private Map<String, Remote> remotes = new LinkedHashMap<>();
    private Map<String, OrdersClient> clients = new LinkedHashMap<>();
    private Map<String, AgentConfig> cfgs = new LinkedHashMap<>();
    private Consumer<OrdersClient> clientInit;

    /** Идущие фоновые проверки, чтобы не плодить их на одного агента. */
    private final Set<String> probing = ConcurrentHashMap.newKeySet();

    /** Поколение состава; растёт при каждом изменении списка. */
    private final AtomicLong generation = new AtomicLong();

    /** Демон-пул: фоновые проверки не должны удерживать JVM при выходе. */
    private final ExecutorService probes = Executors.newCachedThreadPool(r -> {
        Thread t = new Thread(r, "agent-probe");
        t.setDaemon(true);
        return t;
    });

    /** Создаёт реестр по конфигу. Соединения не открываются: агенты подключаются лениво. */
    public Registry(List<AgentConfig> agents, Tracer trace) {
        this.trace = trace;
        for (AgentConfig cfg : agents) {
            order.add(cfg.id());
            remotes.put(cfg.id(), new Remote(cfg, trace));
            cfgs.put(cfg.id(), cfg);
        }
    }

    public List<String> ids() {
        synchronized (lock) {
            return List.copyOf(order);
        }
    }

    public Optional<Remote> get(String id) {
        synchronized (lock) {
            return Optional.ofNullable(remotes.get(id));
        }
    }

    /** Первый агент из конфига — тот, с кем работает REPL; null, если список пуст. */
    public Remote first() {
        synchronized (lock) {
            return order.isEmpty() ? null : remotes.get(order.getFirst());
        }
    }

    /** Делегирующий клиент агента; создаётся при первом обращении. */
    public OrdersClient clientFor(String id) {
        OrdersClient created;
        Consumer<OrdersClient> init;
        synchronized (lock) {
            OrdersClient existing = clients.get(id);
            if (existing != null) {
                return existing;
            }
            Remote r = remotes.get(id);
            if (r == null) {
                return null;
            }
            created = new OrdersClient(r, trace);
            clients.put(id, created);
            init = clientInit;
        }
        // Подготовка — вне блокировки: обработчики ставит исполнитель, и звать
        // чужой код под своим мьютексом незачем.
        if (init != null) {
            init.accept(created);
        }
        return created;
    }

    /**
     * Делегирующие клиенты доступных агентов — набор для режима «Авто», где
     * модель сама выбирает, к кому обратиться. Недоступные пропускаются:
     * инструмент, который заведомо не работает, только сбивает модель с толку.
     */
    public List<OrdersClient> availableClients() {
        List<OrdersClient> out = new ArrayList<>();
        Set<String> seen = new java.util.HashSet<>();
        for (String id : ids()) {
            Remote r = get(id).orElse(null);
            if (r == null || !ready(r)) {
                continue;
            }
            // Имена инструментов выводятся из карточек и могут совпасть;
            // коллизию разрешаем детерминированно, в порядке конфига.
            String name = r.profile().toolName();
            if (seen.contains(name)) {
                String fallback = Remote.defaultToolName(id);
                for (int n = 2; seen.contains(fallback); n++) {
                    fallback = Remote.defaultToolName(id) + "_" + n;
                }
                trace.logf("⚠ tool name \"%s\" is already taken — renaming agent \"%s\" tool to \"%s\"",
                        name, id, fallback);
                r.setToolName(fallback);
            }
            OrdersClient c = clientFor(id);
            if (c == null) {
                continue;
            }
            seen.add(c.profile().toolName());
            out.add(c);
        }
        return out;
    }

    /** Блоки возможностей доступных агентов для промпта. */
    public List<String> summaries() {
        List<String> out = new ArrayList<>();
        for (String id : ids()) {
            Remote r = get(id).orElse(null);
            if (r == null || !ready(r)) {
                continue;
            }
            String s = r.profile().summary();
            if (!s.isEmpty()) {
                out.add(s);
            }
        }
        return out;
    }

    /**
     * Готовит агента к участию в ходе. Уже подключённый проходит мгновенно;
     * заведомо лежащий (проверяли — не ответил) пропускается сразу, а его
     * возвращение к жизни заметит фоновая проверка из {@link #list()}.
     */
    private boolean ready(Remote r) {
        if (r.available()) {
            return true;
        }
        if (r.probed()) {
            probeAsync(r);
            return false;
        }
        try {
            r.connect();
            return true;
        } catch (RuntimeException e) {
            trace.logf("agent \"%s\" unavailable, skipping its tool: %s", r.id(), e.getMessage());
            return false;
        }
    }

    /**
     * Проверяет доступность агента в фоне, по одной проверке на агента
     * одновременно. Синхронно этого делать нельзя: карточка у занятого агента
     * отвечает секундами, а /api/agents браузер дёргает раз в полминуты и после
     * каждого хода.
     */
    private void probeAsync(Remote r) {
        String id = r.id();
        if (!probing.add(id)) {
            return;
        }
        probes.execute(() -> {
            try {
                r.connect();
            } catch (RuntimeException e) {
                trace.logf("agent \"%s\" unavailable: %s", id, e.getMessage());
            } finally {
                probing.remove(id);
            }
        });
    }

    /**
     * Описывает агентов для UI. Отдаёт последнее известное состояние сразу и
     * запускает фоновое обновление — эндпоинт не должен ждать медленного агента.
     */
    public List<AgentInfo> list() {
        List<AgentInfo> out = new ArrayList<>();
        for (String id : ids()) {
            Remote r = get(id).orElse(null);
            if (r == null) {
                continue;
            }
            probeAsync(r);
            out.add(new AgentInfo(r.id(), r.name(), r.description(), r.verbatim(), r.available(), r.probed()));
        }
        return out;
    }

    /**
     * Задаёт подготовку клиента — навешивание обработчиков виджетов, A2UI, файлов
     * и текста. Разовым циклом это делать нельзя: клиент, созданный после правки
     * конфига, остался бы без обработчиков, и виджеты нового агента молча
     * пропадали бы.
     */
    public void setClientInit(Consumer<OrdersClient> fn) {
        List<OrdersClient> existing;
        synchronized (lock) {
            clientInit = fn;
            existing = List.copyOf(clients.values());
        }
        for (OrdersClient c : existing) {
            fn.accept(c);
        }
    }

    /**
     * Номер состава агентов. Растёт при любом изменении списка; исполнитель по
     * нему понимает, что кэш собранных агентов пора выбросить.
     */
    public long generation() {
        return generation.get();
    }

    /**
     * Приводит реестр к новому списку агентов.
     *
     * <p>Сверка идёт по id: {@link AgentConfig} — запись из строк и bool, так что
     * сравнение обычное. Нетронутый агент остаётся тем же {@link Remote} — в нём
     * живое соединение, разобранная карточка и зависшие input-required задачи, и
     * терять их из-за правки соседа нельзя. Изменённый пересоздаётся целиком.
     */
    public void apply(List<AgentConfig> agents) {
        synchronized (lock) {
            List<String> nextOrder = new ArrayList<>(agents.size());
            Map<String, Remote> nextRemotes = new LinkedHashMap<>(agents.size());
            Map<String, OrdersClient> nextClients = new LinkedHashMap<>(agents.size());
            Map<String, AgentConfig> nextCfgs = new LinkedHashMap<>(agents.size());
            boolean changed = agents.size() != order.size();

            for (AgentConfig cfg : agents) {
                nextOrder.add(cfg.id());
                nextCfgs.put(cfg.id(), cfg);
                Remote old = remotes.get(cfg.id());
                if (old != null && cfg.equals(cfgs.get(cfg.id()))) {
                    nextRemotes.put(cfg.id(), old);
                    OrdersClient c = clients.get(cfg.id());
                    if (c != null) {
                        nextClients.put(cfg.id(), c);
                    }
                    continue;
                }
                changed = true;
                trace.logf("agent \"%s\": конфиг изменился — пересоздаём соединение", cfg.id());
                nextRemotes.put(cfg.id(), new Remote(cfg, trace));
            }
            if (!changed && !nextOrder.equals(order)) {
                changed = true;
            }
            order = nextOrder;
            remotes = nextRemotes;
            clients = nextClients;
            cfgs = nextCfgs;
            if (changed) {
                generation.incrementAndGet();
            }
        }
    }
}
```

- [ ] **Step 4: Прогнать тест — должен пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=RegistryTest test`
Expected: PASS (после задачи 12, где `OrdersClient` получит конструктор от `Remote`; если модуль ещё не собирается — выполнить задачу 12 и вернуться к этому шагу).

- [ ] **Step 5: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/Registry.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/RegistryTest.java
git commit -m "feat(java/a2a): реестр агентов с проверкой доступности и живой пересборкой"
```

---

### Task 12: OrdersClient поверх Remote

`OrdersClient` перестаёт сам ходить в сеть: он остаётся делегирующим
инструментом — тем, что видит модель, — а всю протокольную работу ведёт
`Remote`. Так же устроена пара `client.go`/`remote.go` в Go.

**Files:**
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/OrdersClient.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/OrdersClientTest.java` (правка существующего)

**Interfaces:**
- Consumes: `Remote` (задача 10).
- Produces: конструктор `OrdersClient(Remote remote, Tracer trace)`; методы `profile()`, `ask(String sessionId, String text, boolean wantA2ui) -> String`, `pendingTaskId(String)`, `emptyMessageReply(String)`, `clearEmpty(String)`, `remote()`; сеттеры `setWidgetHandler`, `setA2uiHandler`, `setFileHandler`, `setTextHandler`.

- [ ] **Step 1: Написать падающий тест**

Дописать в `OrdersClientTest` (создать файл, если его нет):

```java
    // Виджеты, разметка, файлы и собственный текст агента уходят в UI мимо
    // модели; модели достаётся только текстовый результат инструмента.
    @Test
    void forwardsEveryLayerToItsHandler() {
        Remote remote = new StubRemote(new Remote.Reply(
                "Заказ 1041 доставлен", false,
                List.of(Map.of("version", A2ui.VERSION)),
                List.of(Map.of("_kind", "widget/order")),
                List.of(new Remote.AttachedFile("receipt.html", "text/html", new byte[]{1}))));

        List<Map<String, Object>> widgets = new ArrayList<>();
        List<Map<String, Object>> a2ui = new ArrayList<>();
        List<String> files = new ArrayList<>();
        List<String> texts = new ArrayList<>();

        OrdersClient c = new OrdersClient(remote, Tracer.noop());
        c.setWidgetHandler((s, w) -> widgets.add(w));
        c.setA2uiHandler((s, msgs) -> a2ui.addAll(msgs));
        c.setFileHandler((s, name, mime, data) -> files.add(name));
        c.setTextHandler((s, t) -> texts.add(t));

        assertEquals("Заказ 1041 доставлен", c.ask("s1", "статус 1041", true));
        assertEquals(1, widgets.size());
        assertEquals(1, a2ui.size());
        assertEquals(List.of("receipt.html"), files);
        assertEquals(List.of("Заказ 1041 доставлен"), texts);
    }

    // Задачу, вставшую в input-required, модель обязана увидеть как вопрос.
    @Test
    void marksAPendingAnswerForTheModel() {
        Remote remote = new StubRemote(new Remote.Reply(
                "Подтвердите возврат", true, List.of(), List.of(), List.of()));
        assertEquals("NEEDS_USER_INPUT: Подтвердите возврат",
                new OrdersClient(remote, Tracer.noop()).ask("s1", "верни 1041", false));
    }
```

`StubRemote` — подкласс `Remote`, переопределяющий `ask`; для этого метод `ask`
в `Remote` объявить не-final и вызывать `super` не требуется.

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=OrdersClientTest test`
Expected: FAIL — конструктора `OrdersClient(Remote, Tracer)` нет.

- [ ] **Step 3: Переписать `OrdersClient.java`**

Заменить поля, конструктор и `ask` на:

```java
    private final Remote remote;
    private final Tracer trace;

    private final Map<String, Integer> emptyCalls = new ConcurrentHashMap<>();
    private volatile BiConsumer<String, Map<String, Object>> onWidget;
    private volatile BiConsumer<String, List<Map<String, Object>>> onA2ui;
    private volatile FileHandler onFile;
    private volatile BiConsumer<String, String> onText;

    public OrdersClient(Remote remote, Tracer trace) {
        this.remote = remote;
        this.trace = trace;
    }

    public Remote remote() {
        return remote;
    }

    public WorkerProfile profile() {
        return remote.profile();
    }

    /** Разметка, которую агент написал сам (в отличие от виджетов, что мапит шлюз). */
    public void setA2uiHandler(BiConsumer<String, List<Map<String, Object>>> handler) {
        this.onA2ui = handler;
    }

    /**
     * Собственный текст ответа агента. Не для показа: он уходит модели как
     * результат инструмента и обычно ей же и пересказывается. Нужен как запас на
     * ход, в котором карточки не окажется, — тогда данные есть только здесь.
     */
    public void setTextHandler(BiConsumer<String, String> handler) {
        this.onText = handler;
    }

    /**
     * Шлёт текст удалённому агенту и возвращает то, что увидит модель. Виджеты,
     * разметка и файлы уходят в UI мимо неё.
     */
    public String ask(String sessionId, String text, boolean wantA2ui) {
        Remote.Reply reply = remote.ask(sessionId, text, wantA2ui);
        forward(sessionId, reply);
        return reply.needsInput() ? "NEEDS_USER_INPUT: " + reply.text() : reply.text();
    }

    /** Раскладывает слои ответа по зарегистрированным обработчикам. */
    private void forward(String sessionId, Remote.Reply reply) {
        BiConsumer<String, Map<String, Object>> widget = onWidget;
        if (widget != null) {
            for (Map<String, Object> w : reply.widgets()) {
                trace.logf("    ⟐ widget DataPart (%s) → UI, bypassing LLM", w.get("_kind"));
                widget.accept(sessionId, w);
            }
        }
        BiConsumer<String, List<Map<String, Object>>> a2ui = onA2ui;
        if (a2ui != null && !reply.a2ui().isEmpty()) {
            trace.logf("    ⟐ %d A2UI message(s) from the agent → UI", reply.a2ui().size());
            a2ui.accept(sessionId, reply.a2ui());
        }
        FileHandler file = onFile;
        if (file != null) {
            for (Remote.AttachedFile f : reply.files()) {
                trace.logf("    ⟐ file part \"%s\" (%s, %d bytes) → UI", f.name(), f.mediaType(), f.data().length);
                file.accept(sessionId, f.name(), f.mediaType(), f.data());
            }
        }
        BiConsumer<String, String> textHandler = onText;
        if (textHandler != null && !reply.text().isBlank()) {
            textHandler.accept(sessionId, reply.text());
        }
    }

    public String pendingTaskId(String sessionId) {
        return remote.pendingTaskId(sessionId);
    }
```

Удалить всё, что переехало в `Remote`: поля `client`/`profile`/`pending`, запись
`Pending`, методы `statusParts`, `artifactParts`, `statusMessageText`,
`taskResultText`. `firstWidget` остаётся — теперь `public static`, им пользуется
`Remote`.

- [ ] **Step 4: Прогнать тесты — должны пройти**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest='OrdersClientTest,RegistryTest,RemoteTest' test`
Expected: PASS

- [ ] **Step 5: Прогнать весь модуль**

Run: `cd java && mvn -o test`
Expected: PASS — вызовы `orders.ask(sessionId, message)` в `OrchestratorAgent`
получают третий аргумент (режим), см. задачу 13; временно передать `true`.

- [ ] **Step 6: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/OrdersClient.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/a2a/OrdersClientTest.java
git commit -m "refactor(java/a2a): OrdersClient — делегирующий инструмент поверх Remote"
```

---

### Task 13: Исполнитель — выбор агента, verbatim, ответ в завершающем сообщении

**Files:**
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/OrchestratorWebExecutor.java` (переписывается целиком)
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/A2aWebController.java:88-120`
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/agent/OrchestratorAgent.java` (режим ходом ниже)
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/ExecutorChoicesTest.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/WebE2eTest.java` (правка)

**Interfaces:**
- Consumes: `Registry`, `OrdersClient`, `Remote`, `SessionStore`, `A2uiIngest`, `Surfaces`, `A2uiParts`.
- Produces: конструктор `OrchestratorWebExecutor(Registry reg, AgentBuilder build, SessionStore sessions, Tracer trace)`; функциональный интерфейс `OrchestratorWebExecutor.AgentBuilder`; статики `pickAnswer(String llmText, List<String> agentTexts, boolean visual)`, `actionToPrompt(String, Map)`, `agentErrorText(String, RuntimeException)`, `AUTO_AGENT_ID`.

**Смена места ответа.** Части ответа переезжают из артефакта в **завершающее
сообщение задачи** — туда их кладёт спека расширения A2UI, и оттуда их читает
референсный клиент. Фронтенд уже предпочитает это место, оставляя артефакт
запасным путём (`web/src/client.ts:232`).

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/ExecutorChoicesTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.orchestrator.a2a.A2aClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Remote;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Решения исполнителя, принимаемые за ход: что показать и как назвать сбой. */
class ExecutorChoicesTest {

    // Промпт запрещает модели называть значения — их покажет карточка. Но если
    // карточки не будет, подводка «Вот детали вашего заказа:» бесполезна:
    // данные остались только в ответе агента.
    @Test
    void keepsTheModelsLeadInWhenAWidgetWillBeShown() {
        assertEquals("Вот детали вашего заказа:",
                OrchestratorWebExecutor.pickAnswer("Вот детали вашего заказа:",
                        List.of("Заказ 1041: наушники, доставлен, 4990 ₽"), true));
    }

    @Test
    void fallsBackToTheAgentAnswerWhenThereWillBeNoWidget() {
        assertEquals("Заказ 1041: наушники, доставлен, 4990 ₽",
                OrchestratorWebExecutor.pickAnswer("Вот детали вашего заказа:",
                        List.of("Заказ 1041: наушники, доставлен, 4990 ₽"), false));
    }

    @Test
    void keepsTheModelsOwnDetailedAnswer() {
        String detailed = "Заказ 1041 — наушники, доставлен 3 сентября, сумма 4990 ₽.";
        assertEquals(detailed, OrchestratorWebExecutor.pickAnswer(detailed, List.of("ок"), false));
    }

    // Провалившийся ход и недоступный агент — разные беды: «недоступен»
    // отправит пользователя чинить сеть там, где агент на связи.
    @Test
    void tellsAFailedTurnApartFromAnUnreachableAgent() {
        assertEquals("Агент \"Ouroboros\" не смог выполнить запрос: timed out",
                OrchestratorWebExecutor.agentErrorText("Ouroboros",
                        new Remote.TurnFailedException("ouroboros", "TASK_STATE_FAILED", "timed out\nсм. docs")));
        assertTrue(OrchestratorWebExecutor.agentErrorText("Ouroboros",
                        new A2aClient.A2aException("connection refused")).contains("недоступен"));
    }

    // Наши HITL-кнопки имеют канонические ответы; чужая кнопка описывается
    // вместе с контекстом — иначе модель видит «нажал return_order» и не знает,
    // какой заказ.
    @Test
    void describesAForeignButtonWithItsContext() {
        assertEquals("да", OrchestratorWebExecutor.actionToPrompt("approve_refund", Map.of()));
        assertEquals("нет", OrchestratorWebExecutor.actionToPrompt("decline_refund", Map.of()));
        assertEquals("Пользователь нажал кнопку «Вернуть заказ» (order_id: 1041)",
                OrchestratorWebExecutor.actionToPrompt("return_order",
                        Map.of("label", "Вернуть заказ", "order_id", "1041")));
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=ExecutorChoicesTest test`
Expected: FAIL — статик у исполнителя нет.

- [ ] **Step 3: Провести режим до делегирующего инструмента**

В `OrchestratorAgent.runTurn` добавить параметр `boolean a2uiActive` и передавать
его в `target.ask(sessionId, message, a2uiActive)`. В `Repl` передавать `false`:
терминал виджеты рисует сам, разметка ему не нужна.

Подпись метода меняется, поэтому `OrchestratorAgentToolsTest` из задачи 7 надо
поправить: все три вызова `runTurn(…, new OrchestratorAgent.TurnListener() {})`
получают четвёртым аргументом `false`.

- [ ] **Step 4: Переписать `OrchestratorWebExecutor.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.a2a.A2aMessage;
import io.github.kmpavloff.a2ademo.common.a2a.Part;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.common.util.Cards;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.OrdersClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Remote;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2ui;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.A2uiParts;
import io.github.kmpavloff.a2ademo.orchestrator.a2ui.Surfaces;
import io.github.kmpavloff.a2ademo.orchestrator.agent.OrchestratorAgent;
import io.github.kmpavloff.a2ademo.orchestrator.agent.SessionStore;

import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeSet;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Один ход браузерного разговора: выбор агента, HITL-кнопки, локальная модель и
 * сборка ответных частей (порт a2abridge/orchserver.go).
 */
public class OrchestratorWebExecutor {

    /** Значение agentId, означающее «модель выбирает агента сама». */
    public static final String AUTO_AGENT_ID = "auto";

    /** Собирает агента под конкретный набор делегирующих инструментов. */
    @FunctionalInterface
    public interface AgentBuilder {
        OrchestratorAgent build(List<OrdersClient> tools, String summary);
    }

    private final Registry reg;
    private final AgentBuilder build;
    private final SessionStore sessions;
    private final Tracer trace;

    /**
     * Разводит поверхности разных ходов. Сквозной, а не по сессиям: нужна лишь
     * уникальность в пределах страницы, а карта по сессиям росла бы без конца —
     * «конца сессии» в протоколе нет.
     */
    private final AtomicLong surfaceSeq = new AtomicLong();

    /** agentId (или ключ набора «Авто») → собранный агент. */
    private final Map<String, OrchestratorAgent> agents = new ConcurrentHashMap<>();

    /** Поколение реестра, под которое собран кэш агентов. */
    private final AtomicLong gen = new AtomicLong();

    private final Map<String, List<Map<String, Object>>> widgets = new ConcurrentHashMap<>();
    private final Map<String, List<Map<String, Object>>> a2uis = new ConcurrentHashMap<>();
    private final Map<String, List<Remote.AttachedFile>> files = new ConcurrentHashMap<>();
    private final Map<String, List<String>> texts = new ConcurrentHashMap<>();

    public OrchestratorWebExecutor(Registry reg, AgentBuilder build, SessionStore sessions, Tracer trace) {
        this.reg = reg;
        this.build = build;
        this.sessions = sessions;
        this.trace = trace;
        // Обработчики вешаются на КАЖДОГО клиента, включая созданных после
        // правки конфига: разовый цикл оставил бы новых без них, и их виджеты
        // молча пропадали бы.
        reg.setClientInit(c -> {
            c.setWidgetHandler((s, w) -> widgets.computeIfAbsent(s, k -> new ArrayList<>()).add(w));
            c.setA2uiHandler((s, msgs) -> a2uis.computeIfAbsent(s, k -> new ArrayList<>()).addAll(msgs));
            c.setFileHandler((s, name, mime, data) ->
                    files.computeIfAbsent(s, k -> new ArrayList<>()).add(new Remote.AttachedFile(name, mime, data)));
            c.setTextHandler((s, t) -> texts.computeIfAbsent(s, k -> new ArrayList<>()).add(t));
        });
        gen.set(reg.generation());
    }

    /** Один ход. Возвращает части завершающего сообщения задачи. */
    public List<Part> execute(String sessionId, A2aMessage message, boolean a2uiActive) {
        long reqStart = System.nanoTime();
        trace.logf("▶ orchestrator A2A request | contextID=%s a2ui=%s inParts=%d",
                sessionId, a2uiActive, message.parts == null ? 0 : message.parts.size());

        String userText = "";
        String actionName = "";
        String actionSurface = "";
        String actionSource = "";
        Map<String, Object> actionCtx = Map.of();
        if (message.parts != null) {
            for (Part p : message.parts) {
                A2ui.Action a = A2ui.parseAction(p.data);
                if (a != null) {
                    actionName = a.name();
                    actionCtx = a.context();
                    A2ui.Origin origin = A2ui.actionOrigin(p.data);
                    actionSurface = origin.surfaceId();
                    actionSource = origin.sourceComponentId();
                    userText = actionToPrompt(actionName, actionCtx);
                    trace.logf("  A2UI action \"%s\" ctx=%s → user text \"%s\"",
                            actionName, redactCard(actionCtx), safeActionEcho(actionName, userText));
                    break;
                }
                if (!p.textOrEmpty().isEmpty()) {
                    userText = p.textOrEmpty();
                }
            }
        }

        String agentId = selectAgent(message);
        long turn = surfaceSeq.incrementAndGet();
        trace.logf("  agent selection: %s | ход #%d", agentId, turn);

        drain(sessionId); // сбросить слоты сессии перед ходом

        // Явно выбранный verbatim-агент отвечает пользователю напрямую: его
        // текст и его A2UI уходят в браузер нетронутыми. Гонять локальную модель
        // поверх готового ответа значило бы пересказать его и добавить свою
        // задержку к задержке удалённого агента.
        Remote remote = reg.get(agentId).orElse(null);
        if (remote != null && !agentId.equals(AUTO_AGENT_ID) && remote.verbatim()) {
            trace.logf("  verbatim agent \"%s\" → no local LLM", agentId);
            try {
                Remote.Reply reply;
                if (!actionName.isEmpty()) {
                    // Поверхность возвращается агенту под его собственным
                    // именем: в ленте она переименована по ходам, а сопоставить
                    // событие агент может только со своим id.
                    String surface = Surfaces.untag(actionSurface);
                    trace.logf("  A2UI action \"%s\" → событие агенту | surface=\"%s\" source=\"%s\" ctx=%s",
                            actionName, surface, actionSource, redactCard(actionCtx));
                    reply = remote.askAction(sessionId, actionName, surface, actionSource, actionCtx, a2uiActive);
                } else {
                    reply = remote.ask(sessionId, userText, a2uiActive);
                }
                return emit(reply.text(), reply.widgets(), reply.a2ui(), reply.files(),
                        a2uiActive, turn, "verbatim", reqStart);
            } catch (RuntimeException e) {
                trace.logf("  ✖ verbatim turn failed: %s", e.getMessage());
                return emit(agentErrorText(remote.name(), e), List.of(), List.of(), List.of(),
                        a2uiActive, turn, "verbatim error", reqStart);
            }
        }

        // Кнопка на зависшем HITL-шаге продолжает задачу НАПРЯМУЮ каноническим
        // ответом, минуя модель: та пересказывает «да» целым предложением, и
        // fail-closed парсер воркера такой ответ отвергает, а данные карты через
        // модель вообще проходить не должны.
        boolean directResume = actionName.equals("approve_refund")
                || actionName.equals("decline_refund")
                || actionName.equals("submit_refund_details");
        OrdersClient pendingClient = directResume ? pendingClient(agentId, sessionId) : null;
        if (pendingClient != null) {
            String canonical = actionToText(actionName, actionCtx);
            trace.logf("  confirmation button \"%s\" → resuming worker directly with \"%s\" (LLM bypassed)",
                    actionName, safeActionEcho(actionName, canonical));
            try {
                String result = pendingClient.ask(sessionId, canonical, a2uiActive);
                Drained d = drain(sessionId);
                return emit(stripNeedsInput(result), d.widgets(), d.a2ui(), d.files(),
                        a2uiActive, turn, "direct HITL resume", reqStart);
            } catch (RuntimeException e) {
                trace.logf("  ✖ direct resume error: %s", e.getMessage());
                return emit(agentErrorText(pendingClient.remote().name(), e), List.of(), List.of(), List.of(),
                        a2uiActive, turn, "direct resume error", reqStart);
            }
        }

        OrchestratorAgent agent;
        try {
            agent = agentFor(agentId);
        } catch (RuntimeException e) {
            trace.logf("  ✖ agent unavailable: %s", e.getMessage());
            return emit("Не удалось обратиться к агенту: " + e.getMessage(), List.of(), List.of(), List.of(),
                    a2uiActive, turn, "agent error", reqStart);
        }

        trace.logf("  · оркестратор → LLM: \"%s\"", userText);
        long llmStart = System.nanoTime();
        int[] toolCalls = {0};
        String finalText = agent.runTurn(sessionId, userText, new OrchestratorAgent.TurnListener() {
            @Override
            public void onToolCall(String name, String argsJson) {
                toolCalls[0]++;
                trace.logf("  · LLM → инструмент: %s(%s) [#%d]", name,
                        argsJson == null ? "" : argsJson.trim(), toolCalls[0]);
            }

            @Override
            public void onToolResult(String name) {
                trace.logf("  · инструмент %s → LLM: результат", name);
            }
        }, a2uiActive);
        trace.logf("  LLM finished in %dms | toolCalls=%d finalText=\"%s\"",
                (System.nanoTime() - llmStart) / 1_000_000, toolCalls[0], finalText.trim());

        Drained d = drain(sessionId);
        // Увидит ли пользователь карточку: в текстовом режиме собранные виджеты
        // до него не доедут, и подводка модели останется единственным ответом.
        boolean visual = a2uiActive && (!d.widgets().isEmpty() || !d.a2ui().isEmpty());
        String answer = pickAnswer(finalText, d.texts(), visual);
        if (!answer.equals(finalText)) {
            trace.logf("  карточки не будет, а подводка модели пуста по содержанию — "
                    + "в ленту уходит ответ агента (%d символов вместо %d)",
                    answer.length(), finalText.trim().length());
        }
        return emit(answer, d.widgets(), d.a2ui(), d.files(), a2uiActive, turn, "llm turn", reqStart);
    }

    /**
     * Читает выбранного в UI агента из метаданных сообщения. Незнакомый id молча
     * откатывается в «Авто»: браузер с устаревшим списком не должен ломать
     * разговор.
     */
    private String selectAgent(A2aMessage msg) {
        if (msg == null || msg.metadata == null) {
            return AUTO_AGENT_ID;
        }
        if (!(msg.metadata.get("agentId") instanceof String id) || id.isEmpty() || id.equals(AUTO_AGENT_ID)) {
            return AUTO_AGENT_ID;
        }
        if (reg.get(id).isEmpty()) {
            trace.logf("⚠ unknown agentId \"%s\" — falling back to auto", id);
            return AUTO_AGENT_ID;
        }
        return id;
    }

    /**
     * Агент под выбор пользователя: со всеми инструментами в режиме «Авто» и
     * ровно с одним при явном выборе. Кэшируется по выбору.
     */
    private OrchestratorAgent agentFor(String agentId) {
        // Состав агентов мог измениться из UI. Кэш собран под прежний: в нём и
        // старое описание в промпте, и инструмент удалённого агента.
        long current = reg.generation();
        if (current != gen.getAndSet(current)) {
            agents.clear();
        }

        List<OrdersClient> tools;
        String summary;
        String cacheKey;
        if (agentId.equals(AUTO_AGENT_ID)) {
            tools = reg.availableClients();
            summary = String.join("\n\n", reg.summaries());
            // Кэш «Авто» привязан к набору ДОСТУПНЫХ агентов: лежавший в момент
            // первой сборки агент иначе остался бы без инструмента навсегда,
            // хотя UI уже показывает его живым.
            TreeSet<String> names = new TreeSet<>();
            for (OrdersClient c : tools) {
                names.add(c.profile().toolName());
            }
            cacheKey = AUTO_AGENT_ID + "|" + String.join(",", names);
        } else {
            Remote remote = reg.get(agentId).orElseThrow(
                    () -> new IllegalStateException("unknown agent \"" + agentId + "\""));
            remote.connect();
            tools = List.of(reg.clientFor(agentId));
            summary = remote.profile().summary();
            cacheKey = agentId;
        }
        if (tools.isEmpty()) {
            throw new IllegalStateException("нет доступных агентов");
        }
        return agents.computeIfAbsent(cacheKey, k -> build.build(tools, summary));
    }

    /**
     * Клиент того агента, у которого для сессии висит input-required задача,
     * чтобы HITL-кнопка продолжила именно её. При явном выборе рассматривается
     * только выбранный агент.
     */
    private OrdersClient pendingClient(String agentId, String sessionId) {
        List<String> ids = agentId.equals(AUTO_AGENT_ID) ? reg.ids() : List.of(agentId);
        for (String id : ids) {
            Remote r = reg.get(id).orElse(null);
            if (r == null || r.pendingTaskId(sessionId).isEmpty()) {
                continue;
            }
            return reg.clientFor(id);
        }
        return null;
    }

    /** Всё, что собралось за ход в слотах сессии. */
    private record Drained(List<Map<String, Object>> widgets, List<Map<String, Object>> a2ui,
                           List<Remote.AttachedFile> files, List<String> texts) {}

    private Drained drain(String sessionId) {
        List<Map<String, Object>> w = widgets.remove(sessionId);
        List<Map<String, Object>> a = a2uis.remove(sessionId);
        List<Remote.AttachedFile> f = files.remove(sessionId);
        List<String> t = texts.remove(sessionId);
        return new Drained(w == null ? List.of() : w, a == null ? List.of() : a,
                f == null ? List.of() : f, t == null ? List.of() : t);
    }

    /**
     * Что показать пользователю за ход, отработанный локальной моделью.
     *
     * <p>Промпт запрещает модели называть значения: их покажет карточка, а
     * модель может ошибиться. Для нашего воркера это верно всегда — он шлёт
     * виджет на каждый ответ. Для внешнего агента нет: он вправе прислать один
     * текст, и тогда от подводки «Вот детали вашего заказа:» пользователю нет
     * никакой пользы — данные остались только в ответе агента. То же в текстовом
     * режиме, где виджеты выбрасываются на нашей стороне.
     */
    public static String pickAnswer(String llmText, List<String> agentTexts, boolean visual) {
        if (visual) {
            return llmText;
        }
        String agent = String.join("\n\n", agentTexts).trim();
        String llm = llmText == null ? "" : llmText.trim();
        return agent.isEmpty() || agent.length() <= llm.length() ? llmText : agent;
    }

    /** Канонический ответ для наших HITL-кнопок. */
    static String actionToText(String name, Map<String, Object> ctx) {
        return switch (name) {
            case "approve_refund" -> "да";
            case "decline_refund" -> "нет";
            // Номер карты из TextField формы: связка {path} разрешена рендерером
            // в момент клика. Продолжается напрямую — никогда через модель.
            case "submit_refund_details" -> ctx.get("card_number") instanceof String s ? s : "";
            default -> "Пользователь нажал действие: " + name;
        };
    }

    /**
     * Действие A2UI как фраза для агента, говорящего только текстом. У наших
     * HITL-кнопок есть канонические ответы; чужая кнопка описывается вместе с
     * контекстом — без него модель видит «нажал return_order» и не знает, какой
     * заказ.
     */
    public static String actionToPrompt(String name, Map<String, Object> ctx) {
        if (name.equals("approve_refund") || name.equals("decline_refund")
                || name.equals("submit_refund_details")) {
            return actionToText(name, ctx);
        }
        String label = ctx.get("label") instanceof String s && !s.isEmpty() ? s : name;
        TreeSet<String> details = new TreeSet<>();
        ctx.forEach((k, v) -> {
            if (!k.equals("label")) {
                details.add(k + ": " + v);
            }
        });
        String out = "Пользователь нажал кнопку «" + label + "»";
        return details.isEmpty() ? out : out + " (" + String.join(", ", details) + ")";
    }

    /**
     * Сбой хода строкой для ленты. Провалившийся ход и недоступный агент — разные
     * беды, и валить их в одну формулировку нельзя: «недоступен» отправит
     * пользователя чинить сеть там, где агент на связи и просто не справился.
     */
    public static String agentErrorText(String name, RuntimeException e) {
        if (e instanceof Remote.TurnFailedException failed) {
            String why = failed.firstLine();
            return why.isEmpty()
                    ? "Агент \"" + name + "\" не смог выполнить запрос."
                    : "Агент \"" + name + "\" не смог выполнить запрос: " + why;
        }
        return "Агент \"" + name + "\" недоступен: " + e.getMessage();
    }

    /** Копия контекста без номера карты — для трейса; исходную карту не правим. */
    private static Map<String, Object> redactCard(Map<String, Object> ctx) {
        Map<String, Object> out = new LinkedHashMap<>(ctx);
        out.remove("card_number");
        return out;
    }

    /** Текст действия для трейса: платёжные данные маскируются. */
    private static String safeActionEcho(String name, String userText) {
        return name.equals("submit_refund_details") ? Cards.mask(Cards.digits(userText)) : userText;
    }

    /** Убирает служебный префикс NEEDS_USER_INPUT из напрямую продолженного результата. */
    private static String stripNeedsInput(String s) {
        String t = s.trim();
        return t.startsWith("NEEDS_USER_INPUT:") ? t.substring("NEEDS_USER_INPUT:".length()).trim() : t;
    }

    /** Собирает части ответа и пишет итог хода в трейс. */
    private List<Part> emit(String text, List<Map<String, Object>> ws, List<Map<String, Object>> msgs,
                            List<Remote.AttachedFile> fs, boolean a2uiActive, long turn,
                            String what, long reqStart) {
        List<Part> parts = new ArrayList<>();
        String body = text == null || text.isBlank() ? "Готово." : text.trim();
        parts.add(Part.text(body));
        parts.addAll(widgetParts(a2uiActive, ws));
        parts.addAll(agentMarkupParts(a2uiActive, msgs, turn));
        parts.addAll(fileParts(fs));
        trace.logf("  → emit: completed message | %s | parts=%d requestTook=%dms",
                what, parts.size(), (System.nanoTime() - reqStart) / 1_000_000);
        return parts;
    }

    /** Доменные виджеты, отображённые в A2UI, когда расширение активно. */
    private List<Part> widgetParts(boolean a2uiActive, List<Map<String, Object>> ws) {
        if (ws.isEmpty()) {
            return List.of();
        }
        if (!a2uiActive) {
            trace.logf("  A2UI inactive — %d widget(s) dropped, text-only response", ws.size());
            return List.of();
        }
        List<Part> parts = new ArrayList<>();
        for (Map<String, Object> w : ws) {
            List<Map<String, Object>> msgs = A2ui.fromWidget(w);
            if (msgs == null) {
                continue;
            }
            trace.logf("  A2UI: widget %s → %d message(s) (%s)", w.get("_kind"), msgs.size(), A2ui.MIME_TYPE);
            parts.add(A2uiParts.message(msgs));
        }
        return parts;
    }

    /**
     * Разметка, написанная самим агентом. В отличие от виджетов её не надо
     * отображать — она уже прошла нормализацию; но каждому ходу нужна своя
     * поверхность: повторный createSurface с тем же id рендерер не переживает.
     */
    private List<Part> agentMarkupParts(boolean a2uiActive, List<Map<String, Object>> msgs, long turn) {
        if (msgs.isEmpty()) {
            return List.of();
        }
        if (!a2uiActive) {
            trace.logf("  A2UI inactive — %d agent message(s) dropped, text-only response", msgs.size());
            return List.of();
        }
        List<Map<String, Object>> retagged = Surfaces.retag(msgs, "-t" + turn);
        trace.logf("  A2UI: %d message(s) from the agent passed through (%s)", retagged.size(), A2ui.MIME_TYPE);
        return List.of(A2uiParts.message(retagged));
    }

    /** Файлы уходят как есть, независимо от A2UI: это части протокола, а не UI. */
    private static List<Part> fileParts(List<Remote.AttachedFile> fs) {
        List<Part> parts = new ArrayList<>();
        for (Remote.AttachedFile f : fs) {
            Part p = new Part();
            p.raw = Base64.getEncoder().encodeToString(f.data());
            p.filename = f.name();
            p.mediaType = f.mediaType();
            parts.add(p);
        }
        return parts;
    }
}
```

- [ ] **Step 5: Класть ответ в завершающее сообщение задачи**

В `A2aWebController.sendMessage` заменить хвост (создание артефакта) на:

```java
        List<Part> parts = executor.execute(task.contextId, message, a2uiActive);
        // Части ответа — в завершающем сообщении задачи: туда их кладёт спека
        // расширения A2UI, и оттуда их читает референсный клиент.
        A2aMessage reply = A2aMessage.forTask(A2aMessage.ROLE_AGENT, task.id, task.contextId,
                parts.toArray(new Part[0]));
        task.status = TaskStatus.of(TaskState.COMPLETED, reply);
        return JsonRpc.Response.ok(req.id, Map.of("task", task));
```

Импорт `Artifact` из контроллера убрать.

- [ ] **Step 6: Поправить `WebE2eTest`**

Тест читает части из `result.task.artifacts`. Переключить чтение на
`result.task.status.message.parts`. Действия, которые тест шлёт, обернуть в
массив (`"data":[{...}]`), как это делает фронтенд.

- [ ] **Step 7: Прогнать всё**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 8: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/ \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/agent/OrchestratorAgent.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/tui/Repl.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/
git commit -m "feat(java/web): выбор агента, verbatim-режим и ответ в завершающем сообщении"
```

---

### Task 14: `GET /api/agents`

Тот самый эндпоинт, из-за отсутствия которого браузер сейчас навсегда показывает
«список агентов недоступен»: `WebUiController` ловит `GET /**` и отдаёт на этот
путь `index.html`.

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentsController.java`
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/WebApplication.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentsControllerTest.java`

**Interfaces:**
- Consumes: `Registry.list()`.
- Produces: `GET /api/agents` → JSON-массив `{id, name, description, verbatim, available, probed}`; бин `Registry` в `WebApplication`.

**Про маршруты.** Литеральный путь у Spring специфичнее шаблона `/**`, поэтому
`/api/agents` уводит запрос от `WebUiController`. Тот же приём уже работает для
`/.well-known/agent-card.json` и `/invoke`.

- [ ] **Step 1: Написать падающий тест**

Создать `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentsControllerTest.java`:

```java
package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.common.trace.Tracer;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Список агентов для селектора в браузере. */
class AgentsControllerTest {

    @Test
    void listsEveryAgentInConfigOrder() {
        Registry reg = new Registry(List.of(
                new AgentConfig("orders", "Агент заказов", "http://a", "", "", false, "", "", AuthConfig.NONE),
                new AgentConfig("shop", "Магазин", "http://b", "", "", true, "", "Заказы магазина.", AuthConfig.NONE)),
                Tracer.noop());

        List<Registry.AgentInfo> out = new AgentsController(reg).agents();

        assertEquals(List.of("orders", "shop"), out.stream().map(Registry.AgentInfo::id).toList());
        assertEquals("Магазин", out.get(1).name());
        assertTrue(out.get(1).verbatim());
        // Проб ещё не было — «недоступен» показывать нельзя.
        assertTrue(!out.getFirst().probed() || !out.getFirst().available());
    }

    @Test
    void returnsAnEmptyArrayRatherThanNull() {
        assertEquals(List.of(), new AgentsController(new Registry(List.of(), Tracer.noop())).agents());
    }
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=AgentsControllerTest test`
Expected: FAIL — `AgentsController` не существует.

- [ ] **Step 3: Создать `AgentsController.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.orchestrator.a2a.Registry;
import org.springframework.http.MediaType;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

/**
 * Список агентов, между которыми можно переключаться в браузере (порт
 * internal/webui/agents.go).
 *
 * <p>Отвечает мгновенно: {@link Registry#list()} отдаёт последнее известное
 * состояние и лишь запускает фоновую проверку — ждать медленного агента этому
 * эндпоинту нельзя, браузер дёргает его после каждого хода.
 */
@RestController
public class AgentsController {

    private final Registry registry;

    public AgentsController(Registry registry) {
        this.registry = registry;
    }

    @GetMapping(value = "/api/agents", produces = MediaType.APPLICATION_JSON_VALUE)
    public List<Registry.AgentInfo> agents() {
        return registry.list();
    }
}
```

- [ ] **Step 4: Отдать `Registry` в контекст Spring**

В `WebApplication` добавить держатель и бин по образцу существующих:

```java
    private static Registry registryHolder;

    public static void configure(OrchestratorWebExecutor executor, AgentCard card, Tracer trace, Registry registry) {
        executorHolder = executor;
        cardHolder = card;
        traceHolder = trace;
        registryHolder = registry;
    }

    @Bean
    Registry registry() {
        return registryHolder;
    }
```

- [ ] **Step 5: Прогнать тест и модуль**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 6: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentsController.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/WebApplication.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentsControllerTest.java
git commit -m "feat(java/web): GET /api/agents — список агентов для селектора"
```

---

## Фаза D — правка списка агентов из браузера

Экран «Настройки» во фронтенде готов и ходит в пять эндпоинтов, которых в Java
нет. Базовый список остаётся в рукописном `configs/orchestrator.yaml`; правки из
UI ложатся отдельным overlay-файлом, перекрывающим записи по `id`.

### Task 15: Слияние overlay с базовым списком

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/store/AgentOverride.java`
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/store/Overlays.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/store/OverlaysTest.java`

**Interfaces:**
- Produces: `AgentOverride(AgentConfig agent, boolean hidden)`; `Overlays.merge(List<AgentConfig> base, List<AgentOverride> over) -> List<AgentConfig>`.

- [ ] **Step 1: Написать падающий тест**

```java
package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;

/** Overlay-правки поверх рукописного списка агентов. */
class OverlaysTest {

    private static AgentConfig agent(String id, String url) {
        return new AgentConfig(id, "", url, "", "", false, "", "", AuthConfig.NONE);
    }

    private static List<AgentConfig> base() {
        return List.of(agent("orders", "http://localhost:8081"), agent("ouroboros", "http://192.168.1.68:18800"));
    }

    // Перекрытие идёт целой записью и не двигает агента по списку: порядок
    // задаёт пункты селектора, и агент с новым адресом не должен прыгать в конец.
    @Test
    void overridesInPlace() {
        List<AgentConfig> got = Overlays.merge(base(),
                List.of(new AgentOverride(agent("orders", "http://127.0.0.1:9000"), false)));

        assertEquals(2, got.size());
        assertEquals("orders", got.getFirst().id());
        assertEquals("http://127.0.0.1:9000", got.getFirst().url());
        assertEquals("http://192.168.1.68:18800", got.get(1).url(), "соседа правка не трогает");
    }

    @Test
    void hiddenAgentsDropOutOfTheEffectiveList() {
        List<AgentConfig> got = Overlays.merge(base(),
                List.of(new AgentOverride(agent("ouroboros", ""), true)));
        assertEquals(List.of("orders"), got.stream().map(AgentConfig::id).toList());
    }

    @Test
    void agentsCreatedInTheUiGoToTheEnd() {
        List<AgentConfig> got = Overlays.merge(base(),
                List.of(new AgentOverride(agent("newbie", "http://c"), false)));
        assertEquals(List.of("orders", "ouroboros", "newbie"), got.stream().map(AgentConfig::id).toList());
    }

    @Test
    void emptyOverlayChangesNothing() {
        assertEquals(base(), Overlays.merge(base(), List.of()));
    }
}
```

- [ ] **Step 2: Прогнать и убедиться в падении**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=OverlaysTest test`
Expected: FAIL — типов нет.

- [ ] **Step 3: Создать `AgentOverride.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;

/**
 * Одна overlay-запись: агент целиком плюс признак «скрыт».
 *
 * <p>{@code hidden} живёт здесь, а не в {@link AgentConfig}: он описывает не
 * агента, а решение UI убрать его из списка. Агент, заведённый в YAML, иначе не
 * удаляется — базовый файл мы не трогаем.
 */
public record AgentOverride(AgentConfig agent, boolean hidden) {

    public String id() {
        return agent.id();
    }
}
```

- [ ] **Step 4: Создать `Overlays.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Слияние overlay-правок с базовым списком агентов. */
public final class Overlays {

    private Overlays() {}

    /**
     * Накладывает overlay на базовый список.
     *
     * <p>Запись с совпавшим id заменяет базовую целиком и остаётся на её месте:
     * порядок задаёт пункты селектора в браузере и выбор агента для
     * терминального REPL, и менять его из-за правки адреса нельзя. Записи с
     * незнакомыми id — это заведённые через UI агенты, они уходят в конец в
     * порядке overlay.
     */
    public static List<AgentConfig> merge(List<AgentConfig> base, List<AgentOverride> over) {
        Map<String, AgentOverride> byId = new LinkedHashMap<>(over.size());
        for (AgentOverride o : over) {
            byId.put(o.id(), o);
        }
        Set<String> used = new HashSet<>(over.size());
        List<AgentConfig> out = new ArrayList<>(base.size() + over.size());
        for (AgentConfig b : base) {
            AgentOverride o = byId.get(b.id());
            if (o == null) {
                out.add(b);
                continue;
            }
            used.add(o.id());
            if (!o.hidden()) {
                out.add(o.agent());
            }
        }
        for (AgentOverride o : over) {
            if (!used.contains(o.id()) && !o.hidden()) {
                out.add(o.agent());
            }
        }
        return out;
    }
}
```

- [ ] **Step 5: Прогнать тест и закоммитить**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=OverlaysTest test`
Expected: PASS

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/store/ \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/store/OverlaysTest.java
git commit -m "feat(java/store): слияние overlay с базовым списком агентов"
```

---

### Task 16: Чтение и атомарная запись overlay-файла

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/store/OverlayFile.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/store/OverlayFileTest.java`

**Interfaces:**
- Produces: `OverlayFile.load(Path) -> List<AgentOverride>`, `OverlayFile.save(Path, List<AgentOverride>)`.

**Форма файла та же, что читает Go** — `agents:` со списком записей, ключи в
snake_case (`card_path`), `hidden` у скрытых.

- [ ] **Step 1: Написать падающий тест**

```java
package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Overlay-файл: машинный, читается Go и Java одинаково. */
class OverlayFileTest {

    // Отсутствие файла — обычное состояние свежего клона, а не ошибка.
    @Test
    void missingFileIsAnEmptyOverlay(@TempDir Path dir) {
        assertEquals(List.of(), OverlayFile.load(dir.resolve("agents.local.yaml")));
    }

    @Test
    void roundTripsThroughTheGoFileFormat(@TempDir Path dir) throws IOException {
        Path p = dir.resolve("nested/agents.local.yaml");
        List<AgentOverride> over = List.of(
                new AgentOverride(new AgentConfig("orders", "Мой воркер", "http://127.0.0.1:9000",
                        "/.well-known/agent.json", "shop", true, "180s", "Заказы.",
                        new AuthConfig("basic", "u", "секрет")), false),
                new AgentOverride(new AgentConfig("ouroboros", "", "", "", "", false, "", "", AuthConfig.NONE), true));

        OverlayFile.save(p, over);

        String yaml = Files.readString(p);
        assertTrue(yaml.startsWith("#"), "шапка объясняет, что файл машинный: " + yaml);
        assertTrue(yaml.contains("card_path:"), "ключи в snake_case, как в Go: " + yaml);
        assertTrue(yaml.contains("hidden: true"));

        List<AgentOverride> back = OverlayFile.load(p);
        assertEquals(over, back);
    }

    @Test
    void overwritesTheWholeFile(@TempDir Path dir) {
        Path p = dir.resolve("agents.local.yaml");
        OverlayFile.save(p, List.of(new AgentOverride(
                new AgentConfig("a", "", "http://a", "", "", false, "", "", AuthConfig.NONE), false)));
        OverlayFile.save(p, List.of());
        assertEquals(List.of(), OverlayFile.load(p));
    }

    // Битый overlay — отказ, а не молчаливое игнорирование: агенты, заведённые
    // через UI, не должны исчезать незаметно.
    @Test
    void refusesABrokenFile(@TempDir Path dir) throws IOException {
        Path p = dir.resolve("agents.local.yaml");
        Files.writeString(p, "agents: [ этот список не закрыт");
        assertThrows(IllegalStateException.class, () -> OverlayFile.load(p));
    }
}
```

- [ ] **Step 2: Прогнать и убедиться в падении**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=OverlayFileTest test`
Expected: FAIL — класса нет.

- [ ] **Step 3: Добавить snakeyaml в зависимости оркестратора**

`java/orchestrator/pom.xml` — дописать рядом с существующими:

```xml
    <dependency>
      <groupId>org.yaml</groupId>
      <artifactId>snakeyaml</artifactId>
    </dependency>
```

(Версия управляется родителем Spring Boot; общий модуль уже её использует.)

- [ ] **Step 4: Создать `OverlayFile.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import org.yaml.snakeyaml.DumperOptions;
import org.yaml.snakeyaml.Yaml;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** Overlay-файл со списком агентов: чтение и атомарная запись. */
public final class OverlayFile {

    private OverlayFile() {}

    /** Объясняет тому, кто откроет файл руками, почему его правки проживут до первого сохранения из UI. */
    private static final String HEADER = """
            # Управляется из веб-интерфейса (экран «Настройки»).
            # Правки руками будут перезаписаны при следующем сохранении.
            """;

    /** Читает overlay. Отсутствие файла — обычное состояние свежего клона, а не ошибка. */
    @SuppressWarnings("unchecked")
    public static List<AgentOverride> load(Path path) {
        if (!Files.exists(path)) {
            return List.of();
        }
        Map<String, Object> doc;
        try (InputStream in = Files.newInputStream(path)) {
            doc = new Yaml().load(in);
        } catch (IOException e) {
            throw new IllegalStateException("read agents overlay " + path + ": " + e.getMessage(), e);
        } catch (RuntimeException e) {
            throw new IllegalStateException("parse agents overlay " + path + ": " + e.getMessage(), e);
        }
        if (doc == null || !(doc.get("agents") instanceof List<?> list)) {
            return List.of();
        }
        List<AgentOverride> out = new ArrayList<>(list.size());
        for (Object item : list) {
            if (!(item instanceof Map<?, ?> raw)) {
                continue;
            }
            Map<String, Object> a = (Map<String, Object>) raw;
            Map<String, Object> auth = a.get("auth") instanceof Map<?, ?> m
                    ? (Map<String, Object>) m : Map.of();
            out.add(new AgentOverride(new AgentConfig(
                    str(a, "id"), str(a, "name"), str(a, "url"), str(a, "card_path"), str(a, "skill"),
                    Boolean.TRUE.equals(a.get("verbatim")), str(a, "timeout"), str(a, "description"),
                    new AuthConfig(str(auth, "type"), str(auth, "username"), str(auth, "password"))),
                    Boolean.TRUE.equals(a.get("hidden"))));
        }
        return List.copyOf(out);
    }

    /**
     * Пишет overlay целиком, атомарно: сначала временный файл рядом, потом
     * переименование. Оборванная запись иначе оставила бы половину списка
     * агентов, и оркестратор не поднялся бы вовсе.
     */
    public static void save(Path path, List<AgentOverride> over) {
        List<Object> agents = new ArrayList<>(over.size());
        for (AgentOverride o : over) {
            AgentConfig a = o.agent();
            Map<String, Object> m = new LinkedHashMap<>();
            m.put("id", a.id());
            m.put("name", a.name());
            m.put("url", a.url());
            m.put("card_path", a.cardPath());
            m.put("skill", a.skill());
            m.put("verbatim", a.verbatim());
            m.put("timeout", a.timeout());
            m.put("description", a.description());
            m.put("auth", new LinkedHashMap<>(Map.of(
                    "type", a.auth().type(), "username", a.auth().username(), "password", a.auth().password())));
            if (o.hidden()) {
                m.put("hidden", true);
            }
            agents.add(m);
        }
        DumperOptions opts = new DumperOptions();
        opts.setDefaultFlowStyle(DumperOptions.FlowStyle.BLOCK);
        String body = new Yaml(opts).dump(Map.of("agents", agents));

        Path dir = path.toAbsolutePath().getParent();
        Path tmp = null;
        try {
            Files.createDirectories(dir);
            // Временный файл — в том же каталоге: переименование атомарно только
            // в пределах одной файловой системы.
            tmp = Files.createTempFile(dir, ".agents-", ".yaml");
            try {
                // Пароли лежат открытым текстом, как и в orchestrator.yaml.
                Files.setPosixFilePermissions(tmp, PosixFilePermissions.fromString("rw-------"));
            } catch (UnsupportedOperationException | IOException ignored) {
                // не POSIX-система — пропускаем
            }
            Files.writeString(tmp, HEADER + body, StandardCharsets.UTF_8);
            Files.move(tmp, path, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            tmp = null;
        } catch (IOException e) {
            throw new IllegalStateException("write agents overlay " + path + ": " + e.getMessage(), e);
        } finally {
            if (tmp != null) {
                try {
                    Files.deleteIfExists(tmp);
                } catch (IOException ignored) {
                    // временный файл останется — на работу это не влияет
                }
            }
        }
    }

    private static String str(Map<String, Object> m, String key) {
        Object v = m.get(key);
        return v == null ? "" : String.valueOf(v);
    }
}
```

- [ ] **Step 5: Прогнать тест и закоммитить**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=OverlayFileTest test`
Expected: PASS

```bash
git add java/orchestrator/pom.xml \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/store/OverlayFile.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/store/OverlayFileTest.java
git commit -m "feat(java/store): чтение и атомарная запись overlay-файла"
```

---

### Task 17: AgentStore — CRUD над списком агентов

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/store/AgentStore.java`
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/store/AgentStoreTest.java`

**Interfaces:**
- Consumes: `Overlays.merge`, `OverlayFile`, `ConfigLoader.normalizeAgents`, `AgentConfig.validate`.
- Produces: `AgentStore(List<AgentConfig> base, Path overlayPath)`; методы `onChange(Consumer<List<AgentConfig>>)`, `agents()`, `records()`, `create(AgentConfig)`, `update(String id, AgentConfig)`, `delete(String id)`, `reset(String id)`; запись `AgentStore.Record`; исключения `AgentStore.NotFoundException`, `AgentStore.ExistsException`; константы `SOURCE_FILE`, `SOURCE_UI`.

- [ ] **Step 1: Написать падающий тест**

```java
package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Правки списка агентов, сделанные из UI. */
class AgentStoreTest {

    private static AgentConfig agent(String id, String url) {
        return new AgentConfig(id, "", url, "", "", false, "", "", AuthConfig.NONE);
    }

    private static List<AgentConfig> base() {
        return List.of(agent("orders", "http://localhost:8081"));
    }

    private static AgentStore store(Path dir) {
        return new AgentStore(base(), dir.resolve("agents.local.yaml"));
    }

    @Test
    void createsAndPersists(@TempDir Path dir) {
        AgentStore s = store(dir);
        s.create(agent("shop", "http://b"));

        assertEquals(List.of("orders", "shop"), s.agents().stream().map(AgentConfig::id).toList());
        assertEquals(List.of("orders", "shop"),
                new AgentStore(base(), dir.resolve("agents.local.yaml")).agents()
                        .stream().map(AgentConfig::id).toList());
    }

    @Test
    void refusesAnIdThatIsAlreadyTaken(@TempDir Path dir) {
        assertThrows(AgentStore.ExistsException.class, () -> store(dir).create(agent("orders", "http://x")));
    }

    // Пустой пароль означает «не менять»: прочитать текущий браузер не может, и
    // форма шлёт пустое поле каждый раз, когда пароль не трогали.
    @Test
    void anEmptyPasswordMeansKeepTheCurrentOne(@TempDir Path dir) {
        AgentStore s = new AgentStore(
                List.of(new AgentConfig("orders", "", "http://a", "", "", false, "", "",
                        new AuthConfig("basic", "u", "секрет"))),
                dir.resolve("agents.local.yaml"));

        s.update("orders", new AgentConfig("orders", "Новое имя", "http://a", "", "", false, "", "",
                new AuthConfig("basic", "u", "")));

        assertEquals("секрет", s.agents().getFirst().auth().password());
    }

    // Агент из YAML не удаляется, а помечается скрытым: базовый файл рукописный.
    @Test
    void deletingAFileAgentHidesIt(@TempDir Path dir) {
        AgentStore s = store(dir);
        s.delete("orders");

        assertEquals(List.of(), s.agents());
        AgentStore.Record rec = s.records().getFirst();
        assertTrue(rec.hidden(), "запись остаётся видимой на экране настроек, чтобы агента можно было вернуть");
        assertTrue(rec.inFile());
    }

    @Test
    void deletingAUiAgentRemovesItCompletely(@TempDir Path dir) {
        AgentStore s = store(dir);
        s.create(agent("shop", "http://b"));
        s.delete("shop");
        assertEquals(1, s.records().size());
    }

    // Сбрасывать некуда, если базовой версии нет: для агента, заведённого через
    // UI, «сброс» означал бы безвозвратное удаление — это дело delete.
    @Test
    void resetOnlyWorksForAgentsThatHaveAFileVersion(@TempDir Path dir) {
        AgentStore s = store(dir);
        s.update("orders", agent("orders", "http://127.0.0.1:9000"));
        s.reset("orders");
        assertEquals("http://localhost:8081", s.agents().getFirst().url());

        s.create(agent("shop", "http://b"));
        assertThrows(AgentStore.NotFoundException.class, () -> s.reset("shop"));
    }

    @Test
    void recordsNeverCarryThePasswordButSayWhetherItIsSet(@TempDir Path dir) {
        AgentStore s = new AgentStore(
                List.of(new AgentConfig("orders", "", "http://a", "", "", false, "", "",
                        new AuthConfig("basic", "u", "секрет"))),
                dir.resolve("agents.local.yaml"));

        AgentStore.Record rec = s.records().getFirst();
        assertEquals("", rec.agent().auth().password(), "пароль наружу не уходит никогда");
        assertTrue(rec.hasPassword());
        assertEquals(AgentStore.SOURCE_FILE, rec.source());
        assertFalse(rec.hidden());
    }

    @Test
    void notifiesTheSubscriberOnEveryChange(@TempDir Path dir) {
        AgentStore s = store(dir);
        List<List<AgentConfig>> seen = new ArrayList<>();
        s.onChange(seen::add);

        s.create(agent("shop", "http://b"));
        s.delete("shop");

        assertEquals(2, seen.size());
        assertEquals(List.of("orders"), seen.getLast().stream().map(AgentConfig::id).toList());
    }

    @Test
    void refusesAnInvalidRecordAndKeepsTheOldState(@TempDir Path dir) {
        AgentStore s = store(dir);
        assertThrows(IllegalArgumentException.class, () -> s.create(agent("shop", "")));
        assertEquals(List.of("orders"), s.agents().stream().map(AgentConfig::id).toList());
    }
}
```

- [ ] **Step 2: Прогнать и убедиться в падении**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=AgentStoreTest test`
Expected: FAIL — класса нет.

- [ ] **Step 3: Создать `AgentStore.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.ConfigLoader;

import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Consumer;

/**
 * Владеет списком агентов: рукописной базой из orchestrator.yaml и
 * overlay-файлом с правками из UI (порт internal/agentstore).
 *
 * <p>Базовый список остаётся рукописным: в нём живут комментарии, объясняющие
 * каждого агента, и переписывать его машинно значило бы их потерять.
 */
public class AgentStore {

    /** Только из orchestrator.yaml, overlay-записи нет. */
    public static final String SOURCE_FILE = "file";

    /** Есть overlay-запись, сделанная из интерфейса. */
    public static final String SOURCE_UI = "ui";

    /** Агента с таким id нет. Отдельный тип: обработчику нужен 404, а не 400. */
    public static class NotFoundException extends RuntimeException {
        public NotFoundException(String id) {
            super("agent not found: \"" + id + "\"");
        }
    }

    /** Агент с таким id уже есть — 409, а не 400. */
    public static class ExistsException extends RuntimeException {
        public ExistsException(String id) {
            super("agent already exists: \"" + id + "\"");
        }
    }

    /**
     * Запись для экрана настроек: действующий конфиг агента плюс происхождение.
     * Пароль сюда не попадает никогда — только признак, что он задан.
     *
     * @param envLocked поля, перекрытые окружением: "url", "password"
     * @param inFile    есть ли у агента версия в базовом (рукописном) списке.
     *                  {@code source} говорит лишь о наличии overlay-записи, а её
     *                  наличие означает разное для файлового агента с правкой из
     *                  UI (сброс вернёт версию из YAML) и для агента, целиком
     *                  заведённого через UI (базовой версии нет вовсе).
     */
    public record Record(AgentConfig agent, boolean hidden, String source,
                         boolean hasPassword, List<String> envLocked, boolean inFile) {}

    private final Path overlayPath;
    private final Object lock = new Object();

    private final List<AgentConfig> base;
    private List<AgentOverride> over;
    private List<AgentConfig> merged;
    private Consumer<List<AgentConfig>> onChange;

    /**
     * Читает overlay и складывает его с базовым списком. Битый overlay — отказ на
     * старте, а не молчаливое игнорирование: агенты, заведённые через UI, не
     * должны исчезать незаметно.
     */
    public AgentStore(List<AgentConfig> base, Path overlayPath) {
        this.base = List.copyOf(base);
        this.overlayPath = overlayPath;
        this.over = OverlayFile.load(overlayPath);
        this.merged = mergeNormalized(this.over);
    }

    /**
     * Задаёт подписчика, которому уезжает новый список после каждой удачной
     * правки. Через него живой реестр узнаёт об изменениях.
     */
    public void onChange(Consumer<List<AgentConfig>> fn) {
        synchronized (lock) {
            onChange = fn;
        }
    }

    /** Действующий список — тот, по которому живёт реестр. */
    public List<AgentConfig> agents() {
        synchronized (lock) {
            return merged;
        }
    }

    /**
     * Складывает базу с overlay и прогоняет через ту же нормализацию, что и
     * загрузка конфига, — так env-перекрытия остаются последним словом и после
     * правок из UI.
     */
    private List<AgentConfig> mergeNormalized(List<AgentOverride> o) {
        return ConfigLoader.normalizeAgents(Overlays.merge(base, o));
    }

    /**
     * Описывает агентов для экрана настроек, включая скрытых: вернуть удалённого
     * агента иначе можно было бы только правкой файла.
     */
    public List<Record> records() {
        synchronized (lock) {
            Map<String, AgentOverride> byId = new LinkedHashMap<>(over.size());
            for (AgentOverride o : over) {
                byId.put(o.id(), o);
            }
            List<Record> out = new ArrayList<>(base.size() + over.size());
            Set<String> seen = new HashSet<>(over.size());
            for (AgentConfig b : base) {
                AgentOverride o = byId.get(b.id());
                if (o != null) {
                    seen.add(o.id());
                    out.add(record(o.agent(), o.hidden(), true));
                } else {
                    out.add(record(b, false, false));
                }
            }
            for (AgentOverride o : over) {
                if (!seen.contains(o.id())) {
                    out.add(record(o.agent(), o.hidden(), true));
                }
            }
            return out;
        }
    }

    private Record record(AgentConfig a, boolean hidden, boolean fromOverlay) {
        List<String> envLocked = new ArrayList<>(2);
        // Показываем действующее значение, а не то, что лежит в файле: адрес мог
        // быть перекрыт окружением, и правка такого поля ничего не даст.
        String urlEnv = System.getenv(AgentConfig.envVar(a.id(), "URL"));
        if (urlEnv != null && !urlEnv.isBlank()) {
            a = a.withUrl(urlEnv);
            envLocked.add("url");
        }
        boolean hasPassword = !a.auth().password().isEmpty();
        if (System.getenv(AgentConfig.envVar(a.id(), "PASSWORD")) != null) {
            hasPassword = true;
            envLocked.add("password");
        }
        return new Record(a.withPassword(""), hidden, fromOverlay ? SOURCE_UI : SOURCE_FILE,
                hasPassword, List.copyOf(envLocked), inBase(a.id()));
    }

    /**
     * Общий хвост всех мутаций: пересчитать, записать файл, зафиксировать
     * состояние и позвать подписчика. Пока запись не удалась, состояние прежнее.
     */
    private void apply(List<AgentOverride> next) {
        List<AgentConfig> nextMerged = mergeNormalized(next);
        OverlayFile.save(overlayPath, next);
        over = next;
        merged = nextMerged;
        // Подписчик зовётся под блокировкой хранилища: так правки доезжают до
        // реестра ровно в том порядке, в каком применялись. Отсюда ограничение —
        // подписчику нельзя ходить обратно в AgentStore. Единственный подписчик,
        // Registry.apply, туда и не ходит.
        if (onChange != null) {
            onChange.accept(nextMerged);
        }
    }

    private boolean inBase(String id) {
        return base.stream().anyMatch(a -> a.id().equals(id));
    }

    private int indexOver(List<AgentOverride> list, String id) {
        for (int i = 0; i < list.size(); i++) {
            if (list.get(i).id().equals(id)) {
                return i;
            }
        }
        return -1;
    }

    private boolean known(String id) {
        return inBase(id) || indexOver(over, id) >= 0;
    }

    /**
     * Заводит нового агента. Занятый id — ошибка: правка существующего идёт
     * через {@link #update}, и молча перетереть его нельзя.
     */
    public void create(AgentConfig a) {
        synchronized (lock) {
            AgentConfig.validate(a);
            if (known(a.id())) {
                throw new ExistsException(a.id());
            }
            List<AgentOverride> next = new ArrayList<>(over);
            next.add(new AgentOverride(a, false));
            apply(next);
        }
    }

    /**
     * Заменяет запись агента целиком. Пустой пароль означает «не менять».
     *
     * <p>Существование id проверяется раньше валидации: здесь id указывает на уже
     * существующего агента и обязан быть валиден по построению — если его нет,
     * «нет такого агента» и есть содержательная причина отказа.
     */
    public void update(String id, AgentConfig a) {
        synchronized (lock) {
            if (!known(id)) {
                throw new NotFoundException(id);
            }
            a = a.withId(id);
            AgentConfig.validate(a);
            // Пароль и адрес, перекрытые окружением, в overlay не пишем: форма
            // показывает действующее (env-) значение, и если переносить его в
            // файл как есть, секрет из переменной осел бы открытым текстом на
            // диске, а адрес конкретного контейнера заморозился бы в конфиге.
            if (a.auth().password().isEmpty() && System.getenv(AgentConfig.envVar(id, "PASSWORD")) == null) {
                a = a.withPassword(currentPassword(id));
            }
            if (System.getenv(AgentConfig.envVar(id, "URL")) != null) {
                a = a.withUrl(currentUrl(id));
            }
            List<AgentOverride> next = new ArrayList<>(over);
            int i = indexOver(next, id);
            if (i >= 0) {
                next.set(i, new AgentOverride(a, false));
            } else {
                next.add(new AgentOverride(a, false));
            }
            apply(next);
        }
    }

    /** Действующий пароль агента — из overlay, иначе из базы. */
    private String currentPassword(String id) {
        int i = indexOver(over, id);
        if (i >= 0) {
            return over.get(i).agent().auth().password();
        }
        return base.stream().filter(a -> a.id().equals(id)).findFirst()
                .map(a -> a.auth().password()).orElse("");
    }

    /** Адрес агента, каким он был в overlay/базе ДО текущей правки. */
    private String currentUrl(String id) {
        int i = indexOver(over, id);
        if (i >= 0) {
            return over.get(i).agent().url();
        }
        return base.stream().filter(a -> a.id().equals(id)).findFirst().map(AgentConfig::url).orElse("");
    }

    /**
     * Убирает агента. Заведённый через UI исчезает совсем, пришедший из YAML
     * помечается скрытым: базовый файл рукописный, и мы его не трогаем.
     */
    public void delete(String id) {
        synchronized (lock) {
            if (!known(id)) {
                throw new NotFoundException(id);
            }
            List<AgentOverride> next = new ArrayList<>(over);
            int i = indexOver(next, id);
            AgentOverride hidden = new AgentOverride(
                    new AgentConfig(id, "", "", "", "", false, "", "", null), true);
            if (inBase(id)) {
                if (i >= 0) {
                    next.set(i, hidden);
                } else {
                    next.add(hidden);
                }
            } else {
                next.remove(i);
            }
            apply(next);
        }
    }

    /**
     * Забывает overlay-запись: агент возвращается к версии из YAML, а скрытый —
     * в список. Без базовой версии сбрасывать некуда: агент, целиком заведённый
     * через UI, при таком «сбросе» просто исчез бы — это дело {@link #delete}.
     */
    public void reset(String id) {
        synchronized (lock) {
            int i = indexOver(over, id);
            if (!inBase(id) || i < 0) {
                throw new NotFoundException(id);
            }
            List<AgentOverride> next = new ArrayList<>(over);
            next.remove(i);
            apply(next);
        }
    }
}
```

**Внимание:** скрытая запись строится с `null` в поле `auth` — конструктор
`AgentConfig` подставит `AuthConfig.NONE`. Скрытая запись не проходит
`validate` и не должна: она не агент, а отметка «этого агента в списке нет»,
и `Overlays.merge` выбрасывает её до нормализации.

- [ ] **Step 4: Прогнать тест и закоммитить**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=AgentStoreTest test`
Expected: PASS

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/store/AgentStore.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/store/AgentStoreTest.java
git commit -m "feat(java/store): CRUD над списком агентов поверх overlay"
```

---

### Task 18: HTTP-API экрана настроек

**Files:**
- Create: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentConfigController.java`
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/WebApplication.java` (бин `AgentStore`)
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentConfigControllerTest.java`

**Interfaces:**
- Consumes: `AgentStore` (задача 17).
- Produces: `GET /api/agents/config`, `POST /api/agents/config`, `PUT /api/agents/config/{id}`, `DELETE /api/agents/config/{id}`, `POST /api/agents/config/{id}/reset`.

**Форма ответа задана фронтендом** (`web/src/agent-settings.ts:9-20`): поля
camelCase — `cardPath`, `envLocked`, `canReset`, `auth.hasPassword`, `source`
(`"file"` | `"ui"`), `hidden`.

**Безопасность.** Эндпоинты ничем не защищены — как и `/invoke`: это демо-стенд.
Значит, режим `--web` нельзя выставлять за пределы доверенной сети: через этот
API можно подменить адрес агента и увести туда весь разговор. То же ограничение
записано в Go (`internal/webui/agentconfig.go`).

- [ ] **Step 1: Написать падающий тест**

```java
package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.orchestrator.store.AgentStore;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;

import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Пять эндпоинтов экрана «Настройки». */
class AgentConfigControllerTest {

    private static AgentConfigController controller(Path dir) {
        return new AgentConfigController(new AgentStore(
                List.of(new AgentConfig("orders", "Агент заказов", "http://a", "", "", false, "", "",
                        new AuthConfig("basic", "u", "секрет"))),
                dir.resolve("agents.local.yaml")));
    }

    private static AgentConfigController.AgentIn in(String id, String url) {
        AgentConfigController.AgentIn a = new AgentConfigController.AgentIn();
        a.id = id;
        a.url = url;
        return a;
    }

    @Test
    void listNeverLeaksThePasswordAndDescribesResettability(@TempDir Path dir) {
        List<AgentConfigController.AgentOut> out = controller(dir).list();
        AgentConfigController.AgentOut a = out.getFirst();

        assertEquals("orders", a.id);
        assertEquals(AgentStore.SOURCE_FILE, a.source);
        assertTrue(a.auth.hasPassword, "признак «пароль задан» есть");
        assertEquals(List.of(), a.envLocked);
        // Сбрасывать нечего: агент из файла ещё не правился.
        assertFalse(a.canReset);
    }

    @Test
    void canResetOnlyWhenThereIsBothAFileVersionAndAnEdit(@TempDir Path dir) {
        AgentConfigController c = controller(dir);
        c.update("orders", in("orders", "http://127.0.0.1:9000"));
        assertTrue(c.list().getFirst().canReset);

        c.reset("orders");
        assertFalse(c.list().getFirst().canReset);
    }

    @Test
    void createReturns201AndConflictOnADuplicate(@TempDir Path dir) {
        AgentConfigController c = controller(dir);
        assertEquals(HttpStatus.CREATED, c.create(in("shop", "http://b")).getStatusCode());
        assertEquals(HttpStatus.CONFLICT, c.create(in("shop", "http://b")).getStatusCode());
    }

    @Test
    void badRecordIsARequestError(@TempDir Path dir) {
        assertEquals(HttpStatus.BAD_REQUEST, controller(dir).create(in("shop", "")).getStatusCode());
    }

    @Test
    void missingAgentIsANotFound(@TempDir Path dir) {
        AgentConfigController c = controller(dir);
        assertEquals(HttpStatus.NOT_FOUND, c.update("nope", in("nope", "http://x")).getStatusCode());
        assertEquals(HttpStatus.NOT_FOUND, c.delete("nope").getStatusCode());
        assertEquals(HttpStatus.NOT_FOUND, c.reset("nope").getStatusCode());
    }

    @Test
    void deleteReturns204(@TempDir Path dir) {
        assertEquals(HttpStatus.NO_CONTENT, controller(dir).delete("orders").getStatusCode());
    }
}
```

- [ ] **Step 2: Прогнать и убедиться в падении**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=AgentConfigControllerTest test`
Expected: FAIL — контроллера нет.

- [ ] **Step 3: Создать `AgentConfigController.java`**

```java
package io.github.kmpavloff.a2ademo.orchestrator.web;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.orchestrator.store.AgentStore;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * Правка списка агентов из браузера (порт internal/webui/agentconfig.go).
 *
 * <p>Эндпоинты ничем не защищены — как и {@code /invoke}: это демо-стенд.
 * Значит, режим {@code --web} нельзя выставлять за пределы доверенной сети:
 * через этот API можно подменить адрес агента и увести туда весь разговор.
 */
@RestController
public class AgentConfigController {

    /**
     * Форма записи в ответе. Отдельный от входного тип, и это не дублирование
     * ради красоты: у него просто нет поля пароля, поэтому вернуть пароль наружу
     * нельзя даже по невнимательности.
     */
    @JsonInclude(JsonInclude.Include.ALWAYS)
    public static class AgentOut {
        public String id;
        public String name;
        public String url;
        public String cardPath;
        public String skill;
        public boolean verbatim;
        public String timeout;
        public String description;
        public AuthOut auth = new AuthOut();
        public boolean hidden;
        public String source;
        public List<String> envLocked = List.of();
        /**
         * Есть ли что сбрасывать: базовая версия в orchestrator.yaml, к которой
         * можно вернуться, И правка в overlay, которую для этого надо забыть.
         * Оба условия нужны. У агента, целиком заведённого через UI, нет
         * первого: «сброс» означал бы безвозвратное удаление, а это дело кнопки
         * «Удалить» с подтверждением. У нетронутого агента из файла нет второго —
         * он и так равен своей версии из конфига.
         */
        public boolean canReset;
    }

    @JsonInclude(JsonInclude.Include.ALWAYS)
    public static class AuthOut {
        public String type = "";
        public String username = "";
        public boolean hasPassword;
    }

    /**
     * То, что присылает форма. Пустой password означает «не менять»: прочитать
     * текущий браузер не может, и форма шлёт пустое поле каждый раз, когда
     * пароль не трогали.
     */
    public static class AgentIn {
        public String id = "";
        public String name = "";
        public String url = "";
        public String cardPath = "";
        public String skill = "";
        public boolean verbatim;
        public String timeout = "";
        public String description = "";
        public AuthIn auth = new AuthIn();

        AgentConfig toConfig() {
            return new AgentConfig(id, name, url, cardPath, skill, verbatim, timeout, description,
                    new AuthConfig(auth.type, auth.username, auth.password));
        }
    }

    public static class AuthIn {
        public String type = "";
        public String username = "";
        public String password = "";
    }

    private final AgentStore store;

    public AgentConfigController(AgentStore store) {
        this.store = store;
    }

    @GetMapping(value = "/api/agents/config", produces = MediaType.APPLICATION_JSON_VALUE)
    public List<AgentOut> list() {
        List<AgentOut> out = new ArrayList<>();
        for (AgentStore.Record r : store.records()) {
            out.add(toOut(r));
        }
        return out;
    }

    @PostMapping(value = "/api/agents/config", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<Object> create(@RequestBody AgentIn in) {
        return result(HttpStatus.CREATED, () -> store.create(in.toConfig()));
    }

    @PutMapping(value = "/api/agents/config/{id}", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<Object> update(@PathVariable String id, @RequestBody AgentIn in) {
        return result(HttpStatus.OK, () -> store.update(id, in.toConfig()));
    }

    @DeleteMapping(value = "/api/agents/config/{id}", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<Object> delete(@PathVariable String id) {
        return result(HttpStatus.NO_CONTENT, () -> store.delete(id));
    }

    @PostMapping(value = "/api/agents/config/{id}/reset", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<Object> reset(@PathVariable String id) {
        return result(HttpStatus.OK, () -> store.reset(id));
    }

    private static AgentOut toOut(AgentStore.Record r) {
        AgentConfig a = r.agent();
        AgentOut o = new AgentOut();
        o.id = a.id();
        o.name = a.name();
        o.url = a.url();
        o.cardPath = a.cardPath();
        o.skill = a.skill();
        o.verbatim = a.verbatim();
        o.timeout = a.timeout();
        o.description = a.description();
        o.auth.type = a.auth().type();
        o.auth.username = a.auth().username();
        o.auth.hasPassword = r.hasPassword();
        o.hidden = r.hidden();
        o.source = r.source();
        o.envLocked = r.envLocked();
        o.canReset = r.inFile() && r.source().equals(AgentStore.SOURCE_UI);
        return o;
    }

    /**
     * Переводит ошибку хранилища в код ответа: «нет такого» и «уже есть» — не то
     * же самое, что кривая запись, и форма показывает их по-разному.
     */
    private static ResponseEntity<Object> result(HttpStatus okStatus, Runnable mutation) {
        try {
            mutation.run();
        } catch (AgentStore.NotFoundException e) {
            return ResponseEntity.status(HttpStatus.NOT_FOUND).body(Map.of("error", e.getMessage()));
        } catch (AgentStore.ExistsException e) {
            return ResponseEntity.status(HttpStatus.CONFLICT).body(Map.of("error", e.getMessage()));
        } catch (RuntimeException e) {
            return ResponseEntity.badRequest().body(Map.of("error", String.valueOf(e.getMessage())));
        }
        if (okStatus == HttpStatus.NO_CONTENT) {
            return ResponseEntity.noContent().build();
        }
        return ResponseEntity.status(okStatus).body(Map.of("status", "ok"));
    }
}
```

- [ ] **Step 4: Отдать `AgentStore` в контекст Spring**

В `WebApplication` добавить держатель `AgentStore` и бин по образцу `Registry`
из задачи 14, расширив `configure(...)` ещё одним аргументом.

- [ ] **Step 5: Прогнать тест и модуль**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 6: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentConfigController.java \
        java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/web/WebApplication.java \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/AgentConfigControllerTest.java
git commit -m "feat(java/web): HTTP-эндпоинты правки списка агентов"
```

---

## Фаза E — сборка и документация

### Task 19: Разводка запуска

**Files:**
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/OrchestratorApplication.java` (переписывается)
- Modify: `java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/tui/Repl.java` (первый агент из реестра)
- Test: `java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/MultiAgentE2eTest.java`

**Interfaces:**
- Consumes: всё предыдущее.
- Produces: рабочий `--web` с селектором агентов и экраном настроек; REPL, работающий с первым агентом списка.

- [ ] **Step 1: Написать падающий тест**

Создать `MultiAgentE2eTest` по образцу `WebE2eTest`, но с ДВУМЯ поддельными
агентами:

```java
    // Явно выбранный verbatim-агент отвечает без локальной модели.
    @Test
    void anExplicitlyChosenVerbatimAgentAnswersWithoutTheLocalModel() {
        // модель не скриптуется вовсе: любой её вызов уронит StubModel
        A2aMessage msg = A2aMessage.of(A2aMessage.ROLE_USER, Part.text("статус 1041"));
        msg.metadata = Map.of("agentId", "shop");
        shopResults.push(taskWithText("Заказ 1041 доставлен"));

        List<Part> parts = executor.execute("c1", msg, true);

        assertEquals("Заказ 1041 доставлен", parts.getFirst().textOrEmpty());
    }

    // Незнакомый agentId молча откатывается в «Авто»: браузер с устаревшим
    // списком не должен ломать разговор.
    @Test
    void anUnknownAgentIdFallsBackToAuto() {
        A2aMessage msg = A2aMessage.of(A2aMessage.ROLE_USER, Part.text("привет"));
        msg.metadata = Map.of("agentId", "нет такого");
        model.then(new ChatModel.Completion("здравствуйте", null));

        assertEquals("здравствуйте", executor.execute("c1", msg, true).getFirst().textOrEmpty());
    }

    // Правка списка из UI применяется к живому реестру без перезапуска.
    @Test
    void anEditFromTheUiReachesTheLiveRegistry() {
        store.create(new AgentConfig("extra", "", ordersBase, "", "", false, "", "", AuthConfig.NONE));
        assertTrue(registry.ids().contains("extra"));
    }
```

- [ ] **Step 2: Прогнать и убедиться в падении**

Run: `cd java && mvn -o -pl orchestrator -am -Dtest=MultiAgentE2eTest test`
Expected: FAIL

- [ ] **Step 3: Переписать `OrchestratorApplication.main`**

```java
    public static void main(String[] args) throws IOException {
        boolean web = false;
        String configPath = "configs/orchestrator.yaml";
        for (String a : args) {
            if (a.equals("--web") || a.equals("-web")) {
                web = true;
            } else if (!a.startsWith("--")) {
                configPath = a;
            }
        }
        ConfigLoader.OrchestratorConfig cfg = ConfigLoader.loadOrchestrator(configPath);

        Tracer console = new Tracer("", System.out);
        FileWriter logFile = new FileWriter(cfg.a2aLogPath(), true);
        Tracer trace = web
                ? new Tracer("[A2A client] ", System.out, logFile)
                : new Tracer("[A2A client] ", logFile);
        console.logf("A2A protocol trace → %s%s", cfg.a2aLogPath(), web ? " + stdout" : "");

        // Правки из UI лежат отдельным файлом поверх рукописного конфига.
        AgentStore store = new AgentStore(cfg.agents(), Path.of(cfg.agentsOverlayPath()));
        List<AgentConfig> agents = store.agents();
        Registry registry = new Registry(agents, trace);
        // Правка из UI доезжает до живого реестра: новый агент появляется в
        // селекторе и в режиме «Авто» без перезапуска.
        store.onChange(registry::apply);

        OpenAiChatModel model = new OpenAiChatModel(cfg.llm());
        SessionStore sessions = new SessionStore();
        console.logf("orchestrator | LLM=%s model=\"%s\"", cfg.llm().baseUrl(), cfg.llm().model());
        console.logf("agents (%d), overlay %s:", agents.size(), cfg.agentsOverlayPath());
        for (AgentConfig a : agents) {
            // Пароль сюда не попадает намеренно: логи демо показывают целиком.
            console.logf("  - %s → %s (card %s, skill=\"%s\", verbatim=%s, timeout=%s)",
                    a.id(), a.url(), a.cardPath(), a.skill(), a.verbatim(), a.timeoutDuration());
        }

        if (web) {
            OrchestratorWebExecutor executor = new OrchestratorWebExecutor(registry,
                    (tools, summary) -> new OrchestratorAgent(model, tools, summary, sessions),
                    sessions, trace);
            WebApplication.configure(executor, OrchestratorCards.agentCard(cfg.publicUrl()),
                    trace, registry, store);
            SpringApplication app = new SpringApplication(WebApplication.class);
            app.setDefaultProperties(Map.of(
                    "server.port", cfg.port(),
                    "spring.main.banner-mode", "off",
                    "logging.level.root", "warn"));
            app.run(args);
            console.logf("orchestrator web UI on %s", cfg.listenAddr());
            return; // Spring держит JVM; файл трейса живёт вместе с сервером.
        }

        // Терминальный REPL выбора агента не даёт и работает с первым агентом
        // списка. Базовый конфиг гарантирует минимум одного, но overlay может
        // скрыть их все — тогда first() вернёт null.
        Remote first = registry.first();
        if (first == null) {
            console.logf("нет ни одного агента: все скрыты в %s — удалите файл или верните агента через UI",
                    cfg.agentsOverlayPath());
            logFile.close();
            System.exit(1);
            return;
        }
        try {
            first.connect();
        } catch (RuntimeException e) {
            console.logf("agent \"%s\" (запущен ли он?): %s", first.id(), e.getMessage());
            logFile.close();
            System.exit(1);
            return;
        }
        OrdersClient orders = registry.clientFor(first.id());
        console.logf("orchestrator tools (1):");
        console.logf("  - %s: %s", orders.profile().toolName(), orders.profile().toolDesc());

        try (logFile) {
            new Repl(new OrchestratorAgent(model, List.of(orders), orders.profile().summary(), sessions),
                    orders).run();
        }
    }
```

- [ ] **Step 4: Прогнать всё**

Run: `cd java && mvn -o test`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add java/orchestrator/src/main/java/io/github/kmpavloff/a2ademo/orchestrator/ \
        java/orchestrator/src/test/java/io/github/kmpavloff/a2ademo/orchestrator/web/MultiAgentE2eTest.java
git commit -m "feat(java): разводка мультиагентного оркестратора и экрана настроек"
```

---

### Task 20: Живая проверка и документация

**Files:**
- Modify: `java/README.md`
- Modify: `README.md` (упоминания Java-порта)

- [ ] **Step 1: Собрать всё**

Run: `cd java && mvn -o clean package && cd .. && go build ./... && go test ./...`
Expected: обе реализации собираются, Go-тесты зелёные.

- [ ] **Step 2: Проверить связки вживую**

Использовать skill `verify` из `.claude/skills/verify/SKILL.md`. Минимум четыре
прогона, каждый — до карточки заказа и до кнопки возврата:

1. Java-воркер ↔ Java-оркестратор `--web`
2. Java-воркер ↔ Go-оркестратор `--web`
3. Go-воркер ↔ Java-оркестратор `--web`
4. Java-оркестратор `--web`: экран `#settings` — завести агента, изменить,
   сбросить, удалить; убедиться, что селектор в шапке подхватывает правку без
   перезапуска.

Отдельно проверить, что клик по кнопке «Оформить возврат» доводит возврат до
квитанции — ровно то, что было сломано до фазы A.

- [ ] **Step 3: Переписать раздел различий в `java/README.md`**

Убрать абзац «A second difference: the multi-agent support … is **not** ported
here» целиком. Дописать в список портированного:

```markdown
- **мультиагентность** — список `agents:` в `configs/orchestrator.yaml`
  (`id`/`name`/`url`/`card_path`/`skill`/`verbatim`/`timeout`/`description` и
  HTTP Basic), env-перекрытия `A2A_AGENT_<ID>_URL` и `A2A_AGENT_<ID>_PASSWORD`,
  селектор агентов в браузере (`GET /api/agents`) и режим `verbatim`, в котором
  ответ выбранного агента уходит в браузер без локальной модели.
- **экран «Настройки»** — правка списка агентов прямо из браузера поверх
  overlay-файла `configs/agents.local.yaml` (`agents_overlay_path`,
  `A2A_AGENTS_OVERLAY_PATH`); правка применяется к живому реестру без
  перезапуска.
- **A2UI v0.9.1** — обе ревизии расширения объявляются и принимаются, часть
  помечается `metadata.mimeType`, полезная нагрузка едет массивом сообщений,
  события клиента несут все пять полей схемы `client_to_server`.
- **разметка чужого агента** — принимается и нормализуется к basic-каталогу
  (обёрнутые компоненты, таблицы, недостающий корень поверхности).
- **дамп протокола** — `A2A_DEBUG=1` печатает тела запросов и ответов целиком, с
  маскированием номера карты.
```

Оставшееся расхождение (единственное) — фронтенд не вшит в jar, он ищется на
диске. Этот абзац не трогать.

- [ ] **Step 4: Поправить корневой `README.md`**

Найти абзац про Java-порт (`> **Java implementation.**`) и убрать из него любые
оговорки о неполноте, если они там есть. В разделе
«[Several agents at once](#several-agents-at-once)» отметить, что режим работает
и в Java-порте.

- [ ] **Step 5: Коммит**

```bash
git add java/README.md README.md
git commit -m "docs: Java-порт догнал Go — мультиагентность, настройки и A2UI 0.9.1"
```

---

## Порядок и зависимости

```
A1 ─ A2 ─ A3          формат A2UI на проводе (чинит кнопки в браузере)
      └── B5          Ingest опирается на A2uiParts.isA2ui
B4                    Surfaces — независима
C6 ─ C7               конфиг и агент с несколькими инструментами
C8 ─ C9 ─ C10 ─ C11 ─ C12    трейс → транспорт → Remote → Registry → OrdersClient
                  └── C13 ─ C14    исполнитель и /api/agents
D15 ─ D16 ─ D17 ─ D18       overlay и экран настроек (нужен C6)
E19 ─ E20                   разводка и документация
```

Фазы A и B дают самостоятельную ценность: после A браузер снова работает с
Java-оркестратором полностью. Фазы C–E можно исполнять только по порядку.
