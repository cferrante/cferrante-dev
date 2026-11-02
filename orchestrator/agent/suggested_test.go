package agent

import (
	"context"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/model/fake"
)

type fakeSuggestions []Suggestion

func (f fakeSuggestions) List(c auth.Caller) []Suggestion { return f }

func (f fakeSuggestions) Find(id string, c auth.Caller) (Suggestion, bool) {
	for _, s := range f {
		if s.ID == id && (!s.InviteeOnly || c.IsInvitee()) {
			return s, true
		}
	}
	return Suggestion{}, false
}

var stored = fakeSuggestions{
	{ID: "alpha-search", Question: "How does Alpha search?", Answer: "It fuses keyword and vector search [1].",
		Chunks: []Chunk{{ID: "proj-alpha#how-it-works", Title: "Project Alpha", URL: "/projects/alpha#how-it-works"}}},
	{ID: "rates", Question: "Rates?", Answer: "Negotiable [1].", InviteeOnly: true,
		Chunks: []Chunk{{ID: "private-notes#rates", Title: "Private notes", URL: "/notes#rates"}}},
}

type countingRetriever struct{ calls int }

func (r *countingRetriever) Retrieve(context.Context, string, uint32, int) ([]Chunk, error) {
	r.calls++
	return nil, nil
}

func TestSuggestedAnswerStreamsFromStorageWithoutAModel(t *testing.T) {
	m := &fake.Model{Name: "groq", Events: answer("should not be used")}
	r := &countingRetriever{}
	h := newHarness(m, &fakeRetriever{})
	h.agent.Retriever = r
	h.agent.Suggestions = stored
	evs := h.turn(t, context.Background(), auth.Caller{}, TurnRequest{SuggestedID: "alpha-search"})

	if got := names(evs); got != "step_start,step_end,token,citation,done" {
		t.Fatalf("events = %s", got)
	}
	if evs[0].data["kind"] != "cache" || evs[1].data["status"] != "ok" || evs[1].data["detail"].(map[string]any)["hit"] != true {
		t.Errorf("cache step = %v / %v", evs[0].data, evs[1].data)
	}
	if evs[2].data["text"] != "It fuses keyword and vector search [1]." ||
		evs[3].data["chunk_id"] != "proj-alpha#how-it-works" || evs[3].data["url"] != "/projects/alpha#how-it-works" {
		t.Errorf("answer = %v, citation = %v", evs[2].data, evs[3].data)
	}
	if len(m.Requests()) != 0 || r.calls != 0 {
		t.Errorf("model called %d times, retriever %d: want none", len(m.Requests()), r.calls)
	}
	conv := evs[4].data["conversation_id"].(string)
	if hist, ok, _ := h.agent.Store.Load(context.Background(), conv, ""); !ok || len(hist) != 2 || hist[0].Content != "How does Alpha search?" {
		t.Errorf("history = %+v", hist)
	}
}

func TestSuggestedAnswerTheCallerMayNotSeeIsNotFound(t *testing.T) {
	h := newHarness(&fake.Model{Name: "groq"}, &fakeRetriever{})
	h.agent.Suggestions = stored
	for _, id := range []string{"rates", "nope"} {
		evs := h.turn(t, context.Background(), auth.Caller{}, TurnRequest{SuggestedID: id})
		if last := evs[len(evs)-1]; last.name != "error" || last.data["code"] != "suggestion_not_found" || last.data["retryable"] != false {
			t.Errorf("%s: ended with %s %v", id, last.name, last.data)
		}
	}
	// An invitee can use the invitee-only one.
	evs := h.turn(t, context.Background(), auth.Caller{ID: "u", Groups: []string{auth.InviteeGroup}}, TurnRequest{SuggestedID: "rates"})
	if evs[len(evs)-1].name != "done" {
		t.Errorf("invitee: %s", names(evs))
	}
}
