package sse

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/clock"
)

// syncBuf is a goroutine-safe buffer: the keep-alive writes from its own goroutine.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type event struct {
	name string
	data map[string]any
}

// parse splits the stream into events; comments (": ping") come back with name ":".
func parse(t *testing.T, raw string) []event {
	t.Helper()
	var out []event
	for _, block := range strings.Split(strings.TrimSuffix(raw, "\n\n"), "\n\n") {
		if block == "" {
			continue
		}
		if strings.HasPrefix(block, ":") {
			out = append(out, event{name: ":"})
			continue
		}
		lines := strings.Split(block, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("malformed event block %q", block)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &data); err != nil {
			t.Fatalf("bad JSON in %q: %v", block, err)
		}
		out = append(out, event{strings.TrimPrefix(lines[0], "event: "), data})
	}
	return out
}

func names(evs []event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.name)
	}
	return out
}

func newWriter() (*Writer, *syncBuf, *clock.Fake, *int) {
	buf := &syncBuf{}
	c := clock.NewFake()
	flushes := new(int)
	return New(buf, func() { *flushes++ }, c), buf, c, flushes
}

func TestEventsCarrySeqAndServerTime(t *testing.T) {
	w, buf, c, flushes := newWriter()
	c.Advance(12400 * time.Microsecond)
	w.Thought("looking", "agent")
	c.Advance(time.Millisecond)
	w.Token("Hello")
	w.Done("c_1")
	evs := parse(t, buf.String())
	if got := strings.Join(names(evs), ","); got != "thought,token,done" {
		t.Fatalf("events = %s", got)
	}
	for i, e := range evs {
		if e.data["seq"] != float64(i+1) {
			t.Errorf("event %d seq = %v, want %d", i, e.data["seq"], i+1)
		}
	}
	if evs[0].data["t_ms"] != 12.4 || evs[1].data["t_ms"] != 13.4 {
		t.Errorf("t_ms = %v, %v; want 12.4, 13.4", evs[0].data["t_ms"], evs[1].data["t_ms"])
	}
	if evs[0].data["text"] != "looking" || evs[0].data["source"] != "agent" {
		t.Errorf("thought = %v", evs[0].data)
	}
	if evs[2].data["conversation_id"] != "c_1" || evs[2].data["total_ms"] != 13.4 {
		t.Errorf("done = %v", evs[2].data)
	}
	if *flushes < 3 {
		t.Errorf("flushed %d times for 3 events", *flushes)
	}
}

// Rule 1: every step_start has exactly one step_end.
func TestStepPairing(t *testing.T) {
	w, buf, c, _ := newWriter()
	if err := w.StepEnd("nope", "ok", nil); err == nil {
		t.Error("step_end without step_start: want an error")
	}
	c.Advance(5 * time.Millisecond) // so a step's start differs from the stream's
	if err := w.StepStart("s1", "retrieval", "Hybrid search"); err != nil {
		t.Fatal(err)
	}
	if err := w.StepStart("s1", "retrieval", "again"); err == nil {
		t.Error("duplicate step_start: want an error")
	}
	c.Advance(410 * time.Microsecond)
	if err := w.StepEnd("s1", "ok", map[string]any{"hits": 4}); err != nil {
		t.Fatal(err)
	}
	if err := w.StepEnd("s1", "ok", nil); err == nil {
		t.Error("second step_end: want an error")
	}
	w.Done("c")
	evs := parse(t, buf.String())
	if got := strings.Join(names(evs), ","); got != "step_start,step_end,done" {
		t.Fatalf("events = %s (rejected calls must write nothing)", got)
	}
	end := evs[1].data
	// Rule 3: the duration comes from the clock, not from t_ms arithmetic.
	if end["duration_ms"] != 0.41 || end["status"] != "ok" || end["step_id"] != "s1" {
		t.Errorf("step_end = %v", end)
	}
	if d, _ := end["detail"].(map[string]any); d["hits"] != float64(4) {
		t.Errorf("detail = %v", end["detail"])
	}
}

func TestDoneAndErrorCloseOpenSteps(t *testing.T) {
	for _, final := range []string{"done", "error"} {
		w, buf, _, _ := newWriter()
		w.StepStart("s1", "retrieval", "Hybrid search")
		w.StepStart("s2", "inference", "Model")
		if final == "done" {
			w.Done("c")
		} else {
			w.Error("cancelled", "client went away", false)
		}
		evs := parse(t, buf.String())
		if got := strings.Join(names(evs), ","); got != "step_start,step_start,step_end,step_end,"+final {
			t.Fatalf("%s: events = %s", final, got)
		}
		// Closed in reverse order of opening, both as errors.
		if evs[2].data["step_id"] != "s2" || evs[3].data["step_id"] != "s1" ||
			evs[2].data["status"] != "error" || evs[3].data["status"] != "error" {
			t.Errorf("%s: closing events = %v, %v", final, evs[2].data, evs[3].data)
		}
	}
}

// Rule 2: done or error is always last.
func TestNothingAfterTheFinalEvent(t *testing.T) {
	w, buf, _, _ := newWriter()
	w.Done("c")
	before := buf.String()
	calls := map[string]error{
		"thought":  w.Thought("x", "agent"),
		"token":    w.Token("x"),
		"citation": w.Citation(Citation{N: 1}),
		"start":    w.StepStart("s", "tool", "t"),
		"confirm":  w.ConfirmRequest("a", "send_message", "Send", time.Minute),
		"error":    w.Error("x", "y", false),
		"done":     w.Done("c"),
	}
	for name, err := range calls {
		if !errors.Is(err, ErrClosed) {
			t.Errorf("%s after done: err = %v, want ErrClosed", name, err)
		}
	}
	w.Close()
	if buf.String() != before {
		t.Errorf("wrote after done: %q", strings.TrimPrefix(buf.String(), before))
	}
}

func TestCloseWithoutFinalEventSendsError(t *testing.T) {
	w, buf, _, _ := newWriter()
	w.StepStart("s1", "inference", "Model")
	w.Close()
	evs := parse(t, buf.String())
	if got := strings.Join(names(evs), ","); got != "step_start,step_end,error" {
		t.Fatalf("events = %s", got)
	}
	if evs[2].data["code"] != "internal" {
		t.Errorf("error = %v", evs[2].data)
	}
}

// Rule 4: a keep-alive comment every 10 s, stopping when the stream ends.
func TestKeepAlive(t *testing.T) {
	w, buf, c, _ := newWriter()
	waitFor(t, func() bool { return c.Waiters() == 1 })
	c.Advance(KeepAlive - time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if strings.Contains(buf.String(), ": ping") {
		t.Fatal("ping before 10 s")
	}
	c.Advance(time.Millisecond)
	waitFor(t, func() bool { return strings.Contains(buf.String(), ": ping\n\n") })
	w.Done("c")
	waitFor(t, func() bool { return c.Waiters() == 0 })
	n := strings.Count(buf.String(), ": ping")
	c.Advance(time.Minute)
	time.Sleep(10 * time.Millisecond)
	if strings.Count(buf.String(), ": ping") != n {
		t.Error("ping after done")
	}
}

func TestConcurrentWritersKeepSeqOrdered(t *testing.T) {
	w, buf, c, _ := newWriter()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				w.Token("x")
				c.Advance(KeepAlive / 4)
			}
		}()
	}
	wg.Wait()
	w.Done("c")
	seq := 0.0
	for _, e := range parse(t, buf.String()) {
		if e.name == ":" {
			continue
		}
		if e.data["seq"] != seq+1 {
			t.Fatalf("seq %v follows %v", e.data["seq"], seq)
		}
		seq++
	}
	if seq != 401 {
		t.Errorf("%v events, want 401", seq)
	}
}

// failWriter fails exactly once, on write number failOn, then works again, so
// only a sticky error explains later failures.
type failWriter struct{ writes, failOn int }

func (f *failWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes == f.failOn {
		return 0, errors.New("broken pipe")
	}
	return len(p), nil
}

func TestWriteFailureIsReturnedAndSticks(t *testing.T) {
	w := New(&failWriter{failOn: 2}, nil, clock.NewFake())
	if err := w.Token("ok"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := w.Token("lost"); err == nil {
		t.Error("write to a broken client: want an error")
	}
	if err := w.Token("again"); err == nil {
		t.Error("later write: want the error to stick")
	}
	w.Close()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2 s")
		}
		time.Sleep(time.Millisecond)
	}
}
