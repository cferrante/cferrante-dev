package groq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// replay serves a recorded SSE stream, one event per write, flushing after
// each so the client sees them arrive separately.
func replay(t *testing.T, file string, gap time.Duration) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range strings.SplitAfter(string(raw), "\n\n") {
			if ev == "" {
				continue
			}
			if gap > 0 {
				time.Sleep(gap)
			}
			io.WriteString(w, ev)
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, c *Client, req Request) ([]Event, Result) {
	t.Helper()
	var events []Event
	res, err := c.Stream(context.Background(), req, func(e Event) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return events, res
}

// The 1.5 test: reasoning and answer text arrive as separate events.
func TestStreamSeparatesReasoningFromText(t *testing.T) {
	srv := replay(t, "testdata/reasoning_text.sse", 0)
	c := &Client{BaseURL: srv.URL, APIKey: "test"}
	events, res := collect(t, c, Request{Model: "openai/gpt-oss-120b"})

	want := []Event{
		{Kind: Reasoning, Text: "The user asks 2+2. "},
		{Kind: Reasoning, Text: "That is 4."},
		{Kind: Text, Text: "2 + 2"},
		{Kind: Text, Text: " = 4."},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events:\n got %+v\nwant %+v", events, want)
	}
	if res.Reasoning != "The user asks 2+2. That is 4." || res.Text != "2 + 2 = 4." {
		t.Errorf("result reasoning %q text %q", res.Reasoning, res.Text)
	}
	if res.StopReason != "stop" || res.TokensIn != 80 || res.TokensOut != 14 ||
		res.ReasoningTokens != 9 || res.CachedTokens != 64 {
		t.Errorf("result usage/stop = %+v", res)
	}
}

func TestStreamAssemblesToolCall(t *testing.T) {
	srv := replay(t, "testdata/tool_call.sse", 0)
	c := &Client{BaseURL: srv.URL, APIKey: "test"}
	events, res := collect(t, c, Request{Model: "m"})

	call := ToolCall{ID: "call_abc", Type: "function",
		Function: FunctionCall{Name: "lookup_project", Arguments: `{"name":"cferrante-dev"}`}}
	want := []Event{
		{Kind: Reasoning, Text: "I should look the project up."},
		{Kind: ToolCallEvent, ToolCall: &call},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events:\n got %+v\nwant %+v", events, want)
	}
	if res.StopReason != "tool_calls" || !reflect.DeepEqual(res.ToolCalls, []ToolCall{call}) {
		t.Errorf("result = %+v", res)
	}
}

func TestStreamSendsRequest(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k"}
	_, err := c.Stream(context.Background(), Request{
		Model: "m", Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:           []Tool{{Type: "function", Function: FunctionDef{Name: "f", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		ReasoningEffort: "low",
	}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k" {
		t.Errorf("Authorization = %q", auth)
	}
	if got["stream"] != true || got["model"] != "m" || got["reasoning_effort"] != "low" {
		t.Errorf("body = %v", got)
	}
	if so, _ := got["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage", got["stream_options"])
	}
	if tools, _ := got["tools"].([]any); len(tools) != 1 {
		t.Errorf("tools = %v", got["tools"])
	}
}

// Timings must come from arrival times, not from parsing a buffered body.
func TestStreamTimesArrivals(t *testing.T) {
	const gap = 60 * time.Millisecond
	srv := replay(t, "testdata/reasoning_text.sse", gap)
	c := &Client{BaseURL: srv.URL, APIKey: "test"}
	_, res := collect(t, c, Request{Model: "m"})

	// First token is the 2nd event (the 1st is an empty role chunk): ~2 gaps.
	if res.TTFT < 2*gap || res.TTFT > 2*gap+gap*3/2 {
		t.Errorf("TTFT = %v, want about %v", res.TTFT, 2*gap)
	}
	// Four token events: three gaps between first and last.
	if res.Generation < 3*gap*8/10 || res.Generation > 3*gap+gap*3/2 {
		t.Errorf("Generation = %v, want about %v", res.Generation, 3*gap)
	}
	want := float64(res.TokensOut) / res.Generation.Seconds()
	if tps := res.TokensPerSecond(); tps != want || tps <= 0 {
		t.Errorf("TokensPerSecond = %v, want %v", tps, want)
	}
}

func TestStreamReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limit"}}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "test"}
	_, err := c.Stream(context.Background(), Request{Model: "m"}, func(Event) error { return nil })
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 429 || !strings.Contains(se.Body, "rate limit") {
		t.Fatalf("err = %v, want StatusError 429", err)
	}
}

func TestStreamFailsWhenTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"half"}}]}`+"\n\n")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "test"}
	_, err := c.Stream(context.Background(), Request{Model: "m"}, func(Event) error { return nil })
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestStreamStopsWhenCallbackFails(t *testing.T) {
	srv := replay(t, "testdata/reasoning_text.sse", 0)
	c := &Client{BaseURL: srv.URL, APIKey: "test"}
	stop := errors.New("stop")
	n := 0
	_, err := c.Stream(context.Background(), Request{Model: "m"}, func(Event) error { n++; return stop })
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("err = %v after %d events, want stop after 1", err, n)
	}
}

// A real Groq stream (turn 1 of a live run, 2026-11-01): reasoning arrives in
// delta.reasoning with "channel":"analysis", the tool call arrives whole in
// one chunk, and usage comes twice (x_groq and the final usage chunk).
func TestStreamParsesLiveRecording(t *testing.T) {
	srv := replay(t, "testdata/live_tool_call.sse", 0)
	c := &Client{BaseURL: srv.URL, APIKey: "test"}
	events, res := collect(t, c, Request{Model: "openai/gpt-oss-120b"})

	count := map[EventKind]int{}
	for _, e := range events {
		count[e.Kind]++
	}
	if count[Reasoning] != 30 || count[Text] != 0 || count[ToolCallEvent] != 1 {
		t.Errorf("event counts = %v, want 30 reasoning, 0 text, 1 tool call", count)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Function.Name != "lookup_project" ||
		res.ToolCalls[0].Function.Arguments != `{"name":"cferrante-dev"}` || res.ToolCalls[0].ID == "" {
		t.Errorf("tool calls = %+v", res.ToolCalls)
	}
	if res.StopReason != "tool_calls" || res.TokensIn != 185 || res.TokensOut != 61 || res.ReasoningTokens != 31 {
		t.Errorf("result = %+v", res)
	}
}
