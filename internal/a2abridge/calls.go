package a2abridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// AgentCall — один JSON-RPC-обмен оркестратора с удалённым агентом, как он
// прошёл по проводу. Нужен панели «A2A-протокол» в браузере: она видит только
// свой обмен с оркестратором, а запрос к агенту (с metadata.skill и прочим)
// оставался невидимым.
type AgentCall struct {
	Seq       int64  `json:"seq"`
	AgentID   string `json:"agentId"`
	AgentName string `json:"agentName"`
	URL       string `json:"url"`
	Method    string `json:"method"`
	// Тела как есть, только с замаскированными номерами карт. Response пуст,
	// если ответа не было (Error).
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response,omitempty"`
	Status   int             `json:"status,omitempty"`
	Error    string          `json:"error,omitempty"`
	TookMs   int64           `json:"tookMs"`
}

const (
	maxCallsPerSession = 50
	maxCallSessions    = 100
	// maxCallBody — предел на одно тело. Больше A2UI-разметки живого агента
	// раз в десять; обрезанное тело уходит JSON-строкой, чтобы не ломать ответ.
	maxCallBody = 256 << 10
)

// CallLog хранит последние обмены с агентами по сессиям (= contextId
// браузерного разговора). Память ограничена: и записи на сессию, и число
// сессий — старые вытесняются.
type CallLog struct {
	mu       sync.Mutex
	seq      int64
	sessions map[string][]AgentCall
	order    []string // сессии в порядке появления, для вытеснения
}

func NewCallLog() *CallLog {
	return &CallLog{sessions: make(map[string][]AgentCall)}
}

func (l *CallLog) add(session string, c AgentCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	c.Seq = l.seq
	calls, ok := l.sessions[session]
	if !ok {
		l.order = append(l.order, session)
		if len(l.order) > maxCallSessions {
			delete(l.sessions, l.order[0])
			l.order = l.order[1:]
		}
	}
	calls = append(calls, c)
	if len(calls) > maxCallsPerSession {
		calls = calls[len(calls)-maxCallsPerSession:]
	}
	l.sessions[session] = calls
}

// Since возвращает обмены сессии с номером больше after — браузер передаёт
// последний уже показанный, чтобы получить только новые.
func (l *CallLog) Since(session string, after int64) []AgentCall {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []AgentCall
	for _, c := range l.sessions[session] {
		if c.Seq > after {
			out = append(out, c)
		}
	}
	return out
}

// callInfoKey несёт в контексте запроса, к какой сессии и какому агенту он
// относится: транспорт видит байты, но не знает, чей это ход.
type callInfoKey struct{}

type callInfo struct {
	session, agentID, agentName string
}

func withCallInfo(ctx context.Context, session, agentID, agentName string) context.Context {
	return context.WithValue(ctx, callInfoKey{}, callInfo{session, agentID, agentName})
}

// callLogTransport пишет POST-обмены в CallLog. Стоит под envelopeTransport,
// то есть ближе к сети: ответ записывается таким, каким его прислал агент, до
// любых наших починок.
type callLogTransport struct {
	base http.RoundTripper
	log  *CallLog
}

func (t *callLogTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	info, ok := req.Context().Value(callInfoKey{}).(callInfo)
	if !ok || req.Method != http.MethodPost {
		return t.base.RoundTrip(req)
	}
	sent, err := dumpBody(req)
	if err != nil {
		return nil, err
	}
	var rpc struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(sent, &rpc)
	c := AgentCall{
		AgentID: info.agentID, AgentName: info.agentName,
		URL: req.URL.String(), Method: rpc.Method, Request: callBody(sent),
	}
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	c.TookMs = time.Since(start).Milliseconds()
	if err != nil {
		c.Error = err.Error()
		t.log.add(info.session, c)
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		c.Error = err.Error()
		t.log.add(info.session, c)
		return nil, err
	}
	c.Status = resp.StatusCode
	c.Response = callBody(body)
	t.log.add(info.session, c)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}

// jsonString — строковый литерал JSON с экранированием.
var jsonString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// callBody готовит тело для панели: номера карт маскируются (ответ на форму
// возврата — это сам номер), не-JSON и слишком большое уходят строкой.
// Маскируются только строковые литералы: числа той же длины (метки времени в
// миллисекундах) картами не бывают, а их замена сломала бы JSON.
func callBody(b []byte) json.RawMessage {
	if json.Valid(b) {
		b = jsonString.ReplaceAllFunc(b, func(lit []byte) []byte {
			return cardLike.ReplaceAll(lit, []byte(cardMask))
		})
	} else {
		b = cardLike.ReplaceAll(b, []byte(cardMask))
	}
	if len(b) <= maxCallBody && json.Valid(b) {
		return json.RawMessage(b)
	}
	if len(b) > maxCallBody {
		b = append(b[:maxCallBody:maxCallBody], "… обрезано"...)
	}
	s, _ := json.Marshal(string(b))
	return s
}
