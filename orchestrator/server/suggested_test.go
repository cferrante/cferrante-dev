package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
)

type twoSuggestions struct{}

func (twoSuggestions) List(c auth.Caller) []agent.Suggestion {
	out := []agent.Suggestion{{ID: "a", Question: "Public?", Answer: "secret-free"}}
	if c.IsInvitee() {
		out = append(out, agent.Suggestion{ID: "b", Question: "Private?", Answer: "the private answer", InviteeOnly: true})
	}
	return out
}

func (twoSuggestions) Find(string, auth.Caller) (agent.Suggestion, bool) {
	return agent.Suggestion{}, false
}

func getList(t *testing.T, caller auth.Caller) (int, string, []map[string]any) {
	t.Helper()
	srv := httptest.NewServer(&SuggestedList{Suggestions: twoSuggestions{}, Auth: auth.Fixed(caller)})
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	json.NewDecoder(resp.Body).Decode(&list)
	return resp.StatusCode, resp.Header.Get("Content-Type"), list
}

func TestSuggestedListShowsQuestionsOnly(t *testing.T) {
	status, ct, list := getList(t, auth.Caller{})
	if status != 200 || ct != "application/json" || len(list) != 1 || list[0]["id"] != "a" || list[0]["question"] != "Public?" {
		t.Fatalf("visitor: %d %q %v", status, ct, list)
	}
	if _, leaked := list[0]["answer"]; leaked || len(list[0]) != 2 {
		t.Errorf("list entry has more than id and question: %v", list[0])
	}
	_, _, list = getList(t, auth.Caller{ID: "u", Groups: []string{auth.InviteeGroup}})
	if len(list) != 2 {
		t.Errorf("invitee sees %d", len(list))
	}
}

func TestChatPassesASuggestedIDWithoutAMessage(t *testing.T) {
	rec := &recorder{ended: make(chan error, 1), release: make(chan struct{})}
	close(rec.release)
	srv := httptest.NewServer(&Chat{Agent: rec, Auth: auth.Fixed{}, Clock: clock.Real{}})
	defer srv.Close()
	if resp := post(t, srv, `{"conversation_id":"c_1","suggested_id":"alpha-search"}`); resp.StatusCode != 200 || rec.req.SuggestedID != "alpha-search" {
		t.Errorf("status %d, request %+v", resp.StatusCode, rec.req)
	}
	if resp := post(t, srv, `{"suggested_id":"  "}`); resp.StatusCode != 400 {
		t.Errorf("blank suggested_id: status %d", resp.StatusCode)
	}
}
