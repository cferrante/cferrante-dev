package suggested

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
)

var chunks = map[string]struct {
	c      agent.Chunk
	access string
}{
	"proj-alpha#how-it-works": {agent.Chunk{ID: "proj-alpha#how-it-works", Title: "Project Alpha", URL: "/projects/alpha#how-it-works"}, "public"},
	"proj-alpha#intro":        {agent.Chunk{ID: "proj-alpha#intro", Title: "Project Alpha", URL: "/projects/alpha"}, "public"},
	"private-notes#rates":     {agent.Chunk{ID: "private-notes#rates", Title: "Private notes", URL: "/notes#rates"}, "invitee"},
}

func lookup(id string) (agent.Chunk, string, bool) {
	c, ok := chunks[id]
	return c.c, c.access, ok
}

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "suggested.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const good = `[
  {"id": "alpha-search", "question": "How does Alpha search?",
   "answer": "A C++ engine fuses keyword and vector search [1].",
   "citations": ["proj-alpha#how-it-works"], "access": "public"},
  {"id": "rates", "question": "What are the rates?",
   "answer": "Negotiable for nonprofits [1].", "citations": ["private-notes#rates"], "access": "invitee"}
]`

var (
	visitor = auth.Caller{}
	invitee = auth.Caller{ID: "u", Groups: []string{auth.InviteeGroup}}
)

func TestLoadResolvesCitationsAndFiltersByAccess(t *testing.T) {
	s, err := Load(write(t, good), lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(visitor); len(got) != 1 || got[0].ID != "alpha-search" {
		t.Errorf("visitor sees %v", got)
	}
	if got := s.List(invitee); len(got) != 2 {
		t.Errorf("invitee sees %d, want 2", len(got))
	}
	sug, ok := s.Find("alpha-search", visitor)
	if !ok || sug.Question != "How does Alpha search?" || len(sug.Chunks) != 1 || sug.Chunks[0].URL != "/projects/alpha#how-it-works" {
		t.Errorf("Find = %+v, %v", sug, ok)
	}
	if _, ok := s.Find("rates", visitor); ok {
		t.Error("visitor found an invitee-only suggestion")
	}
	if sug, ok := s.Find("rates", invitee); !ok || !sug.InviteeOnly {
		t.Errorf("invitee Find = %+v, %v", sug, ok)
	}
	if _, ok := s.Find("nope", invitee); ok {
		t.Error("found a missing id")
	}
}

func TestBadFilesStopStartup(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"public answer citing a private chunk": {`[{"id":"x","question":"q","answer":"a [1]","citations":["private-notes#rates"],"access":"public"}]`, "invitee-only"},
		"unknown chunk":                        {`[{"id":"x","question":"q","answer":"a [1]","citations":["nope#x"],"access":"public"}]`, "nope#x"},
		"marker past the citations":            {`[{"id":"x","question":"q","answer":"a [2]","citations":["proj-alpha#intro"],"access":"public"}]`, "[2]"},
		"duplicate id":                         {`[{"id":"x","question":"q","answer":"a","citations":[],"access":"public"},{"id":"x","question":"q2","answer":"b","citations":[],"access":"public"}]`, "duplicate"},
		"empty answer":                         {`[{"id":"x","question":"q","answer":" ","citations":[],"access":"public"}]`, "empty"},
		"empty question":                       {`[{"id":"x","question":"","answer":"a","citations":[],"access":"public"}]`, "empty"},
		"bad access":                           {`[{"id":"x","question":"q","answer":"a","citations":[],"access":"secret"}]`, "access"},
		"typo in a field":                      {`[{"id":"x","question":"q","answer":"a","citatons":[],"access":"public"}]`, "citatons"},
		"not JSON":                             {`[{`, "JSON"},
	} {
		_, err := Load(write(t, tc.content), lookup)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}
