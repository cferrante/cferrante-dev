package model_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/fake"
)

func text(s string) model.Event { return model.Event{Kind: model.Text, Text: s} }

// collect runs the router and returns what reached the caller.
func collect(ctx context.Context, r *model.Router) ([]string, model.Result, error) {
	var got []string
	res, err := r.Stream(ctx, model.Request{}, func(e model.Event) error {
		got = append(got, e.Text)
		return nil
	})
	return got, res, err
}

func TestPrimaryAnswersWithoutFallback(t *testing.T) {
	p := &fake.Model{Name: "groq", Events: []model.Event{text("a"), text("b")}}
	f := &fake.Model{Name: "bedrock", Events: []model.Event{text("x")}}
	got, res, err := collect(context.Background(), &model.Router{Primary: p, Fallback: f, Clock: clock.NewFake()})
	if err != nil || len(got) != 2 || res.Provider != "groq" || res.Fallback {
		t.Fatalf("got %v, res %+v, err %v", got, res, err)
	}
	if len(f.Requests()) != 0 {
		t.Error("fallback called although the primary answered")
	}
}

func TestRateLimitedPrimaryFallsBack(t *testing.T) {
	p := &fake.Model{Name: "groq", ErrBefore: &model.HTTPError{Status: 429, Body: "rate limited"}}
	f := &fake.Model{Name: "bedrock", Events: []model.Event{text("x")}}
	got, res, err := collect(context.Background(), &model.Router{Primary: p, Fallback: f, Clock: clock.NewFake()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "x" || res.Provider != "bedrock" || !res.Fallback {
		t.Errorf("got %v, res %+v; want the fallback's answer marked as a fallback", got, res)
	}
	var he *model.HTTPError
	if !errors.As(res.PrimaryErr, &he) || he.Status != 429 {
		t.Errorf("PrimaryErr = %v, want the 429", res.PrimaryErr)
	}
}

func TestErrorAfterFirstTokenIsReportedNotHidden(t *testing.T) {
	p := &fake.Model{Name: "groq", Events: []model.Event{text("half an ans")}, ErrAfter: &model.HTTPError{Status: 500}}
	f := &fake.Model{Name: "bedrock", Events: []model.Event{text("x")}}
	got, _, err := collect(context.Background(), &model.Router{Primary: p, Fallback: f, Clock: clock.NewFake()})
	var he *model.HTTPError
	if !errors.As(err, &he) || he.Status != 500 {
		t.Errorf("err = %v, want the primary's 500", err)
	}
	if len(got) != 1 || len(f.Requests()) != 0 {
		t.Errorf("got %v and %d fallback calls; want no stitched answer", got, len(f.Requests()))
	}
}

func TestSilentPrimaryFallsBackAfterTimeout(t *testing.T) {
	c := clock.NewFake()
	// SendAfterCancel: the primary's first token arrives just after the
	// timeout; it must not reach the caller ahead of the fallback's answer.
	p := &fake.Model{Name: "groq", Block: true, SendAfterCancel: true, Events: []model.Event{text("late")}}
	f := &fake.Model{Name: "bedrock", Events: []model.Event{text("x")}}
	done := make(chan struct{})
	var got []string
	var res model.Result
	var err error
	go func() {
		got, res, err = collect(context.Background(), &model.Router{Primary: p, Fallback: f, Clock: c})
		close(done)
	}()
	waitFor(t, func() bool { return c.Waiters() == 1 })
	c.Advance(model.FirstTokenTimeout - time.Millisecond)
	select {
	case <-done:
		t.Fatal("fell back before the timeout")
	case <-time.After(20 * time.Millisecond):
	}
	c.Advance(time.Millisecond)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no fallback 2 s after the first-token timeout")
	}
	if err != nil || len(got) != 1 || got[0] != "x" || !res.Fallback {
		t.Errorf("got %v, res %+v, err %v; want only the fallback's answer", got, res, err)
	}
	if res.PrimaryErr == nil {
		t.Error("PrimaryErr is nil; want the timeout")
	}
}

func TestCallerCancellationDoesNotFallBack(t *testing.T) {
	p := &fake.Model{Name: "groq", Block: true}
	f := &fake.Model{Name: "bedrock", Events: []model.Event{text("x")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := collect(ctx, &model.Router{Primary: p, Fallback: f, Clock: clock.NewFake()})
	if !errors.Is(err, context.Canceled) || len(f.Requests()) != 0 {
		t.Errorf("err %v, %d fallback calls; want Canceled and none", err, len(f.Requests()))
	}
}

func TestCallbackErrorStopsWithoutFallback(t *testing.T) {
	p := &fake.Model{Name: "groq", Events: []model.Event{text("a"), text("b")}}
	f := &fake.Model{Name: "bedrock", Events: []model.Event{text("x")}}
	gone := errors.New("client went away")
	r := &model.Router{Primary: p, Fallback: f, Clock: clock.NewFake()}
	_, err := r.Stream(context.Background(), model.Request{}, func(model.Event) error { return gone })
	if !errors.Is(err, gone) || len(f.Requests()) != 0 {
		t.Errorf("err %v, %d fallback calls", err, len(f.Requests()))
	}
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
