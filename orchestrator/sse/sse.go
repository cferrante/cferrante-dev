// Package sse writes the chat stream (design §3) and enforces its contract:
//
//  1. every step_start has exactly one step_end, even on error or cancellation;
//  2. done or error is always the last event;
//  3. durations come from the monotonic clock;
//  4. a keep-alive comment goes out every 10 s.
package sse

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/clock"
)

// ErrClosed is returned by every write after done or error has been sent.
var ErrClosed = errors.New("sse: stream already ended")

// KeepAlive is how often an idle-proof comment is sent (contract rule 4).
const KeepAlive = 10 * time.Second

// Citation is the payload of a citation event.
type Citation struct {
	N       int    `json:"n"`
	ChunkID string `json:"chunk_id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
}

type openStep struct {
	id    string
	start time.Time
}

// Writer serialises events to one client. Safe for concurrent use: the
// keep-alive and the agent loop write through the same lock, so seq order is
// the order on the wire.
type Writer struct {
	mu       sync.Mutex
	out      io.Writer
	flush    func()
	clock    clock.Clock
	start    time.Time
	seq      int
	open     []openStep      // in opening order
	used     map[string]bool // step ids ever started
	ended    bool
	err      error // first write error; sticks
	stopTick func()
	stop     chan struct{}
}

// New starts a stream on w. flush, if non-nil, is called after every write so
// events reach the client immediately. The keep-alive runs until the stream ends.
func New(w io.Writer, flush func(), c clock.Clock) *Writer {
	tick, stopTick := c.Tick(KeepAlive)
	sw := &Writer{
		out: w, flush: flush, clock: c, start: c.Now(),
		used: map[string]bool{}, stopTick: stopTick, stop: make(chan struct{}),
	}
	go sw.keepAlive(tick)
	return sw
}

func (w *Writer) keepAlive(tick <-chan time.Time) {
	for {
		select {
		case <-w.stop:
			return
		case <-tick:
			w.mu.Lock()
			if !w.ended && w.err == nil {
				w.write([]byte(": ping\n\n"))
			}
			w.mu.Unlock()
		}
	}
}

// Thought sends a chunk of reasoning (source "model") or narration ("agent").
func (w *Writer) Thought(text, source string) error {
	return w.locked(func() error { return w.emit("thought", map[string]any{"text": text, "source": source}) })
}

// Token sends a chunk of the answer.
func (w *Writer) Token(text string) error {
	return w.locked(func() error { return w.emit("token", map[string]any{"text": text}) })
}

// Citation sends a citation.
func (w *Writer) Citation(c Citation) error {
	return w.locked(func() error {
		return w.emit("citation", map[string]any{"n": c.N, "chunk_id": c.ChunkID, "title": c.Title, "url": c.URL})
	})
}

// StepStart opens a step. A step id can be used only once per stream.
func (w *Writer) StepStart(id, kind, label string) error {
	return w.locked(func() error {
		if w.used[id] {
			return fmt.Errorf("sse: step %q already started", id)
		}
		if err := w.emit("step_start", map[string]any{"step_id": id, "kind": kind, "label": label}); err != nil {
			return err
		}
		w.used[id] = true
		w.open = append(w.open, openStep{id, w.clock.Now()})
		return nil
	})
}

// StepEnd closes an open step; its duration is measured from StepStart.
func (w *Writer) StepEnd(id, status string, detail any) error {
	return w.locked(func() error {
		for i := range w.open {
			if w.open[i].id == id {
				return w.endStep(i, status, detail)
			}
		}
		return fmt.Errorf("sse: step %q is not open", id)
	})
}

// ConfirmRequest asks the visitor to confirm a proposed action (design §6).
func (w *Writer) ConfirmRequest(actionID, tool, summary string, expiresIn time.Duration) error {
	return w.locked(func() error {
		return w.emit("confirm_request", map[string]any{
			"action_id": actionID, "tool": tool, "summary": summary, "expires_in_s": int(expiresIn.Seconds()),
		})
	})
}

// Error ends the stream with an error, first closing any open steps.
func (w *Writer) Error(code, message string, retryable bool) error {
	return w.locked(func() error {
		return w.finish("error", map[string]any{"code": code, "message": message, "retryable": retryable})
	})
}

// Done ends the stream normally, first closing any open steps.
func (w *Writer) Done(conversationID string) error {
	return w.locked(func() error {
		return w.finish("done", map[string]any{"conversation_id": conversationID})
	})
}

// Close ends the stream if the handler didn't: open steps are closed and an
// internal error is sent, so the contract holds on every path. Call it with
// defer; after done or error it does nothing.
func (w *Writer) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ended {
		return
	}
	_ = w.finish("error", map[string]any{
		"code": "internal", "message": "the stream ended unexpectedly", "retryable": true,
	})
}

func (w *Writer) locked(f func() error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ended {
		return ErrClosed
	}
	return f()
}

// finish closes open steps newest first, sends the final event and stops the
// keep-alive. The stream counts as ended even if a write fails.
func (w *Writer) finish(name string, fields map[string]any) error {
	defer w.end()
	for len(w.open) > 0 {
		if err := w.endStep(len(w.open)-1, "error", map[string]any{"reason": "unfinished"}); err != nil {
			return err
		}
	}
	if name == "done" {
		fields["total_ms"] = w.sinceStart()
	}
	return w.emit(name, fields)
}

func (w *Writer) end() {
	w.ended = true
	w.stopTick()
	close(w.stop)
}

func (w *Writer) endStep(i int, status string, detail any) error {
	s := w.open[i]
	// Rule 3: Sub of two clock readings uses the monotonic clock.
	d := w.clock.Now().Sub(s.start)
	fields := map[string]any{
		"step_id": s.id, "status": status, "duration_ms": math.Round(float64(d)/1e4) / 100,
	}
	if detail != nil {
		fields["detail"] = detail
	}
	w.open = append(w.open[:i], w.open[i+1:]...)
	return w.emit("step_end", fields)
}

func (w *Writer) sinceStart() float64 {
	return math.Round(float64(w.clock.Now().Sub(w.start))/1e5) / 10
}

func (w *Writer) emit(name string, fields map[string]any) error {
	if w.err != nil {
		return w.err
	}
	w.seq++
	fields["seq"] = w.seq
	fields["t_ms"] = w.sinceStart()
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return w.write([]byte("event: " + name + "\ndata: " + string(data) + "\n\n"))
}

func (w *Writer) write(p []byte) error {
	if _, err := w.out.Write(p); err != nil {
		w.err = fmt.Errorf("sse: write: %w", err)
		return w.err
	}
	if w.flush != nil {
		w.flush()
	}
	return nil
}
