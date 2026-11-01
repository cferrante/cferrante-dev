package rag

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	public  = 0x1
	invitee = 0x2
)

var query = []float32{1, 0, 0, 0}

// writeFixture builds the 3-chunk index the C++ ABI tests use: query
// (1, 0, 0, 0) ranks chunks 0, 1, 2 by vector, and only chunk 2 says "lambda".
func writeFixture(t *testing.T, lambdaAccess uint32) string {
	t.Helper()
	b, err := NewBuilder(4)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	defer b.Close()
	chunks := []struct {
		text   string
		vec    []float32
		access uint32
	}{
		{"go engine", []float32{1, 0, 0, 0}, public},
		{"serverless go", []float32{0.8, 0.6, 0, 0}, public},
		{"lambda functions", []float32{0.6, 0.8, 0, 0}, lambdaAccess},
	}
	for _, c := range chunks {
		if err := b.Add(c.text, c.vec, c.access); err != nil {
			t.Fatalf("Add(%q): %v", c.text, err)
		}
	}
	path := filepath.Join(t.TempDir(), "fixture.idx")
	if err := b.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return path
}

func load(t *testing.T, path string) *Index {
	t.Helper()
	ix, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

func ids(hits []Hit) []uint32 {
	out := []uint32{}
	for _, h := range hits {
		out = append(out, h.ChunkID)
	}
	return out
}

func TestBuildLoadSearch(t *testing.T) {
	ix := load(t, writeFixture(t, public))
	if ix.Dim() != 4 {
		t.Errorf("Dim = %d, want 4", ix.Dim())
	}
	hits, err := ix.Search(query, "Lambda?", public, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got, want := ids(hits), []uint32{2, 0, 1}; !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	if want := 1.0/63 + 1.0/61; math.Abs(float64(hits[0].Score)-want) > 1e-6 {
		t.Errorf("score = %v, want %v", hits[0].Score, want)
	}
}

func TestPublicCallerNeverGetsPrivateChunk(t *testing.T) {
	ix := load(t, writeFixture(t, invitee))
	hits, err := ix.Search(query, "lambda", public, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if slices.Contains(ids(hits), 2) {
		t.Errorf("public search returned private chunk 2: %v", ids(hits))
	}
	hits, _ = ix.Search(query, "lambda", public|invitee, 10)
	if !slices.Contains(ids(hits), 2) {
		t.Errorf("invitee search missed chunk 2: %v", ids(hits))
	}
}

func TestSearchArguments(t *testing.T) {
	ix := load(t, writeFixture(t, public))
	if hits, err := ix.Search(query, "go", public, 0); err != nil || len(hits) != 0 {
		t.Errorf("k=0: hits %v, err %v; want none, nil", hits, err)
	}
	if hits, err := ix.Search(query, "", public, 1); err != nil || len(hits) != 1 {
		t.Errorf("k=1: hits %v, err %v; want one hit", hits, err)
	}
	// The error must name the actual problem, not just fail.
	for name, tc := range map[string]struct {
		vec     []float32
		mention string
	}{
		"short": {[]float32{1, 0, 0}, "4"},
		"long":  {[]float32{1, 0, 0, 0, 0}, "4"},
		"nil":   {nil, "4"},
		"nan":   {[]float32{float32(math.NaN()), 0, 0, 0}, "non-finite"},
		"inf":   {[]float32{float32(math.Inf(1)), 0, 0, 0}, "non-finite"},
	} {
		_, err := ix.Search(tc.vec, "go", public, 10)
		if err == nil || !strings.Contains(err.Error(), tc.mention) {
			t.Errorf("%s vector: err = %v, want it to mention %q", name, err, tc.mention)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.idx")); err == nil {
		t.Error("missing file: want an error")
	}
	bad := filepath.Join(t.TempDir(), "bad.idx")
	if err := os.WriteFile(bad, []byte("not an index at all, but long enough for a header"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(bad)
	if err == nil || !strings.Contains(err.Error(), "magic") {
		t.Errorf("corrupt file: err = %v, want it to mention magic", err)
	}
}

func TestBuilderErrors(t *testing.T) {
	if _, err := NewBuilder(0); err == nil {
		t.Error("dim 0: want an error")
	}
	b, err := NewBuilder(2)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	cases := []struct {
		name   string
		text   string
		vec    []float32
		access uint32
	}{
		{"zero vector", "t", []float32{0, 0}, public},
		{"wrong length", "t", []float32{1, 0, 0}, public},
		{"nil vector", "t", nil, public},
		{"no access bits", "t", []float32{1, 0}, 0},
		{"NUL in text", "before\x00after", []float32{1, 0}, public},
	}
	for _, c := range cases {
		if err := b.Add(c.text, c.vec, c.access); err == nil {
			t.Errorf("%s: want an error", c.name)
		}
	}
	if err := b.Write(filepath.Join(t.TempDir(), "no", "such", "dir.idx")); err == nil {
		t.Error("write to missing directory: want an error")
	}
}

func TestUseAfterClose(t *testing.T) {
	ix, err := Load(writeFixture(t, public))
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ix.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := ix.Search(query, "go", public, 10); !errors.Is(err, ErrClosed) {
		t.Errorf("Search after Close: err = %v, want ErrClosed", err)
	}
}

// Run with -race: many goroutines search one index and must all get the
// single-threaded answer.
func TestConcurrentSearches(t *testing.T) {
	ix := load(t, writeFixture(t, public))
	want, err := ix.Search(query, "lambda go", public, 10)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				got, err := ix.Search(query, "lambda go", public, 10)
				if err != nil || !slices.Equal(got, want) {
					errs <- "mismatch"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// Closing while searches are in flight must never free memory a search is
// still reading: each search either finishes normally or gets ErrClosed.
func TestCloseDuringSearches(t *testing.T) {
	ix, err := Load(writeFixture(t, public))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 500; i++ {
				if _, err := ix.Search(query, "go", public, 10); err != nil && !errors.Is(err, ErrClosed) {
					t.Errorf("unexpected error: %v", err)
					return
				}
			}
		}()
	}
	close(start)
	ix.Close()
	wg.Wait()
}

// Review finding: the output buffer was sized from k alone, so a huge k
// allocated gigabytes. It must be sized by what the index can return.
func TestHugeKAllocatesOnlyWhatCanBeReturned(t *testing.T) {
	ix := load(t, writeFixture(t, public))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	hits, err := ix.Search(query, "go", public, 10_000_000)
	runtime.ReadMemStats(&after)
	if err != nil || len(hits) != 3 {
		t.Fatalf("hits %v, err %v; want 3 hits", ids(hits), err)
	}
	if d := after.TotalAlloc - before.TotalAlloc; d > 1<<20 {
		t.Errorf("k = 1e7 on a 3-chunk index allocated %d bytes", d)
	}
}

// Review finding: C.CString cuts a string at its first NUL, so a path with a
// NUL silently named a different file.
func TestPathsWithNULAreRejected(t *testing.T) {
	dir := t.TempDir()
	cut := filepath.Join(dir, "index.idx")
	if _, err := Load(cut + "\x00.tmp"); err == nil || !strings.Contains(err.Error(), "contains a NUL byte") {
		t.Errorf("Load: err = %v, want \"contains a NUL byte\"", err)
	}
	b, err := NewBuilder(2)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Add("x", []float32{1, 0}, public); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(cut + "\x00.tmp"); err == nil || !strings.Contains(err.Error(), "contains a NUL byte") {
		t.Errorf("Write: err = %v, want \"contains a NUL byte\"", err)
	}
	if _, err := os.Stat(cut); !os.IsNotExist(err) {
		t.Errorf("Write created %s from the cut-short path", cut)
	}
}

// Review finding: query text was cut at its first NUL. The tokenizer treats
// NUL as a separator, so the whole text must search as if NUL were a space.
func TestQueryTextWithNULSearchesAllOfIt(t *testing.T) {
	ix := load(t, writeFixture(t, public))
	want, err := ix.Search([]float32{0, 0, 0, 0}, "go lambda", public, 10)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ix.Search([]float32{0, 0, 0, 0}, "go\x00lambda", public, 10)
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("with NUL: %v (err %v), want %v", ids(got), err, ids(want))
	}
}
