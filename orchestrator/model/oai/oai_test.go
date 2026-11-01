package oai

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

	"github.com/cferrante/cferrante-dev/orchestrator/model"
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

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	t.Cleanup(srv.Close)
	return srv
}

func client(url string) *Client {
	return &Client{Provider: "groq", BaseURL: url, APIKey: "test", Model: "openai/gpt-oss-120b"}
}

func collect(t *testing.T, c *Client, req model.Request) ([]model.Event, model.Result) {
	t.Helper()
	var events []model.Event
	res, err := c.Stream(context.Background(), req, func(e model.Event) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return events, res
}

func TestStreamSeparatesReasoningFromText(t *testing.T) {
	events, res := collect(t, client(replay(t, "testdata/reasoning_text.sse", 0).URL), model.Request{})
	want := []model.Event{
		{Kind: model.Reasoning, Text: "The user asks 2+2. "},
		{Kind: model.Reasoning, Text: "That is 4."},
		{Kind: model.Text, Text: "2 + 2"},
		{Kind: model.Text, Text: " = 4."},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events:\n got %+v\nwant %+v", events, want)
	}
	if res.Provider != "groq" || res.Model != "openai/gpt-oss-120b" || res.StopReason != "stop" ||
		res.TokensIn != 80 || res.TokensOut != 14 || res.CachedTokens != 64 {
		t.Errorf("result = %+v", res)
	}
}

// Plan step 3.5: a real Ollama stream (qwen3:4b, reasoning_effort low,
// recorded 2026-11-01) uses the same delta.reasoning field as Groq.
func TestOllamaRecordingSeparatesReasoningFromText(t *testing.T) {
	c := &Client{Provider: "ollama", BaseURL: replay(t, "testdata/ollama_qwen3_low.sse", 0).URL, Model: "qwen3:4b"}
	events, res := collect(t, c, model.Request{})
	var reasoning, text strings.Builder
	count := map[model.EventKind]int{}
	lastKind := model.EventKind(0)
	for _, e := range events {
		count[e.Kind]++
		if e.Kind == model.Reasoning && lastKind == model.Text {
			t.Error("reasoning arrived after answer text")
		}
		lastKind = e.Kind
		if e.Kind == model.Reasoning {
			reasoning.WriteString(e.Text)
		} else {
			text.WriteString(e.Text)
		}
	}
	if count[model.Reasoning] != 130 || count[model.Text] != 11 {
		t.Errorf("event counts = %v, want 130 reasoning and 11 text", count)
	}
	if got := strings.TrimSpace(text.String()); got != "17 plus 25 equals 42." {
		t.Errorf("answer = %q", got)
	}
	if reasoning.Len() == 0 || strings.Contains(text.String(), reasoning.String()) {
		t.Error("reasoning missing or mixed into the answer")
	}
	if res.Provider != "ollama" || res.StopReason != "stop" {
		t.Errorf("result = %+v", res)
	}
}

func TestStreamAssemblesToolCall(t *testing.T) {
	events, _ := collect(t, client(replay(t, "testdata/tool_call.sse", 0).URL), model.Request{})
	want := []model.Event{
		{Kind: model.Reasoning, Text: "I should look the project up."},
		{Kind: model.ToolCallEvent, ToolCall: &model.ToolCall{ID: "call_abc", Name: "lookup_project",
			Arguments: json.RawMessage(`{"name":"cferrante-dev"}`)}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events:\n got %+v\nwant %+v", events, want)
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
	c := &Client{Provider: "groq", BaseURL: srv.URL, APIKey: "k", Model: "m", ReasoningEffort: "low"}
	_, err := c.Stream(context.Background(), model.Request{
		System: "Be brief.",
		Messages: []model.Message{
			{Role: model.User, Content: "hi"},
			{Role: model.Assistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "f", Arguments: json.RawMessage(`{"a":1}`)}}},
			{Role: model.ToolRole, ToolCallID: "c1", Content: `{"ok":true}`},
		},
		Tools:     []model.ToolSpec{{Name: "f", Description: "does f", Parameters: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens: 500,
	}, func(model.Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k" {
		t.Errorf("Authorization = %q", auth)
	}
	if got["stream"] != true || got["model"] != "m" || got["reasoning_effort"] != "low" || got["max_completion_tokens"] != float64(500) {
		t.Errorf("body = %v", got)
	}
	if so, _ := got["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Errorf("stream_options = %v", got["stream_options"])
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("messages = %v, want system + 3", got["messages"])
	}
	wire, _ := json.Marshal(msgs)
	for _, want := range []string{`{"content":"Be brief.","role":"system"}`, `"tool_call_id":"c1"`,
		`"function":{"arguments":"{\"a\":1}","name":"f"}`, `"role":"tool"`} {
		if !strings.Contains(string(wire), want) {
			t.Errorf("messages lack %s:\n%s", want, wire)
		}
	}
	tools, _ := got["tools"].([]any)
	if wire, _ := json.Marshal(tools); len(tools) != 1 || !strings.Contains(string(wire), `"name":"f"`) || !strings.Contains(string(wire), `"type":"function"`) {
		t.Errorf("tools = %s", wire)
	}
}

func TestRequestEffortOverridesTheDefaultAndNoKeyMeansNoHeader(t *testing.T) {
	var got map[string]any
	var hasAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasAuth = r.Header["Authorization"]
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := &Client{Provider: "ollama", BaseURL: srv.URL, Model: "qwen3:4b", ReasoningEffort: "low"}
	if _, err := c.Stream(context.Background(), model.Request{ReasoningEffort: "high"}, func(model.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got["reasoning_effort"] != "high" || hasAuth {
		t.Errorf("reasoning_effort %v, Authorization sent %v", got["reasoning_effort"], hasAuth)
	}
}

func TestResponseSchemaIsRefusedUntilSupported(t *testing.T) {
	c := client("http://unused")
	_, err := c.Stream(context.Background(), model.Request{ResponseSchema: json.RawMessage(`{}`)}, func(model.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "ResponseSchema") {
		t.Errorf("err = %v, want a clear not-supported error", err)
	}
}

func TestStreamTimesFirstToken(t *testing.T) {
	const gap = 60 * time.Millisecond
	_, res := collect(t, client(replay(t, "testdata/reasoning_text.sse", gap).URL), model.Request{})
	// The first token is the 2nd event (the 1st is an empty role chunk): ~2 gaps.
	if res.TTFT < 2*gap || res.TTFT > 2*gap+gap*3/2 {
		t.Errorf("TTFT = %v, want about %v", res.TTFT, 2*gap)
	}
}

func TestStreamReturnsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limit"}}`)
	}))
	defer srv.Close()
	_, err := client(srv.URL).Stream(context.Background(), model.Request{}, func(model.Event) error { return nil })
	var he *model.HTTPError
	if !errors.As(err, &he) || he.Status != 429 || !strings.Contains(he.Body, "rate limit") {
		t.Fatalf("err = %v, want model.HTTPError 429 (the router falls back on it)", err)
	}
}

func TestStreamFailsWhenTruncated(t *testing.T) {
	srv := serve(t, `data: {"choices":[{"index":0,"delta":{"content":"half"}}]}`+"\n\n")
	if _, err := client(srv.URL).Stream(context.Background(), model.Request{}, func(model.Event) error { return nil }); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestStreamStopsWhenCallbackFails(t *testing.T) {
	stop := errors.New("stop")
	n := 0
	_, err := client(replay(t, "testdata/reasoning_text.sse", 0).URL).Stream(context.Background(), model.Request{},
		func(model.Event) error { n++; return stop })
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("err = %v after %d events, want stop after 1", err, n)
	}
}

// A real Groq stream from spike 1.5: reasoning in delta.reasoning, the tool
// call whole in one chunk, usage twice (x_groq and the final usage chunk).
func TestStreamParsesLiveGroqRecording(t *testing.T) {
	events, res := collect(t, client(replay(t, "testdata/live_tool_call.sse", 0).URL), model.Request{})
	count := map[model.EventKind]int{}
	var call *model.ToolCall
	for _, e := range events {
		count[e.Kind]++
		if e.ToolCall != nil {
			call = e.ToolCall
		}
	}
	if count[model.Reasoning] != 30 || count[model.Text] != 0 || count[model.ToolCallEvent] != 1 {
		t.Errorf("event counts = %v", count)
	}
	if call == nil || call.Name != "lookup_project" || string(call.Arguments) != `{"name":"cferrante-dev"}` || call.ID == "" {
		t.Errorf("tool call = %+v", call)
	}
	if res.StopReason != "tool_calls" || res.TokensIn != 185 || res.TokensOut != 61 {
		t.Errorf("result = %+v", res)
	}
}

func TestStreamMergesUsageCopies(t *testing.T) {
	srv := serve(t, `data: {"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}],"x_groq":{"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}}}`+"\n\n"+
		`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`+"\n\ndata: [DONE]\n\n")
	_, res := collect(t, client(srv.URL), model.Request{})
	if res.TokensIn != 10 || res.TokensOut != 5 || res.CachedTokens != 4 {
		t.Errorf("usage = in %d out %d cached %d, want 10 5 4", res.TokensIn, res.TokensOut, res.CachedTokens)
	}
}

func TestStreamKeepsRepeatedToolNameOnce(t *testing.T) {
	srv := serve(t, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"lookup_project","arguments":"{\"name\":"}}]}}]}`+"\n\n"+
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup_project","arguments":"\"x\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
	events, _ := collect(t, client(srv.URL), model.Request{})
	if len(events) != 1 || events[0].ToolCall.Name != "lookup_project" || string(events[0].ToolCall.Arguments) != `{"name":"x"}` {
		t.Errorf("events = %+v", events)
	}
}
