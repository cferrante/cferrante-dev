package evals

// Judge agreement (design §10, step 6.3c): your labels on a sample of
// answers, compared with the judge's verdicts on the same answers.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
)

// Label is your judgement of one sampled answer.
type Label struct {
	ID              string `json:"id"`
	Verdict         string `json:"verdict"`          // grounded | partially_grounded | ungrounded
	AnswersQuestion string `json:"answers_question"` // yes | partly | no
	Note            string `json:"note,omitempty"`
}

// Passed is the rule the runner applies to the judge's verdict; Agree adds
// the citation check, which is code, to both sides.
func (l Label) Passed() bool { return l.Verdict == "grounded" && l.AnswersQuestion == "yes" }

// Sample picks up to perSide answers the judge passed and up to perSide it
// didn't, never ones that errored, and mixes them so passes and failures
// don't come in blocks. The seed is fixed: the same run gives the same sample.
func Sample(rows []DetailRow, perSide int) []DetailRow {
	var passed, failed []DetailRow
	for _, r := range rows {
		switch {
		case r.Err != "":
		case r.Passed:
			passed = append(passed, r)
		default:
			failed = append(failed, r)
		}
	}
	rng := rand.New(rand.NewPCG(2026, 11))
	pick := func(rs []DetailRow) []DetailRow {
		slices.SortFunc(rs, func(a, b DetailRow) int { return strings.Compare(a.ID, b.ID) })
		rng.Shuffle(len(rs), func(i, j int) { rs[i], rs[j] = rs[j], rs[i] })
		return rs[:min(perSide, len(rs))]
	}
	out := append(pick(passed), pick(failed)...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// LoadLabels reads JSON Lines labels, refusing anything that would quietly
// change the comparison: an unknown field or value, a duplicate, or an id
// that isn't in the sample.
func LoadLabels(raw []byte, sample map[string]bool) ([]Label, error) {
	var out []Label
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var l Label
		if err := dec.Decode(&l); err != nil {
			return nil, fmt.Errorf("label line %d: %w", n, err)
		}
		switch {
		case !sample[l.ID]:
			return nil, fmt.Errorf("label line %d: %q isn't in the sample", n, l.ID)
		case seen[l.ID]:
			return nil, fmt.Errorf("label line %d: %q is labelled twice", n, l.ID)
		case l.Verdict != "grounded" && l.Verdict != "partially_grounded" && l.Verdict != "ungrounded":
			return nil, fmt.Errorf("label line %d: %s: verdict %q", n, l.ID, l.Verdict)
		case l.AnswersQuestion != "yes" && l.AnswersQuestion != "partly" && l.AnswersQuestion != "no":
			return nil, fmt.Errorf("label line %d: %s: answers_question %q, want yes, partly or no", n, l.ID, l.AnswersQuestion)
		}
		seen[l.ID] = true
		out = append(out, l)
	}
	return out, sc.Err()
}

// Disagreement is one answer where you and the judge differ.
type Disagreement struct {
	ID                   string
	Judge, You           string // "verdict, answers_question"
	Note, JudgeReasoning string
}

func (d Disagreement) String() string {
	s := fmt.Sprintf("%s  judge: %s  you: %s", d.ID, d.Judge, d.You)
	if d.Note != "" {
		s += "  (your note: " + d.Note + ")"
	}
	return s
}

// Agreement counts, out of N labelled answers, how many match the judge on
// the verdict, on answers_question, and on pass or fail.
type Agreement struct {
	N, Verdict, Answers, PassFail int
	Disagreements                 []Disagreement
}

// Trusted: at least 80% agreement on both the verdict and answers_question.
func (a Agreement) Trusted() bool {
	return a.N > 0 && 5*a.Verdict >= 4*a.N && 5*a.Answers >= 4*a.N
}

// Agree compares every sampled answer's verdict with your label; an answer
// you haven't labelled is an error, so a partial job can't pass as a result.
func Agree(sample []DetailRow, labels []Label) (Agreement, error) {
	byID := map[string]Label{}
	for _, l := range labels {
		byID[l.ID] = l
	}
	var missing []string
	a := Agreement{}
	for _, r := range sample {
		l, ok := byID[r.ID]
		if !ok {
			missing = append(missing, r.ID)
			continue
		}
		a.N++
		sameVerdict, sameAnswers := r.Verdict == l.Verdict, r.AnswersQuestion == l.AnswersQuestion
		if sameVerdict {
			a.Verdict++
		}
		if sameAnswers {
			a.Answers++
		}
		// The citation rule is code, not judgement: apply it to your label too.
		ans := Answer{Item: Item{ExpectedChunks: r.ExpectedChunks}, Text: r.Answer}
		if r.Passed == (l.Passed() && (ans.Cited() || ans.declines())) {
			a.PassFail++
		}
		if !sameVerdict || !sameAnswers {
			a.Disagreements = append(a.Disagreements, Disagreement{ID: r.ID,
				Judge: r.Verdict + ", " + r.AnswersQuestion, You: l.Verdict + ", " + l.AnswersQuestion,
				Note: l.Note, JudgeReasoning: r.Reasoning})
		}
	}
	if len(missing) > 0 {
		return Agreement{}, fmt.Errorf("%d sampled answers aren't labelled yet: %s", len(missing), strings.Join(missing, ", "))
	}
	return a, nil
}
