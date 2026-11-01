package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
)

func TestIndexerBuildsALoadableIndex(t *testing.T) {
	out := filepath.Join(t.TempDir(), "corpus.idx")
	var stdout bytes.Buffer
	if err := run([]string{"-corpus", "../../corpus/testdata/corpus", "-out", out}, &stdout); err != nil {
		t.Fatal(err)
	}
	if want := "indexed 5 chunks (4 public, 1 invitee) with hash-v1-256"; !strings.Contains(stdout.String(), want) {
		t.Errorf("output %q, want it to contain %q", stdout.String(), want)
	}
	r, err := retrieval.Load(out, embed.Hash{D: 256})
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

func TestIndexerArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"no corpus":        {"-out", "x.idx"},
		"no out":           {"-corpus", "."},
		"unknown embedder": {"-corpus", ".", "-out", "x.idx", "-embedder", "magic"},
		"empty corpus dir": {"-corpus", t.TempDir(), "-out", filepath.Join(t.TempDir(), "x.idx")},
	} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
