import {LitElement, html, css, nothing} from 'lit';
import {customElement, state} from 'lit/decorators.js';

/** Запись агента, как её отдаёт GET /api/agents/config. */
interface AgentConfig {
  id: string;
  name: string;
  url: string;
  cardPath: string;
  skill: string;
  verbatim: boolean;
  timeout: string;
  description: string;
  auth: {type: string; username: string; hasPassword: boolean};
  // Пути к PEM-файлам на машине оркестратора — сами файлы через API не ходят.
  tls: TlsConfig;
  hidden: boolean;
  source: 'file' | 'ui';
  envLocked: string[];
  // Есть ли базовая версия в orchestrator.yaml. У агента, целиком заведённого
  // через UI, её нет — «Сбросить к конфигу» для него означало бы удаление.
  canReset: boolean;
}

interface TlsConfig {
  certFile: string;
  keyFile: string;
  caFile: string;
  insecureSkipVerify: boolean;
}

/** Живой статус из GET /api/agents — им же питается селектор в чате. */
interface AgentStatus {
  id: string;
  available: boolean;
  probed: boolean;
}

/** Форма правки: пароль отдельным полем, пустое значит «не менять». */
interface Draft extends AgentConfig {
  password: string;
  isNew: boolean;
}

const EMPTY: Draft = {
  id: '', name: '', url: '', cardPath: '', skill: '', verbatim: false,
  timeout: '', description: '',
  auth: {type: '', username: '', hasPassword: false},
  tls: {certFile: '', keyFile: '', caFile: '', insecureSkipVerify: false},
  hidden: false, source: 'ui', envLocked: [], canReset: false, password: '', isNew: true,
};

@customElement('agent-settings')
export class AgentSettings extends LitElement {
  @state() private _agents: AgentConfig[] = [];
  @state() private _status: Record<string, AgentStatus> = {};
  @state() private _draft: Draft | null = null;
  @state() private _error = '';
  @state() private _busy = false;

  connectedCallback() {
    super.connectedCallback();
    void this.#load();
  }

  async #load() {
    try {
      const [cfgRes, statusRes] = await Promise.all([
        fetch('/api/agents/config'),
        fetch('/api/agents'),
      ]);
      if (!cfgRes.ok) throw new Error(`HTTP ${cfgRes.status}`);
      this._agents = (await cfgRes.json()) as AgentConfig[];
      this._error = '';
      if (statusRes.ok) {
        const list = (await statusRes.json()) as AgentStatus[];
        this._status = Object.fromEntries(list.map((s) => [s.id, s]));
      }
    } catch (err) {
      this._error = `Не удалось загрузить список агентов: ${err}`;
    }
  }

  /** Правка применилась — чат должен обновить свой селектор. */
  #announce() {
    this.dispatchEvent(new CustomEvent('agents-changed', {bubbles: true, composed: true}));
  }

  #edit(a: AgentConfig) {
    this._draft = {...a, auth: {...a.auth}, tls: {...a.tls}, password: '', isNew: false};
    this._error = '';
  }

  #add() {
    this._draft = {...EMPTY, auth: {...EMPTY.auth}, tls: {...EMPTY.tls}};
    this._error = '';
  }

  /** Одно место для всех мутаций: коды ответов у них разные, разбор — общий. */
  async #send(method: string, path: string, body?: unknown): Promise<boolean> {
    this._busy = true;
    try {
      const res = await fetch(path, {
        method,
        headers: body ? {'Content-Type': 'application/json'} : undefined,
        body: body ? JSON.stringify(body) : undefined,
      });
      if (!res.ok) {
        // Сервер объясняет отказ словами — показываем их, а не голый код.
        let msg = `HTTP ${res.status}`;
        try {
          const j = await res.json();
          if (j?.error) msg = j.error;
        } catch {
          /* тело не JSON — остаётся код */
        }
        this._error = msg;
        return false;
      }
      this._error = '';
      await this.#load();
      this.#announce();
      return true;
    } catch (err) {
      this._error = `Запрос не ушёл: ${err}`;
      return false;
    } finally {
      this._busy = false;
    }
  }

  async #save() {
    const d = this._draft;
    if (!d) return;
    const body = {
      id: d.id, name: d.name, url: d.url, cardPath: d.cardPath, skill: d.skill,
      verbatim: d.verbatim, timeout: d.timeout, description: d.description,
      auth: {type: d.auth.type, username: d.auth.username, password: d.password},
      tls: d.tls,
    };
    const ok = d.isNew
      ? await this.#send('POST', '/api/agents/config', body)
      : await this.#send('PUT', `/api/agents/config/${encodeURIComponent(d.id)}`, body);
    if (ok) this._draft = null;
  }

  async #remove(a: AgentConfig) {
    if (!confirm(`Удалить агента «${a.name || a.id}»?`)) return;
    if (await this.#send('DELETE', `/api/agents/config/${encodeURIComponent(a.id)}`)) {
      if (this._draft?.id === a.id) this._draft = null;
    }
  }

  async #reset(a: AgentConfig) {
    if (await this.#send('POST', `/api/agents/config/${encodeURIComponent(a.id)}/reset`)) {
      if (this._draft?.id === a.id) this._draft = null;
    }
  }

  #patch(part: Partial<Draft>) {
    if (this._draft) this._draft = {...this._draft, ...part};
  }

  static styles = css`
    :host {
      display: block;
      font-family: system-ui, sans-serif;
      color: #222;
    }
    .cols {
      display: flex;
      gap: 16px;
      align-items: flex-start;
    }
    .list {
      flex: 0 0 260px;
      border: 1px solid #d0d7de;
      border-radius: 10px;
      overflow: hidden;
    }
    .row {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 10px 12px;
      border-bottom: 1px solid #eef1f4;
      cursor: pointer;
    }
    .row:last-child {
      border-bottom: none;
    }
    .row:hover {
      background: #f6f8fa;
    }
    .row.hidden-agent {
      opacity: 0.55;
    }
    .row-main {
      flex: 1;
      min-width: 0;
    }
    .row-name {
      font-weight: 600;
      font-size: 14px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .row-id {
      font-size: 12px;
      color: #8b949e;
    }
    .badge {
      font-size: 11px;
      padding: 1px 7px;
      border-radius: 999px;
      border: 1px solid #d0d7de;
      color: #57606a;
      white-space: nowrap;
    }
    .badge.ok {
      color: #1a7f37;
      border-color: #b7e0c1;
    }
    .badge.down {
      color: #b35900;
      border-color: #f0cba8;
    }
    .add {
      width: 100%;
      padding: 10px;
      border: none;
      border-top: 1px solid #eef1f4;
      background: #f6f8fa;
      color: #0b57d0;
      font-weight: 600;
      cursor: pointer;
    }
    .form {
      flex: 1;
      border: 1px solid #d0d7de;
      border-radius: 10px;
      padding: 16px;
    }
    label {
      display: block;
      margin: 10px 0 4px;
      font-size: 13px;
      font-weight: 600;
      color: #57606a;
    }
    input[type='text'],
    input[type='password'],
    select,
    textarea {
      width: 100%;
      box-sizing: border-box;
      padding: 8px 10px;
      border: 1px solid #ccc;
      border-radius: 8px;
      font-size: 14px;
      font-family: inherit;
    }
    input[disabled] {
      background: #f0f1f3;
      color: #8b949e;
    }
    h4 {
      margin: 18px 0 2px;
      font-size: 14px;
    }
    .hint.warn {
      color: #9a6700;
    }
    .hint {
      font-size: 12px;
      color: #8b949e;
      margin-top: 3px;
    }
    .check {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-top: 12px;
      font-size: 14px;
    }
    .actions {
      display: flex;
      gap: 8px;
      margin-top: 18px;
    }
    button.primary {
      padding: 10px 18px;
      border: none;
      border-radius: 8px;
      background: #1177ee;
      color: #fff;
      font-weight: 600;
      cursor: pointer;
    }
    button.ghost {
      padding: 10px 18px;
      border: 1px solid #d0d7de;
      border-radius: 8px;
      background: #fff;
      color: #57606a;
      cursor: pointer;
    }
    button[disabled] {
      opacity: 0.5;
      cursor: default;
    }
    .error {
      margin: 12px 0 0;
      padding: 10px 12px;
      border-radius: 8px;
      background: #fff1f0;
      border: 1px solid #f5c2c0;
      color: #b32020;
      font-size: 13px;
    }
    .empty {
      color: #8b949e;
      font-size: 14px;
    }
  `;

  #renderRow(a: AgentConfig) {
    const st = this._status[a.id];
    return html`<div
      class=${a.hidden ? 'row hidden-agent' : 'row'}
      @click=${() => this.#edit(a)}
    >
      <div class="row-main">
        <div class="row-name">${a.name || a.id}</div>
        <div class="row-id">${a.id} · ${a.source === 'ui' ? 'изменён в UI' : 'из файла'}</div>
      </div>
      ${a.hidden
        ? html`<span class="badge">скрыт</span>`
        : st?.probed
          ? html`<span class=${st.available ? 'badge ok' : 'badge down'}>
              ${st.available ? 'отвечает' : 'не отвечает'}
            </span>`
          : nothing}
    </div>`;
  }

  /**
   * Клиентский сертификат. Отдельно от «Аутентификации»: mTLS — свойство
   * транспорта и сочетается с Basic, а не заменяет его.
   */
  #renderTls(d: Draft) {
    const path = (id: string, label: string, key: 'certFile' | 'keyFile' | 'caFile',
        lock: string, placeholder: string) => {
      const locked = d.envLocked.includes(lock);
      return html`
        <label for=${id}>${label}</label>
        <input id=${id} type="text" .value=${d.tls[key]} ?disabled=${locked}
          placeholder=${placeholder}
          @input=${(e: Event) =>
            this.#patch({tls: {...d.tls, [key]: (e.target as HTMLInputElement).value}})} />
        ${locked ? html`<div class="hint">Перекрыто переменной окружения.</div>` : nothing}`;
    };
    return html`
      <h4>Клиентский сертификат (mTLS)</h4>
      <div class="hint">
        Пути к PEM-файлам на машине оркестратора; сами файлы через браузер не
        передаются. Работает только с адресом https://.
      </div>
      ${path('f-tls-cert', 'Сертификат', 'certFile', 'tlsCert', 'configs/certs/client.crt')}
      ${path('f-tls-key', 'Приватный ключ', 'keyFile', 'tlsKey', 'configs/certs/client.key')}
      ${path('f-tls-ca', 'CA сервера', 'caFile', 'tlsCa', 'пусто — системные корни')}
      <div class="check">
        <input id="f-tls-insecure" type="checkbox" .checked=${d.tls.insecureSkipVerify}
          @change=${(e: Event) =>
            this.#patch({tls: {...d.tls, insecureSkipVerify: (e.target as HTMLInputElement).checked}})} />
        <label for="f-tls-insecure" style="margin:0">Не проверять сертификат сервера</label>
      </div>
      ${d.tls.insecureSkipVerify
        ? html`<div class="hint warn">
            Подменить агента сможет любой в сети. Только для стенда, когда CA нет под рукой.
          </div>`
        : nothing}`;
  }

  #renderForm(d: Draft) {
    const lockedURL = d.envLocked.includes('url');
    const lockedPass = d.envLocked.includes('password');
    return html`<div class="form">
      <label for="f-id">Идентификатор</label>
      <input
        id="f-id"
        type="text"
        .value=${d.id}
        ?disabled=${!d.isNew}
        placeholder="например, shop"
        @input=${(e: Event) => this.#patch({id: (e.target as HTMLInputElement).value})}
      />
      <div class="hint">
        ${d.isNew
          ? 'Латиница в нижнем регистре, цифры, дефис. Из него получается имя делегирующего инструмента: ask_<id>.'
          : 'Идентификатор не меняется: чтобы сменить его, заведите агента заново.'}
      </div>

      <label for="f-name">Название</label>
      <input id="f-name" type="text" .value=${d.name}
        placeholder="подпись в селекторе; пусто — имя из AgentCard"
        @input=${(e: Event) => this.#patch({name: (e.target as HTMLInputElement).value})} />

      <label for="f-url">Адрес</label>
      <input id="f-url" type="text" .value=${d.url} ?disabled=${lockedURL}
        placeholder="http://192.168.1.68:18800"
        @input=${(e: Event) => this.#patch({url: (e.target as HTMLInputElement).value})} />
      ${lockedURL
        ? html`<div class="hint">Перекрыто переменной окружения — правка здесь ни на что не повлияет.</div>`
        : nothing}

      <label for="f-card">Путь к AgentCard</label>
      <input id="f-card" type="text" .value=${d.cardPath}
        placeholder="/.well-known/agent-card.json"
        @input=${(e: Event) => this.#patch({cardPath: (e.target as HTMLInputElement).value})} />

      <label for="f-skill">Навык (metadata.skill)</label>
      <input id="f-skill" type="text" .value=${d.skill}
        placeholder="пусто — не отправлять"
        @input=${(e: Event) => this.#patch({skill: (e.target as HTMLInputElement).value})} />

      <label for="f-timeout">Таймаут хода</label>
      <input id="f-timeout" type="text" .value=${d.timeout} placeholder="120s"
        @input=${(e: Event) => this.#patch({timeout: (e.target as HTMLInputElement).value})} />

      <label for="f-descr">Описание для модели</label>
      <textarea id="f-descr" rows="3" .value=${d.description}
        placeholder="чем этот агент отличается от прочих — по нему модель выбирает его в режиме «Авто»"
        @input=${(e: Event) => this.#patch({description: (e.target as HTMLTextAreaElement).value})}></textarea>

      <div class="check">
        <input id="f-verbatim" type="checkbox" .checked=${d.verbatim}
          @change=${(e: Event) => this.#patch({verbatim: (e.target as HTMLInputElement).checked})} />
        <label for="f-verbatim" style="margin:0">Отвечать напрямую, без локальной модели</label>
      </div>

      <label for="f-auth">Аутентификация</label>
      <!-- .value на самом select, а не ?selected на <option>: элемент
           переиспользуется между агентами (Lit сверяет шаблон по позиции), и
           dirty-флаг, взведённый переключением у одного агента, глушит
           дальнейшие изменения атрибута selected при открытии другого. Опции
           здесь статические дети того же шаблона, так что оговорка из
           app.ts про пустой селектор при появлении дочерних узлов позже сюда
           не относится. -->
      <select id="f-auth" .value=${d.auth.type}
        @change=${(e: Event) =>
          this.#patch({auth: {...d.auth, type: (e.target as HTMLSelectElement).value}})}>
        <option value="">нет</option>
        <option value="basic">HTTP Basic</option>
      </select>

      ${d.auth.type === 'basic'
        ? html`
            <label for="f-user">Логин</label>
            <input id="f-user" type="text" .value=${d.auth.username}
              @input=${(e: Event) =>
                this.#patch({auth: {...d.auth, username: (e.target as HTMLInputElement).value}})} />

            <label for="f-pass">Пароль</label>
            <input id="f-pass" type="password" .value=${d.password} ?disabled=${lockedPass}
              placeholder=${d.auth.hasPassword ? 'задан — оставьте пустым, чтобы не менять' : ''}
              @input=${(e: Event) => this.#patch({password: (e.target as HTMLInputElement).value})} />
            <div class="hint">
              ${lockedPass
                ? 'Перекрыт переменной окружения.'
                : 'Пароль не показывается: сервер его не отдаёт.'}
            </div>
          `
        : nothing}

      ${this.#renderTls(d)}

      ${this._error ? html`<p class="error">${this._error}</p>` : nothing}

      <div class="actions">
        <button class="primary" ?disabled=${this._busy} @click=${() => void this.#save()}>
          Сохранить
        </button>
        <button class="ghost" ?disabled=${this._busy} @click=${() => (this._draft = null)}>
          Отмена
        </button>
        ${!d.isNew && d.canReset
          ? html`<button class="ghost" ?disabled=${this._busy}
              title="Забыть правки из UI и вернуть версию из orchestrator.yaml"
              @click=${() => void this.#reset(d)}>
              Сбросить к конфигу
            </button>`
          : nothing}
        ${!d.isNew && !d.hidden
          ? html`<button class="ghost" ?disabled=${this._busy} @click=${() => void this.#remove(d)}>
              Удалить
            </button>`
          : nothing}
        ${d.hidden
          ? html`<button class="ghost" ?disabled=${this._busy} @click=${() => void this.#reset(d)}>
              Вернуть
            </button>`
          : nothing}
      </div>
    </div>`;
  }

  render() {
    return html`
      <div class="cols">
        <div class="list">
          ${this._agents.length
            ? this._agents.map((a) => this.#renderRow(a))
            : html`<div class="row"><span class="empty">Агентов нет</span></div>`}
          <button class="add" @click=${() => this.#add()}>+ Добавить агента</button>
        </div>
        ${this._draft
          ? this.#renderForm(this._draft)
          : html`<div class="form">
              <p class="empty">Выберите агента слева или добавьте нового.</p>
              ${this._error ? html`<p class="error">${this._error}</p>` : nothing}
            </div>`}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'agent-settings': AgentSettings;
  }
}
