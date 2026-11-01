// Package roundtrip runs spike 1.5's measured scenario: one question that
// needs one tool call, answered in two streamed turns, with timings and a
// list of anything that went wrong.
package roundtrip

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/groq"
)

// Codename is returned only by the tool; an answer that repeats it proves
// the tool result reached the model.
const Codename = "heron-42"

// Turn is one model turn's measurements, in JSON for bench output.
type Turn struct {
	TTFTms          float64 `json:"ttft_ms"`
	GenerationMs    float64 `json:"generation_ms"`
	TokensPerSecond float64 `json:"tokens_per_s"`
	TokensIn        int     `json:"tokens_in"`
	TokensOut       int     `json:"tokens_out"`
	ReasoningTokens int     `json:"reasoning_tokens"`
	CachedTokens    int     `json:"cached_tokens"`
	ReasoningEvents int     `json:"reasoning_events"`
	TextEvents      int     `json:"text_events"`
	ToolCalls       int     `json:"tool_calls"`
	StopReason      string  `json:"stop_reason"`
}

// Report is one run. OK is false if any check failed; Problems says which.
type Report struct {
	Model    string   `json:"model"`
	Turn1    Turn     `json:"turn1"`
	Turn2    Turn     `json:"turn2"`
	Tool     string   `json:"tool"`
	Answer   string   `json:"answer"`
	OK       bool     `json:"ok"`
	Problems []string `json:"problems"`
}

// Run asks the question, executes the tool call and streams the answer.
// It returns an error only when a request fails; a model that misbehaves
// (no tool call, no reasoning) gives a Report with OK false.
func Run(ctx context.Context, c *groq.Client, model string) (Report, error) {
	rep := Report{Model: model, Problems: []string{}}
	problem := func(format string, a ...any) { rep.Problems = append(rep.Problems, fmt.Sprintf(format, a...)) }
	msgs := []groq.Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: question}}
	tools := []groq.Tool{lookupProjectTool}

	res1, t1, err := turn(ctx, c, groq.Request{Model: model, Messages: msgs, Tools: tools})
	rep.Turn1 = t1
	if err != nil {
		return rep, fmt.Errorf("turn 1: %w", err)
	}
	if t1.ReasoningEvents == 0 {
		problem("turn 1: no reasoning events")
	}
	if len(res1.ToolCalls) != 1 {
		if len(res1.ToolCalls) == 0 {
			problem("turn 1: no tool call")
		} else {
			problem("turn 1: %d tool calls, want 1", len(res1.ToolCalls))
		}
		return rep, nil
	}

	call := res1.ToolCalls[0]
	rep.Tool = call.Function.Name
	out, err := lookupProject(call.Function.Arguments)
	if call.Function.Name != lookupProjectTool.Function.Name {
		err = fmt.Errorf("unknown tool %q", call.Function.Name)
	}
	if err != nil {
		problem("tool: %v", err)
		out = `{"error":` + strconv.Quote(err.Error()) + `}`
	}
	msgs = append(msgs,
		groq.Message{Role: "assistant", Content: res1.Text, ToolCalls: res1.ToolCalls},
		groq.Message{Role: "tool", ToolCallID: call.ID, Content: out})

	res2, t2, err := turn(ctx, c, groq.Request{Model: model, Messages: msgs, Tools: tools})
	rep.Turn2 = t2
	rep.Answer = res2.Text
	if err != nil {
		return rep, fmt.Errorf("turn 2: %w", err)
	}
	if t2.ReasoningEvents == 0 {
		problem("turn 2: no reasoning events")
	}
	if t2.TextEvents == 0 {
		problem("turn 2: no text events")
	}
	if t2.ToolCalls > 0 {
		problem("turn 2: %d more tool calls, want an answer", t2.ToolCalls)
	}
	if !strings.Contains(asciiDashes(res2.Text), Codename) {
		problem("turn 2: answer doesn't mention the codename from the tool result")
	}
	rep.OK = len(rep.Problems) == 0
	return rep, nil
}

// lookupProjectTool is the one tool the model may call.
var lookupProjectTool = groq.Tool{Type: "function", Function: groq.FunctionDef{
	Name:        "lookup_project",
	Description: "Look up facts about one of the site owner's projects by name.",
	Parameters: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string",` +
		`"description":"Project name, e.g. cferrante-dev"}},"required":["name"]}`),
}}

const (
	systemPrompt = "You are a concise assistant on a personal website. Use the tools when they help."
	question     = "Use lookup_project to look up the project named cferrante-dev, then describe it " +
		"in about 200 words. Mention its codename."
)

// lookupProject is the tool: fixed facts, so runs are comparable.
func lookupProject(args string) (string, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return "", fmt.Errorf("bad arguments %q: %w", args, err)
	}
	if in.Name != "cferrante-dev" {
		return "", fmt.Errorf("no project named %q", in.Name)
	}
	facts, _ := json.Marshal(map[string]any{
		"name":     "cferrante-dev",
		"codename": Codename,
		"summary":  "A personal website with an AI receptionist that answers questions about my work.",
		"stack": []string{"Go orchestrator on AWS Lambda", "C++ retrieval engine via cgo",
			"Groq gpt-oss-120b with Bedrock fallback", "Server-Sent Events through CloudFront"},
		"goal": "Prove speed: first visible thought within 500 ms, measured and published.",
	})
	return string(facts), nil
}

// turn streams one request and summarises it.
func turn(ctx context.Context, c *groq.Client, req groq.Request) (groq.Result, Turn, error) {
	var t Turn
	res, err := c.Stream(ctx, req, func(e groq.Event) error {
		switch e.Kind {
		case groq.Reasoning:
			t.ReasoningEvents++
		case groq.Text:
			t.TextEvents++
		case groq.ToolCallEvent:
			t.ToolCalls++
		}
		return nil
	})
	if err != nil {
		return res, t, err
	}
	t.TTFTms = ms(res.TTFT)
	t.GenerationMs = ms(res.Generation)
	t.TokensPerSecond = res.TokensPerSecond()
	t.TokensIn, t.TokensOut = res.TokensIn, res.TokensOut
	t.ReasoningTokens, t.CachedTokens = res.ReasoningTokens, res.CachedTokens
	t.StopReason = res.StopReason
	return res, t, nil
}

// asciiDashes maps Unicode hyphens and dashes to "-". gpt-oss writes
// U+2011 (non-breaking hyphen) in names like heron-42, so exact matching on
// model output must normalise them first.
var asciiDashes = strings.NewReplacer(
	"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-", "\u2014", "-",
	"\u2015", "-", "\u2212", "-", "\ufe63", "-", "\uff0d", "-",
).Replace

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
