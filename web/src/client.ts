import type {Message, Part} from '@a2a-js/sdk';
import {ClientFactory, type Client} from '@a2a-js/sdk/client';

const A2UI_EXT = 'https://a2ui.org/a2a-extension/a2ui/v0.9';

// Our Go backend (a2a-go v2.3.1) reads the requested extension from the HTTP
// header named exactly "A2A-Extensions" (no "X-" prefix, unlike the SDK's own
// HTTP_EXTENSION_HEADER constant, which targets the Python-reference
// convention "X-A2A-Extensions"). We therefore set this header ourselves via
// `serviceParameters` on every call instead of using the SDK's
// `withA2AExtensions()` helper.
const A2A_EXTENSIONS_HEADER = 'A2A-Extensions';

/** Thin wrapper around @a2a-js/sdk's Client that speaks the A2UI extension. */
export class A2UIClient {
  #baseUrl: string;
  #client: Client | null = null;
  #contextId: string | undefined;

  constructor(baseUrl = '') {
    this.#baseUrl = baseUrl;
  }

  async #getClient(): Promise<Client> {
    if (!this.#client) {
      const base = this.#baseUrl || location.origin;
      this.#client = await new ClientFactory().createFromUrl(base);
    }
    return this.#client;
  }

  async #send(parts: Part[]): Promise<any[]> {
    const client = await this.#getClient();
    const message: Message = {
      messageId: crypto.randomUUID(),
      role: 'user',
      parts,
      kind: 'message',
    };
    if (this.#contextId) message.contextId = this.#contextId;

    const result = await client.sendMessage(
      {message},
      {serviceParameters: {[A2A_EXTENSIONS_HEADER]: A2UI_EXT}},
    );

    // Track contextId ONLY (never taskId) for follow-up turns: the
    // orchestrator always completes its A2A task at the end of a turn, so
    // referencing a completed taskId on the next send would be rejected by
    // a2asrv as "task in a terminal state". Reusing just the contextId lets
    // the server start a fresh task within the same session.
    if (result.kind !== 'task') return [];
    const task = result;
    if (task.contextId) this.#contextId = task.contextId;

    // Extract A2UI messages from the latest artifact's data parts.
    const artifact = task.artifacts?.[task.artifacts.length - 1];
    const msgs: any[] = [];
    for (const p of artifact?.parts ?? []) {
      if (p.kind === 'data') msgs.push(p.data);
    }
    return msgs;
  }

  sendText(text: string): Promise<any[]> {
    return this.#send([{kind: 'text', text}]);
  }

  sendAction(name: string, context: Record<string, any>): Promise<any[]> {
    return this.#send([{kind: 'data', data: {version: 'v0.9', action: {name, context}}}]);
  }
}
