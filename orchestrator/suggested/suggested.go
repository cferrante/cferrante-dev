// Package suggested loads the suggested questions and their stored answers
// (design §14). Answers are hand-written until the judge (Phase 6) can
// generate and verify them; either way they're checked here at startup.
package suggested

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
)

// ChunkLookup finds a chunk by id, with its access ("public" or "invitee").
type ChunkLookup func(id string) (chunk agent.Chunk, access string, ok bool)

// Store holds the loaded suggestions in file order. Read-only after Load, so
// safe for concurrent use.
type Store struct{ items []agent.Suggestion }

type entry struct {
	ID        string   `json:"id"`
	Question  string   `json:"question"`
	Answer    string   `json:"answer"`
	Citations []string `json:"citations"`
	Access    string   `json:"access"`
}

var marker = regexp.MustCompile(`\[(\d+(?:\s*,\s*\d+)*)\]`)

// Load reads and checks the file at path. Every problem stops startup: a
// broken stored answer would otherwise be served instantly to every visitor.
func Load(path string, lookup ChunkLookup) (*Store, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // a typo in a field name is an error, not ignored
	var entries []entry
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("%s: want a JSON list of suggestions: %w", path, err)
	}
	s := &Store{}
	seen := map[string]bool{}
	for i, e := range entries {
		where := fmt.Sprintf("%s: suggestion %d (%q)", path, i+1, e.ID)
		switch {
		case strings.TrimSpace(e.ID) == "":
			return nil, fmt.Errorf("%s: id is empty", where)
		case seen[e.ID]:
			return nil, fmt.Errorf("%s: duplicate id", where)
		case strings.TrimSpace(e.Question) == "" || strings.TrimSpace(e.Answer) == "":
			return nil, fmt.Errorf("%s: question or answer is empty", where)
		case e.Access != "public" && e.Access != "invitee":
			return nil, fmt.Errorf("%s: access %q: want public or invitee", where, e.Access)
		}
		seen[e.ID] = true
		sug := agent.Suggestion{ID: e.ID, Question: e.Question, Answer: e.Answer, InviteeOnly: e.Access == "invitee"}
		for _, id := range e.Citations {
			c, access, ok := lookup(id)
			if !ok {
				return nil, fmt.Errorf("%s: cites %s, which isn't in the index", where, id)
			}
			// A public answer shown to every visitor must not quote private text.
			if e.Access == "public" && access != "public" {
				return nil, fmt.Errorf("%s: a public answer cites %s, which is invitee-only", where, id)
			}
			sug.Chunks = append(sug.Chunks, c)
		}
		for _, m := range marker.FindAllStringSubmatch(e.Answer, -1) {
			for _, n := range strings.Split(m[1], ",") {
				if k, _ := strconv.Atoi(strings.TrimSpace(n)); k < 1 || k > len(sug.Chunks) {
					return nil, fmt.Errorf("%s: the answer cites %s but has %d citations", where, m[0], len(sug.Chunks))
				}
			}
		}
		s.items = append(s.items, sug)
	}
	return s, nil
}

func visible(s agent.Suggestion, caller auth.Caller) bool {
	return !s.InviteeOnly || caller.IsInvitee()
}

// List returns the suggestions the caller may see, in file order.
func (s *Store) List(caller auth.Caller) []agent.Suggestion {
	var out []agent.Suggestion
	for _, sug := range s.items {
		if visible(sug, caller) {
			out = append(out, sug)
		}
	}
	return out
}

// Find returns a suggestion the caller may see; a hidden one is not found.
func (s *Store) Find(id string, caller auth.Caller) (agent.Suggestion, bool) {
	for _, sug := range s.items {
		if sug.ID == id && visible(sug, caller) {
			return sug, true
		}
	}
	return agent.Suggestion{}, false
}
