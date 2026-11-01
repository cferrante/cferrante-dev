package roundtrip

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/groq"
)

type wireMsg struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []groq.ToolCall `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

func sse(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		fmt.Fprintf(&b, "data: %s\n\n", c)
	}
	return b.String() + "data: [DONE]\n\n"
}

func delta(field, s string) string {
	q, _ := json.Marshal(s)
	return fmt.Sprintf(`{"choices":[{"index":0,"delta":{%q:%s}}]}`, field, q)
}

// fakeGroq plays a two-turn conversation. Turn 1 replays firstTurn; turn 2
// checks the tool result came back and answers with the codename, spelled
// as given (models don't always echo it byte for byte).
func fakeGroq(t *testing.T, firstTurn, spelling string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		turn := n
		mu.Unlock()
		var body struct {
			Messages []wireMsg `json:"messages"`
			Tools    []any     `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("turn %d: bad body: %v", turn, err)
		}
		if len(body.Tools) != 1 {
			t.Errorf("turn %d: %d tools offered, want 1", turn, len(body.Tools))
		}
		switch turn {
		case 1:
			io.WriteString(w, firstTurn)
		case 2:
			m := body.Messages
			if len(m) != 4 || m[2].Role != "assistant" || len(m[2].ToolCalls) != 1 ||
				m[2].ToolCalls[0].ID != "call_abc" || m[3].Role != "tool" || m[3].ToolCallID != "call_abc" {
				t.Errorf("turn 2 messages don't carry the tool call and its result: %+v", m)
			}
			answer := "It is a personal site."
			if len(m) == 4 && strings.Contains(m[3].Content, Codename) {
				answer += " Codename " + spelling + "."
			}
			io.WriteString(w, sse(delta("reasoning", "Summarise the facts."), delta("content", answer),
				`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`))
		default:
			t.Errorf("unexpected request %d", turn)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The 1.5 test, part two: one tool call round-trips.
func TestRunRoundTripsOneToolCall(t *testing.T) {
	first, err := os.ReadFile("../groq/testdata/tool_call.sse")
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeGroq(t, string(first), Codename)
	rep, err := Run(context.Background(), &groq.Client{BaseURL: srv.URL, APIKey: "test"}, "m")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.OK || len(rep.Problems) > 0 {
		t.Errorf("OK = %v, problems %q", rep.OK, rep.Problems)
	}
	if rep.Tool != "lookup_project" || !strings.Contains(rep.Answer, Codename) {
		t.Errorf("tool %q, answer %q", rep.Tool, rep.Answer)
	}
	if rep.Turn1.ToolCalls != 1 || rep.Turn1.ReasoningEvents != 1 || rep.Turn1.StopReason != "tool_calls" {
		t.Errorf("turn 1 = %+v", rep.Turn1)
	}
	if rep.Turn2.ReasoningEvents != 1 || rep.Turn2.TextEvents != 1 || rep.Turn2.StopReason != "stop" {
		t.Errorf("turn 2 = %+v", rep.Turn2)
	}
}

// The checks must be able to fail: a model that answers without the tool.
func TestRunReportsMissingToolCall(t *testing.T) {
	srv := fakeGroq(t, sse(delta("content", "I don't know."),
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`), Codename)
	rep, err := Run(context.Background(), &groq.Client{BaseURL: srv.URL, APIKey: "test"}, "m")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.OK {
		t.Fatalf("OK = true for a turn with no tool call and no reasoning")
	}
	want := []string{"turn 1: no reasoning events", "turn 1: no tool call"}
	if strings.Join(rep.Problems, "|") != strings.Join(want, "|") {
		t.Errorf("problems = %q, want %q", rep.Problems, want)
	}
}

// Found live: gpt-oss wrote the codename with U+2011 (non-breaking hyphen)
// instead of "-". That still proves the tool result reached the model.
func TestRunAcceptsUnicodeHyphenInCodename(t *testing.T) {
	first, err := os.ReadFile("../groq/testdata/tool_call.sse")
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeGroq(t, string(first), strings.Replace(Codename, "-", "\u2011", 1))
	rep, err := Run(context.Background(), &groq.Client{BaseURL: srv.URL, APIKey: "test"}, "m")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.OK {
		t.Errorf("OK = false, problems %q, answer %q", rep.Problems, rep.Answer)
	}
}

func TestLookupProject(t *testing.T) {
	out, err := lookupProject(`{"name":"cferrante-dev"}`)
	if err != nil || !strings.Contains(out, Codename) {
		t.Errorf("lookupProject = %q, %v", out, err)
	}
	if _, err := lookupProject(`{"name":"other"}`); err == nil {
		t.Error("want an error for an unknown project")
	}
}

// Review finding: a turn cut off mid-call (finish_reason "length") must be
// reported as truncated, not as a tool with bad arguments.
func TestRunReportsTruncatedToolCall(t *testing.T) {
	srv := fakeGroq(t, sse(delta("reasoning", "Look it up."),
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"lookup_project","arguments":"{\"name\":\"cferr"}}]},"finish_reason":"length"}]}`), Codename)
	rep, err := Run(context.Background(), &groq.Client{BaseURL: srv.URL, APIKey: "test"}, "m")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{`turn 1: stopped with "length", want "tool_calls"`}
	if rep.OK || strings.Join(rep.Problems, "|") != strings.Join(want, "|") {
		t.Errorf("OK = %v, problems = %q, want %q", rep.OK, rep.Problems, want)
	}
}
