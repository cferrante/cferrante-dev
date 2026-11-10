package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/evals"
)

// detailFile writes a run's detail with two judged answers over the fixture
// corpus: one the judge passed, one it didn't.
func detailFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "answers.jsonl")
	var b bytes.Buffer
	for _, r := range []evals.DetailRow{
		{ID: "g-1", Question: "Which tool parses call records?", ExpectedAnswer: "Relay.", Answer: "Relay does [1].",
			Retrieved: []string{"relay#summary"}, Verdict: "grounded", AnswersQuestion: "yes", Reasoning: "JUDGE-REASONING-1", Passed: true},
		{ID: "g-2", Question: "What does Kiln do?", ExpectedAnswer: "Schedules firings.", Answer: "Kiln bakes bread [1].",
			Retrieved: []string{"kiln#summary"}, Verdict: "ungrounded", UnsupportedClaims: []string{"bakes bread"}, AnswersQuestion: "no", Reasoning: "JUDGE-REASONING-2"},
	} {
		line, _ := json.Marshal(r)
		b.Write(append(line, '\n'))
	}
	os.WriteFile(path, b.Bytes(), 0o600)
	return path
}

func args(dir, detail string, extra ...string) []string {
	return append([]string{"-detail", detail, "-dir", filepath.Join(dir, "labels"), "-corpus", "../../testdata/corpus"}, extra...)
}

func TestLabellingIsBlindAndSavedAsYouGo(t *testing.T) {
	dir := t.TempDir()
	detail := detailFile(t, dir)
	// Take the sample, then label each answer as the judge did, in the
	// order the sample shows them; a typo is asked again.
	if err := run(args(dir, detail), strings.NewReader("q\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "labels", "sample.jsonl"))
	keys := map[string]string{"g-1": "g\ny\nlooks right\n", "g-2": "u\nn\n\n"}
	var order []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r evals.DetailRow
		json.Unmarshal([]byte(line), &r)
		order = append(order, r.ID)
	}
	if len(order) != 2 {
		t.Fatalf("sample %v, want both answers", order)
	}
	var out bytes.Buffer
	if err := run(args(dir, detail), strings.NewReader("x\n"+keys[order[0]]+"q\n"), &out); err != nil {
		t.Fatal(err)
	}
	shown := out.String()
	for _, hidden := range []string{"JUDGE-REASONING", "ungrounded", "bakes bread\"", "verdict:"} {
		if strings.Contains(shown, hidden) {
			t.Errorf("the judge's verdict leaked while labelling (%q):\n%s", hidden, shown)
		}
	}
	if !strings.Contains(shown, "Relay parses telephone call records") && !strings.Contains(shown, "Kiln is a pottery scheduling tool") {
		t.Errorf("the source text isn't shown:\n%s", shown)
	}
	for _, want := range []string{"Question:", "Expected answer:", "Sources:", "Answer:", "please type one of the letters", "1 of 2 labelled"} {
		if !strings.Contains(shown, want) {
			t.Errorf("output lacks %q:\n%s", want, shown)
		}
	}
	// Resume: only the unlabelled answer is shown, then the report works.
	out.Reset()
	if err := run(args(dir, detail), strings.NewReader(keys[order[1]]), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "Question:") != 1 || !strings.Contains(out.String(), "2 of 2 labelled") {
		t.Errorf("resume:\n%s", out.String())
	}
	out.Reset()
	if err := run(args(dir, detail, "-report"), strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "verdict: 2/2 (100%)") || !strings.Contains(out.String(), "The judge is trusted") {
		t.Errorf("report:\n%s", out.String())
	}
}

func TestTheReportRefusesAnUnfinishedSample(t *testing.T) {
	dir := t.TempDir()
	detail := detailFile(t, dir)
	if err := run(args(dir, detail), strings.NewReader("g\ny\n\nq\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	err := run(args(dir, detail, "-report"), strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "aren't labelled yet") {
		t.Errorf("err = %v", err)
	}
}

// The sample is fixed once taken: a later run's detail file must not
// silently swap the answers being labelled.
func TestTheSampleIsTakenOnce(t *testing.T) {
	dir := t.TempDir()
	detail := detailFile(t, dir)
	if err := run(args(dir, detail), strings.NewReader("q\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "labels", "sample.jsonl"))
	os.WriteFile(detail, []byte(`{"id":"g-9","answer":"other","verdict":"grounded","answers_question":"yes","passed":true}`+"\n"), 0o600)
	if err := run(args(dir, detail), strings.NewReader("q\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(filepath.Join(dir, "labels", "sample.jsonl")); !bytes.Equal(first, second) {
		t.Error("the sample changed")
	}
}
