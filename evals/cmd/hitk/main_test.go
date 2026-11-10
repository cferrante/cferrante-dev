package main

import (
	"bytes"
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
	err := run([]string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(dir, "e.idx"), "-golden", golden}, func(string) string { return "" }, &out)
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
	err := run([]string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(t.TempDir(), "e.idx"), "-golden", filepath.Join(t.TempDir(), "*.jsonl")}, func(string) string { return "" }, &bytes.Buffer{})
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
	err := run([]string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(dir, "e.idx"), "-golden", main + ":" + held}, func(string) string { return "" }, &out)
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
