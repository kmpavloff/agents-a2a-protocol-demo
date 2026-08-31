package a2abridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

// attachedFile is a downloadable file the worker attached to an artifact,
// passed through to the browser as an A2A raw part.
type attachedFile struct {
	name      string
	mediaType string
	data      []byte
}

// autoAgentID — значение agentId, означающее «модель выбирает агента сама».
const autoAgentID = "auto"

// RunnerBuilder собирает adk-runner под конкретный набор делегирующих
// инструментов и блок возможностей для промпта. Набор зависит от того, какой
// агент выбран в UI, поэтому runner нельзя собрать один раз на старте.
type RunnerBuilder func(tools []tool.Tool, summary string) (*runner.Runner, error)

type orchExecutor struct {
	reg   *Registry
	build RunnerBuilder
	trace *Tracer

	// surfaceSeq разводит поверхности разных ходов. Сквозной, а не по сессиям:
	// нужна лишь уникальность в пределах страницы, а карта по сессиям росла бы
	// без конца — «конца сессии» в протоколе нет.
	surfaceSeq atomic.Uint64

	mu      sync.Mutex
	runners map[string]*runner.Runner   // agentId (или "auto") → runner
	gen     uint64                      // поколение реестра, под которое собран кэш runner'ов
	widgets map[string][]map[string]any // sessionID → widgets produced this turn
	a2uis   map[string][]map[string]any // sessionID → A2UI messages produced this turn
	files   map[string][]attachedFile   // sessionID → files produced this turn
}

// NewOrchestratorExecutor wraps the registry of remote agents as an A2A server
// that speaks the A2UI extension: it maps worker widgets to A2UI JSON, passes
// agent-authored A2UI through, and translates incoming A2UI button actions into
// task resumes. trace may be nil.
func NewOrchestratorExecutor(reg *Registry, build RunnerBuilder, trace *Tracer) a2asrv.AgentExecutor {
	e := &orchExecutor{
		reg:     reg,
		build:   build,
		trace:   trace,
		runners: make(map[string]*runner.Runner),
		widgets: make(map[string][]map[string]any),
		a2uis:   make(map[string][]map[string]any),
		files:   make(map[string][]attachedFile),
	}
	// Обработчики вешаются на КАЖДОГО клиента, включая созданных после правки
	// конфига: разовый цикл по нынешним агентам оставил бы новых без них, и
	// их виджеты молча пропадали бы.
	reg.SetClientInit(func(c *OrdersClient) {
		c.SetWidgetHandler(func(sessionID string, w map[string]any) {
			e.mu.Lock()
			e.widgets[sessionID] = append(e.widgets[sessionID], w)
			e.mu.Unlock()
		})
		c.SetA2UIHandler(func(sessionID string, msgs []map[string]any) {
			e.mu.Lock()
			e.a2uis[sessionID] = append(e.a2uis[sessionID], msgs...)
			e.mu.Unlock()
		})
		c.SetFileHandler(func(sessionID, filename, mediaType string, data []byte) {
			e.mu.Lock()
			e.files[sessionID] = append(e.files[sessionID], attachedFile{name: filename, mediaType: mediaType, data: data})
			e.mu.Unlock()
		})
	})
	e.gen = reg.Generation()
	return e
}

// selectAgent reads the agent chosen in the UI from the message metadata. An
// unknown id silently falls back to "auto": a browser holding a stale agent list
// must not break the conversation.
func (e *orchExecutor) selectAgent(msg *a2a.Message) string {
	if msg == nil || msg.Metadata == nil {
		return autoAgentID
	}
	id, _ := msg.Metadata["agentId"].(string)
	if id == "" || id == autoAgentID {
		return autoAgentID
	}
	if _, ok := e.reg.Get(id); !ok {
		e.trace.Logf("⚠ unknown agentId %q — falling back to auto", id)
		return autoAgentID
	}
	return id
}

// runnerFor returns the runner for the chosen agent: with every agent's tool in
// "auto" mode, or with exactly one tool when the user picked an agent. Runners
// are cached per selection.
func (e *orchExecutor) runnerFor(ctx context.Context, agentID string) (*runner.Runner, error) {
	// Состав агентов мог измениться из UI. Кэш собран под прежний: в нём и
	// старое описание в промпте, и инструмент удалённого агента.
	if gen := e.reg.Generation(); gen != e.gen {
		e.mu.Lock()
		clear(e.runners)
		e.gen = gen
		e.mu.Unlock()
	}

	cacheKey := agentID
	if agentID != autoAgentID {
		e.mu.Lock()
		r, ok := e.runners[cacheKey]
		e.mu.Unlock()
		if ok {
			return r, nil
		}
	}

	var tools []tool.Tool
	var summary string
	if agentID == autoAgentID {
		tools = e.reg.Tools(ctx)
		summary = strings.Join(e.reg.Summaries(ctx), "\n\n")
		// Кэш «Авто» привязан к набору доступных агентов: лежавший в момент
		// первой сборки агент иначе остался бы без инструмента навсегда, хотя
		// UI уже показывает его живым.
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name())
		}
		sort.Strings(names)
		cacheKey = autoAgentID + "|" + strings.Join(names, ",")
	} else {
		remote, ok := e.reg.Get(agentID)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q", agentID)
		}
		if err := remote.Connect(ctx); err != nil {
			return nil, err
		}
		c, ok := e.reg.ClientFor(agentID)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q", agentID)
		}
		tools = []tool.Tool{c.Tool()}
		summary = remote.Profile().Summary
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("нет доступных агентов")
	}

	e.mu.Lock()
	cached, ok := e.runners[cacheKey]
	e.mu.Unlock()
	if ok {
		return cached, nil
	}

	r, err := e.build(tools, summary)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.runners[cacheKey] = r
	e.mu.Unlock()
	return r, nil
}

// nextTurn возвращает номер очередного хода. Он превращается в суффикс
// surfaceId: агент выводит id поверхности из контекста, который живёт всю
// сессию, и без разведения второй ход роняет рендерер «Surface … already
// exists».
func (e *orchExecutor) nextTurn() int {
	return int(e.surfaceSeq.Add(1))
}

// drain takes everything collected for the session during this turn.
func (e *orchExecutor) drain(sessionID string) ([]map[string]any, []map[string]any, []attachedFile) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ws, msgs, fs := e.widgets[sessionID], e.a2uis[sessionID], e.files[sessionID]
	delete(e.widgets, sessionID)
	delete(e.a2uis, sessionID)
	delete(e.files, sessionID)
	return ws, msgs, fs
}

// actionToText maps an A2UI button action name to the user text the orchestrator
// LLM should process. Unknown actions fall back to a descriptive line.
func actionToText(name string, ctx map[string]any) string {
	switch name {
	case "approve_refund":
		return "да"
	case "decline_refund":
		return "нет"
	case "submit_refund_details":
		// The card number from the form's TextField ({path} binding resolved by
		// the renderer at click time). Resumed directly — never via the LLM.
		if s, _ := ctx["card_number"].(string); s != "" {
			return s
		}
		return ""
	default:
		return "Пользователь нажал действие: " + name
	}
}

// redactCard возвращает копию контекста без номера карты — для трейса. Именно
// копию: исходная карта принадлежит входящему сообщению, которое a2a-go
// параллельно сериализует в историю задачи, и правка на месте — гонка.
func redactCard(ctx map[string]any) map[string]any {
	out := make(map[string]any, len(ctx))
	for k, v := range ctx {
		if k == "card_number" {
			continue
		}
		out[k] = v
	}
	return out
}

// safeActionEcho renders an action's user text for traces, masking payment
// details so a full card number never lands in the protocol log.
func safeActionEcho(name, userText string) string {
	if name == "submit_refund_details" {
		return maskCard(cardDigits(userText))
	}
	return userText
}

func (e *orchExecutor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		reqStart := time.Now()
		sessionID := ec.ContextID
		nParts := 0
		if ec.Message != nil {
			nParts = len(ec.Message.Parts)
		}

		// Activate the A2UI extension if the client requested it — под любым из
		// двух имён ревизии: клиент на 0.9 попросит старый URI, и отказать ему
		// значило бы ответить голым текстом на ровном месте.
		a2uiActive := false
		if exts, ok := a2asrv.ExtensionsFrom(ctx); ok {
			for _, uri := range []string{a2ui.ExtensionURI, a2ui.LegacyExtensionURI} {
				ext := &a2a.AgentExtension{URI: uri}
				if exts.Requested(ext) {
					exts.Activate(ext)
					a2uiActive = true
					break
				}
			}
		}
		// Режим едет дальше по цепочке: не запросив расширение, клиент выбрал
		// текст, и просить разметку у внешнего агента незачем — он потратит на
		// неё время, а мы всё равно выбросим.
		if !a2uiActive {
			ctx = withoutA2UI(ctx)
		}
		e.trace.Logf("▶ orchestrator A2A request | contextID=%s a2ui=%v inParts=%d stored=%v",
			sessionID, a2uiActive, nParts, ec.StoredTask != nil)
		e.dump("сообщение от клиента", ec.Message)

		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// Parse input: an A2UI action DataPart, or plain text.
		userText := ""
		actionName := ""
		actionSurface := ""
		actionSource := ""
		actionCtx := map[string]any{}
		if ec.Message != nil {
			for _, p := range ec.Message.Parts {
				if name, actx, ok := a2ui.ParseAction(p.Data()); ok {
					actionName = name
					actionCtx = actx
					actionSurface, actionSource = a2ui.ActionOrigin(p.Data())
					// Событием действие уходит только verbatim-агенту. Всё
					// остальное — HITL-возврат и делегирование через модель —
					// работает текстом, и в нём должен быть контекст: без него
					// модель видит «нажал return_order» и не знает, какой заказ.
					userText = actionToPrompt(name, actx)
					e.trace.Logf("  A2UI action %q ctx=%s → user text %q",
						name, compactArgs(redactCard(actx)), safeActionEcho(name, userText))
					break
				}
				if t := p.Text(); t != "" {
					userText = t
				}
			}
		}

		agentID := e.selectAgent(ec.Message)
		turn := e.nextTurn()
		e.trace.Logf("  agent selection: %s | ход #%d", agentID, turn)

		// Reset this session's slots before the run.
		e.drain(sessionID)

		// Ответ уезжает частями завершающего сообщения задачи: там их ищет спека
		// расширения A2UI, и оттуда их читает референсный клиент
		// (result.status.message.parts). Артефакт для этого не предназначен.
		emit := func(text string, ws, msgs []map[string]any, fs []attachedFile, what string) {
			parts := []*a2a.Part{a2a.NewTextPart(strings.TrimSpace(orDefault(text, "Готово.")))}
			parts = append(parts, e.a2uiParts(a2uiActive, ws)...)
			parts = append(parts, e.a2uiMessageParts(a2uiActive, msgs, turn)...)
			parts = append(parts, fileParts(fs)...)
			e.trace.Logf("  → emit: completed message | %s | parts=%d requestTook=%s",
				what, len(parts), time.Since(reqStart).Round(time.Millisecond))
			reply := a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, parts...)
			e.dump("ответ клиенту", reply)
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, reply), nil)
		}

		// An explicitly chosen verbatim agent answers the user directly: its text
		// and its A2UI go to the browser untouched. Running a local model over an
		// answer that is already final would only paraphrase it away and add its
		// own latency on top of the remote agent's.
		if remote, ok := e.reg.Get(agentID); ok && agentID != autoAgentID && remote.Verbatim() {
			e.trace.Logf("  verbatim agent %q → no local LLM", agentID)
			var reply Reply
			var err error
			if actionName != "" {
				// Нажатие уезжает штатным событием A2UI: пересказ словами
				// понимает только модель, а событие — любой агент на
				// референсном SDK.
				// Поверхность возвращается агенту под его собственным именем: в
				// ленте она переименована по ходам (см. a2uiMessageParts), а
				// сопоставить событие агент может только со своим id.
				surface := a2ui.UntagSurface(actionSurface)
				e.trace.Logf("  A2UI action %q → событие агенту | surface=%q source=%q ctx=%s",
					actionName, surface, actionSource, compactArgs(redactCard(actionCtx)))
				reply, err = remote.AskAction(ctx, sessionID, actionName, surface, actionSource, actionCtx)
			} else {
				reply, err = remote.Ask(ctx, sessionID, userText)
			}
			if err != nil {
				e.trace.Logf("  ✖ verbatim turn failed: %v", err)
				emit(agentErrorText(remote.Name(), err), nil, nil, nil, "verbatim error")
				return
			}
			emit(reply.Text, reply.Widgets, reply.A2UI, reply.Files, "verbatim")
			return
		}

		// A button on a pending HITL step (yes/no confirmation, or the card
		// form) resumes the worker task DIRECTLY with the canonical answer,
		// bypassing the orchestrator LLM. The LLM tends to paraphrase "да" into
		// a full sentence, which the worker's fail-closed confirmation parser
		// rejects; and card details should never pass through an LLM at all.
		directResume := actionName == "approve_refund" || actionName == "decline_refund" ||
			actionName == "submit_refund_details"
		if oc := e.pendingClient(agentID, sessionID); directResume && oc != nil {
			e.trace.Logf("  confirmation button %q → resuming worker directly with %q (LLM bypassed)",
				actionName, safeActionEcho(actionName, userText))
			result, err := oc.ask(ctx, sessionID, userText)
			if err != nil {
				e.trace.Logf("  ✖ direct resume error: %v", err)
				yield(nil, err)
				return
			}
			// The resume may complete the task (receipt widget + file) or pause
			// it again (the card form after "да") — both carry widgets, and a
			// completion may carry files; emit them like a normal turn.
			ws, msgs, fs := e.drain(sessionID)
			emit(stripNeedsInput(result), ws, msgs, fs, "direct HITL resume")
			return
		}

		// Run the orchestrator LLM. Its delegating tools call the remote agents
		// and forward any widget/A2UI through the session handlers above.
		r, err := e.runnerFor(ctx, agentID)
		if err != nil {
			e.trace.Logf("  ✖ runner unavailable: %v", err)
			emit(fmt.Sprintf("Не удалось обратиться к агенту: %v", err), nil, nil, nil, "runner error")
			return
		}
		e.trace.Logf("%s  · оркестратор → LLM: %q%s", gray, userText, reset)
		llmStart := time.Now()
		msg := genai.NewContentFromText(userText, genai.RoleUser)
		var finalText string
		toolCalls := 0
		limitHit := false
		for event, err := range r.Run(ctx, "a2ui-user", sessionID, msg, agent.RunConfig{}) {
			if err != nil {
				e.trace.Logf("  ✖ runner error: %v", err)
				yield(nil, err)
				return
			}
			if event == nil || event.Content == nil {
				continue
			}
			for _, p := range event.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					toolCalls++
					e.trace.Logf("%s  · LLM → инструмент: %s(%s) [#%d]%s",
						gray, p.FunctionCall.Name, compactArgs(p.FunctionCall.Args), toolCalls, reset)
					if toolCalls > maxToolCallsPerTurn {
						limitHit = true
					}
				case p.FunctionResponse != nil:
					e.trace.Logf("%s  · инструмент %s → LLM: результат%s", gray, p.FunctionResponse.Name, reset)
				case p.Text != "":
					finalText = p.Text
				}
			}
			// Force-stop before the over-limit call executes: breaking the range
			// makes the runner's next yield return false, so adk halts the loop.
			if limitHit {
				e.trace.Logf("✖ tool-call limit (%d) exceeded — force-stopping the agent loop | session=%s",
					maxToolCallsPerTurn, sessionID)
				break
			}
		}
		if limitHit && strings.TrimSpace(finalText) == "" {
			finalText = "Не удалось обработать запрос за отведённое число шагов — возможно, модель зациклилась. Попробуйте переформулировать запрос."
		}
		e.trace.Logf("  LLM finished in %s | toolCalls=%d limitHit=%v finalText=%q",
			time.Since(llmStart).Round(time.Millisecond), toolCalls, limitHit, strings.TrimSpace(finalText))

		// Drain unconditionally so text-only sessions don't leak map entries;
		// only emit A2UI parts when the extension is active.
		ws, msgs, fs := e.drain(sessionID)
		emit(finalText, ws, msgs, fs, "llm turn")
	}
}

// pendingClient returns the client whose remote agent holds a pending
// input-required task for the session, so a HITL button resumes the right one.
// With an explicit selection only that agent is considered.
func (e *orchExecutor) pendingClient(agentID, sessionID string) *OrdersClient {
	ids := e.reg.IDs()
	if agentID != autoAgentID {
		ids = []string{agentID}
	}
	for _, id := range ids {
		r, ok := e.reg.Get(id)
		if !ok || r.PendingTaskID(sessionID) == "" {
			continue
		}
		if c, ok := e.reg.ClientFor(id); ok {
			return c
		}
	}
	return nil
}

// actionToPrompt renders an A2UI action as a sentence for an agent that speaks
// plain text only. Our own HITL actions have canonical answers (see
// actionToText); anything else — e.g. a button a remote agent invented — is
// described together with its context.
func actionToPrompt(name string, ctx map[string]any) string {
	switch name {
	case "approve_refund", "decline_refund", "submit_refund_details":
		return actionToText(name, ctx)
	}
	label, _ := ctx["label"].(string)
	if label == "" {
		label = name
	}
	var details []string
	for k, v := range ctx {
		if k == "label" {
			continue
		}
		details = append(details, fmt.Sprintf("%s: %v", k, v))
	}
	sort.Strings(details)
	out := fmt.Sprintf("Пользователь нажал кнопку «%s»", label)
	if len(details) > 0 {
		out += " (" + strings.Join(details, ", ") + ")"
	}
	return out
}

func (e *orchExecutor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// stripNeedsInput removes the delegating tool's NEEDS_USER_INPUT sentinel from
// a directly-resumed result, leaving the bare question for the browser (the
// accompanying widget carries the interactive form).
func stripNeedsInput(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "NEEDS_USER_INPUT:"))
}

// a2uiParts maps collected widgets to A2UI DataParts when the extension is
// active; otherwise it logs the drop and returns nothing.
func (e *orchExecutor) a2uiParts(a2uiActive bool, ws []map[string]any) []*a2a.Part {
	if !a2uiActive {
		if len(ws) > 0 {
			e.trace.Logf("  A2UI inactive — %d widget(s) dropped, text-only response", len(ws))
		}
		return nil
	}
	var parts []*a2a.Part
	for _, w := range ws {
		if msgs, ok := a2ui.FromWidget(w); ok {
			e.trace.Logf("  A2UI: widget %v → %d message(s) (application/a2ui+json)", w["_kind"], len(msgs))
			parts = append(parts, newA2UIPart(msgs))
		}
	}
	return parts
}

// a2uiMessageParts wraps A2UI messages a remote agent authored itself into
// DataParts. Unlike a2uiParts these need no mapping — they already went through
// a2ui.Ingest, which normalised whatever dialect the agent used.
func (e *orchExecutor) a2uiMessageParts(a2uiActive bool, msgs []map[string]any, turn int) []*a2a.Part {
	if !a2uiActive {
		if len(msgs) > 0 {
			e.trace.Logf("  A2UI inactive — %d agent message(s) dropped, text-only response", len(msgs))
		}
		return nil
	}
	// Каждый ход — своя поверхность: в ленте это отдельная карточка, а
	// повторный createSurface с тем же id рендерер не переживает.
	msgs = a2ui.RetagSurfaces(msgs, fmt.Sprintf("-t%d", turn))
	if len(msgs) == 0 {
		return nil
	}
	e.trace.Logf("  A2UI: %d message(s) from the agent passed through (application/a2ui+json)", len(msgs))
	return []*a2a.Part{newA2UIPart(msgs)}
}

// fileParts passes worker-attached files through as A2A raw parts (attached
// regardless of A2UI: files are plain protocol parts, not generative UI).
func fileParts(fs []attachedFile) []*a2a.Part {
	var parts []*a2a.Part
	for _, f := range fs {
		p := a2a.NewRawPart(f.data)
		p.Filename = f.name
		p.MediaType = f.mediaType
		parts = append(parts, p)
	}
	return parts
}

// dump печатает сообщение целиком, когда включён A2A_DEBUG. Сериализуется тем
// же кодом, что и на проводе: смысл дампа в том, чтобы видеть отправленное, а
// не его пересказ структурами Go.
func (e *orchExecutor) dump(label string, msg *a2a.Message) {
	if !e.trace.Debug() || msg == nil {
		return
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		e.trace.Logf("  ⚠ не удалось сериализовать %s для дампа: %v", label, err)
		return
	}
	e.trace.Dump(label, raw)
}

// agentErrorText превращает сбой хода в строку для ленты. Провалившийся ход и
// недоступный агент — разные беды, и валить их в одну формулировку нельзя:
// «недоступен» отправит пользователя чинить сеть там, где агент на связи и
// просто не справился с запросом.
func agentErrorText(name string, err error) string {
	var failed *TurnFailedError
	if errors.As(err, &failed) {
		if why := failed.FirstLine(); why != "" {
			return fmt.Sprintf("Агент %q не смог выполнить запрос: %s", name, why)
		}
		return fmt.Sprintf("Агент %q не смог выполнить запрос.", name)
	}
	return fmt.Sprintf("Агент %q недоступен: %v", name, err)
}

// withExt returns a context carrying a service-param request to activate the
// A2UI A2A extension, the mechanism a2a-go v2.3.1 clients use to convey
// requested extensions (there is no field on SendMessageRequest for this).
//
// Заголовок уходит под двумя именами. A2A 1.0 зовёт его A2A-Extensions, а спека
// A2UI v0.9 писалась под ранний A2A и знает только X-A2A-Extensions: агент,
// собранный по ней, второго имени не увидит.
func withExt(ctx context.Context) context.Context {
	uris := []string{a2ui.ExtensionURI, a2ui.LegacyExtensionURI}
	return a2aclient.AttachServiceParams(ctx, a2aclient.ServiceParams{
		a2a.SvcParamExtensions: uris,
		legacyExtHeader:        uris,
	})
}

// legacyExtHeader — имя заголовка расширений до A2A 1.0; его называет спека A2UI v0.9.
const legacyExtHeader = "X-" + a2a.SvcParamExtensions

// A2UIProbe is a minimal A2A client that activates the A2UI extension and
// returns the parts of the resulting task artifact. Used by the browser bridge
// tests and available for local diagnostics. It remembers the contextID from
// the last response so a follow-up turn (e.g. an A2UI action) lands on the
// same orchestrator session.
//
// It deliberately does NOT thread the previous taskID through: the
// orchestrator executor always drives its own task to TaskStateCompleted at
// the end of a turn (unlike the worker, which can leave a task
// input-required), so a2asrv rejects a follow-up message that references that
// task ("task in a terminal state"). Reusing only the ContextID lets a2asrv
// start a fresh task within the same context, which is what keeps
// ec.ContextID — and therefore the runner session and OrdersClient's pending
// map — stable across turns.
type A2UIProbe struct {
	client    *a2aclient.Client
	contextID string
	// AgentID, if set, rides along in the message metadata — the same way the
	// browser tells the orchestrator which agent the user picked.
	AgentID string
	// A2UI — режим разговора, как переключатель в браузере. false означает
	// «только текст»: расширение не объявляется ни заголовком, ни
	// capabilities, и оркестратор не просит разметку у внешнего агента.
	A2UI bool
}

func NewA2UIProbe(ctx context.Context, url string) (*A2UIProbe, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, url)
	if err != nil {
		return nil, err
	}
	cl, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return &A2UIProbe{client: cl, A2UI: true}, nil
}

// send delivers part to the agent, carrying over the previously seen
// contextID (if any) so the orchestrator resumes the same session, and
// returns the parts of the resulting task's completing message — то место, куда
// их кладёт спека расширения. Артефакт остаётся запасным путём: так отвечают
// агенты, писавшиеся до неё, включая наш Java-порт.
func (p *A2UIProbe) send(ctx context.Context, part *a2a.Part) ([]*a2a.Part, error) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, part)
	if p.contextID != "" {
		msg.ContextID = p.contextID
	}
	meta := map[string]any{}
	if p.AgentID != "" {
		meta["agentId"] = p.AgentID
	}
	if p.A2UI {
		meta["a2uiClientCapabilities"] = a2ui.ClientCapabilities()
		ctx = withExt(ctx)
	}
	if len(meta) > 0 {
		msg.Metadata = meta
	}
	res, err := p.client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return nil, err
	}
	task, ok := res.(*a2a.Task)
	if !ok {
		return nil, nil
	}
	if task.ContextID != "" {
		p.contextID = task.ContextID
	}
	if task.Status.Message != nil && len(task.Status.Message.Parts) > 0 {
		return task.Status.Message.Parts, nil
	}
	if len(task.Artifacts) > 0 {
		return task.Artifacts[len(task.Artifacts)-1].Parts, nil
	}
	return nil, nil
}

// SendText sends a plain user-text message to the agent.
func (p *A2UIProbe) SendText(ctx context.Context, text string) ([]*a2a.Part, error) {
	return p.send(ctx, a2a.NewTextPart(text))
}

// SendAction sends an A2UI button action back to the agent over A2A.
func (p *A2UIProbe) SendAction(ctx context.Context, name string, actx map[string]any) ([]*a2a.Part, error) {
	return p.send(ctx, newActionPart(name, "", "", actx, time.Now()))
}
