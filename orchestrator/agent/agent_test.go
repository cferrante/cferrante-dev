package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/fake"
	"github.com/cferrante/cferrante-dev/orchestrator/sse"
)

type fakeRetriever struct {
	chunks []Chunk
	err    error
	mu     sync.Mutex
	masks  []uint32
}

func (f *fakeRetriever) Retrieve(_ context.Context, _ string, mask uint32, k int) ([]Chunk, error) {
	f.mu.Lock()
	f.masks = append(f.masks, mask)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.chunks[:min(k, len(f.chunks))], nil
}

var twoChunks = []Chunk{
	{ID: "proj-a#summary", Title: "Project A", URL: "/a", Text: "A is a Go service.", Score: 0.9},
	{ID: "proj-b#summary", Title: "Project B", URL: "/b", Text: "B is a C++ engine.", Score: 0.8},
}

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

type ev struct {
	name string
	data map[string]any
}

func events(t *testing.T, raw string) []ev {
	t.Helper()
	var out []ev
	for _, block := range strings.Split(strings.TrimSpace(raw), "\n\n") {
		if block == "" || strings.HasPrefix(block, ":") {
			continue
		}
		lines := strings.SplitN(block, "\n", 2)
		var d map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &d); err != nil {
			t.Fatalf("bad event %q: %v", block, err)
		}
		out = append(out, ev{strings.TrimPrefix(lines[0], "event: "), d})
	}
	return out
}

func names(evs []ev) string {
	var n []string
	for _, e := range evs {
		n = append(n, e.name)
	}
	return strings.Join(n, ",")
}

type harness struct {
	agent *Agent
	model *fake.Model
	ret   *fakeRetriever
	clock *clock.Fake
	ids   int
}

func newHarness(m *fake.Model, r *fakeRetriever) *harness {
	h := &harness{model: m, ret: r, clock: clock.NewFake()}
	h.agent = &Agent{Model: m, Retriever: r, Store: NewMemoryStore(), Clock: h.clock,
		NewID: func() string { h.ids++; return "c_" + string(rune('0'+h.ids)) }}
	return h
}

// turn runs one turn the way the HTTP handler will and returns its events.
func (h *harness) turn(t *testing.T, ctx context.Context, caller auth.Caller, req TurnRequest) []ev {
	t.Helper()
	buf := &syncBuf{}
	w := sse.New(buf, nil, h.clock)
	_ = h.agent.Turn(ctx, caller, req, w)
	w.Close()
	return events(t, buf.String())
}

func answer(parts ...string) []model.Event {
	out := []model.Event{{Kind: model.Reasoning, Text: "Checking the passages."}}
	for _, p := range parts {
		out = append(out, model.Event{Kind: model.Text, Text: p})
	}
	return out
}

func TestTurnStreamsRetrievalThoughtsTokensAndCitations(t *testing.T) {
	m := &fake.Model{Name: "groq", Events: answer("A is Go [2", "] and B [1].", " Again [2]."),
		Result: model.Result{Model: "gpt-oss-120b", TokensIn: 300, TokensOut: 12}}
	h := newHarness(m, &fakeRetriever{chunks: twoChunks})
	evs := h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "What has he built?"})

	want := "step_start,step_end,thought,step_start,thought,token,token,citation,citation,token,step_end,done"
	if got := names(evs); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
	if evs[0].data["kind"] != "retrieval" || evs[3].data["kind"] != "inference" {
		t.Errorf("step kinds = %v, %v", evs[0].data["kind"], evs[3].data["kind"])
	}
	if d := evs[1].data["detail"].(map[string]any); d["hits"] != float64(2) || d["top_score"] != 0.9 {
		t.Errorf("retrieval detail = %v", d)
	}
	if evs[2].data["source"] != "agent" || evs[4].data["source"] != "model" {
		t.Errorf("thought sources = %v, %v", evs[2].data["source"], evs[4].data["source"])
	}
	// Each [n] cited once, after the token that completes it.
	if evs[7].data["n"] != float64(2) || evs[7].data["chunk_id"] != "proj-b#summary" ||
		evs[8].data["n"] != float64(1) || evs[8].data["url"] != "/a" {
		t.Errorf("citations = %v, %v", evs[7].data, evs[8].data)
	}
	end := evs[10].data
	if end["status"] != "ok" {
		t.Errorf("inference status = %v", end["status"])
	}
	if d := end["detail"].(map[string]any); d["provider"] != "groq" || d["model"] != "gpt-oss-120b" || d["tokens_out"] != float64(12) {
		t.Errorf("inference detail = %v", d)
	}
}

func TestPromptNumbersContextAndEndsWithTheQuestion(t *testing.T) {
	m := &fake.Model{Name: "groq", Events: answer("ok")}
	h := newHarness(m, &fakeRetriever{chunks: twoChunks})
	h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "What has he built?"})
	if len(m.Requests()) != 1 {
		t.Fatalf("model called %d times, want 1", len(m.Requests()))
	}
	req := m.Requests()[0]
	if !strings.Contains(req.System, "[n]") {
		t.Errorf("system prompt doesn't explain [n] citations:\n%s", req.System)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != model.User || !strings.Contains(last.Content, "What has he built?") {
		t.Fatalf("last message = %+v", last)
	}
	for _, want := range []string{"[1] Project A", "A is a Go service.", "[2] Project B", "B is a C++ engine."} {
		if !strings.Contains(last.Content, want) {
			t.Errorf("prompt lacks %q:\n%s", want, last.Content)
		}
	}
	if len(req.Tools) != 0 {
		t.Errorf("tools offered before step 3.6: %v", req.Tools)
	}
}

func TestRetrievalUsesTheCallersMask(t *testing.T) {
	r := &fakeRetriever{chunks: twoChunks}
	h := newHarness(&fake.Model{Name: "groq", Events: answer("ok")}, r)
	h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "q"})
	h.turn(t, context.Background(), auth.Caller{ID: "u", Groups: []string{auth.InviteeGroup}}, TurnRequest{Message: "q"})
	if len(r.masks) != 2 || r.masks[0] != auth.Public || r.masks[1] != auth.Public|auth.Invitee {
		t.Errorf("masks = %v", r.masks)
	}
}

func TestHistoryBelongsToTheConversationAndItsOwner(t *testing.T) {
	m := &fake.Model{Name: "groq", Events: answer("first answer")}
	h := newHarness(m, &fakeRetriever{chunks: twoChunks})
	alice := auth.Caller{ID: "alice", Groups: []string{auth.InviteeGroup}}
	evs := h.turn(t, context.Background(), alice, TurnRequest{Message: "first question"})
	conv, _ := evs[len(evs)-1].data["conversation_id"].(string)
	if conv == "" {
		t.Fatalf("no conversation id in %s", names(evs))
	}

	h.turn(t, context.Background(), alice, TurnRequest{ConversationID: conv, Message: "follow-up"})
	if len(m.Requests()) != 2 {
		t.Fatalf("model called %d times, want 2", len(m.Requests()))
	}
	second := m.Requests()[1].Messages
	if len(second) != 3 || second[0].Content != "first question" || second[1].Content != "first answer" {
		t.Errorf("follow-up history = %+v", second)
	}

	// Someone else presenting alice's id gets a new conversation, not her history.
	evs = h.turn(t, context.Background(), auth.Caller{}, TurnRequest{ConversationID: conv, Message: "steal"})
	if got := evs[len(evs)-1].data["conversation_id"]; got == conv {
		t.Error("another caller continued alice's conversation")
	}
	if third := m.Requests()[2].Messages; len(third) != 1 {
		t.Errorf("another caller's prompt had %d messages, want only the question", len(third))
	}
	// An id the server never issued is not adopted either.
	evs = h.turn(t, context.Background(), alice, TurnRequest{ConversationID: "chosen-by-client", Message: "q"})
	if got := evs[len(evs)-1].data["conversation_id"]; got == "chosen-by-client" {
		t.Error("server adopted a client-chosen conversation id")
	}
}

func TestDefaultConversationIDsAreRandom(t *testing.T) {
	a := &Agent{}
	x, y := a.newID(), a.newID()
	if len(x) < 32 || x == y {
		t.Errorf("ids %q, %q: want 128 random bits, different each time", x, y)
	}
}

func TestModelFailureEndsWithARetryableError(t *testing.T) {
	m := &fake.Model{Name: "groq", ErrBefore: &model.HTTPError{Status: 503}}
	h := newHarness(m, &fakeRetriever{chunks: twoChunks})
	evs := h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "q"})
	if got := names(evs); got != "step_start,step_end,thought,step_start,step_end,error" {
		t.Fatalf("events = %s", got)
	}
	if evs[4].data["status"] != "error" || evs[5].data["code"] != "model_unavailable" || evs[5].data["retryable"] != true {
		t.Errorf("end = %v, error = %v", evs[4].data, evs[5].data)
	}
}

func TestRetrievalFailureEndsWithAnError(t *testing.T) {
	h := newHarness(&fake.Model{Name: "groq", Events: answer("x")}, &fakeRetriever{err: errors.New("index gone")})
	evs := h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "q"})
	if got := names(evs); got != "step_start,step_end,error" || evs[2].data["code"] != "retrieval_failed" {
		t.Errorf("events = %s, last = %v", got, evs[len(evs)-1].data)
	}
}

func TestFallbackIsShownOnTheInferenceStep(t *testing.T) {
	m := &fake.Model{Name: "bedrock", Events: answer("ok"),
		Result: model.Result{Fallback: true, PrimaryErr: &model.HTTPError{Status: 429}}}
	h := newHarness(m, &fakeRetriever{chunks: twoChunks})
	evs := h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "q"})
	for _, e := range evs {
		if e.name == "step_end" && e.data["status"] == "fallback" {
			if d := e.data["detail"].(map[string]any); d["provider"] != "bedrock" {
				t.Errorf("fallback detail = %v", d)
			}
			return
		}
	}
	t.Errorf("no step_end with status fallback in %s", names(evs))
}

func TestCancelledTurnStillClosesEveryStep(t *testing.T) {
	h := newHarness(&fake.Model{Name: "groq", Block: true}, &fakeRetriever{chunks: twoChunks})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool { return len(h.model.Requests()) > 0 })
		cancel()
	}()
	evs := h.turn(t, ctx, auth.Caller{}, TurnRequest{Message: "q"})
	starts, ends := strings.Count(names(evs), "step_start"), strings.Count(names(evs), "step_end")
	if starts != 2 || ends != 2 || evs[len(evs)-1].name != "error" {
		t.Errorf("events = %s", names(evs))
	}
}

func TestTurnDeadline(t *testing.T) {
	h := newHarness(&fake.Model{Name: "groq", Block: true}, &fakeRetriever{chunks: twoChunks})
	done := make(chan []ev)
	go func() { done <- h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "q"}) }()
	waitFor(t, func() bool { return len(h.model.Requests()) > 0 })
	h.clock.Advance(TurnDeadline)
	select {
	case evs := <-done:
		if last := evs[len(evs)-1]; last.name != "error" || last.data["code"] != "timeout" {
			t.Errorf("events = %s, last = %v", names(evs), last.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("turn didn't end at the deadline")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Error("condition not met within 2 s")
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStoreKeepsOnlyRecentHistoryForThePrompt(t *testing.T) {
	s := NewMemoryStore()
	for i := 0; i < HistoryMessages+4; i++ {
		s.Append(context.Background(), "c", "u", model.Message{Role: model.User, Content: string(rune('a' + i))})
	}
	got, ok, _ := s.Load(context.Background(), "c", "u")
	if !ok || len(got) != HistoryMessages || got[0].Content != "e" {
		t.Errorf("loaded %d messages starting %q, want the last %d", len(got), got[0].Content, HistoryMessages)
	}
}

func TestNarrationGroupsPassagesFromTheSameDocument(t *testing.T) {
	got := narrate([]Chunk{{Title: "Project Alpha"}, {Title: "Project Alpha"}, {Title: "A long story"}, {Title: "Project Alpha"}})
	if want := "Found 4 passages: Project Alpha (3 passages), A long story."; got != want {
		t.Errorf("narrate = %q, want %q", got, want)
	}
	if got := narrate([]Chunk{{Title: "Only"}}); got != "Found 1 passage: Only." {
		t.Errorf("narrate one = %q", got)
	}
}
