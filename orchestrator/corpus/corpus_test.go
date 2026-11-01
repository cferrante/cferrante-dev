package corpus

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseFrontMatter(t *testing.T) {
	d, err := Parse("proj-cferrante-dev.md", []byte("---\ntitle: My Site\nurl: /site\naccess: invitee\n---\nBody here.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Slug != "proj-cferrante-dev" || d.Title != "My Site" || d.URL != "/site" || d.Access != "invitee" ||
		strings.TrimSpace(d.Body) != "Body here." {
		t.Errorf("parsed %+v", d)
	}
}

func TestParseRejectsBadFrontMatter(t *testing.T) {
	for name, content := range map[string]string{
		"no front matter":  "Just text.\n",
		"unclosed":         "---\ntitle: T\nurl: /u\naccess: public\nBody\n",
		"missing access":   "---\ntitle: T\nurl: /u\n---\nBody\n",
		"bad access":       "---\ntitle: T\nurl: /u\naccess: secret\n---\nBody\n",
		"typo in a key":    "---\ntitle: T\nurl: /u\nacess: public\naccess: public\n---\nBody\n",
		"duplicate key":    "---\ntitle: T\ntitle: U\nurl: /u\naccess: public\n---\nBody\n",
		"missing title":    "---\nurl: /u\naccess: public\n---\nBody\n",
		"line without key": "---\ntitle: T\nurl: /u\naccess: public\nnonsense\n---\nBody\n",
	} {
		if _, err := Parse("x.md", []byte(content)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

type want struct{ id, access, url, text string }

func check(t *testing.T, got []Chunk, wants []want) {
	t.Helper()
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if len(got) != len(wants) {
		t.Fatalf("chunk ids = %v, want %d chunks", ids, len(wants))
	}
	for i, w := range wants {
		c := got[i]
		if c.ID != w.id || c.Access != w.access || c.URL != w.url || strings.TrimSpace(c.Text) != w.text {
			t.Errorf("chunk %d = {%s %s %s %q}\n         want {%s %s %s %q}", i, c.ID, c.Access, c.URL, c.Text, w.id, w.access, w.url, w.text)
		}
	}
}

func TestFixtureCorpusChunks(t *testing.T) {
	got, err := Chunker{Target: 1000}.ReadDir("testdata/corpus")
	if err != nil {
		t.Fatal(err)
	}
	check(t, got, []want{
		{"long#story", "public", "/story#story", "Sentence one is here. Sentence two follows it. Sentence three is longer than the others by a bit. Sentence four ends it."},
		{"private-notes#rates", "invitee", "/notes#rates", "The consulting rate is negotiable for nonprofits."},
		{"proj-alpha#intro", "public", "/projects/alpha", "Alpha is a small Go service."},
		{"proj-alpha#summary", "public", "/projects/alpha#summary", "Alpha answers questions about a portfolio. It streams answers."},
		// The fenced "# build it" line is code, not a heading; the empty section makes no chunk.
		{"proj-alpha#how-it-works", "public", "/projects/alpha#how-it-works",
			"Retrieval uses a C++ engine. The engine fuses keyword and vector search.\n\n```sh\n# build it (a comment, not a heading)\nmake build\n```"},
	})
	if got[3].Heading != "Summary" || got[3].Title != "Project Alpha" {
		t.Errorf("heading/title = %q/%q", got[3].Heading, got[3].Title)
	}
}

func TestLongSectionSplitsAtSentencesWithOneSentenceOverlap(t *testing.T) {
	d, err := Parse("long.md", mustRead(t, "testdata/corpus/long.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Target 60: "one" + "two" fit (46 bytes); each later piece starts with
	// the previous piece's last sentence and adds at least one new sentence.
	check(t, Chunker{Target: 60}.Split(d), []want{
		{"long#story", "public", "/story#story", "Sentence one is here. Sentence two follows it."},
		{"long#story-2", "public", "/story#story", "Sentence two follows it. Sentence three is longer than the others by a bit."},
		{"long#story-3", "public", "/story#story", "Sentence three is longer than the others by a bit. Sentence four ends it."},
	})
}

func TestParagraphsAreKeptWholeWhenTheyFit(t *testing.T) {
	d := Document{Slug: "p", Title: "P", URL: "/p", Access: "public",
		Body: "## S\n\nFirst paragraph is short.\n\nSecond paragraph is short too.\n\nThird one."}
	got := Chunker{Target: 60}.Split(d)
	if len(got) != 2 || !strings.HasSuffix(got[0].Text, "Second paragraph is short too.") ||
		!strings.HasPrefix(got[1].Text, "Second paragraph is short too.") {
		t.Errorf("chunks = %q", texts(got))
	}
}

func TestHeadingAnchors(t *testing.T) {
	d := Document{Slug: "a", Title: "A", URL: "/a", Access: "public",
		Body: "# Top Level\n\nx\n\n### C++ & Go: notes!\n\ny\n\n## Café 2026\n\nz"}
	var ids []string
	for _, c := range (Chunker{Target: 1000}).Split(d) {
		ids = append(ids, c.ID)
	}
	if want := []string{"a#top-level", "a#c--go-notes", "a#café-2026"}; !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestDuplicateChunkIDsAreAnError(t *testing.T) {
	dir := t.TempDir()
	// "Summary 2" collides with the second piece of a long "Summary".
	body := "---\ntitle: T\nurl: /u\naccess: public\n---\n## Summary\n\nOne sentence here. Two sentence here. Three sentence here.\n\n## Summary 2\n\nOther."
	if err := os.WriteFile(filepath.Join(dir, "d.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Chunker{Target: 30}).ReadDir(dir); err == nil || !strings.Contains(err.Error(), "d#summary-2") {
		t.Errorf("err = %v, want a duplicate id error naming d#summary-2", err)
	}
}

func TestReadDirReportsTheFileOfABadDocument(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "oops.md"), []byte("no front matter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Chunker{Target: 100}).ReadDir(dir); err == nil || !strings.Contains(err.Error(), "oops.md") {
		t.Errorf("err = %v, want it to name oops.md", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func texts(cs []Chunk) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return out
}

func TestCodeBlocksStayWhole(t *testing.T) {
	code := "```go\nfunc a() {}\n\n// Second part. Has sentences. And blank lines.\nfunc b() {}\n```"
	d := Document{Slug: "c", Title: "C", URL: "/c", Access: "public", Body: "## Code\n\n" + code}
	got := Chunker{Target: 20}.Split(d)
	if len(got) != 1 || got[0].Text != code {
		t.Errorf("chunks = %q, want the code block whole", texts(got))
	}
}
