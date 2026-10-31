package sse

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a fake stream: it keeps each Write separately, with its time,
// so the test can tell events sent one by one from events sent in a batch.
type recorder struct {
	mu     sync.Mutex
	writes []string
	at     []time.Time
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, string(bytes.Clone(p)))
	r.at = append(r.at, time.Now())
	return len(p), nil
}

func TestStreamSendsEventsOneByOne(t *testing.T) {
	const n, every = 5, 30 * time.Millisecond
	var rec recorder
	if err := Stream(context.Background(), &rec, n, every); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if len(rec.writes) != n {
		t.Fatalf("got %d writes, want %d (one per event): %q", len(rec.writes), n, rec.writes)
	}
	for i, w := range rec.writes {
		if !strings.HasPrefix(w, "id: ") || !strings.Contains(w, "\nevent: tick\ndata: {") || !strings.HasSuffix(w, "}\n\n") {
			t.Errorf("write %d is not one complete SSE event: %q", i, w)
		}
	}
	for i := 1; i < n; i++ {
		// Allow scheduler jitter below the interval, never a burst.
		if gap := rec.at[i].Sub(rec.at[i-1]); gap < every*8/10 {
			t.Errorf("event %d came %v after the previous one, want about %v", i, gap, every)
		}
	}
}

func TestStreamStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var rec recorder
	err := Stream(ctx, &rec, 100, 20*time.Millisecond)
	if err == nil {
		t.Fatal("want an error when the context is cancelled")
	}
	if len(rec.writes) >= 100 {
		t.Errorf("sent all %d events despite cancellation", len(rec.writes))
	}
}
