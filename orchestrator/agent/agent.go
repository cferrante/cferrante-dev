// Package agent runs one chat turn (design §3): retrieve, prompt, stream the
// answer with citations, and record the turn.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/sse"
)

// TurnDeadline bounds a whole turn (design §3).
const TurnDeadline = 60 * time.Second

// Chunk is a retrieved passage.
type Chunk struct {
	ID, Title, URL, Text string
	Score                float32
}

// Retriever finds the passages most relevant to a query, among those the
// access mask allows.
type Retriever interface {
	Retrieve(ctx context.Context, query string, mask uint32, k int) ([]Chunk, error)
}

// TurnRequest is the visitor's message.
type TurnRequest struct {
	ConversationID string // "" or unknown: a new conversation is started
	Message        string
}

// Agent runs turns. Fields are set once and read concurrently.
type Agent struct {
	Model     model.Model
	Retriever Retriever
	Store     ConversationStore
	Clock     clock.Clock
	TopK      int           // passages per turn; default 5
	NewID     func() string // conversation ids; default 128 random bits
}

var errDeadline = errors.New("agent: turn deadline exceeded")

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
		case <-a.Clock.After(TurnDeadline):
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

	if err := w.StepStart("retrieval", "retrieval", "Hybrid search"); err != nil {
		return err
	}
	chunks, err := a.Retriever.Retrieve(ctx, req.Message, caller.Mask(), a.topK())
	if err != nil {
		_ = w.StepEnd("retrieval", "error", map[string]any{"error": err.Error()})
		return a.fail(ctx, w, "retrieval_failed", "couldn't search the portfolio")
	}
	detail := map[string]any{"hits": len(chunks)}
	if len(chunks) > 0 {
		detail["top_score"] = chunks[0].Score
	}
	if err := w.StepEnd("retrieval", "ok", detail); err != nil {
		return err
	}
	if err := w.Thought(narrate(chunks), "agent"); err != nil {
		return err
	}

	if err := w.StepStart("inference", "inference", "Writing the answer"); err != nil {
		return err
	}
	cites := newCiter(len(chunks))
	var answer strings.Builder
	started := a.Clock.Now()
	res, err := a.Model.Stream(ctx, prompt(chunks, history, req.Message), func(e model.Event) error {
		switch e.Kind {
		case model.Reasoning:
			return w.Thought(e.Text, "model")
		case model.Text:
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
		}
		// Tool calls are handled from step 3.6; no tools are offered yet.
		return nil
	})
	if err != nil {
		_ = w.StepEnd("inference", "error", map[string]any{"error": err.Error()})
		return a.fail(ctx, w, "model_unavailable", "the model couldn't answer right now")
	}
	status := "ok"
	if res.Fallback {
		status = "fallback"
	}
	if err := w.StepEnd("inference", status, inferenceDetail(res, a.Clock.Now().Sub(started))); err != nil {
		return err
	}

	// History keeps the bare question, not the passages, so later turns
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

func narrate(chunks []Chunk) string {
	if len(chunks) == 0 {
		return "No passages matched; I'll say so rather than guess."
	}
	titles := make([]string, len(chunks))
	for i, c := range chunks {
		titles[i] = c.Title
	}
	return fmt.Sprintf("Found %d passages: %s.", len(chunks), strings.Join(titles, ", "))
}

func inferenceDetail(r model.Result, elapsed time.Duration) map[string]any {
	d := map[string]any{
		"provider": r.Provider, "model": r.Model, "ttft_ms": r.TTFT.Milliseconds(),
		"tokens_in": r.TokensIn, "tokens_out": r.TokensOut, "cached_tokens": r.CachedTokens,
	}
	if gen := (elapsed - r.TTFT).Seconds(); gen > 0 && r.TokensOut > 0 {
		d["tokens_per_s"] = float64(r.TokensOut) / gen
	}
	if r.PrimaryErr != nil {
		d["primary_error"] = r.PrimaryErr.Error()
	}
	return d
}
