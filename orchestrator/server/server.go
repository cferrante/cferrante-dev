// Package server is the HTTP front of the orchestrator: POST /chat streams a
// turn as SSE (design §3). It is a plain http.Handler, so `make dev` serves it
// directly and Phase 7 wraps it for Lambda.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	"github.com/cferrante/cferrante-dev/orchestrator/sse"
)

// MaxMessage is the longest visitor message accepted, in characters.
const MaxMessage = 2000

// maxBody caps the request body before it is parsed.
const maxBody = 16 << 10

// request is the POST /chat body (design §3).
type request struct {
	ConversationID string          `json:"conversation_id"`
	Message        string          `json:"message"`
	SuggestedID    *string         `json:"suggested_id"`
	Confirm        json.RawMessage `json:"confirm"`
}

// Turner runs a turn; *agent.Agent implements it.
type Turner interface {
	Turn(ctx context.Context, caller auth.Caller, req agent.TurnRequest, w *sse.Writer) error
}

// Chat handles POST /chat.
type Chat struct {
	Agent Turner
	Auth  auth.Authenticator
	Clock clock.Clock
}

func (h *Chat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "the body must be JSON of at most 16 KB")
		return
	}
	msg := strings.TrimSpace(req.Message)
	switch {
	case msg == "":
		httpError(w, http.StatusBadRequest, "message is empty")
		return
	case utf8.RuneCountInString(msg) > MaxMessage:
		httpError(w, http.StatusBadRequest, "message is longer than 2000 characters")
		return
	case len(req.Confirm) > 0 && string(req.Confirm) != "null":
		// Confirmations arrive with tools in plan step 3.6.
		httpError(w, http.StatusNotImplemented, "confirmations are not supported yet")
		return
	}
	caller, err := h.Auth.Authenticate(r)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // ask proxies not to buffer the stream
	w.WriteHeader(http.StatusOK)
	stream := sse.New(w, flusher.Flush, h.Clock)
	defer stream.Close()
	// r.Context() ends when the client disconnects, which cancels the turn.
	_ = h.Agent.Turn(r.Context(), caller, agent.TurnRequest{ConversationID: req.ConversationID, Message: msg}, stream)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
