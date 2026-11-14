package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportsEachQuestionAndTheScore(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "g.jsonl")
	os.WriteFile(golden, []byte(strings.Join([]string{
		`{"id":"g-hit","question":"Which tool parses telephone call records from switches?","caller":"anonymous","expected_chunks":["relay#summary"]}`,
		`{"id":"g-miss","question":"How are kiln firings scheduled for pottery studios?","caller":"anonymous","expected_chunks":["spindle#summary"]}`,
		`{"id":"g-decline","question":"What's the weather in Paris?","caller":"anonymous"}`,
	}, "\n")), 0o644)
	var out bytes.Buffer
	err := run([]string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(dir, "e.idx"), "-golden", golden, "-results", ""}, func(string) string { return "" }, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"✓ g-hit  rank 1",
		"✗ g-miss  top 5: kiln#summary,",
		"– g-decline  no expected chunk (the answer is scored in 6.3)",
		"hit@5: 1/2 (50%)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestNoGoldenFilesIsAnError(t *testing.T) {
	err := run([]string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(t.TempDir(), "e.idx"), "-golden", filepath.Join(t.TempDir(), "*.jsonl"), "-results", ""}, func(string) string { return "" }, &bytes.Buffer{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no golden files") {
		t.Errorf("err = %v", err)
	}
}

// A held-out file must be scored on its own, or tuning the corpus to the
// main set could hide that the held-out questions didn't improve.
func TestScoresEachGoldenFileSeparately(t *testing.T) {
	dir := t.TempDir()
	main, held := filepath.Join(dir, "public.jsonl"), filepath.Join(dir, "heldout.jsonl")
	os.WriteFile(main, []byte(`{"id":"g-hit","question":"Which tool parses telephone call records from switches?","caller":"anonymous","expected_chunks":["relay#summary"]}`), 0o644)
	os.WriteFile(held, []byte(`{"id":"h-miss","question":"How are kiln firings scheduled for pottery studios?","caller":"anonymous","expected_chunks":["spindle#summary"]}`), 0o644)
	var out bytes.Buffer
	err := run([]string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(dir, "e.idx"), "-golden", main + ":" + held, "-results", ""}, func(string) string { return "" }, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"hit@5: 1/2 (50%)",
		"  " + main + ": 1/1 (100%)",
		"  " + held + ": 0/1 (0%)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func writeGolden(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// publicCorpus copies the fixture's public documents, leaving out ledger.md
// (invitee-only), so a cache may be written from it.
func publicCorpus(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "corpus")
	os.MkdirAll(dir, 0o755)
	for _, name := range []string{"relay.md", "kiln.md", "spindle.md", "harbor.md"} {
		raw, err := os.ReadFile(filepath.Join("../../testdata/corpus", name))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, name), raw, 0o644)
	}
	return dir
}

const relayQ = `{"id":"g-hit","question":"Which tool parses telephone call records from switches?","caller":"anonymous","expected_chunks":["relay#summary"]}`

func noEnv(string) string { return "" }

// A cache written by one run and read by the next gives the same score with
// no model at all: the CI path.
func TestACacheWrittenByOneRunScoresTheNext(t *testing.T) {
	dir := t.TempDir()
	golden, cache := filepath.Join(dir, "g.jsonl"), filepath.Join(dir, "cache.json")
	writeGolden(t, golden, relayQ)
	corpus := publicCorpus(t)
	common := []string{"-corpus", corpus, "-index", filepath.Join(dir, "e.idx"), "-golden", golden, "-results", ""}
	if err := run(append([]string{"-embedder", "hash", "-write-cache", cache}, common...), noEnv, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(append([]string{"-embedder", "cache", "-embed-cache", cache}, common...), noEnv, &out, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"with hash-v1-256", "✓ g-hit  rank 1", "hit@5: 1/1 (100%)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// Vectors of private text can be partly turned back into the text, so they
// must never reach the committed cache.
func TestWritingACacheRefusesPrivateText(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache.json")
	public := filepath.Join(dir, "g.jsonl")
	writeGolden(t, public, relayQ)
	private := filepath.Join(dir, "corpus", "private", "golden", "heldout.jsonl")
	writeGolden(t, private, `{"id":"h-1","question":"Which tool parses call records?","caller":"anonymous","expected_chunks":["relay#summary"]}`)
	invitee := filepath.Join(dir, "inv.jsonl")
	writeGolden(t, invitee, `{"id":"i-1","question":"Which tool parses call records?","caller":"invitee","expected_chunks":["relay#summary"]}`)
	privDir := filepath.Join(dir, "x", "corpus", "private")
	os.MkdirAll(privDir, 0o755)
	for name, tc := range map[string]struct {
		corpus, golden, want string
	}{
		"golden file under corpus/private":   {publicCorpus(t), public + ":" + private, "corpus/private"},
		"corpus folder under corpus/private": {publicCorpus(t) + ":" + privDir, public, "corpus/private"},
		"invitee question":                   {publicCorpus(t), invitee, "invitee"},
		"invitee chunk":                      {"../../testdata/corpus", public, "invitee"},
	} {
		err := run([]string{"-embedder", "hash", "-write-cache", cache, "-corpus", tc.corpus, "-golden", tc.golden,
			"-index", filepath.Join(dir, "e.idx"), "-results", ""}, noEnv, &bytes.Buffer{}, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q", name, err, tc.want)
		}
		if _, statErr := os.Stat(cache); statErr == nil {
			t.Fatalf("%s: a cache was written", name)
		}
	}
}

func TestTheGateFailsBelowTheMinimumAndPassesAtIt(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "g.jsonl")
	writeGolden(t, golden, relayQ,
		`{"id":"g-miss","question":"How are kiln firings scheduled for pottery studios?","caller":"anonymous","expected_chunks":["spindle#summary"]}`)
	args := []string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(dir, "e.idx"), "-golden", golden, "-results", ""}
	if err := run(append(args, "-min", "0.5"), noEnv, &bytes.Buffer{}, nil); err != nil {
		t.Errorf("1/2 with -min 0.5: %v", err)
	}
	err := run(append(args, "-min", "0.51"), noEnv, &bytes.Buffer{}, nil)
	if err == nil || !strings.Contains(err.Error(), "hit@5 1/2 (0.50) is below the minimum 0.51") {
		t.Errorf("1/2 with -min 0.51: err = %v", err)
	}
}

func TestEachRunAppendsOneResultsLine(t *testing.T) {
	dir := t.TempDir()
	golden, results := filepath.Join(dir, "g.jsonl"), filepath.Join(dir, "results", "hitk.jsonl")
	writeGolden(t, golden, relayQ)
	args := []string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(dir, "e.idx"), "-golden", golden, "-results", results}
	for range 2 {
		if err := run(args, noEnv, &bytes.Buffer{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(results)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want 2:\n%s", len(lines), raw)
	}
	var r struct {
		Time     string                    `json:"time"`
		Commit   *string                   `json:"commit"`
		Dirty    *bool                     `json:"dirty"`
		Embedder string                    `json:"embedder"`
		K        int                       `json:"k"`
		Files    map[string]map[string]int `json:"files"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &r); err != nil {
		t.Fatal(err)
	}
	if r.Time == "" || r.Commit == nil || r.Dirty == nil || r.Embedder != "hash-v1-256" || r.K != 5 ||
		r.Files[golden]["hits"] != 1 || r.Files[golden]["scored"] != 1 {
		t.Errorf("results line %s", lines[1])
	}
}
