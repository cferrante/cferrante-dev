package evals

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/judge"
	"github.com/cferrante/cferrante-dev/orchestrator/sse"
)

// fakeTurn retrieves through r, as the real turn does, then streams reply
// as tokens; failCode ends the turn with an error event instead.
type fakeTurn struct {
	r        agent.Retriever
	reply    map[string][]string // question -> tokens
	failCode string
	callers  []auth.Caller
}

func (f *fakeTurn) Turn(ctx context.Context, caller auth.Caller, req agent.TurnRequest, w *sse.Writer) error {
	f.callers = append(f.callers, caller)
	if _, err := f.r.Retrieve(ctx, req.Message, caller.Mask(), 5); err != nil {
		return err
	}
	if f.failCode != "" {
		return w.Error(f.failCode, "the model is busy", true)
	}
	for _, tok := range f.reply[req.Message] {
		if err := w.Token(tok); err != nil {
			return err
		}
	}
	return w.Done("c_1")
}

type fakeRetriever struct{ chunks []agent.Chunk }

func (f fakeRetriever) Retrieve(context.Context, string, uint32, int) ([]agent.Chunk, error) {
	return f.chunks, nil
}

// fakeJudge returns verdicts by question and keeps what it was given.
type fakeJudge struct {
	verdicts map[string]judge.Result
	err      error
	got      []judge.Input
}

func (f *fakeJudge) Judge(_ context.Context, in judge.Input) (judge.Result, error) {
	f.got = append(f.got, in)
	return f.verdicts[in.Question], f.err
}

var (
	telecom = Item{ID: "g-1", File: "public.jsonl", Question: "Has Colleen worked in telecom?", Caller: "anonymous",
		ExpectedChunks: []string{"exp-vartec#auto-dialer"}, ExpectedAnswer: "Yes: Vartec."}
	weather = Item{ID: "g-2", File: "public.jsonl", Question: "What's the weather in Paris?", Caller: "anonymous",
		ExpectedAnswer: "Politely declines."}
	secret = Item{ID: "h-1", File: "heldout.jsonl", Question: "What are her rates?", Caller: "invitee",
		ExpectedChunks: []string{"rates#summary"}, ExpectedAnswer: "Quotes the private rate."}
	chunks = []agent.Chunk{{ID: "exp-vartec#auto-dialer", Title: "Senior Programmer, Vartec Telecom", Text: "At Vartec…"}, {ID: "exp-vartec#teradata-migration", Text: "Teradata…"}}
	pass   = judge.Result{Verdict: judge.Grounded, AnswersQuestion: "yes", Model: "judge-model"}
)

func newRun(reply map[string][]string, j *fakeJudge) (*AnswerRunner, *fakeTurn) {
	rec := &RecordingRetriever{Retriever: fakeRetriever{chunks}}
	ft := &fakeTurn{r: rec, reply: reply}
	return &AnswerRunner{Turn: ft, Retrieved: rec, Judge: j}, ft
}

func TestEachQuestionIsAnsweredAsItsCallerAndJudgedOnItsOwnSources(t *testing.T) {
	j := &fakeJudge{verdicts: map[string]judge.Result{telecom.Question: pass, secret.Question: pass}}
	run, ft := newRun(map[string][]string{telecom.Question: {"Yes, at ", "Vartec [1]."}, secret.Question: {"The rate is X [1]."}}, j)
	got := run.Run(context.Background(), []Item{telecom, secret})
	if len(got) != 2 {
		t.Fatalf("%d answers, want 2", len(got))
	}
	if got[0].Text != "Yes, at Vartec [1]." || !got[0].Passed() || got[0].Err != "" || got[0].Elapsed <= 0 {
		t.Errorf("answer %+v", got[0])
	}
	if ft.callers[0].IsInvitee() || !ft.callers[1].IsInvitee() {
		t.Errorf("callers %+v: want anonymous, then invitee", ft.callers)
	}
	in := j.got[0]
	if in.Expected != telecom.ExpectedAnswer || in.Answer != got[0].Text || len(in.Sources) != 2 ||
		in.Sources[0].ID != "exp-vartec#auto-dialer" || in.Sources[0].Title != "Senior Programmer, Vartec Telecom" || in.Sources[1].Text != "Teradata…" {
		t.Errorf("judge input %+v", in)
	}
	if strings.Join(got[0].Retrieved, ",") != "exp-vartec#auto-dialer,exp-vartec#teradata-migration" {
		t.Errorf("retrieved %v", got[0].Retrieved)
	}
}

func TestADeclineQuestionAnsweredAnywayIsNotAPass(t *testing.T) {
	invented := judge.Result{Verdict: judge.Ungrounded, UnsupportedClaims: []string{"sunny, 21°C"}, AnswersQuestion: "no"}
	j := &fakeJudge{verdicts: map[string]judge.Result{weather.Question: invented}}
	run, _ := newRun(map[string][]string{weather.Question: {"It's sunny and 21°C."}}, j)
	got := run.Run(context.Background(), []Item{weather})
	if got[0].Passed() {
		t.Errorf("an invented forecast passed: %+v", got[0])
	}
	if s := Summarize(got)["public.jsonl"]; s.Questions != 1 || s.Passed != 0 || s.Verdicts["ungrounded"] != 1 || s.Answers["no"] != 1 {
		t.Errorf("summary %+v", s)
	}
}

// A failed turn or judge is an error, never a pass, and the judge isn't
// asked to grade an answer that never arrived.
func TestFailuresAreErrorsNotPasses(t *testing.T) {
	j := &fakeJudge{verdicts: map[string]judge.Result{telecom.Question: pass}}
	run, ft := newRun(nil, j)
	ft.failCode = "rate_limited"
	got := run.Run(context.Background(), []Item{telecom})
	if got[0].Passed() || !strings.Contains(got[0].Err, "rate_limited") || len(j.got) != 0 {
		t.Errorf("failed turn: %+v, judge called %d times", got[0], len(j.got))
	}

	j = &fakeJudge{err: judge.ErrBadVerdict}
	run, _ = newRun(map[string][]string{telecom.Question: {"Yes."}}, j)
	got = run.Run(context.Background(), []Item{telecom})
	if got[0].Passed() || !strings.Contains(got[0].Err, "bad verdict") {
		t.Errorf("failed judge: %+v", got[0])
	}
	if s := Summarize(got)["public.jsonl"]; s.Errors != 1 || s.Passed != 0 {
		t.Errorf("summary %+v", s)
	}
}

func TestSummariesArePerFile(t *testing.T) {
	j := &fakeJudge{verdicts: map[string]judge.Result{telecom.Question: pass, secret.Question: {Verdict: judge.PartiallyGrounded, UnsupportedClaims: []string{"x"}, AnswersQuestion: "partly"}}}
	run, _ := newRun(map[string][]string{telecom.Question: {"Yes."}, secret.Question: {"Maybe."}}, j)
	s := Summarize(run.Run(context.Background(), []Item{telecom, secret}))
	if s["public.jsonl"].Passed != 1 || s["heldout.jsonl"].Passed != 0 || s["heldout.jsonl"].Verdicts["partially_grounded"] != 1 {
		t.Errorf("summaries %+v", s)
	}
}

// Invitee and held-out answers hold private facts, so the committed results
// line must carry counts only; the text goes to the git-ignored detail file.
func TestTheResultsLineHasNoQuestionOrAnswerText(t *testing.T) {
	j := &fakeJudge{verdicts: map[string]judge.Result{secret.Question: {Verdict: judge.Grounded, AnswersQuestion: "yes", Reasoning: "The rate of X is stated."}}}
	run, _ := newRun(map[string][]string{secret.Question: {"The rate is X [1]."}}, j)
	got := run.Run(context.Background(), []Item{secret})
	dir := t.TempDir()
	results, detail := filepath.Join(dir, "results", "answers.jsonl"), filepath.Join(dir, "build", "answers.jsonl")
	meta := RunMeta{Commit: "abc", AnswerModel: "groq/gpt-oss-120b", JudgeModel: "groq/gpt-oss-20b", Embedder: "hash-v1-256", PromptHash: "0123456789ab"}
	if err := WriteAnswers(results, detail, meta, got); err != nil {
		t.Fatal(err)
	}
	line, _ := os.ReadFile(results)
	for _, private := range []string{secret.Question, "The rate is X", "rate of X", "rates#summary"} {
		if bytes.Contains(line, []byte(private)) {
			t.Errorf("results line contains %q:\n%s", private, line)
		}
	}
	for _, want := range []string{`"prompt_sha":"0123456789ab"`, `"answer_model":"groq/gpt-oss-120b"`, `"heldout.jsonl":{"questions":1,"passed":1`} {
		if !bytes.Contains(line, []byte(want)) {
			t.Errorf("results line lacks %s:\n%s", want, line)
		}
	}
	text, _ := os.ReadFile(detail)
	if !bytes.Contains(text, []byte("The rate is X [1].")) || !bytes.Contains(text, []byte("rates#summary")) {
		t.Errorf("detail file lacks the answer or its expected chunk:\n%s", text)
	}
}

// Grounded isn't enough: an answer can stick to the sources and still miss
// the question, such as citing her telecom work when asked for a forecast.
func TestAGroundedAnswerThatMissesTheQuestionIsNotAPass(t *testing.T) {
	offTopic := judge.Result{Verdict: judge.Grounded, AnswersQuestion: "no"}
	j := &fakeJudge{verdicts: map[string]judge.Result{weather.Question: offTopic}}
	run, _ := newRun(map[string][]string{weather.Question: {"Colleen worked at Vartec [1]."}}, j)
	if got := run.Run(context.Background(), []Item{weather}); got[0].Passed() {
		t.Errorf("a grounded answer that misses the question passed: %+v", got[0])
	}
}

// flakyTurn fails its first fails calls, as a rate-limited model does.
type flakyTurn struct {
	fakeTurn
	fails, calls int
}

func (f *flakyTurn) Turn(ctx context.Context, caller auth.Caller, req agent.TurnRequest, w *sse.Writer) error {
	f.calls++
	if f.calls <= f.fails {
		_, _ = f.r.Retrieve(ctx, req.Message, caller.Mask(), 5)
		return w.Error("model_unavailable", "the model couldn't answer right now", true)
	}
	return f.fakeTurn.Turn(ctx, caller, req, w)
}

// Groq's free tier allows 8,000 tokens a minute, so a full run must wait
// and retry rather than fail every question after the limit.
func TestAFailedQuestionIsRetriedAfterAWait(t *testing.T) {
	rec := &RecordingRetriever{Retriever: fakeRetriever{chunks}}
	ft := &flakyTurn{fakeTurn: fakeTurn{r: rec, reply: map[string][]string{telecom.Question: {"Yes."}}}, fails: 2}
	var waits []time.Duration
	run := &AnswerRunner{Turn: ft, Retrieved: rec, Judge: &fakeJudge{verdicts: map[string]judge.Result{telecom.Question: pass}},
		Retries: 2, RetryWait: time.Minute, Sleep: func(_ context.Context, d time.Duration) { waits = append(waits, d) }}
	got := run.Run(context.Background(), []Item{telecom})
	if !got[0].Passed() || got[0].Attempts != 3 || len(waits) != 2 || waits[0] != time.Minute {
		t.Errorf("answer %+v after waits %v: want a pass on the third attempt", got[0], waits)
	}

	ft = &flakyTurn{fakeTurn: fakeTurn{r: rec, reply: map[string][]string{telecom.Question: {"Yes."}}}, fails: 3}
	run.Turn, waits = ft, nil
	got = run.Run(context.Background(), []Item{telecom})
	if got[0].Passed() || got[0].Err == "" || got[0].Attempts != 3 {
		t.Errorf("answer %+v: want an error after 3 attempts", got[0])
	}
}

func TestAJudgeFailureIsRetriedToo(t *testing.T) {
	j := &flakyJudge{fails: 1, result: pass}
	run, _ := newRun(map[string][]string{telecom.Question: {"Yes."}}, nil)
	run.Judge, run.Retries, run.Sleep = j, 1, func(context.Context, time.Duration) {}
	if got := run.Run(context.Background(), []Item{telecom}); !got[0].Passed() || got[0].Attempts != 2 {
		t.Errorf("answer %+v: want a pass on the second attempt", got[0])
	}
}

type flakyJudge struct {
	fails, calls int
	result       judge.Result
}

func (f *flakyJudge) Judge(context.Context, judge.Input) (judge.Result, error) {
	f.calls++
	if f.calls <= f.fails {
		return judge.Result{}, errors.New("HTTP 429: rate limit")
	}
	return f.result, nil
}
