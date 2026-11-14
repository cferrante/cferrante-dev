package judge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// fakeModel returns reply as one Text event and keeps the request.
type fakeModel struct {
	reply string
	err   error
	got   model.Request
}

func (f *fakeModel) Stream(_ context.Context, req model.Request, onEvent func(model.Event) error) (model.Result, error) {
	f.got = req
	if f.err != nil {
		return model.Result{}, f.err
	}
	if err := onEvent(model.Event{Kind: model.Reasoning, Text: "thinking"}); err != nil {
		return model.Result{}, err
	}
	return model.Result{Model: "fake-judge", TokensIn: 100, TokensOut: 20}, onEvent(model.Event{Kind: model.Text, Text: f.reply})
}

var input = Input{
	Question: "Has Colleen worked in telecom?",
	Answer:   "Yes, at Vartec Telecom [1].",
	Sources:  []Source{{ID: "exp-vartec#call-record-conversion", Title: "Senior Programmer, Vartec Telecom (Jan 2001 – Feb 2004)", Text: "At Vartec Telecom, Colleen re-architected…"}},
	Expected: "Yes: Vartec Telecom, 2001–2004.",
}

func TestAValidVerdictIsReturned(t *testing.T) {
	f := &fakeModel{reply: `{"verdict":"partially_grounded","unsupported_claims":["She led a team of 40"],"answers_question":"partly","reasoning":"r"}`}
	got, err := Judge{Model: f}.Judge(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != PartiallyGrounded || len(got.UnsupportedClaims) != 1 || got.AnswersQuestion != "partly" ||
		got.Reasoning != "r" || got.Model != "fake-judge" || got.TokensIn != 100 || got.TokensOut != 20 {
		t.Errorf("result %+v", got)
	}
}

// A judge that can't be read must never count as a pass.
func TestABadVerdictIsAnErrorNeverAPass(t *testing.T) {
	for name, tc := range map[string]struct {
		reply string
		in    Input
	}{
		"malformed JSON":             {`{"verdict":"grounded"`, input},
		"missing field":              {`{"verdict":"grounded","unsupported_claims":[],"reasoning":"r"}`, input},
		"unknown verdict":            {`{"verdict":"mostly_grounded","unsupported_claims":[],"answers_question":"yes","reasoning":"r"}`, input},
		"unknown answers_question":   {`{"verdict":"grounded","unsupported_claims":[],"answers_question":"maybe","reasoning":"r"}`, input},
		"extra field":                {`{"verdict":"grounded","unsupported_claims":[],"answers_question":"yes","reasoning":"r","score":5}`, input},
		"grounded with claims":       {`{"verdict":"grounded","unsupported_claims":["x"],"answers_question":"yes","reasoning":"r"}`, input},
		"ungrounded without claims":  {`{"verdict":"ungrounded","unsupported_claims":[],"answers_question":"no","reasoning":"r"}`, input},
		"no expected answer but yes": {`{"verdict":"grounded","unsupported_claims":[],"answers_question":"yes","reasoning":"r"}`, Input{Question: "q", Answer: "a"}},
		"expected answer but n/a":    {`{"verdict":"grounded","unsupported_claims":[],"answers_question":"not_applicable","reasoning":"r"}`, input},
		"empty reply":                {``, input},
	} {
		_, err := Judge{Model: &fakeModel{reply: tc.reply}}.Judge(context.Background(), tc.in)
		if !errors.Is(err, ErrBadVerdict) {
			t.Errorf("%s: err = %v, want ErrBadVerdict", name, err)
		}
	}
}

func TestAModelErrorPassesThrough(t *testing.T) {
	boom := errors.New("429 rate limited")
	_, err := Judge{Model: &fakeModel{err: boom}}.Judge(context.Background(), input)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the model's error", err)
	}
}

// The judge must see sources numbered as the answer cited them, and ask for
// strict JSON with no tools.
func TestTheRequestNumbersSourcesAndAsksForTheSchema(t *testing.T) {
	f := &fakeModel{reply: `{"verdict":"grounded","unsupported_claims":[],"answers_question":"yes","reasoning":"r"}`}
	in := input
	in.Sources = append(in.Sources, Source{ID: "exp-vartec#auto-dialer", Text: "Executives chose Colleen…"})
	if _, err := (Judge{Model: f}).Judge(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(f.got.Tools) != 0 || f.got.ResponseSchema == nil || !json.Valid(f.got.ResponseSchema) {
		t.Errorf("tools %v, schema %s", f.got.Tools, f.got.ResponseSchema)
	}
	user := f.got.Messages[len(f.got.Messages)-1].Content
	for _, want := range []string{"[1] exp-vartec#call-record-conversion: Senior Programmer, Vartec Telecom (Jan 2001 – Feb 2004)", "[2] exp-vartec#auto-dialer", in.Question, in.Answer, in.Expected} {
		if !strings.Contains(user, want) {
			t.Errorf("judge's message lacks %q:\n%s", want, user)
		}
	}
	if f.got.Temperature == nil || *f.got.Temperature != 0 {
		t.Errorf("temperature %v, want 0: the same answer must get the same verdict (plan 6.3c)", f.got.Temperature)
	}
	if !strings.Contains(f.got.System, "not_applicable") || !strings.Contains(f.got.System, "unsupported_claims") {
		t.Errorf("system prompt doesn't explain the fields:\n%s", f.got.System)
	}
}

// The live judge has no expected answer; its message must not invent one.
func TestWithoutAnExpectedAnswerTheMessageSaysSo(t *testing.T) {
	f := &fakeModel{reply: `{"verdict":"grounded","unsupported_claims":[],"answers_question":"not_applicable","reasoning":"r"}`}
	if _, err := (Judge{Model: f}).Judge(context.Background(), Input{Question: "q", Answer: "a"}); err != nil {
		t.Fatal(err)
	}
	if user := f.got.Messages[0].Content; !strings.Contains(user, "Expected answer: none") || !strings.Contains(user, "Sources: none") {
		t.Errorf("message:\n%s", user)
	}
}

// The rules from the owner's labels (plan 6.3c): interpretation of the
// visitor's problem isn't a claim, listed details are examples, named steps
// are required, and a claim beyond its source is unsupported.
func TestThePromptCarriesTheLabelledRules(t *testing.T) {
	for _, want := range []string{
		"Restating or interpreting the visitor's own problem",
		"fair summary of what several sources say together",
		"goes beyond its source",
		"main point",
		"examples, not a checklist",
		"each step must be there",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
}
