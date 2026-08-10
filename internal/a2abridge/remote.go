package a2abridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// pollInterval — как часто опрашивать GetTask, пока задача в работе. Контракт
// внешнего агента предписывает 1–2 секунды.
const pollInterval = 2 * time.Second

// Reply — то, что удалённый агент вернул за один ход.
type Reply struct {
	Text string
	// NeedsInput означает, что задача встала в input-required: агент ждёт
	// ответа пользователя и продолжит ТУ ЖЕ задачу.
	NeedsInput bool
	// A2UI — готовые к рендеру A2UI-сообщения (уже прошедшие нормализацию).
	A2UI []map[string]any
	// Widgets — доменные виджеты нашего воркера (DataPart с metadata.kind).
	Widgets []map[string]any
	Files   []attachedFile
}

// basicAuthTransport подставляет заголовок Basic на каждый запрос. Встроенный
// a2aclient.AuthInterceptor не годится: он умеет только Bearer.
type basicAuthTransport struct {
	base       http.RoundTripper
	user, pass string
}

func (t *basicAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.SetBasicAuth(t.user, t.pass)
	return t.base.RoundTrip(clone)
}

// envelopeTransport чинит ответ агента, который отдаёт SendMessage не по A2A
// 1.0: спека (и a2a-go вслед за ней) ждёт oneof-обёртку {"task": {...}} либо
// {"message": {...}}, а агент кладёт объект задачи в result напрямую. Без этого
// клиент падает с «could not determine type».
//
// Правка точечная: трогается только ответ на SendMessage, и только когда
// обёртки действительно нет. Корректный агент проходит насквозь.
type envelopeTransport struct {
	base  http.RoundTripper
	trace *Tracer
}

func (t *envelopeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	isSend, req, err := peekSendMessage(req)
	if err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || !isSend || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	fixed, changed := wrapBareResult(body)
	if changed {
		t.trace.Logf("    ⚠ агент вернул голый объект вместо oneof-обёртки A2A 1.0 — оборачиваю")
	}
	resp.Body = io.NopCloser(bytes.NewReader(fixed))
	resp.ContentLength = int64(len(fixed))
	resp.Header.Del("Content-Length")
	return resp, nil
}

// peekSendMessage отвечает, является ли запрос вызовом SendMessage, и
// возвращает запрос с восстановленным телом.
func peekSendMessage(req *http.Request) (bool, *http.Request, error) {
	if req.Body == nil {
		return false, req, nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return false, nil, err
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	var rpc struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &rpc)
	return rpc.Method == "SendMessage", clone, nil
}

// wrapBareResult оборачивает голый Task/Message в JSON-RPC-ответе в oneof-форму
// A2A 1.0. Возвращает changed=false, если правка не понадобилась.
func wrapBareResult(body []byte) ([]byte, bool) {
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, false
	}
	raw, ok := resp["result"]
	if !ok {
		return body, false
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return body, false
	}
	for _, k := range []string{"task", "message", "statusUpdate", "artifactUpdate"} {
		if _, ok := result[k]; ok {
			return body, false // обёртка уже есть
		}
	}
	key := ""
	switch {
	case hasAny(result, "status", "artifacts"):
		key = "task"
	case hasAny(result, "parts", "role"):
		key = "message"
	default:
		return body, false
	}
	wrapped, err := json.Marshal(map[string]json.RawMessage{key: raw})
	if err != nil {
		return body, false
	}
	resp["result"] = wrapped
	out, err := json.Marshal(resp)
	if err != nil {
		return body, false
	}
	return out, true
}

func hasAny(m map[string]json.RawMessage, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// tolerantCardParser разбирает AgentCard, переживая нераспознанные блоки
// безопасности. a2a-go ждёт их в oneof-форме ({"httpAuth": {...}}), а агенты
// нередко описывают схемы в стиле OpenAPI ({"type":"http","scheme":"basic"}) —
// и тогда падает разбор всей карточки. Нам эти блоки не нужны: аутентификацию
// мы ставим сами из конфига, поэтому при сбое просто выкидываем их и пробуем
// снова.
func tolerantCardParser(trace *Tracer) agentcard.Parser {
	return func(body []byte) (*a2a.AgentCard, error) {
		card, err := agentcard.DefaultCardParser(body)
		if err == nil {
			return card, nil
		}
		var raw map[string]json.RawMessage
		if jsonErr := json.Unmarshal(body, &raw); jsonErr != nil {
			return nil, err
		}
		if _, hasSchemes := raw["securitySchemes"]; !hasSchemes {
			return nil, err
		}
		delete(raw, "securitySchemes")
		delete(raw, "security")
		stripped, jsonErr := json.Marshal(raw)
		if jsonErr != nil {
			return nil, err
		}
		card, retryErr := agentcard.DefaultCardParser(stripped)
		if retryErr != nil {
			return nil, err
		}
		trace.Logf("    ⚠ карточка объявляет securitySchemes в нераспознанной форме (%v) — игнорирую их, авторизация берётся из конфига", err)
		return card, nil
	}
}

// mergeEndpoint строит рабочий адрес транспорта: схема и хост из конфига (по
// нему карточка и была получена), путь из карточки. Так и нерабочий 0.0.0.0
// внутри чужой карточки не мешает, и объявленный агентом путь (/invoke у нашего
// воркера) не теряется.
func mergeEndpoint(base, declared string) string {
	b, err := url.Parse(base)
	if err != nil {
		return declared
	}
	d, err := url.Parse(declared)
	if err != nil {
		return base
	}
	out := *b
	if d.Path != "" && d.Path != "/" {
		out.Path = path.Join(b.Path, d.Path)
	}
	return out.String()
}

// Remote — одно соединение с одним удалённым A2A-агентом: карточка,
// аутентификация, сессии и один ход разговора. Знает про особенности чужих
// агентов (нестандартный путь карточки, нерабочий адрес внутри неё,
// metadata.skill), но ничего не знает про LLM и про домен заказов.
type Remote struct {
	cfg   config.AgentConfig
	trace *Tracer

	mu        sync.Mutex
	client    *a2aclient.Client
	card      *a2a.AgentCard
	profile   WorkerProfile
	available bool
	toolName  string             // перекрытие имени инструмента (см. Registry)
	pending   map[string]pending // sessionID → зависшая input-required задача
	contexts  map[string]string  // sessionID → contextId удалённого агента
}

// NewRemote создаёт соединение, но ещё не открывает его: карточка резолвится
// лениво при первом обращении, чтобы выключенный агент в локальной сети не
// мешал оркестратору стартовать. trace может быть nil.
func NewRemote(cfg config.AgentConfig, trace *Tracer) *Remote {
	return &Remote{
		cfg:      cfg,
		trace:    trace,
		pending:  make(map[string]pending),
		contexts: make(map[string]string),
	}
}

func (r *Remote) ID() string { return r.cfg.ID }

// Name — подпись агента для UI: из конфига, иначе из карточки, иначе id.
func (r *Remote) Name() string {
	if r.cfg.Name != "" {
		return r.cfg.Name
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.card != nil && r.card.Name != "" {
		return r.card.Name
	}
	return r.cfg.ID
}

// Description — краткое описание для UI.
func (r *Remote) Description() string {
	if r.cfg.Description != "" {
		return r.cfg.Description
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.card != nil {
		return r.card.Description
	}
	return ""
}

func (r *Remote) Verbatim() bool { return r.cfg.Verbatim }

// Available сообщает, удалось ли последнее подключение.
func (r *Remote) Available() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.available
}

// Profile возвращает выведенный профиль агента (имя и описание делегирующего
// инструмента плюс блок для промпта).
func (r *Remote) Profile() WorkerProfile {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.profile
}

// SetToolName перекрывает имя делегирующего инструмента. Нужен реестру для
// разрешения коллизий, когда два агента вывели одно и то же имя.
func (r *Remote) SetToolName(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolName = name
	if r.profile.ToolName != "" {
		r.profile.ToolName = name
	}
}

// httpClient собирает HTTP-клиент агента: таймаут из конфига плюс Basic-auth,
// если он задан.
func (r *Remote) httpClient() *http.Client {
	var rt http.RoundTripper = http.DefaultTransport
	if strings.EqualFold(r.cfg.Auth.Type, "basic") && r.cfg.Auth.Username != "" {
		rt = &basicAuthTransport{base: rt, user: r.cfg.Auth.Username, pass: r.cfg.Auth.Password}
	}
	rt = &envelopeTransport{base: rt, trace: r.trace}
	return &http.Client{Transport: rt, Timeout: r.cfg.TimeoutDuration()}
}

// Connect резолвит AgentCard и создаёт клиента. Идемпотентен; после неудачи
// следующий вызов пробует снова, поэтому выключенный агент оживает сам.
func (r *Remote) Connect(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		return nil
	}

	hc := r.httpClient()
	resolver := &agentcard.Resolver{Client: hc, CardParser: tolerantCardParser(r.trace)}
	card, err := resolver.Resolve(ctx, r.cfg.URL, agentcard.WithPath(r.cfg.CardPath))
	if err != nil {
		r.available = false
		return fmt.Errorf("resolve card of %q at %s%s: %w", r.cfg.ID, r.cfg.URL, r.cfg.CardPath, err)
	}
	// Хост из карточки бывает нерабочим (живой агент объявляет
	// http://0.0.0.0:18800/), поэтому схему и хост берём из конфига — по нему мы
	// карточку и получили. Путь остаётся из карточки: наш воркер объявляет
	// эндпоинт /invoke, и потерять его нельзя.
	if len(card.SupportedInterfaces) == 0 {
		card.SupportedInterfaces = []*a2a.AgentInterface{
			a2a.NewAgentInterface(r.cfg.URL, a2a.TransportProtocolJSONRPC),
		}
	} else {
		for _, iface := range card.SupportedInterfaces {
			if iface != nil {
				iface.URL = mergeEndpoint(r.cfg.URL, iface.URL)
			}
		}
	}

	cl, err := a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(hc))
	if err != nil {
		r.available = false
		return fmt.Errorf("a2a client for %q: %w", r.cfg.ID, err)
	}

	r.card = card
	r.client = cl
	r.available = true
	r.profile = r.buildProfile(card)
	r.trace.Logf("resolved AgentCard %q of agent %q at %s (tool %q)",
		card.Name, r.cfg.ID, r.cfg.URL, r.profile.ToolName)
	return nil
}

// buildProfile выводит профиль агента. Если агент описан в конфиге, конфиг
// вытесняет карточку целиком, включая имя инструмента: у внешнего агента может
// быть сотня навыков и имя вроде «Who I Am», непригодное ни для промпта, ни для
// идентификатора функции.
func (r *Remote) buildProfile(card *a2a.AgentCard) WorkerProfile {
	if r.cfg.Description == "" {
		p := ProfileFromCard(card)
		if r.toolName != "" {
			p.ToolName = r.toolName
		}
		return p
	}
	name := r.cfg.Name
	if name == "" && card != nil {
		name = card.Name
	}
	if name == "" {
		name = r.cfg.ID
	}
	toolName := r.toolName
	if toolName == "" {
		toolName = defaultToolName(r.cfg.ID)
	}
	return WorkerProfile{
		ToolName: toolName,
		ToolDesc: "Делегировать запрос удалённому агенту. " + r.cfg.Description + " " + needsInputTail,
		Summary:  fmt.Sprintf("Агент по имени «%s» умеет: %s", name, r.cfg.Description),
	}
}

// defaultToolName строит имя инструмента из id агента: ask_<id> с заменой
// недопустимых для имени функции символов.
func defaultToolName(id string) string {
	slug := strings.Trim(nonAlnum.ReplaceAllString(id, "_"), "_")
	if slug == "" {
		return "ask_agent"
	}
	return "ask_" + slug
}

// PendingTaskID возвращает id зависшей input-required задачи сессии.
func (r *Remote) PendingTaskID(sessionID string) a2a.TaskID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[sessionID].taskID
}

// Ask отправляет агенту одно сообщение и возвращает его ответ. Если у сессии
// есть зависшая input-required задача, сообщение продолжает именно её.
func (r *Remote) Ask(ctx context.Context, sessionID, text string) (Reply, error) {
	if err := r.Connect(ctx); err != nil {
		return Reply{}, err
	}

	r.mu.Lock()
	client := r.client
	p, hasPending := r.pending[sessionID]
	contextID := r.contexts[sessionID]
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, r.cfg.TimeoutDuration())
	defer cancel()

	r.trace.Logf("──▶ delegating to agent %q | session=%s", r.cfg.ID, sessionID)

	var msg *a2a.Message
	if hasPending {
		r.trace.Logf("    resuming input-required task | taskID=%s contextID=%s", p.taskID, p.contextID)
		msg = a2a.NewMessageForTask(a2a.MessageRoleUser,
			a2a.TaskInfo{TaskID: p.taskID, ContextID: p.contextID}, a2a.NewTextPart(text))
	} else {
		msg = a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
		if contextID != "" {
			msg.ContextID = contextID
		}
	}
	// metadata.skill включает у внешнего агента инструменты нужного навыка;
	// без него он отвечает только текстом.
	if r.cfg.Skill != "" {
		msg.Metadata = map[string]any{"skill": r.cfg.Skill}
	}
	r.trace.Logf("    SendMessage role=user skill=%q text=%q", r.cfg.Skill, text)

	res, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		r.trace.Logf("    ✖ SendMessage failed: %v", err)
		return Reply{}, fmt.Errorf("agent %q unreachable: %w", r.cfg.ID, err)
	}

	switch v := res.(type) {
	case *a2a.Message:
		r.trace.Logf("◀── response: Message (synchronous, no task) | parts=%d", len(v.Parts))
		r.clearPending(sessionID)
		return r.replyFromParts(v.Parts, ""), nil

	case *a2a.Task:
		task, err := r.awaitTerminal(ctx, v)
		if err != nil {
			return Reply{}, err
		}
		return r.replyFromTask(sessionID, task), nil

	default:
		r.trace.Logf("    ✖ unexpected A2A result type %T", res)
		return Reply{}, fmt.Errorf("unexpected A2A result type %T", res)
	}
}

// awaitTerminal опрашивает GetTask, пока задача не выйдет из рабочего
// состояния. Контракт внешнего агента прямо предписывает такой поллинг.
func (r *Remote) awaitTerminal(ctx context.Context, task *a2a.Task) (*a2a.Task, error) {
	for {
		state := task.Status.State
		if state != a2a.TaskStateWorking && state != a2a.TaskStateSubmitted {
			return task, nil
		}
		r.trace.Logf("    … task %s is %s, polling GetTask in %s", task.ID, state, pollInterval)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("agent %q: task %s still %s: %w", r.cfg.ID, task.ID, state, ctx.Err())
		case <-time.After(pollInterval):
		}
		next, err := r.client.GetTask(ctx, &a2a.GetTaskRequest{ID: task.ID})
		if err != nil {
			return nil, fmt.Errorf("agent %q: GetTask %s: %w", r.cfg.ID, task.ID, err)
		}
		task = next
	}
}

// replyFromTask собирает ответ из терминальной задачи и запоминает состояние
// сессии.
func (r *Remote) replyFromTask(sessionID string, task *a2a.Task) Reply {
	r.trace.Logf("◀── response: Task | id=%s contextID=%s state=%s", task.ID, task.ContextID, task.Status.State)

	r.mu.Lock()
	if task.ContextID != "" {
		r.contexts[sessionID] = task.ContextID
	}
	if task.Status.State == a2a.TaskStateInputRequired {
		r.pending[sessionID] = pending{taskID: task.ID, contextID: task.ContextID}
	} else {
		delete(r.pending, sessionID)
	}
	r.mu.Unlock()

	if task.Status.State == a2a.TaskStateInputRequired {
		reply := r.replyFromParts(statusParts(task), statusMessageText(task))
		reply.NeedsInput = true
		r.trace.Logf("    ⏸ input-required — stored pending task, asking user: %q", reply.Text)
		return reply
	}

	// Текст берём из статусного сообщения, если агент положил его туда
	// (так предписывает контракт), иначе — из артефакта, как делает наш воркер.
	fallback := taskResultText(task)
	if task.Status.Message != nil {
		for _, p := range task.Status.Message.Parts {
			if txt := p.Text(); txt != "" {
				fallback = txt
				break
			}
		}
	}
	parts := append(append([]*a2a.Part{}, artifactParts(task)...), statusParts(task)...)
	reply := r.replyFromParts(parts, fallback)
	r.trace.Logf("    ✔ terminal state | text=%q a2ui=%d widgets=%d files=%d",
		reply.Text, len(reply.A2UI), len(reply.Widgets), len(reply.Files))
	return reply
}

// replyFromParts раскладывает части ответа по слоям: текст, A2UI, доменные
// виджеты и файлы.
func (r *Remote) replyFromParts(parts []*a2a.Part, fallback string) Reply {
	reply := Reply{Text: fallback}
	if reply.Text == "" {
		for _, p := range parts {
			if p == nil || p.MediaType == a2ui.MIMEType {
				continue
			}
			if txt := p.Text(); txt != "" {
				reply.Text = txt
				break
			}
		}
	}
	if strings.TrimSpace(reply.Text) == "" {
		reply.Text = "Готово."
	}
	reply.A2UI = a2ui.Ingest(a2uiParts(parts))
	if w := firstWidget(parts); w != nil {
		reply.Widgets = append(reply.Widgets, w)
	}
	for _, p := range parts {
		if p == nil || p.Filename == "" {
			continue
		}
		if data := p.Raw(); len(data) > 0 {
			reply.Files = append(reply.Files, attachedFile{name: p.Filename, mediaType: p.MediaType, data: data})
		}
	}
	return reply
}

// a2uiParts переводит A2A-части в транспортно-независимый вид, понятный
// пакету a2ui.
func a2uiParts(parts []*a2a.Part) []a2ui.Part {
	out := make([]a2ui.Part, 0, len(parts))
	for _, p := range parts {
		if p == nil {
			continue
		}
		out = append(out, a2ui.Part{MediaType: p.MediaType, Text: p.Text(), Data: p.Data()})
	}
	return out
}

// clearPending забывает зависшую задачу сессии.
func (r *Remote) clearPending(sessionID string) {
	r.mu.Lock()
	delete(r.pending, sessionID)
	r.mu.Unlock()
}

// statusParts returns the parts of an input-required task's status message.
func statusParts(t *a2a.Task) []*a2a.Part {
	if t.Status.Message == nil {
		return nil
	}
	return t.Status.Message.Parts
}

// artifactParts returns the parts of a task's last artifact.
func artifactParts(t *a2a.Task) []*a2a.Part {
	if len(t.Artifacts) == 0 {
		return nil
	}
	return t.Artifacts[len(t.Artifacts)-1].Parts
}

// firstWidget returns the payload of the first DataPart whose metadata.kind
// marks it as a widget ("widget/..."), with the kind injected under "_kind" so
// a renderer can dispatch on it. Returns nil when there is no widget part.
func firstWidget(parts []*a2a.Part) map[string]any {
	for _, p := range parts {
		if p == nil {
			continue
		}
		kind, _ := p.Metadata["kind"].(string)
		if !strings.HasPrefix(kind, "widget/") {
			continue
		}
		data, ok := p.Data().(map[string]any)
		if !ok {
			continue
		}
		out := map[string]any{"_kind": kind}
		for k, v := range data {
			out[k] = v
		}
		return out
	}
	return nil
}

// statusMessageText extracts the question text from an input-required task's
// status message.
func statusMessageText(t *a2a.Task) string {
	if t.Status.Message != nil && len(t.Status.Message.Parts) > 0 {
		return t.Status.Message.Parts[0].Text()
	}
	return "Агенту по заказам нужны дополнительные данные."
}

// taskResultText returns the last artifact text of a completed task, falling
// back to the last history message text, then "Done." if neither is present.
// Empty-text parts are skipped so that a blank artifact falls through to the
// "Done." fallback rather than returning an empty string.
func taskResultText(t *a2a.Task) string {
	if len(t.Artifacts) > 0 {
		last := t.Artifacts[len(t.Artifacts)-1]
		for _, p := range last.Parts {
			if txt := p.Text(); txt != "" {
				return txt
			}
		}
	}
	if len(t.History) > 0 {
		last := t.History[len(t.History)-1]
		for _, p := range last.Parts {
			if txt := p.Text(); txt != "" {
				return txt
			}
		}
	}
	return "Готово."
}
