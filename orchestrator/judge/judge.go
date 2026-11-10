// Package judge grades an answer against the sources it was given (design
// §7): is every claim supported, and does it answer the question? The eval
// runner (step 6.3) uses it now and the live judge (Phase 8) later. A
// verdict that can't be read or contradicts itself is an error, never a pass.
package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// ErrBadVerdict marks a verdict that can't be read or contradicts itself.
var ErrBadVerdict = errors.New("judge: bad verdict")

// Verdict says how much of an answer its sources support.
type Verdict string

const (
	Grounded          Verdict = "grounded"
	PartiallyGrounded Verdict = "partially_grounded"
	Ungrounded        Verdict = "ungrounded"
)

// Source is one retrieved chunk, in the order the answer turn numbered it,
// with the document title the answering model also saw.
type Source struct{ ID, Title, Text string }

// Input is what the judge sees. Expected is the golden set's expected
// answer; the live judge has none.
type Input struct {
	Question, Answer string
	Sources          []Source
	Expected         string
}

// Result is a checked verdict and what it cost.
type Result struct {
	Verdict           Verdict
	UnsupportedClaims []string
	AnswersQuestion   string // yes | partly | no | not_applicable
	Reasoning         string // logged, never shown to visitors
	Model             string
	TokensIn          int
	TokensOut         int
}

// Judge grades answers with Model, which should be a different model from
// the one that wrote them.
type Judge struct{ Model model.Model }

// schema is the strict output Groq is held to (step 5.4): every property
// required, nothing extra.
var schema = json.RawMessage(`{"type":"object","properties":{
"verdict":{"type":"string","enum":["grounded","partially_grounded","ungrounded"]},
"unsupported_claims":{"type":"array","items":{"type":"string"}},
"answers_question":{"type":"string","enum":["yes","partly","no","not_applicable"]},
"reasoning":{"type":"string"}},
"required":["verdict","unsupported_claims","answers_question","reasoning"],"additionalProperties":false}`)

const system = `You check an AI assistant's answer about a person's professional work. The assistant may use only the numbered sources it was given.

Return JSON with these fields:
- verdict: "grounded" if every factual claim in the answer is supported by the sources; "partially_grounded" if some are and some aren't; "ungrounded" if the main claims aren't. Greetings, small talk, questions back to the visitor, and saying the sources don't cover something are not claims, so a polite decline is grounded. A claim is supported only if a source states it or it follows directly from one; general knowledge doesn't count. A citation like [2] points to source [2]; a claim cited to the wrong source is unsupported.
- unsupported_claims: each unsupported claim, quoted or closely paraphrased from the answer. Empty exactly when the verdict is "grounded".
- answers_question: when an expected answer is given, "yes" if the answer covers its main points, "partly" if it covers some, "no" if it covers none or contradicts it. When the expected answer is "none", use "not_applicable".
- reasoning: one or two sentences explaining the verdict.

Judge only what the answer says against the sources and the expected answer. Don't reward length or tone.`

// Judge asks the model for a verdict and checks it.
func (j Judge) Judge(ctx context.Context, in Input) (Result, error) {
	var text strings.Builder
	res, err := j.Model.Stream(ctx, model.Request{
		System:         system,
		Messages:       []model.Message{{Role: model.User, Content: message(in)}},
		ResponseSchema: schema,
	}, func(e model.Event) error {
		if e.Kind == model.Text {
			text.WriteString(e.Text)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	out, err := parse(text.String(), in.Expected != "")
	if err != nil {
		return Result{}, err
	}
	out.Model, out.TokensIn, out.TokensOut = res.Model, res.TokensIn, res.TokensOut
	return out, nil
}

func message(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\n", in.Question)
	if len(in.Sources) == 0 {
		b.WriteString("Sources: none\n\n")
	} else {
		b.WriteString("Sources:\n")
		for i, s := range in.Sources {
			fmt.Fprintf(&b, "[%d] %s: %s\n%s\n\n", i+1, s.ID, s.Title, s.Text)
		}
	}
	fmt.Fprintf(&b, "Answer:\n%s\n\n", in.Answer)
	if in.Expected == "" {
		b.WriteString("Expected answer: none")
	} else {
		fmt.Fprintf(&b, "Expected answer: %s", in.Expected)
	}
	return b.String()
}

// parse reads the verdict strictly: every field present, no others, values
// from the lists, and claims consistent with the verdict.
func parse(text string, hasExpected bool) (Result, error) {
	var v struct {
		Verdict           *Verdict  `json:"verdict"`
		UnsupportedClaims *[]string `json:"unsupported_claims"`
		AnswersQuestion   *string   `json:"answers_question"`
		Reasoning         *string   `json:"reasoning"`
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return Result{}, fmt.Errorf("%w: %v (%d bytes)", ErrBadVerdict, err, len(text))
	}
	if v.Verdict == nil || v.UnsupportedClaims == nil || v.AnswersQuestion == nil || v.Reasoning == nil {
		return Result{}, fmt.Errorf("%w: a field is missing", ErrBadVerdict)
	}
	switch *v.Verdict {
	case Grounded, PartiallyGrounded, Ungrounded:
	default:
		return Result{}, fmt.Errorf("%w: verdict %q", ErrBadVerdict, *v.Verdict)
	}
	if (*v.Verdict == Grounded) != (len(*v.UnsupportedClaims) == 0) {
		return Result{}, fmt.Errorf("%w: verdict %s with %d unsupported claims", ErrBadVerdict, *v.Verdict, len(*v.UnsupportedClaims))
	}
	switch a := *v.AnswersQuestion; {
	case a == "not_applicable" && hasExpected, a != "not_applicable" && !hasExpected:
		return Result{}, fmt.Errorf("%w: answers_question %q with expected answer given: %v", ErrBadVerdict, a, hasExpected)
	case a != "yes" && a != "partly" && a != "no" && a != "not_applicable":
		return Result{}, fmt.Errorf("%w: answers_question %q", ErrBadVerdict, a)
	}
	return Result{Verdict: *v.Verdict, UnsupportedClaims: *v.UnsupportedClaims,
		AnswersQuestion: *v.AnswersQuestion, Reasoning: *v.Reasoning}, nil
}
