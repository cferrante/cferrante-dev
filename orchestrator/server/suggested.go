package server

import (
	"encoding/json"
	"net/http"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
)

// SuggestedList handles GET /suggested: the questions this caller may see,
// without their answers, which stream through POST /chat.
type SuggestedList struct {
	Suggestions agent.Suggestions
	Auth        auth.Authenticator
}

func (h *SuggestedList) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	caller, err := h.Auth.Authenticate(r)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	type item struct {
		ID       string `json:"id"`
		Question string `json:"question"`
	}
	list := []item{}
	for _, s := range h.Suggestions.List(caller) {
		list = append(list, item{s.ID, s.Question})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}
