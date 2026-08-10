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

A second difference: the multi-agent support added to the Go orchestrator
(a list of remote agents in the config plus the agent selector in the browser —
see [«Several agents at once»](../README.md#several-agents-at-once)) is **not**
ported here. The Java orchestrator still talks to exactly one worker via
`worker_url`.

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
`WORKER_PUBLIC_URL`, `WORKER_DATA_PATH`, `ORDER_LINK_BASE`, `A2A_LOG_PATH`).

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

Covered (mirroring the Go suite):

- the five order tools incl. error branches and widget building (`worker`)
- `parseAffirmative` fail-closed confirmation parsing (`worker`)
- full JSON-RPC round-trips with a stub LLM: completed-with-widget,
  refund → `input-required` → resume → `completed`, declined refund,
  `NEED_INPUT` clarification, task-not-found / method-not-found errors (`worker`)
- AgentCard → delegating-tool profile derivation (`orchestrator`)
- client wire format + pending-task resume bookkeeping against a canned
  JSON-RPC server (`orchestrator`)
- A2UI gateway: widget → `createSurface`/`updateComponents` mapping, action
  parsing, extension negotiation, and the confirmation-button direct resume
  that bypasses the LLM (`orchestrator`)
- Part/Message/Task JSON pinned to the `a2a-go v2` fixtures (`common`)
