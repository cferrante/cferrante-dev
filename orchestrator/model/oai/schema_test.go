package oai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

const verdictSchema = `{"type":"object","properties":{"score":{"type":"integer"},"reasons":{"type":"array","items":{"type":"object",
"properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}}},"required":["score","reasons"],"additionalProperties":false}`

// jsonServer answers one non-streamed chat completion and records the request.
func jsonServer(t *testing.T, status int, reply string, got *map[string]any) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if got != nil {
			json.Unmarshal(raw, got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func completion(content, reasoning, finish string) string {
	raw, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content, "reasoning": reasoning}, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 300, "completion_tokens": 40, "prompt_tokens_details": map[string]any{"cached_tokens": 256}},
	})
	return string(raw)
}

func schemaRequest() model.Request {
	return model.Request{System: "Judge the answer.", Messages: []model.Message{{Role: model.User, Content: "Answer: 42"}},
		ResponseSchema: json.RawMessage(verdictSchema)}
}

// Groq's structured outputs don't stream (docs, 2026-11-03), so a schema
// request is one plain call with a strict json_schema response_format.
func TestSchemaRequestShape(t *testing.T) {
	var got map[string]any
	srv, _ := jsonServer(t, 200, completion(`{"score":4,"reasons":[]}`, "", "stop"), &got)
	req := schemaRequest()
	zero := 0.0
	req.Temperature = &zero
	if _, err := client(srv.URL).Stream(context.Background(), req, func(model.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if temp, ok := got["temperature"]; !ok || temp != float64(0) {
		t.Errorf("temperature = %v (sent: %v), want 0", temp, ok)
	}
	if got["stream"] != false {
		t.Errorf("stream = %v, want false", got["stream"])
	}
	if _, ok := got["stream_options"]; ok {
		t.Error("stream_options sent on a non-streamed call")
	}
	rf, _ := got["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["name"] != "response" || js["strict"] != true {
		t.Errorf("response_format = %v", rf)
	}
	if schema, _ := json.Marshal(js["schema"]); !strings.Contains(string(schema), `"additionalProperties":false`) {
		t.Errorf("schema not sent as given: %s", schema)
	}
	if msgs := got["messages"].([]any); msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("first message = %v, want the system prompt", msgs[0])
	}
}

func TestSchemaOutputIsOneTextEvent(t *testing.T) {
	srv, _ := jsonServer(t, 200, completion(`{"score":4,"reasons":[{"text":"cited"}]}`, "Checking the citation.", "stop"), nil)
	var events []model.Event
	res, err := client(srv.URL).Stream(context.Background(), schemaRequest(), func(e model.Event) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != model.Reasoning || events[0].Text != "Checking the citation." ||
		events[1].Kind != model.Text || events[1].Text != `{"score":4,"reasons":[{"text":"cited"}]}` {
		t.Errorf("events = %+v", events)
	}
	if res.Provider != "groq" || res.TokensIn != 300 || res.TokensOut != 40 || res.CachedTokens != 256 || res.StopReason != "stop" || res.TTFT <= 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestSchemaOutputIsValidJSONOrAnError(t *testing.T) {
	for name, reply := range map[string]string{
		"not JSON":       completion(`{"score": 4,`, "", "stop"),
		"not an object":  completion(`[4]`, "", "stop"),
		"empty":          completion(``, "", "stop"),
		"cut off":        completion(`{"score":4,"reasons":[]}`, "", "length"),
		"no choices":     `{"choices":[]}`,
		"reply not JSON": `<html>`,
	} {
		srv, _ := jsonServer(t, 200, reply, nil)
		var texts int
		_, err := client(srv.URL).Stream(context.Background(), schemaRequest(), func(e model.Event) error {
			if e.Kind == model.Text {
				texts++
			}
			return nil
		})
		if err == nil || texts != 0 {
			t.Errorf("%s: err = %v after %d text events, want an error and no text", name, err, texts)
		}
	}
	srv, _ := jsonServer(t, 400, `{"error":{"message":"Generated JSON does not match the expected schema."}}`, nil)
	_, err := client(srv.URL).Stream(context.Background(), schemaRequest(), func(model.Event) error { return nil })
	var he *model.HTTPError
	if !errors.As(err, &he) || he.Status != 400 {
		t.Errorf("HTTP 400: err = %v, want a model.HTTPError", err)
	}
}

// Refused before any call: Groq doesn't combine tools with structured output,
// and strict mode rejects schemas that leave properties optional or open.
func TestSchemaRequestsRefusedBeforeSending(t *testing.T) {
	loose := func(schema string) model.Request {
		r := schemaRequest()
		r.ResponseSchema = json.RawMessage(schema)
		return r
	}
	withTools := schemaRequest()
	withTools.Tools = []model.ToolSpec{{Name: "t", Parameters: json.RawMessage(`{"type":"object"}`)}}
	for name, tc := range map[string]struct {
		req  model.Request
		want string
	}{
		"tools":                {withTools, "tools"},
		"not JSON":             {loose(`{`), "schema"},
		"open object":          {loose(`{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`), "additionalProperties"},
		"optional property":    {loose(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a"],"additionalProperties":false}`), `"b"`},
		"nested open object":   {loose(`{"type":"object","properties":{"a":{"type":"object","properties":{}}},"required":["a"],"additionalProperties":false}`), "additionalProperties"},
		"open object in items": {loose(`{"type":"object","properties":{"a":{"type":"array","items":{"type":"object","properties":{"x":{"type":"string"}}}}},"required":["a"],"additionalProperties":false}`), "additionalProperties"},
	} {
		srv, calls := jsonServer(t, 200, completion(`{}`, "", "stop"), nil)
		_, err := client(srv.URL).Stream(context.Background(), tc.req, func(model.Event) error { return nil })
		if err == nil || !strings.Contains(err.Error(), tc.want) || *calls != 0 {
			t.Errorf("%s: err = %v after %d calls, want an error mentioning %q and no call", name, err, *calls, tc.want)
		}
	}
}
