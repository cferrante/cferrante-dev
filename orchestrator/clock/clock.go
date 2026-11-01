// Package clock lets timing code run on the real clock in production and a
// manually advanced one in tests, so timeouts and keep-alives are tested
// without real waiting.
package clock

import (
	"sync"
	"time"
)

// Clock is the time source for durations and periodic ticks.
type Clock interface {
	// Now returns the current time. Real times carry Go's monotonic reading,
	// so Sub between two of them is immune to wall-clock changes.
	Now() time.Time
	// Tick returns a channel that receives every d, and a function that stops it.
	Tick(d time.Duration) (<-chan time.Time, func())
	// After returns a channel that receives once after d.
	After(d time.Duration) <-chan time.Time
}

// Real is the system clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

func (Real) Tick(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a clock that only moves when Advance is called. Safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
}

type waiter struct {
	at      time.Time
	every   time.Duration // 0 for one-shot
	ch      chan time.Time
	stopped bool
}

// NewFake returns a fake clock starting at an arbitrary fixed time.
func NewFake() *Fake { return &Fake{now: time.Unix(1_000_000, 0)} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Tick(d time.Duration) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &waiter{at: f.now.Add(d), every: d, ch: make(chan time.Time, 1)}
	f.waiters = append(f.waiters, w)
	return w.ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.stopped = true
	}
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &waiter{at: f.now.Add(d), ch: make(chan time.Time, 1)}
	f.waiters = append(f.waiters, w)
	return w.ch
}

// Advance moves the clock forward and fires every tick and timer that falls
// due, like a real ticker: a tick that finds its channel full is dropped.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	kept := f.waiters[:0]
	for _, w := range f.waiters {
		if w.stopped {
			continue
		}
		for !w.at.After(f.now) {
			select {
			case w.ch <- w.at:
			default:
			}
			if w.every == 0 {
				w.stopped = true
				break
			}
			w.at = w.at.Add(w.every)
		}
		if !w.stopped {
			kept = append(kept, w)
		}
	}
	f.waiters = kept
}

// Waiters reports how many ticks and timers are active; tests use it to wait
// until a goroutine has set one up before advancing.
func (f *Fake) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, w := range f.waiters {
		if !w.stopped {
			n++
		}
	}
	return n
}
