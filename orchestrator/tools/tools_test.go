package tools

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/auth"
)

var (
	visitor = auth.Caller{}
	invitee = auth.Caller{ID: "u1", Groups: []string{auth.InviteeGroup}}
	static  = Static{ContactOptions: "Message me here.", Availability: "Mon–Fri 9–5"}
)

func names(r Registry, c auth.Caller) []string {
	var out []string
	for _, s := range r.For(c) {
		out = append(out, s.Name)
	}
	return out
}

func TestVisitorsSeeOnlyThePublicTool(t *testing.T) {
	r := New(static, &MemoryOutbox{})
	if got := names(r, visitor); !slices.Equal(got, []string{"get_contact_options"}) {
		t.Errorf("visitor tools = %v", got)
	}
	want := []string{"get_contact_options", "check_availability", "request_meeting", "send_message"}
	if got := names(r, invitee); !slices.Equal(got, want) {
		t.Errorf("invitee tools = %v", got)
	}
	for _, s := range r.For(invitee) {
		if s.Description == "" || !json.Valid(s.Parameters) {
			t.Errorf("%s: missing description or invalid parameter schema", s.Name)
		}
	}
}

func TestAllowedIsCheckedAgainForEveryTool(t *testing.T) {
	r := New(static, &MemoryOutbox{})
	for _, name := range []string{"check_availability", "request_meeting", "send_message"} {
		d, ok := r.Lookup(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if Allowed(d, visitor) {
			t.Errorf("visitor allowed to use %s", name)
		}
		if !Allowed(d, invitee) {
			t.Errorf("invitee refused %s", name)
		}
	}
	d := lookup(t, r, "get_contact_options")
	if !Allowed(d, visitor) {
		t.Error("visitor refused get_contact_options")
	}
	if _, ok := r.Lookup("delete_everything"); ok {
		t.Error("unknown tool found")
	}
}

func TestSideEffectsAndStaticAnswers(t *testing.T) {
	r := New(static, &MemoryOutbox{})
	for name, side := range map[string]bool{"get_contact_options": false, "check_availability": false, "request_meeting": true, "send_message": true} {
		if d := lookup(t, r, name); d.SideEffect != side {
			t.Errorf("%s SideEffect = %v", name, d.SideEffect)
		}
	}
	d := lookup(t, r, "check_availability")
	got, err := d.Run(context.Background(), invitee, json.RawMessage(`{}`))
	if raw, _ := json.Marshal(got); err != nil || !strings.Contains(string(raw), "Mon–Fri 9–5") {
		t.Errorf("availability = %s, %v", raw, err)
	}
}

func TestSummariesValidateArguments(t *testing.T) {
	r := New(static, &MemoryOutbox{})
	send := lookup(t, r, "send_message")
	s, err := send.Summary(json.RawMessage(`{"message":"Can we talk about Go?","reply_to":"me@example.org"}`))
	if err != nil || !strings.Contains(s, "Can we talk about Go?") || !strings.Contains(s, "me@example.org") {
		t.Errorf("summary %q, err %v", s, err)
	}
	for name, args := range map[string]string{
		"not JSON":      `{"message":`,
		"empty message": `{"message":"  "}`,
		"too long":      `{"message":"` + strings.Repeat("x", 2001) + `"}`,
	} {
		if _, err := send.Summary(json.RawMessage(args)); err == nil {
			t.Errorf("send_message %s: want an error", name)
		}
	}
	meet := lookup(t, r, "request_meeting")
	if _, err := meet.Summary(json.RawMessage(`{"topic":""}`)); err == nil {
		t.Error("request_meeting without a topic: want an error")
	}
	if s, err := meet.Summary(json.RawMessage(`{"topic":"Go consulting","preferred_times":"Tuesday pm"}`)); err != nil ||
		!strings.Contains(s, "Go consulting") || !strings.Contains(s, "Tuesday pm") {
		t.Errorf("meeting summary %q, err %v", s, err)
	}
}

func TestSendMessageRecordsToTheOutbox(t *testing.T) {
	box := &MemoryOutbox{}
	send := lookup(t, New(static, box), "send_message")
	if _, err := send.Run(context.Background(), invitee, json.RawMessage(`{"message":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if got := box.Records(); len(got) != 1 || !strings.Contains(got[0], "send_message") || !strings.Contains(got[0], "u1") || !strings.Contains(got[0], "hello") {
		t.Errorf("outbox = %v", got)
	}
}

func TestPendingActionsRunAtMostOnce(t *testing.T) {
	p := NewMemoryPending()
	ctx, now := context.Background(), time.Unix(1000, 0)
	a := PendingAction{ID: "a1", CallerID: "u1", Tool: "send_message", Expires: now.Add(PendingTTL)}
	if err := p.Propose(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Claim(ctx, "a1", "someone-else", now); !errors.Is(err, ErrNoAction) {
		t.Errorf("foreign claim: err = %v, want ErrNoAction (indistinguishable from missing)", err)
	}
	if _, err := p.Claim(ctx, "nope", "u1", now); !errors.Is(err, ErrNoAction) {
		t.Errorf("missing claim: err = %v", err)
	}
	got, err := p.Claim(ctx, "a1", "u1", now)
	if err != nil || got.Tool != "send_message" {
		t.Fatalf("claim: %+v, %v", got, err)
	}
	if _, err := p.Claim(ctx, "a1", "u1", now); !errors.Is(err, ErrAlreadyDone) {
		t.Errorf("second claim: err = %v, want ErrAlreadyDone", err)
	}
	p.Propose(ctx, PendingAction{ID: "a2", CallerID: "u1", Expires: now.Add(PendingTTL)})
	if _, err := p.Claim(ctx, "a2", "u1", now.Add(PendingTTL)); !errors.Is(err, ErrExpired) {
		t.Errorf("expired claim: err = %v, want ErrExpired", err)
	}
	if err := p.Propose(ctx, PendingAction{ID: "a1", CallerID: "u1"}); err == nil {
		t.Error("proposing a duplicate id: want an error")
	}
}

func TestConcurrentClaimsRunOnce(t *testing.T) {
	p := NewMemoryPending()
	now := time.Unix(1000, 0)
	p.Propose(context.Background(), PendingAction{ID: "a1", CallerID: "u1", Expires: now.Add(PendingTTL)})
	wins := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		go func() {
			_, err := p.Claim(context.Background(), "a1", "u1", now)
			wins <- err == nil
		}()
	}
	n := 0
	for i := 0; i < 20; i++ {
		if <-wins {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d of 20 concurrent claims succeeded, want exactly 1", n)
	}
}

func lookup(t *testing.T, r Registry, name string) Def {
	t.Helper()
	d, ok := r.Lookup(name)
	if !ok || d.Summary == nil || d.Run == nil {
		t.Fatalf("tool %s missing or incomplete", name)
	}
	return d
}
