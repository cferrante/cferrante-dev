package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cferrante/cferrante-dev/evals"
	"github.com/cferrante/cferrante-dev/orchestrator/judge"
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
	for _, want := range []string{"Question:", "Expected answer:", "Sources:", "Answer:", "please type one of the letters", "1 of 2 labelled",
		"saying what the documents don't cover"} {
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

// fakeGrader returns the given verdict for every answer and counts calls;
// the first fails calls fail, as a rate-limited judge does.
type fakeGrader struct {
	verdicts     map[string]judge.Result // by answer text
	fails, calls int
}

func (f *fakeGrader) Judge(_ context.Context, in judge.Input) (judge.Result, error) {
	f.calls++
	if f.calls <= f.fails {
		return judge.Result{}, errors.New("HTTP 429")
	}
	return f.verdicts[in.Answer], nil
}

func withGrader(g evals.Grader) deps {
	return deps{
		newGrader: func(context.Context) (evals.Grader, error) { return g, nil },
		sleep:     func(context.Context, time.Duration) {},
	}
}

// -rejudge grades the sampled answers again with today's judge and compares
// those verdicts with the labels, leaving the sample as it was.
func TestRejudgeComparesTheNewVerdictsWithTheLabels(t *testing.T) {
	dir := t.TempDir()
	detail := detailFile(t, dir)
	// Label both answers grounded and answering, though the judge failed g-2.
	if err := run(args(dir, detail), strings.NewReader("g\ny\n\ng\ny\n\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "labels", "sample.jsonl"))
	g := &fakeGrader{fails: 1, verdicts: map[string]judge.Result{
		"Relay does [1].":       {Verdict: judge.Grounded, AnswersQuestion: "yes"},
		"Kiln bakes bread [1].": {Verdict: judge.Grounded, AnswersQuestion: "no"},
	}}
	var out bytes.Buffer
	if err := runDeps(args(dir, detail, "-report", "-rejudge"), noEnv, strings.NewReader(""), &out, withGrader(g)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"re-judged 2 answers", "verdict: 2/2 (100%)", "answers_question: 1/2 (50%)", "pass or fail: 1/2 (50%)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if g.calls != 3 {
		t.Errorf("%d judge calls, want 3 (one retried)", g.calls)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "labels", "sample.jsonl")); !bytes.Equal(before, after) {
		t.Error("re-judging changed the stored sample")
	}
}

// The fresh sample confirms the fix on answers whose labels didn't shape
// it: none of its questions may have been sampled before.
func TestAFreshSampleLeavesOutQuestionsAlreadySampled(t *testing.T) {
	dir := t.TempDir()
	detail := detailFile(t, dir)
	if err := run(args(dir, detail, "-per-side", "0"), strings.NewReader("q\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "labels", "sample.jsonl"), []byte(`{"id":"g-1","answer":"Relay does [1].","verdict":"grounded","answers_question":"yes","passed":true}`+"\n"), 0o644)
	if err := run(args(dir, detail, "-name", "fresh"), strings.NewReader("q\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "labels", "fresh-sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"id":"g-1"`)) || !bytes.Contains(raw, []byte(`"id":"g-2"`)) {
		t.Errorf("fresh sample:\n%s", raw)
	}
}
func noEnv(string) string { return "" }
