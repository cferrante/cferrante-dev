// Package groq is a minimal streaming client for Groq's OpenAI-compatible
// chat completions API, written for spike 1.5. It reports gpt-oss reasoning
// (delta.reasoning), answer text (delta.content) and tool calls as separate
// events, which langchaingo's OpenAI provider cannot: it only reads
// DeepSeek's delta.reasoning_content and drops Groq's field.
package groq

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
)

// DefaultBaseURL is Groq's OpenAI-compatible endpoint.
const DefaultBaseURL = "https://api.groq.com/openai/v1"

// Message is one chat message in OpenAI's wire format.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall names the function and carries its JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool declares a function the model may call.
type Tool struct {
	Type     string      `json:"type"` // always "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef describes a callable function; Parameters is a JSON Schema.
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Request is one model turn.
type Request struct {
	Model           string
	Messages        []Message
	Tools           []Tool
	ReasoningEffort string // "low" | "medium" | "high" | "" (provider default)
	MaxTokens       int    // 0 = provider default
}

// EventKind says what an Event carries.
type EventKind int

// Event kinds, matching design §4's model.Event.
const (
	Reasoning EventKind = iota + 1
	Text
	ToolCallEvent
)

func (k EventKind) String() string {
	switch k {
	case Reasoning:
		return "reasoning"
	case Text:
		return "text"
	case ToolCallEvent:
		return "tool_call"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Event is one piece of a streamed turn.
type Event struct {
	Kind     EventKind
	Text     string
	ToolCall *ToolCall
}

// Result summarises a finished turn. Timings follow docs/benchmarks.md.
type Result struct {
	// TTFT is request sent → first reasoning or text token received.
	TTFT time.Duration
	// Generation is first token received → last token received.
	Generation                                         time.Duration
	TokensIn, TokensOut, ReasoningTokens, CachedTokens int
	StopReason                                         string
	Reasoning, Text                                    string
	ToolCalls                                          []ToolCall
}

// TokensPerSecond is output tokens ÷ (last token time − first token time);
// 0 when the turn produced fewer than two token arrivals.
func (r Result) TokensPerSecond() float64 {
	if r.Generation <= 0 {
		return 0
	}
	return float64(r.TokensOut) / r.Generation.Seconds()
}

// StatusError is a non-200 response, kept typed so a router can tell a
// rate limit (429) or server error (5xx) from other failures.
type StatusError struct {
	Code int
	Body string // truncated
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("groq: HTTP %d: %s", e.Code, e.Body)
}

// ErrTruncated means the stream ended without its [DONE] marker.
var ErrTruncated = errors.New("groq: stream ended before [DONE]")

// Client calls Groq. The zero value is not usable: set APIKey.
type Client struct {
	BaseURL string       // default DefaultBaseURL
	APIKey  string       // never logged
	HTTP    *http.Client // default http.DefaultClient
}

// Stream runs one turn, calling onEvent for each reasoning chunk, text chunk
// and completed tool call as it arrives. Returning an error from onEvent
// cancels the stream.
func (c *Client) Stream(ctx context.Context, req Request, onEvent func(Event) error) (Result, error) {
	body, err := json.Marshal(wireRequest{
		Model:           req.Model,
		Messages:        req.Messages,
		Tools:           req.Tools,
		Stream:          true,
		StreamOptions:   streamOptions{IncludeUsage: true},
		ReasoningEffort: req.ReasoningEffort,
		MaxTokens:       req.MaxTokens,
	})
	if err != nil {
		return Result{}, err
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}

	start := time.Now()
	resp, err := hc.Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Result{}, &StatusError{Code: resp.StatusCode, Body: string(msg)}
	}

	var (
		res         Result
		first, last time.Time
		calls       []*ToolCall // by stream index
	)
	// A token arrived: time it, keep it, pass it on.
	token := func(kind EventKind, s string, into *string) error {
		now := time.Now()
		if first.IsZero() {
			first = now
		}
		last = now
		*into += s
		return onEvent(Event{Kind: kind, Text: s})
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
			return res, fmt.Errorf("groq: bad chunk: %w", err)
		}
		if ch.Error != nil {
			return res, fmt.Errorf("groq: stream error: %s", ch.Error.Message)
		}
		// Groq reports usage in x_groq; with include_usage it also sends
		// OpenAI's final usage chunk. Either fills the same fields.
		for _, u := range []*wireUsage{ch.Usage, ch.XGroq.Usage} {
			if u != nil {
				res.TokensIn, res.TokensOut = u.PromptTokens, u.CompletionTokens
				res.ReasoningTokens = u.CompletionDetails.ReasoningTokens
				if u.PromptDetails.CachedTokens > 0 {
					res.CachedTokens = u.PromptDetails.CachedTokens
				}
			}
		}
		for _, choice := range ch.Choices {
			d := choice.Delta
			if d.Reasoning != "" {
				if err := token(Reasoning, d.Reasoning, &res.Reasoning); err != nil {
					return res, err
				}
			}
			if d.Content != "" {
				if err := token(Text, d.Content, &res.Text); err != nil {
					return res, err
				}
			}
			for _, tc := range d.ToolCalls {
				for len(calls) <= tc.Index {
					calls = append(calls, &ToolCall{})
				}
				call := calls[tc.Index]
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Type != "" {
					call.Type = tc.Type
				}
				call.Function.Name += tc.Function.Name
				call.Function.Arguments += tc.Function.Arguments
			}
			if choice.FinishReason != "" {
				res.StopReason = choice.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("groq: reading stream: %w", err)
	}
	if !done {
		return res, ErrTruncated
	}

	// Tool call arguments arrive in pieces; report each call once, complete.
	for _, call := range calls {
		res.ToolCalls = append(res.ToolCalls, *call)
		if err := onEvent(Event{Kind: ToolCallEvent, ToolCall: call}); err != nil {
			return res, err
		}
	}
	if !first.IsZero() {
		res.TTFT = first.Sub(start)
		res.Generation = last.Sub(first)
	}
	return res, nil
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireRequest struct {
	Model           string        `json:"model"`
	Messages        []Message     `json:"messages"`
	Tools           []Tool        `json:"tools,omitempty"`
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
	CompletionDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"` // Groq's field for gpt-oss
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
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
