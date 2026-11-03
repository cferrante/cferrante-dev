package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

const fixture = "../../corpus/testdata/corpus"

func TestIndexerBuildsALoadableIndex(t *testing.T) {
	out := filepath.Join(t.TempDir(), "corpus.idx")
	var stdout bytes.Buffer
	if err := run([]string{"-corpus", fixture, "-index", out, "-embedder", "hash"}, envOf(nil), &stdout, nil); err != nil {
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

// The indexer reads the same variables as make dev (the known issue from 3.5).
func TestIndexerReadsTheEnvironment(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env.idx")
	var stdout bytes.Buffer
	env := envOf(map[string]string{"CORPUS_DIRS": fixture, "INDEX_PATH": out, "EMBEDDER": "hash", "EMBED_DIM": "64"})
	if err := run(nil, env, &stdout, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "with hash-v1-64 -> "+out) {
		t.Errorf("output %q", stdout.String())
	}
}

type fakeTitan struct{}

func (fakeTitan) InvokeModel(_ context.Context, in *bedrockruntime.InvokeModelInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	v := make([]float32, 512)
	v[len(in.Body)%512] = 1
	raw, _ := json.Marshal(map[string]any{"embedding": v, "inputTextTokenCount": 10})
	return &bedrockruntime.InvokeModelOutput{Body: raw}, nil
}

func TestIndexerWithTitanReportsTokens(t *testing.T) {
	var stdout bytes.Buffer
	args := []string{"-corpus", fixture, "-index", filepath.Join(t.TempDir(), "t.idx"), "-embedder", "titan"}
	if err := run(args, envOf(nil), &stdout, func(string, string) (embed.Invoker, error) { return fakeTitan{}, nil }); err != nil {
		t.Fatal(err)
	}
	if want := "with bedrock/amazon.titan-embed-text-v2:0/512"; !strings.Contains(stdout.String(), want) || !strings.Contains(stdout.String(), "50 Titan tokens") {
		t.Errorf("output %q, want %q and 50 Titan tokens (5 chunks x 10)", stdout.String(), want)
	}
}

func TestIndexerArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown embedder": {"-corpus", fixture, "-index", "x.idx", "-embedder", "magic"},
		"empty corpus dir": {"-corpus", t.TempDir(), "-index", filepath.Join(t.TempDir(), "x.idx"), "-embedder", "hash"},
		"missing folder":   {"-corpus", fixture + ":nope", "-index", filepath.Join(t.TempDir(), "x.idx"), "-embedder", "hash"},
		"no folders":       {"-corpus", ":", "-index", filepath.Join(t.TempDir(), "x.idx"), "-embedder", "hash"},
	} {
		if err := run(args, envOf(nil), &bytes.Buffer{}, nil); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

type failingTitan struct{}

func (failingTitan) InvokeModel(context.Context, *bedrockruntime.InvokeModelInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	return nil, errors.New("ExpiredToken")
}

// Only a Titan failure suggests logging in; a corpus mistake is reported as itself.
func TestIndexerLoginHintOnlyForTitanFailures(t *testing.T) {
	newBedrock := func(string, string) (embed.Invoker, error) { return failingTitan{}, nil }
	err := run([]string{"-corpus", fixture, "-index", filepath.Join(t.TempDir(), "t.idx"), "-embedder", "titan"}, envOf(nil), &bytes.Buffer{}, newBedrock)
	if err == nil || !strings.Contains(err.Error(), "aws sso login --profile cferrante-dev-admin") {
		t.Errorf("titan failure: err = %v, want the login command", err)
	}
	err = run([]string{"-corpus", "nope", "-index", filepath.Join(t.TempDir(), "t.idx"), "-embedder", "titan"}, envOf(nil), &bytes.Buffer{}, newBedrock)
	if err == nil || strings.Contains(err.Error(), "sso") {
		t.Errorf("missing folder: err = %v, want no login hint", err)
	}
}
