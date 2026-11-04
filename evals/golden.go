// Package evals runs the golden and adversarial evaluations (design §2,
// §10). This file is the golden set's retrieval check, hit@k: did the chunk
// that answers each question come back in the top k? It tests search apart
// from the model, so a miss means the corpus or the search needs work, not
// the prompt.
package evals

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
)

// Item is one golden question (design §10).
type Item struct {
	ID             string   `json:"id"`
	Question       string   `json:"question"`
	Caller         string   `json:"caller"` // anonymous | invitee
	ExpectedChunks []string `json:"expected_chunks"`
	ExpectedAnswer string   `json:"expected_answer"`
	Tags           []string `json:"tags"`
}

// Load reads JSON Lines files (blank lines allowed). Anything that would
// silently change the set being scored is an error naming file and line: an
// unknown field, a duplicate or missing id, an empty question, or a caller
// other than anonymous or invitee.
func Load(paths []string, read func(string) ([]byte, error)) ([]Item, error) {
	var items []Item
	seen := map[string]string{}
	for _, path := range paths {
		raw, err := read(path)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(bytes.NewReader(raw))
		sc.Buffer(nil, 1<<20)
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			at := fmt.Sprintf("%s:%d", path, n)
			dec := json.NewDecoder(strings.NewReader(line))
			dec.DisallowUnknownFields()
			var it Item
			if err := dec.Decode(&it); err != nil {
				return nil, fmt.Errorf("%s: %w", at, err)
			}
			switch {
			case it.ID == "":
				return nil, fmt.Errorf("%s: no id", at)
			case seen[it.ID] != "":
				return nil, fmt.Errorf("%s: id %q is also at %s", at, it.ID, seen[it.ID])
			case strings.TrimSpace(it.Question) == "":
				return nil, fmt.Errorf("%s: %q has no question", at, it.ID)
			case it.Caller != "anonymous" && it.Caller != "invitee":
				return nil, fmt.Errorf("%s: %q: caller %q, want anonymous or invitee", at, it.ID, it.Caller)
			}
			seen[it.ID] = at
			items = append(items, it)
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return items, nil
}

// Searcher is the part of retrieval.Retriever the check uses.
type Searcher interface {
	Retrieve(ctx context.Context, query string, mask uint32, k int) ([]agent.Chunk, error)
	Chunk(id string) (agent.Chunk, string, bool)
}

// Result is one question's outcome. Scored is false for a question with no
// expected chunk (one the answer should decline; 6.3 scores those). Rank is
// the 1-based position of the first expected chunk found, 0 for a miss.
type Result struct {
	Item   Item
	Scored bool
	Hit    bool
	Rank   int
	Got    []string // the top-k chunk ids, best first
}

func callerFor(name string) auth.Caller {
	if name == "invitee" {
		return auth.Caller{Groups: []string{auth.InviteeGroup}}
	}
	return auth.Caller{}
}

// HitAtK searches each question as its caller would and records whether any
// expected chunk is in the top k. An expected chunk the index doesn't have,
// or one the caller may never see, is an error: a typo or a broken
// question, not a miss.
func HitAtK(ctx context.Context, items []Item, s Searcher, k int) ([]Result, error) {
	results := make([]Result, 0, len(items))
	for _, it := range items {
		caller := callerFor(it.Caller)
		for _, id := range it.ExpectedChunks {
			_, access, ok := s.Chunk(id)
			if !ok {
				return nil, fmt.Errorf("%s: expected chunk %q isn't in the index", it.ID, id)
			}
			if access == "invitee" && !caller.IsInvitee() {
				return nil, fmt.Errorf("%s: expected chunk %q is invitee-only, but the caller is %s", it.ID, id, it.Caller)
			}
		}
		r := Result{Item: it, Scored: len(it.ExpectedChunks) > 0}
		chunks, err := s.Retrieve(ctx, it.Question, caller.Mask(), k)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", it.ID, err)
		}
		for i, c := range chunks {
			r.Got = append(r.Got, c.ID)
			if !r.Hit && slices.Contains(it.ExpectedChunks, c.ID) {
				r.Hit, r.Rank = true, i+1
			}
		}
		results = append(results, r)
	}
	return results, nil
}

// Score counts hits among the scored questions.
func Score(results []Result) (hits, scored int) {
	for _, r := range results {
		if r.Scored {
			scored++
			if r.Hit {
				hits++
			}
		}
	}
	return hits, scored
}
