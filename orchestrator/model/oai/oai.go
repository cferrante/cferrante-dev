// Package oai is a streaming client for OpenAI-compatible chat completion
// APIs, serving both Groq and Ollama (design §4). Built on the standard
// library: the spike 1.5 client, moved here and adapted to model.Model. Both
// providers stream reasoning in delta.reasoning (Groq for gpt-oss, Ollama for
// qwen3, checked against recordings), separately from delta.content.
package oai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// Base URLs of the two providers it serves.
const (
	GroqBaseURL   = "https://api.groq.com/openai/v1"
	OllamaBaseURL = "http://localhost:11434/v1"
)

// ErrTruncated means the stream ended without its [DONE] marker.
var ErrTruncated = errors.New("oai: stream ended before [DONE]")

// Client calls one model at one provider. Safe for concurrent use.
type Client struct {
	Provider        string // reported in Result, e.g. "groq" or "ollama"
	BaseURL         string
	APIKey          string // optional (Ollama needs none); never logged
	Model           string
	ReasoningEffort string // used when a request leaves it empty
	HTTP            *http.Client
}

// Stream implements model.Model. Reasoning and text chunks are passed on as
// they arrive; a tool call's arguments arrive in pieces, so each call is
// passed on once, complete, after the stream ends.
func (c *Client) Stream(ctx context.Context, req model.Request, onEvent func(model.Event) error) (model.Result, error) {
	res := model.Result{Provider: c.Provider, Model: c.Model}
	if req.ResponseSchema != nil {
		return res, errors.New("oai: ResponseSchema is not supported yet (planned with the judge, Phase 6)")
	}
	effort := req.ReasoningEffort
	if effort == "" {
		effort = c.ReasoningEffort
	}
	body, err := json.Marshal(wireRequest{
		Model: c.Model, Messages: wireMessages(req), Tools: wireTools(req.Tools),
		Stream: true, StreamOptions: streamOptions{IncludeUsage: true},
		ReasoningEffort: effort, MaxTokens: req.MaxTokens,
	})
	if err != nil {
		return res, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}

	start := time.Now()
	resp, err := hc.Do(httpReq)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return res, &model.HTTPError{Status: resp.StatusCode, Body: string(msg)}
	}

	var calls []*model.ToolCall // by stream index
	var args []strings.Builder
	token := func(kind model.EventKind, s string) error {
		if res.TTFT == 0 {
			res.TTFT = time.Since(start)
		}
		return onEvent(model.Event{Kind: kind, Text: s})
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	done := false
	for !done && sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // blank separators, comments, other SSE fields
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var ch wireChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return res, fmt.Errorf("oai: bad chunk: %w", err)
		}
		if ch.Error != nil {
			return res, fmt.Errorf("oai: stream error: %s", ch.Error.Message)
		}
		// Groq reports usage in x_groq and, with include_usage, in a final
		// usage chunk too. A copy that omits a field must not zero it.
		for _, u := range []*wireUsage{ch.Usage, ch.XGroq.Usage} {
			if u != nil {
				setIfPositive(&res.TokensIn, u.PromptTokens)
				setIfPositive(&res.TokensOut, u.CompletionTokens)
				setIfPositive(&res.CachedTokens, u.PromptDetails.CachedTokens)
			}
		}
		for _, choice := range ch.Choices {
			d := choice.Delta
			if d.Reasoning != "" {
				if err := token(model.Reasoning, d.Reasoning); err != nil {
					return res, err
				}
			}
			if d.Content != "" {
				if err := token(model.Text, d.Content); err != nil {
					return res, err
				}
			}
			for _, tc := range d.ToolCalls {
				for len(calls) <= tc.Index {
					calls = append(calls, &model.ToolCall{})
					args = append(args, strings.Builder{})
				}
				call := calls[tc.Index]
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if call.Name == "" {
					call.Name = tc.Function.Name // some providers repeat it in every delta
				}
				args[tc.Index].WriteString(tc.Function.Arguments)
			}
			if choice.FinishReason != "" {
				res.StopReason = choice.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("oai: reading stream: %w", err)
	}
	if !done {
		return res, ErrTruncated
	}
	for i, call := range calls {
		call.Arguments = json.RawMessage(args[i].String())
		if err := onEvent(model.Event{Kind: model.ToolCallEvent, ToolCall: call}); err != nil {
			return res, err
		}
	}
	return res, nil
}

func setIfPositive(dst *int, v int) {
	if v > 0 {
		*dst = v
	}
}

func wireMessages(req model.Request) []wireMessage {
	var out []wireMessage
	if req.System != "" {
		out = append(out, wireMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		w := wireMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			w.ToolCalls = append(w.ToolCalls, wireToolCall{ID: tc.ID, Type: "function",
				Function: wireFunctionCall{Name: tc.Name, Arguments: string(tc.Arguments)}})
		}
		out = append(out, w)
	}
	return out
}

func wireTools(specs []model.ToolSpec) []wireTool {
	var out []wireTool
	for _, s := range specs {
		out = append(out, wireTool{Type: "function",
			Function: wireFunctionDef{Name: s.Name, Description: s.Description, Parameters: s.Parameters}})
	}
	return out
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireFunctionCall `json:"function"`
}

type wireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string          `json:"type"`
	Function wireFunctionDef `json:"function"`
}

type wireFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireRequest struct {
	Model           string        `json:"model"`
	Messages        []wireMessage `json:"messages"`
	Tools           []wireTool    `json:"tools,omitempty"`
	Stream          bool          `json:"stream"`
	StreamOptions   streamOptions `json:"stream_options"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	MaxTokens       int           `json:"max_completion_tokens,omitempty"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	PromptDetails    struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	XGroq struct {
		Usage *wireUsage `json:"usage"`
	} `json:"x_groq"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}
