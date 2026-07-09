import {LitElement, html, css, nothing} from 'lit';
import {customElement, state} from 'lit/decorators.js';
import {unsafeHTML} from 'lit/directives/unsafe-html.js';
import {until} from 'lit/directives/until.js';
import {provide} from '@lit/context';
import {MessageProcessor} from '@a2ui/web_core/v0_9';
import {basicCatalog, Context} from '@a2ui/lit/v0_9';
import '@a2ui/lit/v0_9'; // registers <a2ui-surface>
import {renderMarkdown} from '@a2ui/markdown-it';
import {A2UIClient, onA2ATraffic, type TrafficEntry} from './client.js';

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
  | {kind: 'widget'; surface: any};

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
  #processor = new MessageProcessor([basicCatalog], async (action: any) => {
    // Consume the widget that owned the clicked button (A2UI never sends
    // deleteSurface here), and echo the button's human label, not its raw
    // action name.
    this._items = this._items.filter(
      (it) => !(it.kind === 'widget' && it.surface?.id === action.surfaceId),
    );
    const label = (action.context?.label as string) || action.name;
    await this.#turn(label, () =>
      this.#client.sendAction(action.name, action.context ?? {}),
    );
  });

  @state() private _items: Item[] = [];
  @state() private _busy = false;
  @state() private _traffic: TrafficEntry[] = [];

  connectedCallback() {
    super.connectedCallback();
    this.#processor.onSurfaceCreated((s: any) => {
      this._items = [...this._items, {kind: 'widget', surface: s}];
    });
    onA2ATraffic((e) => {
      this._traffic = [...this._traffic, e];
    });
  }

  // Runs one exchange: optionally record a user entry, call the agent, then
  // append either widget(s) or the plain-text reply (a widget already conveys
  // the message, so the text is suppressed when widgets are present).
  async #turn(userText: string | null, run: () => Promise<{a2ui: any[]; text: string}>) {
    if (userText) this._items = [...this._items, {kind: 'user', text: userText}];
    this._busy = true;
    try {
      const {a2ui, text} = await run();
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
    } catch (err) {
      console.error('turn failed:', err);
      this._items = [...this._items, {kind: 'assistant', text: `Ошибка: ${err}`}];
    } finally {
      this._busy = false;
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
    return html`
      <h2>Ассистент заказов · A2UI</h2>
      <div class="feed">
        ${this._items.map((it) => this.#renderItem(it))}
        ${this._busy
          ? html`<div class="thinking"><span class="spinner"></span> агент печатает…</div>`
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
              </div>`,
            )}
          </details>`
        : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'orders-app': OrdersApp;
  }
}
