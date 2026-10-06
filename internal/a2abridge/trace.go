package a2abridge

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
)

// DebugEnvVar включает дамп полных тел A2A-обмена. Отдельная переменная, а не
// поле конфига: это инструмент разбирательства, который включают на один
// прогон, а не настройка развёртывания.
const DebugEnvVar = "A2A_DEBUG"

// maxDumpBytes ограничивает одну запись. Разметка A2UI бывает под два десятка
// килобайт, и без предела один ход способен утопить весь лог.
const maxDumpBytes = 64 << 10

// cardLike — последовательность из 13–19 цифр, возможно разбитая пробелами или
// дефисами. Такие маскируются везде, где текст попадает в лог: через форму
// возврата уезжает настоящий номер карты, и ни трейс, ни дамп не должны
// превращаться в утечку.
var cardLike = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)

const cardMask = "«номер карты скрыт»"

// MaskCardLike прячет номера карт в произвольном тексте. Нужна там, где в лог
// уходит пользовательский ввод: ответ на форму возврата состоит ровно из
// номера карты и иначе печатался бы целиком.
func MaskCardLike(s string) string {
	return cardLike.ReplaceAllString(s, cardMask)
}

// Tracer writes human-readable, step-by-step traces of the A2A protocol
// exchange between the orchestrator (client) and the orders worker (server).
// It is intended for learning/debugging: each line shows one protocol step.
//
// A nil *Tracer is valid and silently discards all output, so callers that do
// not want tracing (e.g. tests) can pass nil.
type Tracer struct {
	l *log.Logger
	// debug включает Dump — полные тела запросов и ответов A2A.
	debug bool
	// calls — те же обмены с агентами, но для панели в браузере, а не для лога.
	calls *CallLog
}

// NewTracer returns a Tracer that writes to w with the given line prefix.
// A nil w discards output. Lines are timestamped so the interleaving of the
// two-agent conversation is visible.
func NewTracer(w io.Writer, prefix string) *Tracer {
	if w == nil {
		w = io.Discard
	}
	return &Tracer{
		l:     log.New(w, prefix, log.LstdFlags|log.Lmsgprefix),
		debug: debugEnabled(),
		calls: NewCallLog(),
	}
}

// Calls — журнал обменов с агентами для панели «A2A-протокол». У nil-трейсера
// журнала нет, и обмены никуда не пишутся.
func (t *Tracer) Calls() *CallLog {
	if t == nil {
		return nil
	}
	return t.calls
}

// debugEnabled читает A2A_DEBUG. Пустое и «0»/«false» — выключено.
func debugEnabled() bool {
	v := os.Getenv(DebugEnvVar)
	if v == "" {
		return false
	}
	on, err := strconv.ParseBool(v)
	return err != nil || on // A2A_DEBUG=payloads тоже включает
}

// Debug отвечает, включён ли дамп тел. Нужен вызывающему, чтобы не собирать
// дорогой JSON впустую.
func (t *Tracer) Debug() bool { return t != nil && t.debug }

// Dump печатает тело A2A-обмена целиком: label — что это и в какую сторону.
// Молчит, когда debug выключен.
//
// JSON переформатируется с отступами — читать однострочный ответ с разметкой
// A2UI невозможно, а ради этого дамп и включают. Неразбираемое тело печатается
// как есть: в разбирательстве важнее увидеть мусор, чем не увидеть ничего.
func (t *Tracer) Dump(label string, body []byte) {
	if !t.Debug() || len(body) == 0 {
		return
	}
	out := body
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "    ", "  ") == nil {
		out = pretty.Bytes()
	}
	out = cardLike.ReplaceAll(out, []byte(cardMask))
	suffix := ""
	if len(out) > maxDumpBytes {
		out = out[:maxDumpBytes]
		suffix = "\n    … обрезано"
	}
	t.l.Printf("⇄ %s:\n    %s%s", label, out, suffix)
}

// Logf writes one trace line. Safe to call on a nil *Tracer.
func (t *Tracer) Logf(format string, args ...any) {
	if t == nil {
		return
	}
	t.l.Printf(format, args...)
}
