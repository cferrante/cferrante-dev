// Package model is the provider-agnostic model interface (design §4). The
// agent loop depends only on this package, never on a provider.
package model

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Model runs one model turn.
type Model interface {
	// Stream runs one turn. onEvent receives reasoning, text and tool calls as
	// they arrive; returning an error from onEvent cancels the stream.
	Stream(ctx context.Context, req Request, onEvent func(Event) error) (Result, error)
}

// Request is one model turn's input.
type Request struct {
	System          string
	Tools           []ToolSpec // already filtered to what the caller may use (design §6)
	CacheAfterTools bool       // place a prompt-cache point after tool definitions
	Messages        []Message
	ResponseSchema  json.RawMessage // JSON Schema; non-nil = constrained JSON output
	MaxTokens       int
	ReasoningEffort string // "low" | "medium" | "high" | "" (provider default)
}

// Role is who wrote a message.
type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
	ToolRole  Role = "tool"
)

// Message is one entry in the conversation sent to the model.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // assistant messages that called tools
	ToolCallID string     // tool messages: which call this answers
}

// ToolSpec describes a tool the model may call.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema for the arguments
}

// ToolCall is a tool invocation requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// EventKind says what an Event carries.
type EventKind int

const (
	Reasoning EventKind = iota + 1
	Text
	ToolCallEvent
)

// Event is one streamed piece of a turn.
type Event struct {
	Kind     EventKind
	Text     string
	ToolCall *ToolCall
}

// Result summarises a finished turn.
type Result struct {
	Provider, Model                   string
	TTFT                              time.Duration
	TokensIn, TokensOut, CachedTokens int
	StopReason                        string
	// Fallback is true when the router answered with its fallback model;
	// PrimaryErr then says why the primary was abandoned.
	Fallback   bool
	PrimaryErr error
}

// HTTPError is a provider's non-2xx response.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("model: HTTP %d: %s", e.Status, e.Body) }
