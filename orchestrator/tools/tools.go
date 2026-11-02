// Package tools defines the receptionist's tools and who may use them
// (design §6): filtering decides which tools the model is shown, Allowed is
// the executor's second check, and side-effecting tools only run through a
// confirmed pending action.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// Def is one tool.
type Def struct {
	Spec       model.ToolSpec
	Invitee    bool // only invitees may use it
	SideEffect bool // runs only after the visitor confirms
	// Summary validates the arguments and says in plain words what the call
	// will do; the confirm button shows it.
	Summary func(args json.RawMessage) (string, error)
	Run     func(ctx context.Context, caller auth.Caller, args json.RawMessage) (any, error)
}

// Registry is the set of tools, in the order the model sees them.
type Registry []Def

// Static is the configuration read-only tools answer from.
type Static struct {
	ContactOptions string // e.g. "Use the message tool here, or LinkedIn"
	Availability   string // e.g. "Mon–Fri 09:00–17:00 UK time"
}

// Outbox records what side-effecting tools send. In memory for now; Postgres
// and the notification channel [D2] replace it in later phases.
type Outbox interface {
	Record(ctx context.Context, kind, callerID string, payload any) error
}

// MaxText is the longest message or topic a tool accepts, in characters.
const MaxText = 2000

var noArgs = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

// New returns the four tools of design §6.
func New(static Static, outbox Outbox) Registry {
	none := func(json.RawMessage) (string, error) { return "", nil }
	return Registry{
		{
			Spec: model.ToolSpec{Name: "get_contact_options",
				Description: "How a visitor can get in touch with the site owner.", Parameters: noArgs},
			Summary: none,
			Run: func(context.Context, auth.Caller, json.RawMessage) (any, error) {
				return map[string]string{"contact_options": static.ContactOptions}, nil
			},
		},
		{
			Spec: model.ToolSpec{Name: "check_availability",
				Description: "The site owner's usual weekly hours for meetings.", Parameters: noArgs},
			Invitee: true, Summary: none,
			Run: func(context.Context, auth.Caller, json.RawMessage) (any, error) {
				return map[string]string{"weekly_hours": static.Availability}, nil
			},
		},
		{
			Spec: model.ToolSpec{Name: "request_meeting",
				Description: "Ask the site owner for a meeting. The visitor must confirm before it is sent.",
				Parameters: json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string","description":"What the meeting is about"},` +
					`"preferred_times":{"type":"string","description":"When suits the visitor"}},"required":["topic"],"additionalProperties":false}`)},
			Invitee: true, SideEffect: true, Summary: meetingSummary,
			Run: func(ctx context.Context, c auth.Caller, args json.RawMessage) (any, error) {
				var m meeting
				if err := decode(args, &m); err != nil {
					return nil, err
				}
				return map[string]string{"status": "requested"}, outbox.Record(ctx, "request_meeting", c.ID, m)
			},
		},
		{
			Spec: model.ToolSpec{Name: "send_message",
				Description: "Send the site owner a message. The visitor must confirm before it is sent.",
				Parameters: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","description":"The message"},` +
					`"reply_to":{"type":"string","description":"How the owner can reply, only if the visitor gave it; omit it otherwise"}},"required":["message"],"additionalProperties":false}`)},
			Invitee: true, SideEffect: true, Summary: messageSummary,
			Run: func(ctx context.Context, c auth.Caller, args json.RawMessage) (any, error) {
				var m message
				if err := decode(args, &m); err != nil {
					return nil, err
				}
				return map[string]string{"status": "sent"}, outbox.Record(ctx, "send_message", c.ID, m)
			},
		},
	}
}

type message struct {
	Message string `json:"message"`
	ReplyTo string `json:"reply_to,omitempty"`
}

type meeting struct {
	Topic          string `json:"topic"`
	PreferredTimes string `json:"preferred_times,omitempty"`
}

func decode(args json.RawMessage, v any) error {
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("arguments aren't valid JSON: %w", err)
	}
	return nil
}

func text(field, v string) (string, error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return "", fmt.Errorf("%s is empty", field)
	case utf8.RuneCountInString(v) > MaxText:
		return "", fmt.Errorf("%s is longer than %d characters", field, MaxText)
	}
	return v, nil
}

func messageSummary(args json.RawMessage) (string, error) {
	var m message
	if err := decode(args, &m); err != nil {
		return "", err
	}
	msg, err := text("message", m.Message)
	if err != nil {
		return "", err
	}
	s := fmt.Sprintf("Send the site owner this message: %q", msg)
	if r := strings.TrimSpace(m.ReplyTo); r != "" {
		s += fmt.Sprintf(" (reply to %s)", r)
	}
	return s, nil
}

func meetingSummary(args json.RawMessage) (string, error) {
	var m meeting
	if err := decode(args, &m); err != nil {
		return "", err
	}
	topic, err := text("topic", m.Topic)
	if err != nil {
		return "", err
	}
	s := fmt.Sprintf("Ask the site owner for a meeting about %q", topic)
	if p := strings.TrimSpace(m.PreferredTimes); p != "" {
		s += fmt.Sprintf(", preferably %s", p)
	}
	return s, nil
}

// For lists the tools caller may use: the first authorization layer.
func (r Registry) For(caller auth.Caller) []model.ToolSpec {
	var out []model.ToolSpec
	for _, d := range r {
		if Allowed(d, caller) {
			out = append(out, d.Spec)
		}
	}
	return out
}

// Lookup finds a tool by name.
func (r Registry) Lookup(name string) (Def, bool) {
	for _, d := range r {
		if d.Spec.Name == name {
			return d, true
		}
	}
	return Def{}, false
}

// Allowed is the executor's check, made again before any tool runs: the
// second authorization layer.
func Allowed(d Def, caller auth.Caller) bool { return !d.Invitee || caller.IsInvitee() }

// PendingTTL is how long a proposed action waits for confirmation.
const PendingTTL = 10 * time.Minute

// Pending action errors. A foreign action id gets ErrNoAction, like a
// missing one, so ids can't be probed.
var (
	ErrNoAction    = errors.New("tools: no such action")
	ErrExpired     = errors.New("tools: action expired")
	ErrAlreadyDone = errors.New("tools: action already done")
)

// PendingAction is a side-effecting call waiting for confirmation.
type PendingAction struct {
	ID, CallerID, Tool, Summary string
	Args                        json.RawMessage
	Expires                     time.Time
}

// PendingActions stores proposals; Claim is one conditional update from
// pending to executed, so each action runs at most once (design §6).
type PendingActions interface {
	Propose(ctx context.Context, a PendingAction) error
	Claim(ctx context.Context, id, callerID string, now time.Time) (PendingAction, error)
}

// MemoryPending is an in-memory PendingActions for tests and `make dev`;
// DynamoDB implements it from Phase 7.
type MemoryPending struct {
	mu      sync.Mutex
	actions map[string]*memAction
}

type memAction struct {
	PendingAction
	done bool
}

func NewMemoryPending() *MemoryPending { return &MemoryPending{actions: map[string]*memAction{}} }

func (m *MemoryPending) Propose(_ context.Context, a PendingAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.actions[a.ID]; dup {
		return fmt.Errorf("tools: action %s already exists", a.ID)
	}
	m.actions[a.ID] = &memAction{PendingAction: a}
	return nil
}

// Claim checks owner, expiry and status and marks the action done, all under
// one lock: the in-memory form of a conditional update.
func (m *MemoryPending) Claim(_ context.Context, id, callerID string, now time.Time) (PendingAction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.actions[id]
	switch {
	case !ok || a.CallerID != callerID:
		return PendingAction{}, ErrNoAction
	case a.done:
		return PendingAction{}, ErrAlreadyDone
	case !now.Before(a.Expires):
		return PendingAction{}, ErrExpired
	}
	a.done = true
	return a.PendingAction, nil
}

// MemoryOutbox records in memory and, if Log is set, echoes each record.
type MemoryOutbox struct {
	Log     io.Writer
	mu      sync.Mutex
	records []string
}

// Records returns everything recorded.
func (o *MemoryOutbox) Records() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.records...)
}

func (o *MemoryOutbox) Record(_ context.Context, kind, callerID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("outbox: %s from %s: %s", kind, callerID, raw)
	o.mu.Lock()
	o.records = append(o.records, line)
	o.mu.Unlock()
	if o.Log != nil {
		fmt.Fprintln(o.Log, line)
	}
	return nil
}
