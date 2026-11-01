package server

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/fake"
	"github.com/cferrante/cferrante-dev/orchestrator/sse"
)

type oneChunk struct{}

func (oneChunk) Retrieve(context.Context, string, uint32, int) ([]agent.Chunk, error) {
	return []agent.Chunk{{ID: "proj-a#summary", Title: "Project A", URL: "/a", Text: "A is Go.", Score: 0.9}}, nil
}

func realAgent() *agent.Agent {
	return &agent.Agent{
		Model: &fake.Model{Name: "ollama", Events: []model.Event{
			{Kind: model.Reasoning, Text: "Passage 1 says Go."},
			{Kind: model.Text, Text: "A is written in Go [1]."},
		}},
		Retriever: oneChunk{}, Store: agent.NewMemoryStore(), Clock: clock.Real{},
	}
}

func post(t *testing.T, srv *httptest.Server, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func eventNames(t *testing.T, resp *http.Response) []string {
	t.Helper()
	var names []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			names = append(names, name)
		}
	}
	return names
}

// The automated version of the step 3.5 smoke test.
func TestChatStreamsATurnEndToEnd(t *testing.T) {
	srv := httptest.NewServer(&Chat{Agent: realAgent(), Auth: auth.Fixed{}, Clock: clock.Real{}})
	defer srv.Close()
	resp := post(t, srv, `{"message":"What is A written in?"}`)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" ||
		resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("status %d, headers %v", resp.StatusCode, resp.Header)
	}
	got := strings.Join(eventNames(t, resp), ",")
	want := "step_start,step_end,thought,step_start,thought,token,citation,step_end,done"
	if got != want {
		t.Errorf("events\n got %s\nwant %s", got, want)
	}
}

func TestChatRejectsBadRequests(t *testing.T) {
	srv := httptest.NewServer(&Chat{Agent: realAgent(), Auth: auth.Fixed{}, Clock: clock.Real{}})
	defer srv.Close()
	for name, tc := range map[string]struct {
		body   string
		status int
	}{
		"not JSON":      {`{"message":`, 400},
		"empty":         {`{"message":"   "}`, 400},
		"too long":      {`{"message":"` + strings.Repeat("é", MaxMessage+1) + `"}`, 400},
		"huge body":     {`{"message":"hi","pad":"` + strings.Repeat("x", 100_000) + `"}`, 400},
		"confirm early": {`{"message":"yes","confirm":{"action_id":"a1"}}`, 501},
	} {
		if resp := post(t, srv, tc.body); resp.StatusCode != tc.status {
			t.Errorf("%s: status %d, want %d", name, resp.StatusCode, tc.status)
		}
	}
	// Exactly the limit is fine (counted in characters, not bytes).
	if resp := post(t, srv, `{"message":"`+strings.Repeat("é", MaxMessage)+`"}`); resp.StatusCode != 200 {
		t.Errorf("message at the limit: status %d", resp.StatusCode)
	}
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "POST" {
		t.Errorf("GET: status %d, Allow %q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}

type denyAll struct{}

func (denyAll) Authenticate(*http.Request) (auth.Caller, error) {
	return auth.Caller{}, errors.New("bad token")
}

func TestChatRejectsUnauthenticatedCallers(t *testing.T) {
	srv := httptest.NewServer(&Chat{Agent: realAgent(), Auth: denyAll{}, Clock: clock.Real{}})
	defer srv.Close()
	if resp := post(t, srv, `{"message":"hi"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", resp.StatusCode)
	}
}

// recorder's turn waits for cancellation, or for release, which a failing
// test closes so the handler returns and the server can shut down.
type recorder struct {
	caller  auth.Caller
	req     agent.TurnRequest
	ended   chan error
	release chan struct{}
}

func (r *recorder) Turn(ctx context.Context, caller auth.Caller, req agent.TurnRequest, w *sse.Writer) error {
	r.caller, r.req = caller, req
	w.Thought("waiting", "agent")
	select {
	case <-ctx.Done():
		r.ended <- ctx.Err()
		return ctx.Err()
	case <-r.release:
		return nil
	}
}

func TestChatPassesTheCallerAndCancelsTheTurnWhenTheClientLeaves(t *testing.T) {
	rec := &recorder{ended: make(chan error, 1), release: make(chan struct{})}
	invitee := auth.Caller{ID: "u1", Groups: []string{auth.InviteeGroup}}
	srv := httptest.NewServer(&Chat{Agent: rec, Auth: auth.Fixed(invitee), Clock: clock.Real{}})
	defer srv.Close()
	released := false
	release := func() {
		if !released {
			released = true
			close(rec.release)
		}
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL,
		strings.NewReader(`{"conversation_id":"c_1","message":"hello"}`))
	// Without a flush not even the headers arrive, so Do itself gets a limit.
	type result struct {
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		done <- result{resp, err}
	}()
	var resp *http.Response
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		resp = r.resp
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("no response headers within 2 s: the stream isn't flushed")
	}
	// Read the first event while the turn is still running, proving it was
	// flushed rather than buffered until the end.
	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if line != "event: thought\n" {
			t.Errorf("first line %q", line)
		}
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("no event within 2 s: the stream isn't flushed")
	}
	cancel()
	resp.Body.Close()
	select {
	case err := <-rec.ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("turn ended with %v", err)
		}
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("the turn kept running after the client left")
	}
	if rec.caller.ID != "u1" || rec.req.ConversationID != "c_1" || rec.req.Message != "hello" {
		t.Errorf("turn got caller %+v, request %+v", rec.caller, rec.req)
	}
}
