import {LitElement, html, css} from 'lit';
import {customElement, state} from 'lit/decorators.js';
import {MessageProcessor} from '@a2ui/web_core/v0_9';
import {basicCatalog} from '@a2ui/lit/v0_9';
import '@a2ui/lit/v0_9'; // registers <a2ui-surface>
import {A2UIClient} from './client.js';

@customElement('orders-app')
export class OrdersApp extends LitElement {
  #client = new A2UIClient();

  // The processor renders surfaces and routes button actions back to the agent.
  #processor = new MessageProcessor(
    [basicCatalog],
    async (action) => {
      this._busy = true;
      try {
        const {a2ui, text} = await this.#client.sendAction(action.name, action.context ?? {});
        if (text) this._log = [...this._log, `ассистент: ${text}`];
        this.#ingest(a2ui);
      } catch (err) {
        console.error('A2UI action failed:', err);
        this._log = [...this._log, `ошибка: ${err}`];
      } finally {
        this._busy = false;
      }
    },
  );

  @state() private _surfaces: any[] = [];
  @state() private _log: string[] = [];
  @state() private _busy = false;

  connectedCallback() {
    super.connectedCallback();
    this.#processor.onSurfaceCreated((s) => {
      this._surfaces = [...this._surfaces, s];
    });
  }

  #ingest(msgs: any[]) {
    const a2ui = msgs.filter(m => m && m.version === 'v0.9');
    if (a2ui.length) this.#processor.processMessages(a2ui);
  }

  async #send(text: string) {
    if (!text.trim()) return;
    this._log = [...this._log, `вы: ${text}`];
    this._busy = true;
    try {
      const {a2ui, text: reply} = await this.#client.sendText(text);
      if (reply) this._log = [...this._log, `ассистент: ${reply}`];
      this.#ingest(a2ui);
    } catch (err) {
      console.error('send failed:', err);
      this._log = [...this._log, `ошибка: ${err}`];
    } finally {
      this._busy = false;
    }
  }

  static styles = css`
    :host { display: block; max-width: 640px; margin: 0 auto; padding: 24px; font-family: system-ui; }
    .log { margin: 12px 0; color: #555; }
    .surfaces { display: flex; flex-direction: column; gap: 16px; }
    form { display: flex; gap: 8px; margin-top: 16px; }
    input { flex: 1; padding: 12px; border-radius: 8px; border: 1px solid #ccc; }
    button { padding: 12px 20px; border-radius: 8px; border: none; background: #3367d6; color: #fff; }
  `;

  render() {
    return html`
      <h2>Ассистент заказов · A2UI</h2>
      <div class="log">${this._log.map(l => html`<div>${l}</div>`)}</div>
      <div class="surfaces">
        ${this._surfaces.map(s => html`<a2ui-surface .surface=${s}></a2ui-surface>`)}
      </div>
      <form @submit=${(e: Event) => {
        e.preventDefault();
        const input = (e.target as HTMLFormElement).querySelector('input')!;
        this.#send(input.value);
        input.value = '';
      }}>
        <input placeholder="Напишите запрос…" ?disabled=${this._busy} />
        <button type="submit" ?disabled=${this._busy}>Отправить</button>
      </form>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'orders-app': OrdersApp;
  }
}
