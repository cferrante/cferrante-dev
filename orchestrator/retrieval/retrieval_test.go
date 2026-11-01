package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
)

var hash = embed.Hash{D: 256}

func fixtureChunks(t *testing.T) []corpus.Chunk {
	t.Helper()
	chunks, err := corpus.Chunker{Target: 1000}.ReadDir("../corpus/testdata/corpus")
	if err != nil {
		t.Fatal(err)
	}
	return chunks
}

func build(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus.idx")
	if err := Build(context.Background(), fixtureChunks(t), hash, path); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return path
}

func retrieve(t *testing.T, r *Retriever, q string, mask uint32) []agent.Chunk {
	t.Helper()
	got, err := r.Retrieve(context.Background(), q, mask, 3)
	if err != nil {
		t.Fatalf("Retrieve(%q): %v", q, err)
	}
	return got
}

func TestBuildLoadAndFindKnownChunks(t *testing.T) {
	r, err := Load(build(t), hash)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer r.Close()
	got := retrieve(t, r, "How does the engine fuse keyword and vector search?", auth.Public)
	if len(got) == 0 || got[0].ID != "proj-alpha#how-it-works" {
		t.Fatalf("top hit = %+v, want proj-alpha#how-it-works", got)
	}
	if got[0].Title != "Project Alpha" || got[0].URL != "/projects/alpha#how-it-works" ||
		got[0].Text == "" || got[0].Score <= 0 {
		t.Errorf("chunk fields = %+v", got[0])
	}
	// Title words are indexed too, so the document title finds its chunks.
	// "long" appears only in the title "A long story", not in the heading or text.
	if top := retrieve(t, r, "long", auth.Public); len(top) == 0 || top[0].ID != "long#story" {
		t.Errorf("title search top = %+v", top)
	}
}

func TestPrivateChunksOnlyForInvitees(t *testing.T) {
	r, err := Load(build(t), hash)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, c := range retrieve(t, r, "consulting rate negotiable", auth.Public) {
		if c.ID == "private-notes#rates" {
			t.Fatal("public caller got the invitee-only chunk")
		}
	}
	if got := retrieve(t, r, "consulting rate negotiable", auth.Public|auth.Invitee); len(got) == 0 || got[0].ID != "private-notes#rates" {
		t.Errorf("invitee top hit = %+v", got)
	}
}

type failingEmbedder struct{ embed.Hash }

func (failingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("embedding service down")
}

func TestFailedBuildLeavesNoFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Build(context.Background(), fixtureChunks(t), failingEmbedder{hash}, filepath.Join(dir, "corpus.idx")); err == nil {
		t.Fatal("want an error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind %v", entries)
	}
}

func TestRebuildReplacesTheIndex(t *testing.T) {
	path := build(t)
	one := fixtureChunks(t)[:1]
	if err := Build(context.Background(), one, hash, path); err != nil {
		t.Fatal(err)
	}
	r, err := Load(path, hash)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got := retrieve(t, r, "story", auth.Public); len(got) != 1 {
		t.Errorf("after rebuilding with one chunk, got %d hits", len(got))
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 2 {
		t.Errorf("directory holds %d files, want the index and its sidecar", len(entries))
	}
}

func TestLoadRejectsMismatches(t *testing.T) {
	path := build(t)
	if _, err := Load(path, embed.Hash{D: 128}); err == nil {
		t.Error("different dimension: want an error")
	}
	if _, err := Load(path, renamed{hash}); err == nil {
		t.Error("different model with the same dimension: want an error")
	}
	var sc Sidecar
	raw, _ := os.ReadFile(path + ".json")
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatal(err)
	}
	if sc.Version != SidecarVersion || sc.Embedding.Model != hash.Model() || sc.Embedding.Dim != 256 || len(sc.Chunks) != 5 {
		t.Fatalf("sidecar = version %d, %+v, %d chunks", sc.Version, sc.Embedding, len(sc.Chunks))
	}
	sc.Chunks = sc.Chunks[1:]
	raw, _ = json.Marshal(sc)
	os.WriteFile(path+".json", raw, 0o600)
	if _, err := Load(path, hash); err == nil {
		t.Error("sidecar with fewer chunks than the index: want an error")
	}
	os.Remove(path + ".json")
	if _, err := Load(path, hash); err == nil {
		t.Error("missing sidecar: want an error")
	}
}

func TestBuildFailingAfterTempFilesCleansUpAndKeepsTheOldIndex(t *testing.T) {
	path := build(t)
	// A failed rebuild must leave the previous index usable.
	if err := Build(context.Background(), fixtureChunks(t), failingEmbedder{hash}, path); err == nil {
		t.Fatal("want an error")
	}
	if r, err := Load(path, hash); err != nil {
		t.Fatalf("previous index no longer loads: %v", err)
	} else {
		r.Close()
	}

	// Fail at the rename, after both temporary files exist: the sidecar's
	// target is a directory.
	dir := t.TempDir()
	target := filepath.Join(dir, "corpus.idx")
	if err := os.Mkdir(target+".json", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Build(context.Background(), fixtureChunks(t), hash, target); err == nil {
		t.Fatal("rename onto a directory: want an error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("left behind %v, want only the blocking directory", names)
	}
}

// renamed is the same vectors under another model name, as a different
// embedding model of the same size would be.
type renamed struct{ embed.Hash }

func (renamed) Model() string { return "other-model-256" }

func TestIndexTextIncludesTitleAndHeading(t *testing.T) {
	got := indexText(corpus.Chunk{Title: "Project Alpha", Heading: "How it works", Text: "Body."})
	if got != "Project Alpha — How it works\n\nBody." {
		t.Errorf("indexText = %q", got)
	}
	if got := indexText(corpus.Chunk{Title: "Project Alpha", Text: "Intro."}); got != "Project Alpha\n\nIntro." {
		t.Errorf("indexText without heading = %q", got)
	}
}
