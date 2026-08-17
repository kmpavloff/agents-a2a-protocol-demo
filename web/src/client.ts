import {ClientFactory, type Client} from '@a2a-js/sdk/client';
import type {Message, Part, Task, SendMessageResult} from '@a2a-js/sdk';

// Ревизия A2UI, на которой мы говорим, и её предшественница: полезная нагрузка
// у них общая, но URI разные, и агент, знающий только 0.9, по одному лишь новому
// имени нас за рисующего клиента не примет. v1.0 существует, но это
// релиз-кандидат — пока не берём.
const A2UI_EXT = 'https://a2ui.org/a2a-extension/a2ui/v0.9.1';
const A2UI_EXT_LEGACY = 'https://a2ui.org/a2a-extension/a2ui/v0.9';
// Значение поля version в сообщениях: схемы 0.9.1 объявляют его enum'ом из двух.
const A2UI_VERSION = 'v0.9.1';
// Ключ внутри a2uiClientCapabilities — именно "v0.9": другого свойства схема
// возможностей клиента не знает даже в наборе 0.9.1.
const A2UI_CAPS_KEY = 'v0.9';
const A2UI_MIME = 'application/a2ui+json';
// Тип из ранних сборок A2UI. Только принимаем — отдаём всегда A2UI_MIME.
const A2UI_MIME_LEGACY = 'application/json+a2ui';
const A2UI_CATALOG = 'https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json';

/**
 * Несёт ли часть разметку A2UI. Спека расширения помечает её metadata.mimeType;
 * mediaType — родное поле A2A, которым пользуемся мы сами и живые агенты.
 * Принимаем обе пометки и оба типа, отдаём всегда metadata.mimeType.
 */
function isA2UIPart(p: any): boolean {
  const mime = p?.metadata?.mimeType ?? p?.mediaType;
  return mime === A2UI_MIME || mime === A2UI_MIME_LEGACY;
}

/** Разворачивает полезную нагрузку A2UI-части: по спеке это массив сообщений. */
function a2uiMessages(value: unknown): any[] {
  return Array.isArray(value) ? value : value == null ? [] : [value];
}

// ROLE_USER in the A2A v1.0 proto enum (@a2a-js/sdk v1.0.0-beta.0).
const ROLE_USER = 1;

/** One captured A2A JSON-RPC exchange over the wire (for the protocol log). */
export interface TrafficEntry {
  request: any;
  response: any;
}

let trafficListener: ((e: TrafficEntry) => void) | null = null;

/** Subscribe to raw A2A traffic on /invoke (the real JSON-RPC wire format). */
export function onA2ATraffic(cb: (e: TrafficEntry) => void) {
  trafficListener = cb;
}

// Wrap the global fetch ONCE so /invoke POSTs (the A2A calls the SDK makes) are
// captured as parsed JSON and forwarded to the traffic listener. This shows the
// true wire format, not the SDK's in-memory objects.
let tapInstalled = false;
function installFetchTap() {
  if (tapInstalled || typeof window === 'undefined') return;
  tapInstalled = true;
  const orig = window.fetch.bind(window);
  window.fetch = async (input: any, init?: any) => {
    const url = typeof input === 'string' ? input : input?.url;
    const isInvoke = !!url && url.includes('/invoke') && (init?.method || 'GET').toUpperCase() === 'POST';
    let request: any;
    if (isInvoke && typeof init?.body === 'string') {
      try {
        request = JSON.parse(init.body);
      } catch {
        request = init.body;
      }
    }
    const res = await orig(input, init);
    if (isInvoke && trafficListener) {
      res
        .clone()
        .json()
        .then((response) => trafficListener?.({request, response}))
        .catch(() => {});
    }
    return res;
  };
}

/** A downloadable file the agent attached to the turn (an A2A raw part). */
export interface FileAttachment {
  name: string;
  mediaType: string;
  bytes: Uint8Array;
}

/** What one turn returns: A2UI messages, the agent's text, attached files. */
export interface SendResult {
  a2ui: any[];
  text: string;
  files: FileAttachment[];
}

// rawToBytes normalises an A2A raw part's payload: the SDK may surface it as
// Uint8Array or as the wire's base64 string depending on the decode path.
function rawToBytes(v: unknown): Uint8Array {
  if (v instanceof Uint8Array) return v;
  if (typeof v === 'string') {
    return Uint8Array.from(atob(v), (ch) => ch.charCodeAt(0));
  }
  return new Uint8Array();
}

/**
 * Thin wrapper around @a2a-js/sdk's A2A v1.0 Client that speaks the A2UI
 * extension. It targets the A2A **1.0** wire format (matching the Go a2a-go
 * v2.3.1 server): parts are the proto oneof `{content: {$case, value}}`, the
 * requested extension rides the `A2A-Extensions` header, and only the
 * contextId is reused across turns.
 */
export class A2UIClient {
  #baseUrl: string;
  #client: Client | null = null;
  #contextId: string | undefined;
  // Which agent the user picked in the selector. 'auto' lets the orchestrator's
  // LLM route the request itself, so it is never sent over the wire.
  #agentId = 'auto';
  // Режим разговора: false — «только текст», расширение не объявляем вовсе.
  #a2ui = true;
  // Поставщик модели данных поверхностей — см. setDataModelProvider.
  #dataModel: (() => unknown) | null = null;

  constructor(baseUrl = '') {
    this.#baseUrl = baseUrl;
    installFetchTap();
  }

  setAgent(id: string) {
    this.#agentId = id || 'auto';
  }

  /**
   * Режим разговора. В текстовом не объявляем расширение ни одним из способов:
   * ни заголовком, ни a2uiClientCapabilities. Оркестратор по этому молчанию
   * понимает, что рисовать некому, и не просит разметку у внешнего агента —
   * тот отвечает текстом сразу, а не делает виджет в стол.
   */
  setA2UI(on: boolean) {
    this.#a2ui = on;
  }

  /**
   * Подключает источник a2uiClientDataModel — второй канал, которым введённое
   * пользователем возвращается агенту.
   *
   * Первый канал — привязка в контексте кнопки: рендерер разрешает {path} в
   * момент клика. Но если агент включил на поверхности sendDataModel, значения
   * он ждёт здесь, и спека требует слать модель с КАЖДЫМ сообщением, а не
   * только с нажатием: без этого поле формы для агента навсегда пустое.
   */
  setDataModelProvider(provider: () => unknown) {
    this.#dataModel = provider;
  }

  /**
   * Забывает contextId, поэтому следующее сообщение начнёт новый разговор:
   * и у оркестратора (новая сессия), и у удалённого агента (новый контекст).
   */
  resetContext() {
    this.#contextId = undefined;
  }

  async #getClient(): Promise<Client> {
    if (!this.#client) {
      const base = this.#baseUrl || location.origin;
      this.#client = await new ClientFactory().createFromUrl(base);
    }
    return this.#client;
  }

  async #send(parts: Part[]): Promise<SendResult> {
    const client = await this.#getClient();
    const message = {
      messageId: crypto.randomUUID(),
      role: ROLE_USER,
      parts,
      ...(this.#contextId ? {contextId: this.#contextId} : {}),
      metadata: {
        // The chosen agent rides in the message metadata — the same mechanism
        // A2A agents use for extension-specific hints.
        ...(this.#agentId !== 'auto' ? {agentId: this.#agentId} : {}),
        // Всё, что относится к A2UI, объявляется только в режиме виджетов.
        // Каталоги рендерера вместе с заголовком расширения — штатный признак
        // «клиент говорит на A2UI»; acceptedOutputModes спека таким признаком
        // не считает.
        ...(this.#a2ui
          ? {
              a2uiClientCapabilities: {[A2UI_CAPS_KEY]: {supportedCatalogIds: [A2UI_CATALOG]}},
              // Модель данных поверхностей, если хоть одна её запросила.
              // Пустую не шлём: у большинства ходов синхронизировать нечего.
              ...(() => {
                const model = this.#dataModel?.();
                return model ? {a2uiClientDataModel: model} : {};
              })(),
            }
          : {}),
      },
    } as unknown as Message;

    const result: SendMessageResult = await client.sendMessage(
      // The proto-generated SendMessageRequest type lists tenant/configuration/
      // metadata as required; only `message` is needed at runtime (proto fills
      // the rest), so cast past the over-strict type.
      {message} as any,
      // Request the A2UI extension. a2a-go reads the header named exactly
      // "A2A-Extensions" (keys are lowercased server-side for lookup);
      // X-A2A-Extensions — то же под именем, которое знает спека A2UI v0.9.
      // Ровно один URI: два значения в одном заголовке несовместимы между
      // реализациями — a2a-go сравнивает строку целиком и запятых не разбирает,
      // Spring в Java-порте, наоборот, разбивает по ним. Шлём предыдущую
      // ревизию: её понимают оба наших оркестратора, а Go-порт принимает и
      // новую. Сами сообщения при этом идут в 0.9.1 — на согласование
      // расширения это не влияет.
      this.#a2ui
        ? {
            serviceParameters: {
              'A2A-Extensions': A2UI_EXT_LEGACY,
              'X-A2A-Extensions': A2UI_EXT_LEGACY,
            } as any,
          }
        : {},
    );

    // The orchestrator always returns a Task (submitted → working →
    // completed). Reuse ONLY its contextId for follow-up turns: the task is
    // terminal at the end of each turn, so referencing its taskId would be
    // rejected; the shared contextId keeps the orchestrator session stable.
    const task = result as Task;
    if (task.contextId) this.#contextId = task.contextId;

    // Части завершающего сообщения — место, куда их кладёт спека расширения.
    // Артефакт остаётся запасным путём: так отвечают агенты, писавшиеся до
    // неё, включая наш Java-порт.
    const status = (task.status as any)?.message?.parts;
    const replyParts = status?.length
      ? status
      : (task.artifacts?.[task.artifacts.length - 1]?.parts ?? []);

    // A2A v1.0 parts are a proto oneof: `part.content = {$case, value}`.
    const a2ui: any[] = [];
    const files: FileAttachment[] = [];
    let text = '';
    for (const p of replyParts) {
      const c = (p as any).content;
      if (c?.$case === 'data') {
        // Чужая data-часть (например доменный виджет) — не A2UI: отдать её
        // рендереру значит показать пользователю мусор.
        if (isA2UIPart(p)) a2ui.push(...a2uiMessages(c.value));
      } else if (c?.$case === 'text') text += c.value;
      else if (c?.$case === 'raw') {
        // A downloadable file (e.g. the refund receipt).
        files.push({
          name: (p as any).filename || 'attachment',
          mediaType: (p as any).mediaType || 'application/octet-stream',
          bytes: rawToBytes(c.value),
        });
      }
    }
    return {a2ui, text, files};
  }

  sendText(text: string): Promise<SendResult> {
    return this.#send([{content: {$case: 'text', value: text}} as unknown as Part]);
  }

  /**
   * Нажатие кнопки — событие по схеме client_to_server: одно сообщение внутри
   * массива, тип в metadata.mimeType. Все пять полей действия схема объявляет
   * обязательными, поэтому пустые surfaceId/sourceComponentId пустеют, а не
   * исчезают — иначе payload не пройдёт валидацию на стороне агента.
   */
  sendAction(
    name: string,
    context: Record<string, any>,
    surfaceId = '',
    sourceComponentId = '',
  ): Promise<SendResult> {
    const action = {
      name,
      surfaceId,
      sourceComponentId,
      timestamp: new Date().toISOString(),
      context,
    };
    return this.#send([
      {
        content: {$case: 'data', value: [{version: A2UI_VERSION, action}]},
        mediaType: A2UI_MIME,
        metadata: {mimeType: A2UI_MIME},
      } as unknown as Part,
    ]);
  }
}
