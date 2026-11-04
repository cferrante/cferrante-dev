// Package agent runs one chat turn (design §3): retrieve, prompt, stream the
// answer with citations, and record the turn.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/sse"
	"github.com/cferrante/cferrante-dev/orchestrator/tools"
	"github.com/cferrante/cferrante-dev/orchestrator/wording"
)

// TurnDeadline bounds a whole turn (design §3).
const TurnDeadline = 60 * time.Second

// Chunk is a retrieved excerpt.
type Chunk struct {
	ID, Title, URL, Text string
	Score                float32
}

// Retriever finds the excerpts most relevant to a query, among those the
// access mask allows.
type Retriever interface {
	Retrieve(ctx context.Context, query string, mask uint32, k int) ([]Chunk, error)
}

// Suggestion is a suggested question with its stored, pre-checked answer
// (design §14). Chunks are the answer's citations: [n] refers to Chunks[n-1].
type Suggestion struct {
	ID, Question, Answer string
	Chunks               []Chunk
	InviteeOnly          bool
}

// Suggestions holds the suggested questions; both methods see only what the
// caller may.
type Suggestions interface {
	List(caller auth.Caller) []Suggestion
	Find(id string, caller auth.Caller) (Suggestion, bool)
}

// TurnRequest is the visitor's message.
type TurnRequest struct {
	ConversationID string // "" or unknown: a new conversation is started
	Message        string
	// ConfirmActionID, when set, confirms a proposed action instead of
	// asking a question (design §6).
	ConfirmActionID string
	// SuggestedID, when set, answers a suggested question from storage, with
	// no model call.
	SuggestedID string
}

// MaxToolRounds bounds how many times one turn may call tools (design §3).
const MaxToolRounds = 3

// Agent runs turns. Fields are set once and read concurrently.
type Agent struct {
	Model     model.Model
	Retriever Retriever
	Store     ConversationStore
	Clock     clock.Clock
	TopK      int           // excerpts per turn; default 5
	Deadline  time.Duration // whole-turn limit; default TurnDeadline
	// Log, when set, gets one summary line per turn (no visitor text).
	Log io.Writer
	// AssistantName is what the model is told it is; default "Context Concierge".
	AssistantName string
	// Wording is the visitor-facing retrieval wording (MESSAGES_FILE); the
	// zero value is the defaults.
	Wording wording.Wording
	Tools   tools.Registry // nil: no tools offered
	// Suggestions answers suggested questions from storage; nil: none.
	Suggestions Suggestions
	Pending     tools.PendingActions // required when Tools has side effects
	NewID       func() string        // conversation ids; default 128 random bits
}

var errDeadline = errors.New("agent: turn deadline exceeded")

// turnLog is one turn's summary for Agent.Log: timings, tokens and how it
// ended, never the visitor's text.
type turnLog struct {
	conv, outcome, model, stop                     string
	hits, rounds, tokensIn, tokensOut, answerChars int
	retrieval, inference                           time.Duration
}

func (a *Agent) writeLog(l *turnLog) {
	if a.Log == nil {
		return
	}
	fmt.Fprintf(a.Log, "turn conversation=%s outcome=%s hits=%d retrieval_ms=%d inference_ms=%d rounds=%d model=%s tokens_in=%d tokens_out=%d stop=%s answer_chars=%d\n",
		l.conv, l.outcome, l.hits, l.retrieval.Milliseconds(), l.inference.Milliseconds(), l.rounds,
		l.model, l.tokensIn, l.tokensOut, l.stop, l.answerChars)
}

// Turn runs one turn, writing every event to w. It returns when the turn is
// over; the caller still defers w.Close, which keeps the stream contract if
// Turn returns early.
func (a *Agent) Turn(ctx context.Context, caller auth.Caller, req TurnRequest, w *sse.Writer) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-a.Clock.After(a.deadline()):
			cancel(errDeadline)
		case <-stop:
		}
	}()

	// Only a conversation this caller started continues; anything else,
	// including an id the client made up, starts afresh (design §8).
	convID := req.ConversationID
	history, ok, err := a.Store.Load(ctx, convID, caller.ID)
	if err != nil {
		return w.Error("internal", "couldn't load the conversation", true)
	}
	if !ok {
		convID, history = a.newID(), nil
	}
	if req.ConfirmActionID != "" {
		return a.confirm(ctx, caller, req.ConfirmActionID, convID, w)
	}
	if req.SuggestedID != "" {
		return a.suggested(ctx, caller, req.SuggestedID, convID, w)
	}
	tl := &turnLog{conv: convID, outcome: "error"}
	// Runs before the deferred cancel(nil) above (defers run last-first), so
	// ctx still says whether the deadline or the caller ended the turn.
	defer func() {
		if tl.outcome == "error" {
			switch {
			case errors.Is(context.Cause(ctx), errDeadline):
				tl.outcome = "timeout"
			case ctx.Err() != nil:
				tl.outcome = "cancelled" // the visitor pressed Stop or left
			}
		}
		a.writeLog(tl)
	}()

	if err := w.StepStart("retrieval", "retrieval", "Hybrid search"); err != nil {
		return err
	}
	retrievalStart := a.Clock.Now()
	chunks, err := a.Retriever.Retrieve(ctx, req.Message, caller.Mask(), a.topK())
	tl.retrieval, tl.hits = a.Clock.Now().Sub(retrievalStart), len(chunks)
	if err != nil {
		_ = w.StepEnd("retrieval", "error", map[string]any{"error": err.Error()})
		return a.fail(ctx, w, "retrieval_failed", "couldn't search the portfolio")
	}
	detail := map[string]any{"hits": len(chunks), "sources": len(sourceCounts(chunks))}
	if len(chunks) > 0 {
		detail["top_score"] = chunks[0].Score
	}
	if err := w.StepEnd("retrieval", "ok", detail); err != nil {
		return err
	}
	if err := w.Thought(narrate(chunks, a.Wording), "agent"); err != nil {
		return err
	}

	// The model may call tools; their results go back to it, for at most
	// MaxToolRounds rounds (design §3). Layer 1 of authorization: the model
	// is only shown the tools this caller may use (design §6).
	mreq := prompt(chunks, history, req.Message)
	mreq.System = named(mreq.System, a.AssistantName, a.Wording.Get("collection"))
	mreq.Tools = a.Tools.For(caller)
	if len(mreq.Tools) > 0 {
		mreq.System += toolGuidance
	}
	cites := newCiter(len(chunks))
	var answer strings.Builder
	toolSteps := 0
	for round := 0; ; round++ {
		stepID := "inference"
		if round > 0 {
			stepID = fmt.Sprintf("inference-%d", round+1)
		}
		if err := w.StepStart(stepID, "inference", "Writing the answer"); err != nil {
			return err
		}
		var calls []model.ToolCall
		var text strings.Builder
		started := a.Clock.Now()
		res, err := a.Model.Stream(ctx, mreq, func(e model.Event) error {
			switch e.Kind {
			case model.Reasoning:
				return w.Thought(e.Text, "model")
			case model.Text:
				text.WriteString(e.Text)
				answer.WriteString(e.Text)
				if err := w.Token(e.Text); err != nil {
					return err
				}
				for _, n := range cites.feed(e.Text) {
					c := chunks[n-1]
					if err := w.Citation(sse.Citation{N: n, ChunkID: c.ID, Title: c.Title, URL: c.URL}); err != nil {
						return err
					}
				}
			case model.ToolCallEvent:
				calls = append(calls, *e.ToolCall)
			case model.Notice: // the router fell back (design D4)
				return w.Thought(fmt.Sprintf("The main model is %s, so the backup model is answering.", e.Text), "agent")
			}
			return nil
		})
		if err != nil {
			tl.inference += a.Clock.Now().Sub(started) // a stopped or slow model's time still counts
			_ = w.StepEnd(stepID, "error", map[string]any{"error": err.Error()})
			return a.fail(ctx, w, "model_unavailable", "the model couldn't answer right now")
		}
		status := "ok"
		if res.Fallback {
			status = "fallback"
		}
		tl.rounds++
		tl.inference += a.Clock.Now().Sub(started)
		tl.model, tl.stop = res.Model, res.StopReason
		tl.tokensIn, tl.tokensOut = tl.tokensIn+res.TokensIn, tl.tokensOut+res.TokensOut
		if err := w.StepEnd(stepID, status, inferenceDetail(res, a.Clock.Now().Sub(started))); err != nil {
			return err
		}
		if len(calls) == 0 {
			break
		}
		if round == MaxToolRounds {
			tl.outcome = "tool_limit"
			return w.Error("tool_limit", "the answer needed too many tool calls", true)
		}
		mreq.Messages = append(mreq.Messages, model.Message{Role: model.Assistant, Content: text.String(), ToolCalls: calls})
		for _, c := range calls {
			toolSteps++
			result, err := a.runTool(ctx, caller, c, fmt.Sprintf("tool-%d", toolSteps), w)
			if err != nil {
				return err
			}
			mreq.Messages = append(mreq.Messages, model.Message{Role: model.ToolRole, ToolCallID: c.ID, Content: result})
		}
	}

	// A model can stop without writing any answer (seen with qwen3 on a busy
	// CPU: it reasoned, then stopped). Say so rather than end silently.
	tl.answerChars = len(answer.String())
	if strings.TrimSpace(answer.String()) == "" {
		tl.outcome = "empty_answer"
		return w.Error("empty_answer", "the model finished without an answer", true)
	}
	tl.outcome = "done"

	// History keeps the bare question, not the excerpts, so later turns
	// retrieve afresh instead of carrying stale context.
	if err := a.Store.Append(ctx, convID, caller.ID,
		model.Message{Role: model.User, Content: req.Message},
		model.Message{Role: model.Assistant, Content: answer.String()}); err != nil {
		return w.Error("internal", "couldn't save the conversation", true)
	}
	return w.Done(convID)
}

// fail ends the stream, naming a deadline or cancellation over the step's own error.
func (a *Agent) fail(ctx context.Context, w *sse.Writer, code, message string) error {
	switch {
	case errors.Is(context.Cause(ctx), errDeadline):
		return w.Error("timeout", "the answer took too long", true)
	case ctx.Err() != nil:
		return w.Error("cancelled", "the request was cancelled", false)
	}
	return w.Error(code, message, true)
}

func (a *Agent) deadline() time.Duration {
	if a.Deadline > 0 {
		return a.Deadline
	}
	return TurnDeadline
}

func (a *Agent) topK() int {
	if a.TopK > 0 {
		return a.TopK
	}
	return 5
}

func (a *Agent) newID() string {
	if a.NewID != nil {
		return a.NewID()
	}
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails on supported platforms
	return "c_" + hex.EncodeToString(b[:])
}

// sourceCounts lists each source (document title) once, in first-seen order,
// with how many excerpts it supplied.
func sourceCounts(chunks []Chunk) []sourceCount {
	var out []sourceCount
	at := map[string]int{}
	for _, c := range chunks {
		i, seen := at[c.Title]
		if !seen {
			i = len(out)
			at[c.Title] = i
			out = append(out, sourceCount{title: c.Title})
		}
		out[i].n++
	}
	return out
}

type sourceCount struct {
	title string
	n     int
}

// narrate is the thought-stream line about what retrieval found, in the
// visitor's words (design §14): "Found 4 excerpts from 2 sources: Project
// Alpha (3), A long story."
func narrate(chunks []Chunk, w wording.Wording) string {
	if len(chunks) == 0 {
		return wording.Fill(w.Get("retrieval_none"), map[string]string{"collection": w.Get("collection")})
	}
	sources := sourceCounts(chunks)
	titles := make([]string, len(sources))
	for i, s := range sources {
		titles[i] = s.title
		if s.n > 1 {
			titles[i] = fmt.Sprintf("%s (%d)", s.title, s.n)
		}
	}
	return wording.Fill(w.Get("retrieval_found"), map[string]string{
		"excerpts":   w.Count(len(chunks), "excerpt"),
		"sources":    w.Count(len(sources), "source"),
		"titles":     strings.Join(titles, ", "),
		"collection": w.Get("collection"),
	})
}

func inferenceDetail(r model.Result, elapsed time.Duration) map[string]any {
	d := map[string]any{
		"provider": r.Provider, "model": r.Model, "ttft_ms": r.TTFT.Milliseconds(),
		"tokens_in": r.TokensIn, "tokens_out": r.TokensOut, "cached_tokens": r.CachedTokens,
		"stop_reason": r.StopReason,
	}
	if gen := (elapsed - r.TTFT).Seconds(); gen > 0 && r.TokensOut > 0 {
		d["tokens_per_s"] = float64(r.TokensOut) / gen
	}
	if r.PrimaryErr != nil {
		d["primary_error"] = r.PrimaryErr.Error()
	}
	return d
}

// runTool handles one tool call from the model as a tool step and returns
// the result to send back to the model. Layer 2 of authorization: whatever
// the model was shown, the call is checked again here, so a forged or
// hallucinated call runs nothing. A side-effecting call never runs here: it
// becomes a pending action the visitor must confirm (design §6). The error
// is non-nil only when the stream itself failed.
func (a *Agent) runTool(ctx context.Context, caller auth.Caller, c model.ToolCall, stepID string, w *sse.Writer) (string, error) {
	if err := w.StepStart(stepID, "tool", c.Name); err != nil {
		return "", err
	}
	end := func(status string, detail map[string]any, result any) (string, error) {
		detail["tool"] = c.Name
		raw, _ := json.Marshal(result)
		return string(raw), w.StepEnd(stepID, status, detail)
	}
	def, ok := a.Tools.Lookup(c.Name)
	if !ok || !tools.Allowed(def, caller) {
		return end("error", map[string]any{"authorized": false}, map[string]string{"error": "not authorized to use " + c.Name})
	}
	summary, err := def.Summary(c.Arguments)
	if err != nil {
		return end("error", map[string]any{"authorized": true, "error": err.Error()}, map[string]string{"error": "invalid arguments: " + err.Error()})
	}
	if !def.SideEffect {
		result, err := def.Run(ctx, caller, c.Arguments)
		if err != nil {
			return end("error", map[string]any{"authorized": true, "error": err.Error()}, map[string]string{"error": err.Error()})
		}
		return end("ok", map[string]any{"authorized": true}, result)
	}

	action := tools.PendingAction{ID: a.newActionID(), CallerID: caller.ID, Tool: c.Name, Summary: summary,
		Args: c.Arguments, Expires: a.Clock.Now().Add(tools.PendingTTL)}
	if err := a.Pending.Propose(ctx, action); err != nil {
		return end("error", map[string]any{"authorized": true, "error": err.Error()}, map[string]string{"error": "couldn't prepare the action"})
	}
	if err := w.ConfirmRequest(action.ID, c.Name, summary, tools.PendingTTL); err != nil {
		return "", err
	}
	return end("ok", map[string]any{"authorized": true, "confirmation": "requested"},
		map[string]string{"status": "awaiting_confirmation", "summary": summary,
			"note": "Nothing has been sent yet. Ask the visitor to press Confirm."})
}

// confirm runs a pending action the visitor confirmed: no retrieval and no
// model call. The claim is the single conditional update that makes the
// action run at most once; the caller is checked again first.
func (a *Agent) confirm(ctx context.Context, caller auth.Caller, actionID, convID string, w *sse.Writer) error {
	if err := w.StepStart("tool-1", "tool", "Confirmed action"); err != nil {
		return err
	}
	action, err := a.Pending.Claim(ctx, actionID, caller.ID, a.Clock.Now())
	if err != nil {
		code := "internal"
		switch {
		case errors.Is(err, tools.ErrNoAction):
			code = "action_not_found"
		case errors.Is(err, tools.ErrExpired):
			code = "action_expired"
		case errors.Is(err, tools.ErrAlreadyDone):
			code = "action_already_done"
		}
		_ = w.StepEnd("tool-1", "error", map[string]any{"error": err.Error()})
		return w.Error(code, "that action can't be confirmed", false)
	}
	def, ok := a.Tools.Lookup(action.Tool)
	if !ok || !tools.Allowed(def, caller) {
		_ = w.StepEnd("tool-1", "error", map[string]any{"tool": action.Tool, "authorized": false})
		return w.Error("not_authorized", "you can no longer use "+action.Tool, false)
	}
	if _, err := def.Run(ctx, caller, action.Args); err != nil {
		_ = w.StepEnd("tool-1", "error", map[string]any{"tool": action.Tool, "authorized": true, "error": err.Error()})
		return w.Error("action_failed", "the action failed and was not retried", false)
	}
	if err := w.StepEnd("tool-1", "ok", map[string]any{"tool": action.Tool, "authorized": true}); err != nil {
		return err
	}
	done := "Done: " + action.Summary + "."
	if err := w.Token(done); err != nil {
		return err
	}
	if err := a.Store.Append(ctx, convID, caller.ID,
		model.Message{Role: model.User, Content: "(Confirmed: " + action.Summary + ")"},
		model.Message{Role: model.Assistant, Content: done}); err != nil {
		return w.Error("internal", "couldn't save the conversation", true)
	}
	return w.Done(convID)
}

func (a *Agent) newActionID() string { return "a_" + strings.TrimPrefix(a.newID(), "c_") }

// suggested streams a stored, pre-checked answer: no retrieval and no model
// call, so it appears at once (design §14). A suggestion the caller may not
// see is answered exactly like a missing one.
func (a *Agent) suggested(ctx context.Context, caller auth.Caller, id, convID string, w *sse.Writer) error {
	if err := w.StepStart("cache", "cache", "Stored answer"); err != nil {
		return err
	}
	var sug Suggestion
	ok := false
	if a.Suggestions != nil {
		sug, ok = a.Suggestions.Find(id, caller)
	}
	if !ok {
		_ = w.StepEnd("cache", "error", map[string]any{"hit": false})
		return w.Error("suggestion_not_found", "that suggested question isn't available", false)
	}
	if err := w.StepEnd("cache", "ok", map[string]any{"hit": true}); err != nil {
		return err
	}
	if err := w.Token(sug.Answer); err != nil {
		return err
	}
	cites := newCiter(len(sug.Chunks))
	for _, n := range cites.feed(sug.Answer) {
		c := sug.Chunks[n-1]
		if err := w.Citation(sse.Citation{N: n, ChunkID: c.ID, Title: c.Title, URL: c.URL}); err != nil {
			return err
		}
	}
	if err := a.Store.Append(ctx, convID, caller.ID,
		model.Message{Role: model.User, Content: sug.Question},
		model.Message{Role: model.Assistant, Content: sug.Answer}); err != nil {
		return w.Error("internal", "couldn't save the conversation", true)
	}
	return w.Done(convID)
}
