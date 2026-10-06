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
	"slices"
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

// a2uiSuppressedKey помечает контекст хода, на котором клиент не просил
// generative UI. Значение контекста, а не поле Remote: Remote общий для всех
// сессий, а режим выбирается на разговор.
type a2uiSuppressedKey struct{}

// withoutA2UI помечает ход как текстовый: у внешнего агента разметку не просим.
func withoutA2UI(ctx context.Context) context.Context {
	return context.WithValue(ctx, a2uiSuppressedKey{}, true)
}

// a2uiSuppressed отвечает, выбран ли на этом ходу текстовый режим.
func a2uiSuppressed(ctx context.Context) bool {
	v, _ := ctx.Value(a2uiSuppressedKey{}).(bool)
	return v
}

// TurnFailedError — агент ответил, но ход не удался: задача пришла в
// FAILED/REJECTED/CANCELED, а причина лежит в её статусном сообщении.
//
// Отдельный тип нужен, чтобы интерфейс не путал это с недоступностью: агент на
// связи и отвечает, просто не смог выполнить именно этот запрос. Формулировка
// «агент недоступен» на таком сбое — неправда, а пользователь по ней пойдёт
// проверять сеть вместо того, чтобы повторить ход.
type TurnFailedError struct {
	AgentID string
	State   a2a.TaskState
	Reason  string
}

func (e *TurnFailedError) Error() string {
	return fmt.Sprintf("agent %q turn failed (%s): %s", e.AgentID, e.State, e.Reason)
}

// FirstLine возвращает первую содержательную строку причины. Агенты кладут в
// неё многострочный текст — сообщение об ошибке плюс ссылку на документацию, — а
// в ленте нужна одна строка.
func (e *TurnFailedError) FirstLine() string {
	for _, line := range strings.Split(e.Reason, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return e.Reason
}

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
	// Дамп тел — здесь, а не уровнем выше: транспорт видит ровно те байты, что
	// уходят и приходят, уже после всех преобразований SDK.
	if t.trace.Debug() {
		if sent, dumpErr := dumpBody(req); dumpErr == nil {
			t.trace.Dump("запрос агенту "+req.URL.String(), sent)
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	if !isSend && !t.trace.Debug() {
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	t.trace.Dump("ответ агента", body)
	if !isSend {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Del("Content-Length")
		return resp, nil
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

// dumpBody читает тело запроса для дампа и возвращает его на место.
func dumpBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	return body, nil
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

	// connectMu сериализует попытки подключения, mu защищает только поля.
	// Держать mu на время сетевого запроса нельзя: за ним встают Name(),
	// Available() и прочие дешёвые чтения — например, из обработчика
	// /api/agents, который обязан отвечать мгновенно.
	connectMu sync.Mutex

	mu        sync.Mutex
	client    *a2aclient.Client
	card      *a2a.AgentCard
	profile   WorkerProfile
	available bool
	probed    bool               // завершилась ли хоть одна попытка подключения
	toolName  string             // перекрытие имени инструмента (см. Registry)
	pending   map[string]pending // sessionID → зависшая input-required задача
	contexts  map[string]string  // sessionID → contextId удалённого агента
	skills    map[string]string  // sessionID → навык, выбранный в чате для этого разговора
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
		skills:   make(map[string]string),
	}
}

// Skills — навыки агента из настроек: их предлагает селектор в чате.
func (r *Remote) Skills() []string { return slices.Clone(r.cfg.Skills) }

// SetSessionSkill задаёт навык, который уйдёт в metadata.skill следующих
// запросов этого разговора. Пустой — навык не передаётся. Навык не из списка
// агента отвергается: браузер со старым списком не должен слать агенту то,
// чего в настройках уже нет. Возвращает навык, который реально установлен.
func (r *Remote) SetSessionSkill(sessionID, skill string) string {
	if skill != "" && !r.cfg.HasSkill(skill) {
		r.trace.Logf("⚠ unknown skill %q for agent %q — sending none", skill, r.cfg.ID)
		skill = ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if skill == "" {
		delete(r.skills, sessionID)
	} else {
		r.skills[sessionID] = skill
	}
	return skill
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

// Probed сообщает, завершилась ли хоть одна попытка подключиться. Пока нет,
// «недоступен» — не факт, а домысел, и показывать его пользователю нельзя.
func (r *Remote) Probed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.probed
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

// httpClient собирает HTTP-клиент агента: таймаут из конфига, клиентский
// сертификат (mTLS) и Basic-auth, если они заданы.
func (r *Remote) httpClient() (*http.Client, error) {
	var rt http.RoundTripper = http.DefaultTransport
	if r.cfg.TLS.Enabled() {
		tc, err := r.cfg.TLS.ClientConfig()
		if err != nil {
			return nil, err
		}
		// Клон, а не правка DefaultTransport: прокси, таймауты и пул
		// соединений остаются прежними, а сертификат не утекает к другим агентам.
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = tc
		rt = t
	}
	if strings.EqualFold(r.cfg.Auth.Type, "basic") && r.cfg.Auth.Username != "" {
		rt = &basicAuthTransport{base: rt, user: r.cfg.Auth.Username, pass: r.cfg.Auth.Password}
	}
	if calls := r.trace.Calls(); calls != nil {
		rt = &callLogTransport{base: rt, log: calls}
	}
	rt = &envelopeTransport{base: rt, trace: r.trace}
	return &http.Client{Transport: rt, Timeout: r.cfg.TimeoutDuration()}, nil
}

// Connect резолвит AgentCard и создаёт клиента. Идемпотентен; после неудачи
// следующий вызов пробует снова, поэтому выключенный агент оживает сам.
func (r *Remote) Connect(ctx context.Context) error {
	if r.connected() {
		return nil
	}
	r.connectMu.Lock()
	defer r.connectMu.Unlock()
	if r.connected() {
		return nil
	}

	// probed ставится по итогу попытки, а не на входе: пока карточка в
	// пути, агент ещё не проверен. Иначе /api/agents показывал бы живого
	// агента «не отвечает», а ready() счёл бы его лежащим и уронил ход,
	// пришедший во время первого подключения.
	hc, err := r.httpClient()
	if err != nil {
		r.markUnavailable()
		return fmt.Errorf("http client for %q: %w", r.cfg.ID, err)
	}
	resolver := &agentcard.Resolver{Client: hc, CardParser: tolerantCardParser(r.trace)}
	card, err := resolver.Resolve(ctx, r.cfg.URL, agentcard.WithPath(r.cfg.CardPath))
	if err != nil {
		r.markUnavailable()
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
		r.markUnavailable()
		return fmt.Errorf("a2a client for %q: %w", r.cfg.ID, err)
	}

	r.mu.Lock()
	r.card = card
	r.client = cl
	r.available = true
	r.probed = true
	r.profile = r.buildProfile(card)
	toolName := r.profile.ToolName
	r.mu.Unlock()

	r.trace.Logf("resolved AgentCard %q of agent %q at %s (tool %q)",
		card.Name, r.cfg.ID, r.cfg.URL, toolName)
	return nil
}

// connected сообщает, есть ли готовый клиент.
func (r *Remote) connected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.client != nil
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

// acceptsA2UI сообщает, объявляет ли агент способность отдавать A2UI: только
// таким агентам есть смысл слать запрос на generative UI.
// wantsA2UI — просим ли разметку на этом ходу. Двух условий: агент умеет её
// отдавать (объявил в карточке) и клиент её на этом разговоре хочет. Второе
// приезжает значением контекста: режим выбирается в браузере на разговор, а
// Remote общий для всех сессий.
func (r *Remote) wantsA2UI(ctx context.Context) bool {
	return r.acceptsA2UI() && !a2uiSuppressed(ctx)
}

func (r *Remote) acceptsA2UI() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.card == nil {
		return false
	}
	for _, mode := range r.card.DefaultOutputModes {
		if mode == a2ui.MIMEType {
			return true
		}
	}
	for _, ext := range r.card.Capabilities.Extensions {
		if ext.URI == a2ui.ExtensionURI || ext.URI == a2ui.LegacyExtensionURI {
			return true
		}
	}
	return false
}

// markUnavailable роняет пометку доступности и заставляет следующий Connect
// заново резолвить карточку: соединение могло умереть вместе с агентом.
func (r *Remote) markUnavailable() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.available = false
	r.probed = true
	r.client = nil
}

// PendingTaskID возвращает id зависшей input-required задачи сессии.
func (r *Remote) PendingTaskID(sessionID string) a2a.TaskID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[sessionID].taskID
}

// Ask отправляет агенту одно текстовое сообщение и возвращает его ответ. Если у
// сессии есть зависшая input-required задача, сообщение продолжает именно её.
func (r *Remote) Ask(ctx context.Context, sessionID, text string) (Reply, error) {
	return r.ask(ctx, sessionID, a2a.NewTextPart(text), text)
}

// AskAction отправляет агенту нажатие кнопки — штатным событием A2UI, а не
// пересказом на человеческом языке. Так это описывает схема client_to_server, и
// так его ждёт агент, собранный на любом из референсных SDK.
func (r *Remote) AskAction(ctx context.Context, sessionID, name, surfaceID, sourceComponentID string, actx map[string]any) (Reply, error) {
	part := newActionPart(name, surfaceID, sourceComponentID, actx, time.Now())
	return r.ask(ctx, sessionID, part, "action "+name)
}

// ask — общий путь обоих способов обратиться к агенту. echo попадает только в
// трейс: части бывают не текстовые, а лог должен показывать, что именно ушло.
func (r *Remote) ask(ctx context.Context, sessionID string, outgoing *a2a.Part, echo string) (Reply, error) {
	if err := r.Connect(ctx); err != nil {
		return Reply{}, err
	}

	r.mu.Lock()
	client := r.client
	p, hasPending := r.pending[sessionID]
	contextID := r.contexts[sessionID]
	skill := r.skills[sessionID]
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, r.cfg.TimeoutDuration())
	defer cancel()
	// По этой метке транспорт кладёт обмен в журнал нужной сессии — и
	// SendMessage, и опросы GetTask, которые идут с тем же ctx.
	ctx = withCallInfo(ctx, sessionID, r.cfg.ID, r.Name())

	// Сказать агенту, что клиент умеет рендерить generative UI: заголовок
	// расширения (обоими именами, см. withExt) и a2uiClientCapabilities ниже.
	// Именно эту пару спека расширения называет способом договориться.
	wantsA2UI := r.wantsA2UI(ctx)
	if wantsA2UI {
		ctx = withExt(ctx)
	}

	r.trace.Logf("──▶ delegating to agent %q | session=%s", r.cfg.ID, sessionID)

	var msg *a2a.Message
	if hasPending {
		r.trace.Logf("    resuming input-required task | taskID=%s contextID=%s", p.taskID, p.contextID)
		msg = a2a.NewMessageForTask(a2a.MessageRoleUser,
			a2a.TaskInfo{TaskID: p.taskID, ContextID: p.contextID}, outgoing)
	} else {
		msg = a2a.NewMessage(a2a.MessageRoleUser, outgoing)
		if contextID != "" {
			msg.ContextID = contextID
		}
	}
	meta := map[string]any{}
	// metadata.skill включает у внешнего агента инструменты нужного навыка;
	// без него он отвечает только текстом. Уходит лишь явно выбранный в чате.
	if skill != "" {
		meta["skill"] = skill
	}
	// Какие каталоги умеет наш рендерер. Штатный признак «клиент говорит на
	// A2UI»: acceptedOutputModes спека таким признаком не считает.
	if wantsA2UI {
		meta["a2uiClientCapabilities"] = a2ui.ClientCapabilities()
	}
	if len(meta) > 0 {
		msg.Metadata = meta
	}
	// contextId в трейсе — потому что «агент забывает разговор» это первый
	// вопрос, который приходится проверять, а без него не видно, передаём ли мы
	// контекст или начинаем беседу заново.
	sentCtx := msg.ContextID
	if hasPending {
		sentCtx = p.contextID
	}
	if sentCtx == "" {
		sentCtx = "(новый разговор)"
	}
	// Текст маскируется: ответом на форму возврата служит сам номер карты, и
	// без этого он ложился бы в лог при каждом возврате.
	r.trace.Logf("    SendMessage role=user skill=%q contextId=%s text=%q", skill, sentCtx, MaskCardLike(echo))

	req := &a2a.SendMessageRequest{Message: msg}
	if wantsA2UI {
		// Поле родное для A2A и агенту не мешает; триггером A2UI оно, по спеке
		// расширения, не является — им служат заголовок и capabilities выше.
		req.Config = &a2a.SendMessageConfig{
			AcceptedOutputModes: []string{"text/plain", a2ui.MIMEType},
		}
	}
	res, err := client.SendMessage(ctx, req)
	if err != nil {
		r.trace.Logf("    ✖ SendMessage failed: %v", err)
		// Сорвавшийся запрос — единственный честный признак, что агент лёг:
		// Connect после первого успеха уже не переспрашивает карточку, и без
		// этого пометка «доступен» осталась бы навсегда.
		r.markUnavailable()
		return Reply{}, fmt.Errorf("agent %q unreachable: %w", r.cfg.ID, err)
	}

	switch v := res.(type) {
	case *a2a.Message:
		r.trace.Logf("◀── response: Message (synchronous, no task) | parts=%d", len(v.Parts))
		r.clearPending(sessionID)
		// Синхронный ответ тоже несёт контекст разговора: без этого агент,
		// отвечающий Message вместо Task, начинал бы беседу заново каждый ход.
		if v.ContextID != "" {
			r.mu.Lock()
			r.contexts[sessionID] = v.ContextID
			r.mu.Unlock()
		}
		return r.replyFromParts(v.Parts, nil, ""), nil

	case *a2a.Task:
		task, err := r.awaitTerminal(ctx, client, v)
		if err != nil {
			return Reply{}, err
		}
		return r.replyFromTask(ctx, sessionID, task)

	default:
		r.trace.Logf("    ✖ unexpected A2A result type %T", res)
		return Reply{}, fmt.Errorf("unexpected A2A result type %T", res)
	}
}

// awaitTerminal опрашивает GetTask, пока задача не выйдет из рабочего
// состояния. Контракт внешнего агента прямо предписывает такой поллинг.
// client передаётся снимком: markUnavailable из параллельного хода зануляет
// r.client, и чтение поля прямо здесь было бы гонкой с разыменованием nil.
func (r *Remote) awaitTerminal(ctx context.Context, client *a2aclient.Client, task *a2a.Task) (*a2a.Task, error) {
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
		next, err := client.GetTask(ctx, &a2a.GetTaskRequest{ID: task.ID})
		if err != nil {
			return nil, fmt.Errorf("agent %q: GetTask %s: %w", r.cfg.ID, task.ID, err)
		}
		task = next
	}
}

// replyFromTask собирает ответ из терминальной задачи и запоминает состояние
// сессии.
//
// Провалившаяся задача возвращается ошибкой, а не ответом: агент в этом случае
// кладёт в статусное сообщение причину отказа («timed out» и подобное), и без
// разделения она попадала бы в ленту как обычная реплика — пользователь видел
// бы сбой агента в виде ответа на свой вопрос.
func (r *Remote) replyFromTask(ctx context.Context, sessionID string, task *a2a.Task) (Reply, error) {
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
		reply := r.replyFromParts(statusParts(task), nil, statusMessageText(task))
		reply.NeedsInput = true
		r.trace.Logf("    ⏸ input-required — stored pending task, asking user: %q", reply.Text)
		return reply, nil
	}

	switch task.Status.State {
	case a2a.TaskStateFailed, a2a.TaskStateRejected, a2a.TaskStateCanceled:
		why := strings.TrimSpace(statusMessageText(task))
		r.trace.Logf("    ✖ задача завершилась неуспехом: state=%s reason=%q", task.Status.State, why)
		return Reply{}, &TurnFailedError{AgentID: r.cfg.ID, State: task.Status.State, Reason: why}
	}

	// Текст берём из статусного сообщения, если агент положил его туда
	// (так предписывает контракт), иначе — из артефакта, как делает наш воркер.
	// Части с mediaType A2UI пропускаем: агент вполне может прислать разметку
	// интерфейса текстовой частью, и она не должна стать ответом пользователю.
	fallback := taskResultText(task)
	if task.Status.Message != nil {
		if txt := firstProseText(task.Status.Message.Parts); txt != "" {
			fallback = txt
		}
	}
	reply := r.replyFromParts(statusParts(task), artifactParts(task), fallback)
	r.trace.Logf("    ✔ terminal state | text=%q a2ui=%d widgets=%d files=%d",
		reply.Text, len(reply.A2UI), len(reply.Widgets), len(reply.Files))
	switch {
	case len(reply.A2UI) > 0 || len(reply.Widgets) > 0:
	case a2uiSuppressed(ctx):
		// Не «агент не прислал», а «мы не просили»: на этом разговоре выбран
		// текстовый режим. Без пометки строка ниже вводила бы в заблуждение.
		r.trace.Logf("    ⓘ текстовый режим — разметку у агента не запрашивали")
	case r.acceptsA2UI():
		// Частый вопрос «почему нет виджета»: агент объявляет A2UI в карточке,
		// мы его запросили, но на этом ходу он решил ответить только текстом.
		r.trace.Logf("    ⓘ агент объявляет A2UI и получил запрос на него, но в этом ответе прислал только текст — виджета не будет")
	}
	return reply, nil
}

// replyFromParts раскладывает части ответа по слоям: текст, A2UI, доменные
// виджеты и файлы.
//
// statusP и artifactP разделены из-за A2UI: спека расширения кладёт разметку в
// части сообщения, и оттуда её читает стандартный клиент. Артефакт — второе
// место, куда её кладут живые агенты; берём его, только если в сообщении
// разметки не было, иначе поверхность приехала бы дважды.
func (r *Remote) replyFromParts(statusP, artifactP []*a2a.Part, fallback string) Reply {
	parts := append(append([]*a2a.Part{}, artifactP...), statusP...)
	reply := Reply{Text: fallback}
	if reply.Text == "" {
		for _, p := range parts {
			if p == nil || isA2UIPart(p) {
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
	if reply.A2UI = a2ui.Ingest(a2uiParts(statusP)); len(reply.A2UI) == 0 {
		reply.A2UI = a2ui.Ingest(a2uiParts(artifactP))
	}
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

// firstProseText возвращает первую человекочитаемую текстовую часть, пропуская
// разметку интерфейса: A2UI приезжает и текстовой частью с mediaType
// application/a2ui+json, и показывать её пользователю нельзя.
func firstProseText(parts []*a2a.Part) string {
	for _, p := range parts {
		if p == nil || isA2UIPart(p) {
			continue
		}
		if txt := p.Text(); txt != "" {
			return txt
		}
	}
	return ""
}

// statusMessageText extracts the question text from an input-required task's
// status message.
func statusMessageText(t *a2a.Task) string {
	if t.Status.Message != nil {
		if txt := firstProseText(t.Status.Message.Parts); txt != "" {
			return txt
		}
	}
	return "Агенту по заказам нужны дополнительные данные."
}

// taskResultText returns the last artifact text of a completed task, falling
// back to the last history message text, then "Done." if neither is present.
// Empty-text parts are skipped so that a blank artifact falls through to the
// "Done." fallback rather than returning an empty string.
func taskResultText(t *a2a.Task) string {
	if len(t.Artifacts) > 0 {
		if txt := firstProseText(t.Artifacts[len(t.Artifacts)-1].Parts); txt != "" {
			return txt
		}
	}
	if len(t.History) > 0 {
		if txt := firstProseText(t.History[len(t.History)-1].Parts); txt != "" {
			return txt
		}
	}
	return "Готово."
}
