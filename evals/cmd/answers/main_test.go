package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// textModel answers every request with the same text.
type textModel struct{ text string }

func (m textModel) Stream(_ context.Context, _ model.Request, onEvent func(model.Event) error) (model.Result, error) {
	return model.Result{Model: "fake", StopReason: "stop"}, onEvent(model.Event{Kind: model.Text, Text: m.text})
}

func fakes(answer, verdict string) deps {
	return deps{newModels: func(context.Context, settings) (model.Model, model.Model, error) {
		return textModel{answer}, textModel{verdict}, nil
	}}
}

const grounded = `{"verdict":"grounded","unsupported_claims":[],"answers_question":"yes","reasoning":"r"}`

func setup(t *testing.T) (golden, results, detail string, args []string) {
	t.Helper()
	dir := t.TempDir()
	golden = filepath.Join(dir, "g.jsonl")
	os.WriteFile(golden, []byte(`{"id":"g-1","question":"Which tool parses telephone call records?","caller":"anonymous","expected_chunks":["relay#summary"],"expected_answer":"Relay."}
{"id":"g-2","question":"Who keeps the ledger?","caller":"invitee","expected_chunks":["ledger#summary"],"expected_answer":"A private answer."}`), 0o644)
	results, detail = filepath.Join(dir, "results.jsonl"), filepath.Join(dir, "build", "answers.jsonl")
	args = []string{"-embedder", "hash", "-corpus", "../../testdata/corpus", "-index", filepath.Join(dir, "e.idx"),
		"-golden", golden, "-results", results, "-detail", detail, "-owner", "Colleen Ferrante", "-owner-first", "Colleen", "-retry-wait", "1ms"}
	return
}

func TestARunAnswersJudgesAndWritesBothFiles(t *testing.T) {
	_, results, detail, args := setup(t)
	var out bytes.Buffer
	if err := run(args, func(string) string { return "" }, &out, fakes("Relay parses them [1].", grounded)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 questions from", "prompt sha256=", "✓ g-1", "✓ g-2", "2/2 passed", "0 uncited"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	line, err := os.ReadFile(results)
	if err != nil || !bytes.Contains(line, []byte(`"prompt_sha":"`)) || bytes.Contains(line, []byte("ledger")) {
		t.Errorf("results line (%v):\n%s", err, line)
	}
	text, err := os.ReadFile(detail)
	if err != nil || bytes.Count(text, []byte("\n")) != 2 || !bytes.Contains(text, []byte("Relay parses them [1].")) {
		t.Errorf("detail file (%v):\n%s", err, text)
	}
}

func TestIdsPicksQuestionsAndRefusesUnknownOnes(t *testing.T) {
	_, _, _, args := setup(t)
	var out bytes.Buffer
	if err := run(append(args, "-ids", "g-2"), func(string) string { return "" }, &out, fakes("x", grounded)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 questions from") || strings.Contains(out.String(), "g-1") {
		t.Errorf("-ids g-2 ran:\n%s", out.String())
	}
	err := run(append(args, "-ids", "g-1,g-9"), func(string) string { return "" }, &bytes.Buffer{}, fakes("x", grounded))
	if err == nil || !strings.Contains(err.Error(), "g-9") {
		t.Errorf("unknown id: err = %v", err)
	}
}

// A judge that returns nonsense must show up as an error, not a pass.
func TestABadVerdictShowsAsAnError(t *testing.T) {
	_, _, _, args := setup(t)
	var out bytes.Buffer
	if err := run(append(args, "-ids", "g-1"), func(string) string { return "" }, &out, fakes("x", `{"verdict":"great"}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "! g-1") || !strings.Contains(out.String(), "bad verdict") || !strings.Contains(out.String(), "0/1 passed, 1 errors") {
		t.Errorf("output:\n%s", out.String())
	}
}
