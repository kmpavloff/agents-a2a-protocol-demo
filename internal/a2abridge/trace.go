package a2abridge

import (
	"io"
	"log"
)

// Tracer writes human-readable, step-by-step traces of the A2A protocol
// exchange between the orchestrator (client) and the orders worker (server).
// It is intended for learning/debugging: each line shows one protocol step.
//
// A nil *Tracer is valid and silently discards all output, so callers that do
// not want tracing (e.g. tests) can pass nil.
type Tracer struct {
	l *log.Logger
}

// NewTracer returns a Tracer that writes to w with the given line prefix.
// A nil w discards output. Lines are timestamped so the interleaving of the
// two-agent conversation is visible.
func NewTracer(w io.Writer, prefix string) *Tracer {
	if w == nil {
		w = io.Discard
	}
	return &Tracer{l: log.New(w, prefix, log.LstdFlags|log.Lmsgprefix)}
}

// Logf writes one trace line. Safe to call on a nil *Tracer.
func (t *Tracer) Logf(format string, args ...any) {
	if t == nil {
		return
	}
	t.l.Printf(format, args...)
}
