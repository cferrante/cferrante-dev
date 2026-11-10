package evals

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func rows(passed, failed, errored int) []DetailRow {
	var out []DetailRow
	for i := range passed {
		out = append(out, DetailRow{ID: fmt.Sprintf("p-%02d", i), Verdict: "grounded", AnswersQuestion: "yes", Passed: true})
	}
	for i := range failed {
		out = append(out, DetailRow{ID: fmt.Sprintf("f-%02d", i), Verdict: "partially_grounded", AnswersQuestion: "partly"})
	}
	for i := range errored {
		out = append(out, DetailRow{ID: fmt.Sprintf("e-%02d", i), Err: "turn: model_unavailable"})
	}
	return out
}

func ids(rs []DetailRow) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// Both kinds of mistake must be measurable, so the sample holds passes and
// failures alike, never answers that errored, and is the same every time.
func TestTheSampleHoldsPassesAndFailuresAndIsRepeatable(t *testing.T) {
	all := rows(40, 15, 3)
	s := Sample(all, 10)
	var passed, failed int
	for _, r := range s {
		switch {
		case r.Err != "":
			t.Errorf("errored answer %s sampled", r.ID)
		case r.Passed:
			passed++
		default:
			failed++
		}
	}
	if passed != 10 || failed != 10 {
		t.Errorf("%d passed and %d failed, want 10 and 10", passed, failed)
	}
	if !slices.Equal(ids(s), ids(Sample(all, 10))) {
		t.Error("two samples of the same run differ")
	}
	if got := Sample(rows(3, 2, 0), 10); len(got) != 5 {
		t.Errorf("short run: %d sampled, want all 5", len(got))
	}
}

func TestLabelsWithBadValuesAreRefused(t *testing.T) {
	sample := map[string]bool{"g-001": true, "g-002": true}
	for name, raw := range map[string]string{
		"unknown verdict":  `{"id":"g-001","verdict":"mostly","answers_question":"yes"}`,
		"unknown answers":  `{"id":"g-001","verdict":"grounded","answers_question":"maybe"}`,
		"not_applicable":   `{"id":"g-001","verdict":"grounded","answers_question":"not_applicable"}`,
		"id not in sample": `{"id":"g-999","verdict":"grounded","answers_question":"yes"}`,
		"duplicate":        `{"id":"g-001","verdict":"grounded","answers_question":"yes"}` + "\n" + `{"id":"g-001","verdict":"ungrounded","answers_question":"no"}`,
		"unknown field":    `{"id":"g-001","verdict":"grounded","answers_question":"yes","score":3}`,
	} {
		if _, err := LoadLabels([]byte(raw), sample); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	got, err := LoadLabels([]byte(`{"id":"g-001","verdict":"grounded","answers_question":"yes","note":"fine"}`+"\n"), sample)
	if err != nil || len(got) != 1 || got[0].Note != "fine" {
		t.Errorf("valid label: %v, %v", got, err)
	}
}

func TestAgreementCountsEachFieldAndListsDisagreements(t *testing.T) {
	sample := []DetailRow{
		{ID: "a", Verdict: "grounded", AnswersQuestion: "yes", Passed: true},
		{ID: "b", Verdict: "partially_grounded", AnswersQuestion: "yes"},
		{ID: "c", Verdict: "grounded", AnswersQuestion: "partly"},
		{ID: "d", Verdict: "ungrounded", AnswersQuestion: "no"},
		{ID: "e", Verdict: "grounded", AnswersQuestion: "yes", Passed: true},
	}
	labels := []Label{
		{ID: "a", Verdict: "grounded", AnswersQuestion: "yes"},
		{ID: "b", Verdict: "grounded", AnswersQuestion: "yes", Note: "reflection, not a claim"}, // verdict and pass differ
		{ID: "c", Verdict: "grounded", AnswersQuestion: "partly"},
		{ID: "d", Verdict: "ungrounded", AnswersQuestion: "partly"}, // answers differ
		{ID: "e", Verdict: "grounded", AnswersQuestion: "yes"},
	}
	a, err := Agree(sample, labels)
	if err != nil {
		t.Fatal(err)
	}
	if a.N != 5 || a.Verdict != 4 || a.Answers != 4 || a.PassFail != 4 {
		t.Errorf("agreement %+v, want 5 / 4 / 4 / 4", a)
	}
	if !a.Trusted() {
		t.Error("4/5 on both fields (80%) is not trusted")
	}
	if len(a.Disagreements) != 2 || a.Disagreements[0].ID != "b" || !strings.Contains(a.Disagreements[0].String(), "reflection, not a claim") {
		t.Errorf("disagreements %+v", a.Disagreements)
	}
	labels[2].AnswersQuestion = "no" // now 3/5 on answers_question
	if a, _ := Agree(sample, labels); a.Trusted() {
		t.Error("3/5 on answers_question (60%) is trusted")
	}
}

func TestAgreementRefusesAnUnlabelledSample(t *testing.T) {
	_, err := Agree([]DetailRow{{ID: "a"}, {ID: "b"}}, []Label{{ID: "a", Verdict: "grounded", AnswersQuestion: "yes"}})
	if err == nil || !strings.Contains(err.Error(), "b") {
		t.Errorf("err = %v, want one naming the unlabelled b", err)
	}
}
