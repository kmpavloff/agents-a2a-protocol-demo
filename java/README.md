# A2A Orders Assistant — Java implementation

A Java (Spring Boot) port of the Go **worker** and **orchestrator** agents from
the repository root. Both build into **single runnable jars** and speak the
same **A2A 1.0** wire format (JSON-RPC binding, as implemented by `a2a-go v2`),
so the Java and Go agents are interchangeable:

- Java worker ↔ Go orchestrator ✔
- Go worker ↔ Java orchestrator ✔
- Java worker ↔ Java orchestrator ✔

What is ported:

- **worker (`orders-agent`)** — A2A server on `:8081`: AgentCard at
  `/.well-known/agent-card.json`, JSON-RPC `SendMessage` at `/invoke`, the five
  mock order tools, the `NEED_INPUT` clarification flow (`input-required` +
  task resume), the two-step human-in-the-loop refund (yes/no confirmation,
  then card details validated by code with a Luhn check), the downloadable
  refund receipt (an A2A raw/file part), and widget `DataPart`s
  (`widget/order`, `widget/order_list`, `widget/confirmation`,
  `widget/refund_form`, `widget/refund_receipt`).
- **orchestrator** — terminal REPL: resolves the worker AgentCard, derives the
  `ask_orders_agent` delegating tool from it, resumes pending `input-required`
  tasks, renders widgets inline, and writes the A2A protocol trace to
  `a2a-orchestrator.log`.
- **orchestrator `--web`** — the A2UI browser gateway, same as the Go binary:
  the orchestrator becomes an A2A server on `:8080` (AgentCard advertising the
  A2UI extension, JSON-RPC `/invoke`), translates worker widgets into A2UI
  `createSurface`/`updateComponents` messages (`application/a2ui+json`
  DataParts), maps incoming A2UI button actions back onto the conversation
  (`approve_refund`/`decline_refund` resume the pending worker task directly,
  bypassing the LLM), and serves the browser frontend.
- **multi-agent support** — the `agents:` list in `configs/orchestrator.yaml`
  (`id`/`name`/`url`/`card_path`/`skill`/`verbatim`/`timeout`/`description`
  plus HTTP Basic), the env overrides `A2A_AGENT_<ID>_URL` and
  `A2A_AGENT_<ID>_PASSWORD`, the agent selector in the browser
  (`GET /api/agents`), and `verbatim` mode, where the selected agent's reply
  goes straight to the browser without the local model.
- **Settings screen** — editing the agent list right from the browser, on top
  of the overlay file `configs/agents.local.yaml` (`agents_overlay_path`,
  `A2A_AGENTS_OVERLAY_PATH`); edits apply to the live registry without a
  restart.
- **A2UI v0.9.1** — both extension revisions are advertised and accepted, some
  parts are tagged with `metadata.mimeType`, the payload travels as a message
  array, and client events carry all five fields of the `client_to_server`
  schema.
- **third-party agent markup** — accepted and normalized to the basic catalog
  (wrapped components, tables, a missing surface root).
- **protocol dump** — `A2A_DEBUG=1` prints request and response bodies in
  full on both legs (browser↔orchestrator and orchestrator↔agent), with the
  card number masked.

One difference from Go: the jar does **not** embed the frontend build. At
startup the web mode looks for it on disk — `$WEBUI_DIST`, then
`internal/webui/dist`, then `web/dist` (relative to the working directory) —
and serves a friendly "frontend not built" page when none is found. Build it
once with `cd web && yarn install && yarn build`, as for the Go binary.

## Layout

```
java/
  common/        # A2A wire types, JSON-RPC, OpenAI-compatible LLM client, config loader
  worker/        # Spring Boot web app: A2A server + LLM agent + order tools
  orchestrator/  # single-jar TUI app: A2A client + LLM agent + REPL
```

## Prerequisites

| Requirement | Notes |
|---|---|
| **JDK 21+** | `java.version` is 21; any newer JDK works |
| **Maven 3.9+** | Wrapper not included; use a system Maven |
| **LM Studio** (or any OpenAI-compatible endpoint) | Same as the Go demo — a tool-capable model on port 1234 |

The test suite (`mvn test`) runs without LM Studio — it uses a scripted stub
LLM and exercises the full A2A round-trip (including `input-required` → resume)
in-process.

## Build

```bash
cd java
mvn package
# → worker/target/a2a-demo-worker-0.1.0.jar
# → orchestrator/target/a2a-demo-orchestrator-0.1.0.jar
```

## Run

The jars read the **same configs and data as the Go agents** — run them from
the repository root so `configs/worker.yaml`, `configs/orchestrator.yaml` and
`data/orders.json` resolve (see the root README for the first-time
`cp configs/*.example.yaml` setup). A different config path can be passed as
the first argument. All the Go env overrides work too (`LLM_BASE_URL`,
`LLM_MODEL`, `LLM_API_KEY`, `WORKER_URL`, `WORKER_LISTEN_ADDR`,
`WORKER_PUBLIC_URL`, `WORKER_DATA_PATH`, `ORDER_LINK_BASE`, `A2A_LOG_PATH`,
`A2A_AGENT_<ID>_URL`, `A2A_AGENT_<ID>_PASSWORD`, `A2A_AGENTS_OVERLAY_PATH`).

**Terminal 1 — worker (A2A server):**

```bash
java -jar java/worker/target/a2a-demo-worker-0.1.0.jar
# orders-agent listening on :8081
```

**Terminal 2 — orchestrator (TUI):**

```bash
java -jar java/orchestrator/target/a2a-demo-orchestrator-0.1.0.jar
# вы> статус заказа 1041
```

**…or the web UI (A2UI) instead of the TUI:**

```bash
cd web && yarn install && yarn build && cd ..   # once, if not built yet
java -jar java/orchestrator/target/a2a-demo-orchestrator-0.1.0.jar --web
# orchestrator web UI on :8080 → open http://localhost:8080
```

Any combination with the Go binaries works — e.g. Go orchestrator against the
Java worker:

```bash
java -jar java/worker/target/a2a-demo-worker-0.1.0.jar   # terminal 1
go run ./cmd/orchestrator                                # terminal 2
```

## Tests

```bash
cd java
mvn test
```

This machine has no LM Studio, so nothing below exercises the real language
model or a browser — the stub-LLM and in-process HTTP fixtures below are what
`mvn test` actually runs and verifies.

Covered (mirroring the Go suite):

- the five order tools incl. error branches and widget building (`worker`)
- `parseAffirmative` fail-closed confirmation parsing (`worker`)
- full JSON-RPC round-trips with a stub LLM: completed-with-widget,
  refund → `input-required` → resume → `completed`, declined refund,
  `NEED_INPUT` clarification, task-not-found / method-not-found errors (`worker`)
- AgentCard → delegating-tool profile derivation (`orchestrator`)
- client wire format (incl. `configuration.acceptedOutputModes` and both
  extension-header names) + pending-task resume bookkeeping against a canned
  JSON-RPC server (`orchestrator`)
- the agent `Registry`: lazy connect, probe/availability bookkeeping, live
  `apply()` of overlay changes without a restart (`orchestrator`)
- the agent store and its overlay file: base config vs. UI overrides,
  env-var precedence (`A2A_AGENT_<ID>_URL`/`_PASSWORD`), atomic save,
  deterministic YAML key order (`orchestrator`)
- multi-agent selection in the web gateway: explicit agent choice, `auto`
  (model-picked), and `verbatim` agents whose reply skips the local model
  (`orchestrator`)
- third-party agent markup ingest: normalization of wrapped components,
  tables, and a missing surface root into the basic A2UI catalog
  (`orchestrator`)
- A2UI gateway: widget → `createSurface`/`updateComponents` mapping, action
  parsing, extension negotiation (including the positive case — a card that
  declares A2UI gets both the header and `a2uiClientCapabilities`), and the
  confirmation-button direct resume that bypasses the LLM (`orchestrator`)
- routing: `GET /api/agents` resolves to the JSON API and not the SPA
  fallback when both controllers share a Spring context (`orchestrator`)
- Part/Message/Task JSON pinned to the `a2a-go v2` fixtures (`common`)
- protocol-dump body formatting, truncation, and card masking (`common`)
