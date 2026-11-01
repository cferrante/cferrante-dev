package clock

import (
	"testing"
	"time"
)

func TestFakeAdvanceFiresTicksAndTimers(t *testing.T) {
	f := NewFake()
	start := f.Now()
	tick, stop := f.Tick(10 * time.Second)
	after := f.After(5 * time.Second)

	f.Advance(4 * time.Second)
	select {
	case <-tick:
		t.Fatal("tick fired early")
	case <-after:
		t.Fatal("timer fired early")
	default:
	}
	f.Advance(6 * time.Second) // now +10 s
	if got := f.Now().Sub(start); got != 10*time.Second {
		t.Errorf("Now moved %v, want 10s", got)
	}
	select {
	case <-after:
	default:
		t.Error("timer didn't fire at 5 s")
	}
	select {
	case <-tick:
	default:
		t.Error("tick didn't fire at 10 s")
	}
	stop()
	f.Advance(20 * time.Second)
	select {
	case <-tick:
		t.Error("stopped ticker fired")
	default:
	}
	if f.Waiters() != 0 {
		t.Errorf("Waiters = %d after stop and fire, want 0", f.Waiters())
	}
}
