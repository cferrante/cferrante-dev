package evals

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/indexing"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
)

func read(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
}

func TestLoadReadsJSONLinesFromEveryFile(t *testing.T) {
	items, err := Load([]string{"a.jsonl", "b.jsonl"}, read(map[string]string{
		"a.jsonl": `{"id":"g-1","question":"What is Kiln?","caller":"anonymous","expected_chunks":["kiln#summary"]}` + "\n\n",
		"b.jsonl": `{"id":"g-2","question":"Rates?","caller":"invitee","expected_chunks":["ledger#rates"],"expected_answer":"Negotiable.","tags":["private"]}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].Caller != "invitee" || items[1].Tags[0] != "private" || items[0].ExpectedChunks[0] != "kiln#summary" {
		t.Errorf("items = %+v", items)
	}
}

// A typo in the golden set must stop the run, not quietly drop or change a
// question: the score would be computed over the wrong set.
func TestBadGoldenFilesAreRefused(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"not JSON":      {`{"id":`, "a.jsonl:1"},
		"unknown field": {`{"id":"g-1","question":"q","caller":"anonymous","expected_chunk":["x"]}`, "expected_chunk"},
		"duplicate id":  {`{"id":"g-1","question":"q","caller":"anonymous"}` + "\n" + `{"id":"g-1","question":"r","caller":"anonymous"}`, `"g-1"`},
		"no id":         {`{"question":"q","caller":"anonymous"}`, "id"},
		"no question":   {`{"id":"g-1","question":" ","caller":"anonymous"}`, "question"},
		"bad caller":    {`{"id":"g-1","question":"q","caller":"admin"}`, "admin"},
		"second line":   {`{"id":"g-1","question":"q","caller":"anonymous"}` + "\n" + `{"id":"g-2"`, "a.jsonl:2"},
	} {
		if _, err := Load([]string{"a.jsonl"}, read(map[string]string{"a.jsonl": tc.content})); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

// fixture indexes evals/testdata/corpus (seven public documents and one
// invitee-only one) with the hash embedder, so search is deterministic and
// keyword-driven, and a top-5 miss is possible.
func fixture(t *testing.T) *retrieval.Retriever {
	t.Helper()
	s := indexing.Settings{CorpusDirs: "testdata/corpus", IndexPath: filepath.Join(t.TempDir(), "e.idx"), ChunkTarget: 1000, Embedder: "hash"}
	e, err := s.NewEmbedder(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Build(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	r, err := retrieval.Load(s.IndexPath, e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestHitAt5(t *testing.T) {
	items := []Item{
		{ID: "hit", Question: "Which tool parses telephone call records from switches?", Caller: "anonymous", ExpectedChunks: []string{"relay#summary"}},
		// The plan's failing test for 6.1: the question has no supporting
		// chunk in the top 5, so it must count as a miss.
		{ID: "miss", Question: "How are kiln firings scheduled for pottery studios?", Caller: "anonymous", ExpectedChunks: []string{"spindle#summary"}},
		{ID: "private", Question: "What is the consulting rate for nonprofits?", Caller: "invitee", ExpectedChunks: []string{"ledger#rates"}},
		// No expected chunk: an answer that should decline, scored in 6.3.
		{ID: "decline", Question: "What's the weather in Paris?", Caller: "anonymous"},
		// A visitor asking for private text must search as a visitor.
		{ID: "leak", Question: "What is the consulting rate for nonprofits?", Caller: "anonymous"},
	}
	results, err := HitAtK(context.Background(), items, fixture(t), 5)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Result{}
	for _, r := range results {
		got[r.Item.ID] = r
	}
	if r := got["hit"]; !r.Scored || !r.Hit || r.Rank != 1 {
		t.Errorf("hit: %+v", r)
	}
	if r := got["miss"]; !r.Scored || r.Hit || r.Rank != 0 || len(r.Got) != 5 || r.Got[0] != "kiln#summary" {
		t.Errorf("miss: %+v", r)
	}
	if r := got["private"]; !r.Scored || !r.Hit {
		t.Errorf("private: %+v", r)
	}
	if r := got["decline"]; r.Scored {
		t.Errorf("decline was scored: %+v", r)
	}
	if r := got["leak"]; slices.Contains(r.Got, "ledger#rates") {
		t.Errorf("an anonymous search returned the invitee-only chunk: %+v", r)
	}
	if hits, scored := Score(results); hits != 2 || scored != 3 {
		t.Errorf("score = %d/%d, want 2/3", hits, scored)
	}
}

// An expected chunk the index doesn't have is a typo, and one the caller
// can never see is a broken question: both stop the run instead of
// counting as ordinary misses.
func TestImpossibleExpectationsAreErrors(t *testing.T) {
	r := fixture(t)
	for name, tc := range map[string]struct {
		item Item
		want string
	}{
		"unknown chunk":        {Item{ID: "x", Question: "q", Caller: "anonymous", ExpectedChunks: []string{"kiln#sumary"}}, "kiln#sumary"},
		"private to a visitor": {Item{ID: "y", Question: "Rates?", Caller: "anonymous", ExpectedChunks: []string{"ledger#rates"}}, "invitee"},
	} {
		if _, err := HitAtK(context.Background(), []Item{tc.item}, r, 5); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}
