import {ClientFactory, type Client} from '@a2a-js/sdk/client';
import type {Message, Part, Task, SendMessageResult} from '@a2a-js/sdk';

const A2UI_EXT = 'https://a2ui.org/a2a-extension/a2ui/v0.9';

// ROLE_USER in the A2A v1.0 proto enum (@a2a-js/sdk v1.0.0-beta.0).
const ROLE_USER = 1;

/** What one turn returns: A2UI messages to render, plus the agent's plain text. */
export interface SendResult {
  a2ui: any[];
  text: string;
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

  async #send(parts: Part[]): Promise<SendResult> {
    const client = await this.#getClient();
    const message = {
      messageId: crypto.randomUUID(),
      role: ROLE_USER,
      parts,
      ...(this.#contextId ? {contextId: this.#contextId} : {}),
    } as unknown as Message;

    const result: SendMessageResult = await client.sendMessage(
      // The proto-generated SendMessageRequest type lists tenant/configuration/
      // metadata as required; only `message` is needed at runtime (proto fills
      // the rest), so cast past the over-strict type.
      {message} as any,
      // Request the A2UI extension. a2a-go reads the header named exactly
      // "A2A-Extensions" (keys are lowercased server-side for lookup).
      {serviceParameters: {'A2A-Extensions': A2UI_EXT} as any},
    );

    // The orchestrator always returns a Task (submitted → working → artifact →
    // completed). Reuse ONLY its contextId for follow-up turns: the task is
    // terminal at the end of each turn, so referencing its taskId would be
    // rejected; the shared contextId keeps the orchestrator session stable.
    const task = result as Task;
    if (task.contextId) this.#contextId = task.contextId;

    // A2A v1.0 parts are a proto oneof: `part.content = {$case, value}`.
    const artifact = task.artifacts?.[task.artifacts.length - 1];
    const a2ui: any[] = [];
    let text = '';
    for (const p of artifact?.parts ?? []) {
      const c = (p as any).content;
      if (c?.$case === 'data') a2ui.push(c.value);
      else if (c?.$case === 'text') text += c.value;
    }
    return {a2ui, text};
  }

  sendText(text: string): Promise<SendResult> {
    return this.#send([{content: {$case: 'text', value: text}} as unknown as Part]);
  }

  sendAction(name: string, context: Record<string, any>): Promise<SendResult> {
    return this.#send([
      {
        content: {$case: 'data', value: {version: 'v0.9', action: {name, context}}},
        mediaType: 'application/a2ui+json',
      } as unknown as Part,
    ]);
  }
}
