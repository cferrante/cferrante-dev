package model

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/clock"
)

// FirstTokenTimeout is how long the primary may stay silent before the
// router falls back (design §4).
const FirstTokenTimeout = 5 * time.Second

// ErrFirstTokenTimeout is the primary's error when it sent nothing in time.
var ErrFirstTokenTimeout = errors.New("model: no first token within the timeout")

// Router answers with Primary and falls back to Fallback only before the
// first token: switching mid-answer would stitch two models' replies together,
// so after the first token an error is reported, not hidden (design §4).
//
// Any error before the first token triggers the fallback (rate limits, 5xx,
// timeouts, and also unexpected errors, so the visitor still gets an answer;
// Result.PrimaryErr keeps the cause visible), except the caller's own
// cancellation.
type Router struct {
	Primary, Fallback Model
	Clock             clock.Clock
}

const (
	waiting = iota
	started
	timedOut
)

// Stream implements Model.
func (r *Router) Stream(ctx context.Context, req Request, onEvent func(Event) error) (Result, error) {
	pctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var mu sync.Mutex
	state := waiting
	first := make(chan struct{})
	forward := func(e Event) error {
		mu.Lock()
		switch state {
		case timedOut:
			// A token already in flight when we gave up must not reach the
			// caller ahead of the fallback's answer.
			mu.Unlock()
			return ErrFirstTokenTimeout
		case waiting:
			state = started
			close(first)
		}
		mu.Unlock()
		return onEvent(e)
	}

	stopTimer := make(chan struct{})
	go func() {
		select {
		case <-r.Clock.After(FirstTokenTimeout):
			mu.Lock()
			if state == waiting {
				state = timedOut
				cancel(ErrFirstTokenTimeout)
			}
			mu.Unlock()
		case <-first:
		case <-stopTimer:
		}
	}()

	res, err := r.Primary.Stream(pctx, req, forward)
	close(stopTimer)
	mu.Lock()
	s := state
	mu.Unlock()
	if err == nil || s == started || ctx.Err() != nil {
		return res, err
	}
	if s == timedOut {
		err = ErrFirstTokenTimeout
	}
	res, ferr := r.Fallback.Stream(ctx, req, onEvent)
	res.Fallback = true
	res.PrimaryErr = err
	return res, ferr
}
