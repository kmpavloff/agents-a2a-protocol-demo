---
name: verify
description: Build, launch and drive the A2A orders demo (Go and/or Java agents) to verify changes end-to-end — headlessly over JSON-RPC, plus a screenshot recipe for the parts only a browser can prove.
---

# Verifying the A2A orders demo

Two interchangeable implementations of the same two agents: Go (`cmd/worker`,
`cmd/orchestrator`) and Java (`java/worker`, `java/orchestrator`). Same A2A 1.0
wire format, same `configs/*.yaml` + env overrides, same `data/orders.json`.
Any worker↔orchestrator combination is a valid interop check.

## Build & launch (from repo root)

```bash
# Java (single jars):
cd java && mvn -q package && cd ..
java -jar java/worker/target/a2a-demo-worker-0.1.0.jar &          # A2A server :8081
java -jar java/orchestrator/target/a2a-demo-orchestrator-0.1.0.jar  # TUI REPL

# Go:
go run ./cmd/worker &     # :8081
go run ./cmd/orchestrator # TUI REPL (or --web for the browser mode)
```

Both orchestrators support `--web` (A2A server + A2UI gateway on :8080). The
Java jar serves the frontend from disk (`internal/webui/dist` or `web/dist`,
built via `cd web && yarn build`); Go embeds it at compile time. Web mode is
drivable without a browser: POST JSON-RPC `SendMessage` to `:8080/invoke` with
header `A2A-Extensions: https://a2ui.org/a2a-extension/a2ui/v0.9` and expect
`application/a2ui+json` DataParts (createSurface/updateComponents) in the task
artifact; a confirmation is answered by sending a DataPart
`{"version":"v0.9","action":{"name":"approve_refund","context":{...}}}` with
the SAME contextId (watch "LLM bypassed" in the trace). The real browser SDK
path can be exercised headlessly with a node script from `web/` using
`@a2a-js/sdk` (`ClientFactory().createFromUrl('http://localhost:8080')`).

Worker readiness: `curl -s localhost:8081/.well-known/agent-card.json`.
The orchestrator REPL is interactive — drive it in tmux
(`tmux -L <sock> new-session -d 'java -jar …'`, `send-keys`, `capture-pane -p`)
and wait until the last pane line is exactly `вы> ` (turn finished). One turn
with a real LLM takes ~10s–3min; poll, don't sleep a fixed time.

## LLM

`configs/{worker,orchestrator}.yaml` hold the real endpoint/key (gitignored).
For deterministic runs without any LLM there is a scripted stub pattern: a tiny
OpenAI-compatible HTTP server that answers a tool_call first, then a text —
point the agents at it with `LLM_BASE_URL=http://127.0.0.1:<port>/v1`.

## Flows worth driving

- `статус заказа 1041` → order card (`widget/order`)
- `последние заказы alice` → list card, newest first
- `верни деньги за <id>` → confirmation card (refund must NOT run yet) → `да`
  resumes the SAME task into a SECOND `input-required`: the card form
  (`widget/refund_form`). An invalid number (`1234 5678 9012 3456`, Luhn
  fails) is re-asked; a valid one (`4111 1111 1111 1111`) executes the refund
  → receipt card (`widget/refund_receipt`, card masked `•••• 1111`) + a
  `receipt-<id>.html` A2A raw part — the TUI saves it to CWD, web serves a
  download chip. In web mode the form submit is the
  `submit_refund_details` action with `card_number` resolved from the
  TextField's `{path}` binding; it resumes the worker directly ("LLM
  bypassed" in the trace) and the card is masked in all trace lines.
- decline probe: answer `нет, передумал` → «Возврат отменён…», order unchanged
  (works at both the confirmation and the card step — any digit-less reply at
  the card step cancels)
- error probes: unknown order 9999, non-refundable 1055
- worker-side trace: worker prints `[A2A worker]` lines to stdout; the
  orchestrator's trace goes to `a2a-orchestrator.log` (path: `a2a_log_path`)

## Screenshot check (when JSON is not enough)

The JSON-RPC path above proves the wire format, **not** that the browser
renders anything. Some failures are invisible to it: a widget whose component
tree the renderer refuses to walk still arrives as perfectly valid
`application/a2ui+json`. Take a screenshot whenever the change touches A2UI
generation/ingestion, the agent selector, or anything else visual.

Chromium binaries usually already live in `~/.cache/ms-playwright`; the driver
does not. Install it **outside the repo** so nothing lands in `web/package.json`:

```bash
SHOT=$(mktemp -d)
cd "$SHOT" && npm init -y >/dev/null && npm install playwright-core
# no browsers in the cache? npx playwright install chromium
```

Driver skeleton (adjust the prompt and the agent):

```js
import {chromium} from 'playwright-core';
const browser = await chromium.launch({
  executablePath: process.env.HOME + '/.cache/ms-playwright/chromium-1217/chrome-linux64/chrome',
  args: ['--no-sandbox'],
});
const page = await browser.newPage({viewport: {width: 900, height: 1000}});
const errors = [];
page.on('pageerror', (e) => errors.push('pageerror: ' + e));
page.on('console', (m) => m.type() === 'error' && errors.push('console: ' + m.text()));

await page.goto('http://localhost:8080', {waitUntil: 'networkidle'});
// EVERYTHING lives in the shadow root of <orders-app>; plain selectors miss it.
await page.waitForFunction(
  () => document.querySelector('orders-app')?.shadowRoot?.querySelector('#agent'),
  null, {timeout: 15000},
);
await page.evaluate(() => {
  const root = document.querySelector('orders-app').shadowRoot;
  const sel = root.querySelector('#agent');       // agent selector: auto | <id>
  sel.value = 'ouroboros';
  sel.dispatchEvent(new Event('change', {bubbles: true}));
  root.querySelector('input').value = 'покажи статус заказа ORD-001';
  root.querySelector('form').dispatchEvent(new Event('submit', {bubbles: true, cancelable: true}));
});
await page.waitForFunction(
  () => document.querySelector('orders-app').shadowRoot.querySelector('.widget a2ui-surface'),
  null, {timeout: 180000},                        // a remote agent turn can take ~1 min
);
await page.waitForTimeout(1200);                  // let the surface paint
await page.screenshot({path: 'widget.png', fullPage: true});
console.log('ERRORS:', errors.length ? errors.join('\n') : 'none');
await browser.close();
```

**Then look at the image.** The checks that matter are visual:

- `Loading surface…` inside the widget frame = the component tree has no
  component with id exactly `root`. `@a2ui/lit` starts its walk there
  (`a2ui-surface.js:125`), so a tree rooted at any other id renders nothing.
  `a2ui.Ingest` adds the missing root; if this reappears, that is where to look.
- An empty widget frame, or plain text where a card belongs, means the A2UI
  never reached the renderer — check `A2A-Extensions` negotiation in the trace.
- `console --errors` equivalent: the `errors` array above must print `none`.

Driver gotchas:

- `page.waitForFunction(fn, arg, options)` — the timeout is the **third**
  argument. Passing `{timeout: …}` second silently keeps the 30 s default.
- Shadow DOM: `page.click('button')` will not find anything; go through
  `orders-app.shadowRoot` inside `page.evaluate`.
- A `<select>` popup does not render headlessly. Read `sel.options` for the
  list and set `sel.value` + dispatch `change` to switch agents.

Clean up the temp dir when done; never commit the driver or `node_modules`.

## Gotchas

- Refunds mutate only the in-memory store — restarting the worker resets data.
- Kill leftovers between runs: `lsof -ti:8081 | xargs -r kill`,
  `pkill -f a2a-demo-worker`.
- Assistant text after a widget is suppressed unless short (`isBriefComment`) —
  a missing `ассистент>` line after a card can be that filter, not a hang.
