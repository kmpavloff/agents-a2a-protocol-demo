// Buffer polyfill MUST come first: @a2a-js/sdk's generated proto helpers call
// globalThis.Buffer unconditionally (bytesFromBase64 / base64FromBytes), which
// only exists in Node. Without this, receiving a file (raw) part — e.g. the
// refund receipt — crashes in the browser with "Buffer is not defined".
import {Buffer} from 'buffer';
(globalThis as any).Buffer ??= Buffer;

import {LitElement, html, css, nothing} from 'lit';
import {customElement, state} from 'lit/decorators.js';
import {unsafeHTML} from 'lit/directives/unsafe-html.js';
import {until} from 'lit/directives/until.js';
import {provide} from '@lit/context';
import {MessageProcessor} from '@a2ui/web_core/v0_9';
import {basicCatalog, Context} from '@a2ui/lit/v0_9';
import '@a2ui/lit/v0_9'; // registers <a2ui-surface>
import {renderMarkdown} from '@a2ui/markdown-it';
import {A2UIClient, onA2ATraffic, type FileAttachment, type TrafficEntry} from './client.js';
import './agent-settings.js';

// highlightJson pretty-prints a value and wraps JSON tokens in <span> classes
// for syntax colouring. The raw JSON is HTML-escaped FIRST, so the only markup
// added is our own spans — safe to feed to unsafeHTML even with agent/user text.
function highlightJson(value: unknown): string {
  const escaped = JSON.stringify(value, null, 2)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
  return escaped.replace(
    /("(?:\\.|[^"\\])*"(\s*:)?|\b(?:true|false)\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,
    (m) => {
      let cls = 'num';
      if (m[0] === '"') cls = m.endsWith(':') || /"\s*:$/.test(m) ? 'key' : 'str';
      else if (m === 'true' || m === 'false') cls = 'bool';
      else if (m === 'null') cls = 'null';
      return `<span class="tok-${cls}">${m}</span>`;
    },
  );
}

// isBriefComment decides whether an assistant reply is a short lead-in worth
// keeping next to a widget, versus a restatement of the widget's own data that
// would just duplicate it (too long, a markdown table, or many lines).
function isBriefComment(text: string): boolean {
  const s = (text || '').trim();
  if (!s) return false;
  if (s.length > 200) return false; // likely a full restatement
  if (/\|.+\|/.test(s)) return false; // markdown table row
  if ((s.match(/\n/g)?.length ?? 0) > 2) return false; // multi-line dump
  return true;
}

/** One entry in the conversation feed, kept in chronological order. */
type Item =
  | {kind: 'user'; text: string}
  | {kind: 'assistant'; text: string}
  | {kind: 'widget'; surface: any}
  | {kind: 'file'; name: string; href: string}
  | {kind: 'timing'; seconds: number; agent: string};

/**
 * Человекочитаемая причина сорвавшегося хода. Сетевой сбой браузер сообщает
 * голым «TypeError: Failed to fetch», из чего непонятно даже, чья это сторона,
 * — а чаще всего это просто не запущенный оркестратор.
 */
function describeTurnError(err: unknown): string {
  if (err instanceof TypeError) {
    return (
      'Оркестратор не отвечает — запрос не ушёл. Проверьте, запущен ли он: ' +
      '`go run ./cmd/orchestrator --web`. Подробности в консоли браузера.'
    );
  }
  return `Ошибка: ${err}`;
}

/** Длительность хода: «52,3 с» / «1 мин 04 с». */
function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds.toFixed(1).replace('.', ',')} с`;
  // Округляем целое число секунд ДО деления: иначе 119,6 с даёт «1 мин 60 с».
  const total = Math.round(seconds);
  const m = Math.floor(total / 60);
  return `${m} мин ${String(total % 60).padStart(2, '0')} с`;
}

/** One selectable agent, as served by GET /api/agents. */
interface AgentInfo {
  id: string;
  name: string;
  description: string;
  verbatim: boolean;
  available: boolean;
  // probed=false — «ещё не проверяли». Пометка о недоступности до первой
  // проверки была бы домыслом: агент отвечает не мгновенно.
  probed: boolean;
  // Навыки из настроек агента — варианты второго селектора.
  skills: string[];
}

const AGENT_STORAGE_KEY = 'a2a.agentId';
// Навык помнится отдельно для каждого агента: у разных агентов разные списки.
const SKILL_STORAGE_PREFIX = 'a2a.skill.';
const NO_SKILL = '';

function storedSkill(agentId: string): string {
  try {
    return localStorage.getItem(SKILL_STORAGE_PREFIX + agentId) ?? NO_SKILL;
  } catch {
    return NO_SKILL;
  }
}
const AUTO_AGENT = 'auto';

@customElement('orders-app')
export class OrdersApp extends LitElement {
  // Give the A2UI Text components a markdown renderer; without one they show
  // raw markdown (e.g. a heading as literal "### ...").
  @provide({context: Context.markdown})
  markdownRenderer = (value: string, options?: any): Promise<string> =>
    Promise.resolve(renderMarkdown(value, options));

  #client = new A2UIClient();

  // The processor renders A2UI surfaces and routes button clicks back to the
  // agent. Each created surface becomes a widget item in the feed, in order.
  #processor = this.#makeProcessor();

  constructor() {
    super();
    // Модель данных берётся у процессора на каждом ходу: он пересоздаётся
    // кнопкой «Новый разговор», поэтому читаем поле, а не захватываем ссылку.
    this.#client.setDataModelProvider(() => this.#processor.getClientDataModel());
  }

  #makeProcessor() {
    return new MessageProcessor([basicCatalog], async (action: any) => {
      // Ход уже идёт — просто игнорируем клик. Иначе карточка исчезала бы из
      // ленты (её убирает строка ниже), а запрос всё равно не уходил бы.
      if (this._busy) return;
    // Consume the widget that owned the clicked button (A2UI never sends
    // deleteSurface here), and echo the button's human label, not its raw
    // action name.
    this._items = this._items.filter(
      (it) => !(it.kind === 'widget' && it.surface?.id === action.surfaceId),
    );
      const label = (action.context?.label as string) || action.name;
      await this.#turn(label, () =>
        this.#client.sendAction(
          action.name,
          action.context ?? {},
          action.surfaceId,
          action.sourceComponentId,
        ),
      );
    });
  }

  // Начать заново: пустая лента, новый контекст у оркестратора и у агента,
  // чистый реестр поверхностей. Нужно, когда агент потерял нить разговора —
  // до этого помогала только перезагрузка страницы.
  #newConversation() {
    if (this._busy) return;
    this._items = [];
    this.#client.resetContext();
    this.#processor = this.#makeProcessor();
    this.#watchSurfaces();
  }

  // Подписка на созданные поверхности; вызывается заново для каждого
  // процессора.
  #watchSurfaces() {
    this.#processor.onSurfaceCreated((s: any) => {
      this._items = [...this._items, {kind: 'widget', surface: s}];
    });
  }

  @state() private _items: Item[] = [];
  @state() private _busy = false;
  @state() private _traffic: TrafficEntry[] = [];
  // Режим разговора: виджеты или только текст. Не переживает перезагрузку —
  // это переключатель демонстрации, а не пользовательская настройка.
  @state() private _a2ui = true;

  // Вкладка: чат или настройки агентов. Хэш нужен, чтобы перезагрузка не
  // выкидывала обратно в чат посреди правки конфига.
  @state() private _tab: 'chat' | 'settings' =
    location.hash === '#settings' ? 'settings' : 'chat';
  #onHashChange: (() => void) | undefined;

  @state() private _agents: AgentInfo[] = [];
  // Причина, по которой список агентов не удалось загрузить. Панель выбора
  // никогда не исчезает молча: либо список, либо видимая ошибка.
  @state() private _agentsError = '';
  @state() private _agentId =
    localStorage.getItem(AGENT_STORAGE_KEY) ?? AUTO_AGENT;
  @state() private _skill = storedSkill(this._agentId);
  // Секунды текущего хода. Удалённый агент отвечает десятки секунд, поэтому
  // ожидание должно быть видимым, а не просто крутящимся кружком.
  @state() private _elapsed = 0;
  #tick: ReturnType<typeof setInterval> | undefined;
  #agentsPoll: ReturnType<typeof setInterval> | undefined;
  #agentsRetry: ReturnType<typeof setTimeout> | undefined;

  connectedCallback() {
    super.connectedCallback();
    this.#watchSurfaces();
    onA2ATraffic((e) => {
      this._traffic = [...this._traffic, e];
    });
    this.#client.setAgent(this._agentId);
    this.#client.setSkill(this._skill);
    void this.#loadAgents();
    // Доступность агентов меняется на ходу (перезапуск, кратковременный 503),
    // а список грузится один раз — поэтому обновляем его периодически, иначе
    // на живом агенте навсегда останется пометка «недоступен».
    this.#agentsPoll = setInterval(() => void this.#loadAgents(), 30_000);
    this.#onHashChange = () => {
      this._tab = location.hash === '#settings' ? 'settings' : 'chat';
    };
    window.addEventListener('hashchange', this.#onHashChange);
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    clearInterval(this.#agentsPoll);
    clearTimeout(this.#agentsRetry);
    clearInterval(this.#tick);
    if (this.#onHashChange) window.removeEventListener('hashchange', this.#onHashChange);
  }

  #selectTab(tab: 'chat' | 'settings') {
    this._tab = tab;
    location.hash = tab === 'settings' ? '#settings' : '';
  }

  async #loadAgents() {
    try {
      const res = await fetch('/api/agents');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      clearTimeout(this.#agentsRetry);
      const agents: unknown = await res.json();
      if (!Array.isArray(agents)) {
        throw new Error('ответ не является списком агентов');
      }
      this._agents = agents as AgentInfo[];
      this._agentsError = '';
      // A stale selection (agent removed from the config) falls back to auto,
      // so the selector never shows a target the server does not know.
      if (this._agentId !== AUTO_AGENT && !agents.some((a) => a.id === this._agentId)) {
        this.#selectAgent(AUTO_AGENT);
      }
      // Так же и навык, убранный из настроек агента: селектор не должен
      // показывать то, чего оркестратор уже не примет.
      const cur = this._agents.find((a) => a.id === this._agentId);
      if (this._skill !== NO_SKILL && !cur?.skills?.includes(this._skill)) {
        this.#selectSkill(NO_SKILL);
      }
    } catch (err) {
      console.error('agent list failed:', err);
      // Список сохраняем: сорвавшийся опрос не повод терять рабочий селектор.
      this._agentsError = String(err instanceof Error ? err.message : err);
      // Первая загрузка провалилась — пробуем чаще, чем раз в полминуты. Ровно
      // одна отложенная попытка: и опрос по таймеру, и конец хода зовут этот
      // метод, а каждый сбой плодил бы ещё одну независимую цепочку.
      if (!this._agents.length) {
        clearTimeout(this.#agentsRetry);
        this.#agentsRetry = setTimeout(() => void this.#loadAgents(), 5_000);
      }
    }
  }

  /**
   * Переключает режим разговора. Действует со следующего хода: карточки, уже
   * лежащие в ленте, остаются кликабельными — нажатие на них по-прежнему
   * валидное событие, и агент ответит на него текстом.
   */
  #selectMode(a2ui: boolean) {
    this._a2ui = a2ui;
    this.#client.setA2UI(a2ui);
  }

  #selectAgent(id: string) {
    this._agentId = id;
    localStorage.setItem(AGENT_STORAGE_KEY, id);
    this.#client.setAgent(id);
    // У каждого агента свой запомненный навык.
    this._skill = id === AUTO_AGENT ? NO_SKILL : storedSkill(id);
    this.#client.setSkill(this._skill);
  }

  #selectSkill(skill: string) {
    this._skill = skill;
    if (this._agentId !== AUTO_AGENT) {
      try {
        localStorage.setItem(SKILL_STORAGE_PREFIX + this._agentId, skill);
      } catch {
        /* без хранилища навык просто не запомнится */
      }
    }
    this.#client.setSkill(skill);
  }

  // Whose answer we are waiting for. At tens of seconds per remote turn it
  // matters that the user can tell which agent is busy.
  get #busyLabel(): string {
    const agent = this._agents.find((a) => a.id === this._agentId);
    return agent ? agent.name : 'агент';
  }

  // Runs one exchange: optionally record a user entry, call the agent, then
  // append either widget(s) or the plain-text reply (a widget already conveys
  // the message, so the text is suppressed when widgets are present).
  async #turn(
    userText: string | null,
    run: () => Promise<{a2ui: any[]; text: string; files?: FileAttachment[]}>,
  ) {
    // Кнопки внутри виджета не блокируются на время хода, поэтому клик по ним
    // мог запустить второй ход поверх первого: прежний интервал терялся и до
    // конца жизни страницы дописывал чужое время.
    if (this._busy) return;
    if (userText) this._items = [...this._items, {kind: 'user', text: userText}];
    const startedAt = performance.now();
    const agent = this.#busyLabel;
    this._busy = true;
    this._elapsed = 0;
    clearInterval(this.#tick);
    this.#tick = setInterval(() => {
      this._elapsed = (performance.now() - startedAt) / 1000;
    }, 100);
    try {
      const {a2ui, text, files} = await run();
      if (a2ui.length) {
        // Keep a short comment above the widget; drop it if it restates the
        // widget's data (long / table) so the two never duplicate each other.
        if (isBriefComment(text)) {
          this._items = [...this._items, {kind: 'assistant', text}];
        }
        this.#processor.processMessages(a2ui);
      } else if (text) {
        this._items = [...this._items, {kind: 'assistant', text}];
      }
      // Attached files (e.g. the refund receipt) become download chips. The
      // Blob URL keeps the bytes local — nothing is uploaded anywhere.
      for (const f of files ?? []) {
        const href = URL.createObjectURL(
          new Blob([f.bytes as BlobPart], {type: f.mediaType}),
        );
        this._items = [...this._items, {kind: 'file', name: f.name, href}];
      }
    } catch (err) {
      console.error('turn failed:', err);
      this._items = [...this._items, {kind: 'assistant', text: describeTurnError(err)}];
    } finally {
      clearInterval(this.#tick);
      this._busy = false;
      // Время хода — и для удачного ответа, и для ошибки: «сколько мы ждали»
      // одинаково интересно в обоих случаях.
      this._items = [
        ...this._items,
        {kind: 'timing', seconds: (performance.now() - startedAt) / 1000, agent},
      ];
      void this.#loadAgents();
    }
  }

  #send(text: string) {
    if (!text.trim()) return;
    void this.#turn(text, () => this.#client.sendText(text));
  }

  static styles = css`
    :host {
      display: block;
      max-width: 640px;
      margin: 0 auto;
      padding: 24px;
      color-scheme: light;
      font-family: system-ui, sans-serif;
      color: #222;
    }
    h2 {
      font-weight: 700;
      margin-bottom: 8px;
    }
    /* Чат прячется, но остаётся в DOM: в _items лежат живые A2UI-поверхности,
       и пересоздавать <a2ui-surface> на каждом переключении вкладки —
       напрашиваться на «Surface … already exists». */
    [hidden] {
      display: none !important;
    }
    .tabs {
      display: flex;
      gap: 4px;
      margin-bottom: 12px;
      border-bottom: 1px solid #e1e4e8;
    }
    .tab {
      padding: 8px 16px;
      border: none;
      border-radius: 8px 8px 0 0;
      background: transparent;
      color: #57606a;
      font-size: 14px;
      font-weight: 600;
      cursor: pointer;
    }
    .tab.active {
      background: #f0f1f3;
      color: #0b57d0;
    }
    .agent-bar {
      display: flex;
      align-items: center;
      gap: 8px;
      font-size: 13px;
      color: #57606a;
    }
    .agent-bar select {
      padding: 6px 10px;
      border-radius: 8px;
      border: 1px solid #ccc;
      background: #fff;
      color: #222;
      font-size: 13px;
    }
    .agent-note {
      font-size: 12px;
      color: #8b949e;
    }
    .new-chat {
      margin-left: auto;
      padding: 6px 12px;
      border-radius: 8px;
      border: 1px solid #d0d7de;
      background: #fff;
      color: #57606a;
      font-size: 13px;
      cursor: pointer;
    }
    .new-chat:hover:not([disabled]) {
      background: #f6f8fa;
    }
    .feed {
      display: flex;
      flex-direction: column;
      gap: 10px;
      margin: 18px 0;
    }
    .bubble {
      padding: 8px 12px;
      border-radius: 14px;
      max-width: 85%;
      white-space: pre-wrap;
      line-height: 1.45;
    }
    .bubble.user {
      align-self: flex-end;
      background: #1177ee;
      color: #fff;
      border-bottom-right-radius: 4px;
    }
    .bubble.assistant {
      align-self: flex-start;
      background: #f0f1f3;
      color: #222;
      border-bottom-left-radius: 4px;
    }
    /* Markdown-rendered assistant bubbles produce block elements. */
    .bubble.md {
      white-space: normal;
    }
    .bubble.md :is(p, ul, ol) {
      margin: 0.35em 0;
    }
    .bubble.md :is(p, ul, ol):first-child {
      margin-top: 0;
    }
    .bubble.md :is(p, ul, ol):last-child {
      margin-bottom: 0;
    }
    .widget {
      align-self: stretch;
      position: relative;
      margin: 6px 0;
      border: 1px solid #d0d7de;
      border-radius: 14px;
      background: #fff;
      box-shadow: 0 1px 4px rgba(0, 0, 0, 0.07);
      padding: 16px 14px 14px;
    }
    .widget-tag {
      position: absolute;
      top: -9px;
      left: 14px;
      font-size: 11px;
      font-weight: 600;
      color: #57606a;
      background: #fff;
      padding: 1px 8px;
      border: 1px solid #d0d7de;
      border-radius: 999px;
    }
    .file-chip {
      align-self: flex-start;
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 8px 14px;
      border: 1px solid #d0d7de;
      border-radius: 999px;
      background: #f6f8fa;
      color: #0b57d0;
      font-size: 14px;
      font-weight: 600;
      text-decoration: none;
    }
    .file-chip:hover {
      background: #eef2f6;
    }
    .timing {
      align-self: flex-start;
      font-size: 12px;
      color: #8b949e;
      margin: -4px 0 2px;
    }
    .thinking {
      align-self: flex-start;
      display: flex;
      align-items: center;
      gap: 8px;
      color: #57606a;
      font-size: 14px;
    }
    .spinner {
      width: 16px;
      height: 16px;
      border: 2px solid #d7dbe0;
      border-top-color: #1177ee;
      border-radius: 50%;
      animation: spin 0.8s linear infinite;
    }
    @keyframes spin {
      to {
        transform: rotate(360deg);
      }
    }
    form {
      display: flex;
      gap: 8px;
      margin-top: 8px;
    }
    input {
      flex: 1;
      padding: 12px;
      border-radius: 10px;
      border: 1px solid #ccc;
      font-size: 15px;
    }
    button {
      padding: 12px 20px;
      border-radius: 10px;
      border: none;
      background: #1177ee;
      color: #fff;
      cursor: pointer;
    }
    button[disabled] {
      opacity: 0.5;
      cursor: default;
    }
    .proto {
      margin-top: 20px;
      border: 1px solid #e1e4e8;
      border-radius: 10px;
      background: #fafbfc;
      padding: 8px 12px;
    }
    .proto summary {
      cursor: pointer;
      color: #57606a;
      font-size: 13px;
      font-weight: 600;
    }
    .exchange {
      margin: 10px 0;
      border-top: 1px dashed #e1e4e8;
      padding-top: 8px;
    }
    .agent-call {
      margin: 8px 0 0 14px;
      border-left: 2px solid #d0d7de;
      padding-left: 10px;
    }
    .agent-call summary {
      cursor: pointer;
      font-size: 12px;
      font-weight: 600;
      color: #57606a;
    }
    .ac-meta {
      font-weight: 400;
      color: #8b949e;
      margin-left: 6px;
      overflow-wrap: anywhere;
    }
    .ex-h {
      font-size: 11px;
      font-weight: 600;
      color: #8b949e;
      margin: 6px 0 2px;
    }
    .proto pre {
      margin: 0;
      padding: 8px 10px;
      background: #0d1117;
      color: #c9d1d9;
      border-radius: 6px;
      font-size: 11.5px;
      line-height: 1.4;
      overflow-x: auto;
      white-space: pre;
    }
    .tok-key {
      color: #7ee787;
    }
    .tok-str {
      color: #a5d6ff;
    }
    .tok-num {
      color: #79c0ff;
    }
    .tok-bool {
      color: #ffa657;
    }
    .tok-null {
      color: #ff7b72;
    }
  `;

  #renderItem(it: Item) {
    if (it.kind === 'widget') {
      return html`<div class="widget">
        <span class="widget-tag">🧩 виджет</span>
        <a2ui-surface .surface=${it.surface}></a2ui-surface>
      </div>`;
    }
    if (it.kind === 'timing') {
      return html`<div class="timing" title="время полного хода: запрос → ответ">
        ⏱ ${it.agent} · ${formatDuration(it.seconds)}
      </div>`;
    }
    if (it.kind === 'file') {
      return html`<a class="file-chip" href=${it.href} download=${it.name}>
        💾 Скачать ${it.name}
      </a>`;
    }
    if (it.kind === 'assistant') {
      // Render the agent's markdown (e.g. **bold**) instead of showing the raw
      // syntax. Same (async) renderer the A2UI widgets use; markdown-it escapes
      // raw HTML. Show the plain text as the fallback until it resolves.
      return html`<div class="bubble assistant md">
        ${until(
          renderMarkdown(it.text).then((h) => unsafeHTML(h)),
          it.text,
        )}
      </div>`;
    }
    return html`<div class="bubble user">${it.text}</div>`;
  }

  render() {
    const selected = this._agents.find((a) => a.id === this._agentId);
    // Выбор мог не найтись в списке (список не загрузился или агент исчез из
    // конфига). Тогда рисуем для него отдельную опцию: пустой <select> хуже
    // любого объяснения, а сам выбор остаётся рабочим — оркестратор либо знает
    // этот id, либо тихо откатится в «Авто».
    const orphanSelection =
      this._agentId !== AUTO_AGENT && !selected ? this._agentId : '';
    return html`
      <h2>Ассистент заказов · A2UI</h2>
      <nav class="tabs">
        <button
          type="button"
          class=${this._tab === 'chat' ? 'tab active' : 'tab'}
          @click=${() => this.#selectTab('chat')}
        >
          Чат
        </button>
        <button
          type="button"
          class=${this._tab === 'settings' ? 'tab active' : 'tab'}
          @click=${() => this.#selectTab('settings')}
        >
          Настройки
        </button>
      </nav>
      ${this._tab === 'settings'
        ? html`<agent-settings
            @agents-changed=${() => void this.#loadAgents()}
          ></agent-settings>`
        : nothing}
      <div class="chat" ?hidden=${this._tab !== 'chat'}>
      ${html`<div class="agent-bar">
            <label for="agent">Агент:</label>
            <!-- Выбранность отмечается на самих <option>, а не через .value
                 на <select>: Lit выставляет свойство до того, как появятся
                 дочерние узлы, и одиночная привязка больше не переприменяется —
                 селектор остаётся визуально пустым. -->
            <select
              id="agent"
              @change=${(e: Event) =>
                this.#selectAgent((e.target as HTMLSelectElement).value)}
            >
              <option value=${AUTO_AGENT} ?selected=${this._agentId === AUTO_AGENT}>
                Авто (выбирает модель)
              </option>
              ${orphanSelection
                ? html`<option value=${orphanSelection} selected>
                    ${orphanSelection} — нет в списке
                  </option>`
                : nothing}
              ${this._agents.map(
                (a) => html`<option value=${a.id} ?selected=${a.id === this._agentId}>
                  ${a.name}${a.probed && !a.available ? ' — не отвечает' : ''}
                </option>`,
              )}
            </select>
            ${selected?.skills?.length
              ? html`<label for="skill">Навык:</label>
                  <select
                    id="skill"
                    @change=${(e: Event) =>
                      this.#selectSkill((e.target as HTMLSelectElement).value)}
                  >
                    <option value=${NO_SKILL} ?selected=${this._skill === NO_SKILL}>
                      без навыка
                    </option>
                    ${selected.skills.map(
                      (s) => html`<option value=${s} ?selected=${s === this._skill}>${s}</option>`,
                    )}
                  </select>`
              : nothing}
            <label for="mode">Режим:</label>
            <select
              id="mode"
              @change=${(e: Event) =>
                this.#selectMode((e.target as HTMLSelectElement).value === 'a2ui')}
            >
              <option value="a2ui" ?selected=${this._a2ui}>Виджеты (A2UI)</option>
              <option value="text" ?selected=${!this._a2ui}>Только текст</option>
            </select>
            ${selected?.verbatim
              ? html`<span class="agent-note">отвечает напрямую, без локальной модели</span>`
              : nothing}
            ${this._agentsError
              ? html`<span class="agent-note" title=${this._agentsError}>
                  список агентов недоступен (${this._agentsError})
                </span>`
              : nothing}
            <button
              type="button"
              class="new-chat"
              ?disabled=${this._busy || !this._items.length}
              title="Забыть разговор: следующий запрос уйдёт с новым контекстом"
              @click=${() => this.#newConversation()}
            >
              Новый разговор
            </button>
          </div>`}
      <div class="feed">
        ${this._items.map((it) => this.#renderItem(it))}
        ${this._busy
          ? html`<div class="thinking">
              <span class="spinner"></span> ${this.#busyLabel} печатает…
              <span class="timing">${formatDuration(this._elapsed)}</span>
            </div>`
          : nothing}
      </div>
      <form
        @submit=${(e: Event) => {
          e.preventDefault();
          const input = (e.target as HTMLFormElement).querySelector('input')!;
          this.#send(input.value);
          input.value = '';
        }}
      >
        <input placeholder="Напишите запрос…" ?disabled=${this._busy} />
        <button type="submit" ?disabled=${this._busy}>Отправить</button>
      </form>
      ${this._traffic.length
        ? html`<details class="proto">
            <summary>A2A-протокол · ${this._traffic.length} обмен(а/ов) — показать сырой JSON</summary>
            ${this._traffic.map(
              (e, i) => html`<div class="exchange">
                <div class="ex-h">#${i + 1} → запрос · message/send</div>
                <pre>${unsafeHTML(highlightJson(e.request))}</pre>
                <div class="ex-h">← ответ</div>
                <pre>${unsafeHTML(highlightJson(e.response))}</pre>
                ${e.agentCalls.map(
                  (c) => html`<details class="agent-call">
                    <summary>
                      ↳ оркестратор → ${c.agentName || c.agentId} · ${c.method || 'POST'}
                      <span class="ac-meta">
                        ${c.error ? `ошибка: ${c.error}` : `HTTP ${c.status}`} · ${c.tookMs} мс · ${c.url}
                      </span>
                    </summary>
                    <div class="ex-h">→ запрос агенту</div>
                    <pre>${unsafeHTML(highlightJson(c.request))}</pre>
                    ${c.response !== undefined
                      ? html`<div class="ex-h">← ответ агента</div>
                          <pre>${unsafeHTML(highlightJson(c.response))}</pre>`
                      : nothing}
                  </details>`,
                )}
              </div>`,
            )}
          </details>`
        : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'orders-app': OrdersApp;
  }
}
