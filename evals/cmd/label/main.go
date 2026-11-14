// Command label checks the judge against you (plan step 6.3c, design §10).
// The first run takes a sample of the latest answer-eval run into the
// private repo; then it shows each sampled answer blind (never the judge's
// verdict) and saves your label as you go. -report compares your labels
// with the judge's verdicts.
//
//	make label                         # label, resumable; q to stop
//	make label LABEL_FLAGS=-report     # agreement, once all are labelled
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/cferrante/cferrante-dev/evals"
	cfg "github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
	"github.com/cferrante/cferrante-dev/orchestrator/judge"
	"github.com/cferrante/cferrante-dev/orchestrator/model/oai"
	"github.com/cferrante/cferrante-dev/orchestrator/secrets"
)

// sampled is one answer as stored in the sample: the run's row plus the
// text of each retrieved chunk, so labels never depend on a later corpus.
type sampled struct {
	evals.DetailRow
	Sources []source `json:"sources"`
}

type source struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// deps are what -rejudge needs from outside: the judge on Groq in main,
// fakes in tests.
type deps struct {
	newGrader func(ctx context.Context) (evals.Grader, error)
	sleep     func(ctx context.Context, d time.Duration)
}

func main() {
	getenv, err := cfg.WithDotEnv(".env", os.Getenv)
	if err == nil {
		env := cfg.New(getenv)
		s := groqSettings{model: env.String("JUDGE_MODEL", "openai/gpt-oss-20b"),
			keyParam: env.String("GROQ_KEY_PARAM", "/cferrante-dev/groq-api-key"),
			profile:  env.String("AWS_PROFILE", "cferrante-dev-admin"), region: env.String("AWS_REGION", "us-east-1")}
		err = runDeps(os.Args[1:], getenv, os.Stdin, os.Stdout, deps{newGrader: s.grader, sleep: sleep})
	}
	if err != nil {
		log.Fatalf("label: %v", err)
	}
}

type groqSettings struct{ model, keyParam, profile, region string }

// grader reads the Groq key from SSM and returns the judge on Groq.
func (s groqSettings) grader(ctx context.Context) (evals.Grader, error) {
	c, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithSharedConfigProfile(s.profile), awsconfig.WithRegion(s.region))
	if err != nil {
		return nil, err
	}
	key, err := (&secrets.SSM{API: ssm.NewFromConfig(c)}).Get(ctx, s.keyParam)
	if err != nil {
		return nil, fmt.Errorf("%w (if your AWS session has expired: aws sso login --profile %s)", err, s.profile)
	}
	return judge.Judge{Model: &oai.Client{Provider: "groq", BaseURL: oai.GroqBaseURL, APIKey: key, Model: s.model}}, nil
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func run(args []string, in io.Reader, out io.Writer) error {
	return runDeps(args, func(string) string { return "" }, in, out, deps{})
}

func runDeps(args []string, getenv cfg.Getenv, in io.Reader, out io.Writer, d deps) error {
	env := cfg.New(getenv)
	fs := flag.NewFlagSet("label", flag.ContinueOnError)
	detail := fs.String("detail", "build/evals/answers.jsonl", "the answer-eval run to sample from (make answers writes it)")
	dir := fs.String("dir", "corpus/private/labels", "where the sample and your labels live (the private repo)")
	corpusDirs := fs.String("corpus", env.String("CORPUS_DIRS", "corpus/public:corpus/private"), "corpus folders, for the chunks' text [CORPUS_DIRS]")
	perSide := fs.Int("per-side", 10, "answers the judge passed, and answers it didn't, to sample")
	report := fs.Bool("report", false, "compare your labels with the judge's verdicts")
	rejudge := fs.Bool("rejudge", false, "with -report: grade the sampled answers again with today's judge first (needs aws sso login)")
	name := fs.String("name", "", "a further sample alongside the first, such as fresh: <name>-sample.jsonl and <name>-labels.jsonl")
	if err := env.Err(); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	prefix := ""
	if *name != "" {
		prefix = *name + "-"
	}
	samplePath, labelsPath := filepath.Join(*dir, prefix+"sample.jsonl"), filepath.Join(*dir, prefix+"labels.jsonl")

	sample, err := loadSample(samplePath)
	if errors.Is(err, os.ErrNotExist) {
		if *report {
			return fmt.Errorf("no sample yet: run make label first")
		}
		if sample, err = takeSample(*detail, *corpusDirs, *perSide, sampledBefore(*dir)); err != nil {
			return err
		}
		if err := writeSample(samplePath, sample); err != nil {
			return err
		}
		fmt.Fprintf(out, "sampled %d answers from %s into %s\n", len(sample), *detail, samplePath)
	} else if err != nil {
		return err
	}
	ids := map[string]bool{}
	rows := make([]evals.DetailRow, len(sample))
	for i, s := range sample {
		ids[s.ID], rows[i] = true, s.DetailRow
	}
	raw, err := os.ReadFile(labelsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	labels, err := evals.LoadLabels(raw, ids)
	if err != nil {
		return fmt.Errorf("%s: %w", labelsPath, err)
	}
	if *report {
		if *rejudge {
			if rows, err = rejudgeAll(context.Background(), out, sample, d); err != nil {
				return err
			}
		}
		return printReport(out, rows, labels)
	}
	return label(in, out, sample, labels, labelsPath)
}

func loadSample(path string) ([]sampled, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []sampled
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var s sampled
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// sampledBefore is every question id in the samples already in dir: a
// fresh sample must not repeat them.
func sampledBefore(dir string) map[string]bool {
	ids := map[string]bool{}
	paths, _ := filepath.Glob(filepath.Join(dir, "*sample.jsonl"))
	for _, p := range paths {
		s, err := loadSample(p)
		if err != nil {
			continue
		}
		for _, x := range s {
			ids[x.ID] = true
		}
	}
	return ids
}

func takeSample(detailPath, corpusDirs string, perSide int, exclude map[string]bool) ([]sampled, error) {
	raw, err := os.ReadFile(detailPath)
	if err != nil {
		return nil, fmt.Errorf("%w (run make answers first)", err)
	}
	var rows []evals.DetailRow
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r evals.DetailRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s: %w", detailPath, err)
		}
		if !exclude[r.ID] {
			rows = append(rows, r)
		}
	}
	chunks, err := corpus.Chunker{Target: 1000}.ReadDirs(corpus.SplitDirs(corpusDirs))
	if err != nil {
		return nil, err
	}
	byID := map[string]corpus.Chunk{}
	for _, c := range chunks {
		byID[c.ID] = c
	}
	var out []sampled
	for _, r := range evals.Sample(rows, perSide) {
		s := sampled{DetailRow: r}
		for _, id := range r.Retrieved {
			c, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("%s: retrieved chunk %q isn't in the corpus any more; run make answers again", r.ID, id)
			}
			s.Sources = append(s.Sources, source{ID: id, Title: c.Title, Text: c.Text})
		}
		out = append(out, s)
	}
	return out, nil
}

func writeSample(path string, sample []sampled) error {
	var b bytes.Buffer
	for _, s := range sample {
		line, err := json.Marshal(s)
		if err != nil {
			return err
		}
		b.Write(append(line, '\n'))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// label shows each unlabelled answer blind and appends your label at once.
func label(in io.Reader, out io.Writer, sample []sampled, labels []evals.Label, path string) error {
	done := map[string]bool{}
	for _, l := range labels {
		done[l.ID] = true
	}
	r := bufio.NewReader(in)
	ask := func(prompt string, allowed map[string]string) (string, bool) {
		for {
			fmt.Fprint(out, prompt)
			line, err := r.ReadString('\n')
			answer := strings.TrimSpace(line)
			if answer == "q" || (err != nil && answer == "") {
				return "", false
			}
			if v, ok := allowed[answer]; ok {
				return v, true
			}
			fmt.Fprintln(out, "  please type one of the letters shown, or q to stop")
		}
	}
	fmt.Fprint(out, rubric)
	for _, s := range sample {
		if done[s.ID] {
			continue
		}
		fmt.Fprintf(out, "\n──── %s (%d of %d labelled so far) ────\n", s.ID, len(done), len(sample))
		fmt.Fprintf(out, "Question: %s\nExpected answer: %s\n\nSources:\n", s.Question, s.ExpectedAnswer)
		if len(s.Sources) == 0 {
			fmt.Fprintln(out, "  (none)")
		}
		for i, src := range s.Sources {
			fmt.Fprintf(out, "[%d] %s: %s\n%s\n\n", i+1, src.ID, src.Title, src.Text)
		}
		fmt.Fprintf(out, "Answer:\n%s\n\n", s.Answer)
		verdict, ok := ask("Are its claims supported by the sources? [g] all  [p] some  [u] not the main ones  (q stops): ",
			map[string]string{"g": "grounded", "p": "partially_grounded", "u": "ungrounded"})
		if !ok {
			break
		}
		answers, ok := ask("Does it answer as the expected answer says? [y] yes  [p] partly  [n] no: ",
			map[string]string{"y": "yes", "p": "partly", "n": "no"})
		if !ok {
			break
		}
		fmt.Fprint(out, "Note (optional, Enter to skip): ")
		note, _ := r.ReadString('\n')
		line, err := json.Marshal(evals.Label{ID: s.ID, Verdict: verdict, AnswersQuestion: answers, Note: strings.TrimSpace(note)})
		if err != nil {
			return err
		}
		if err := appendLine(path, line); err != nil {
			return err
		}
		done[s.ID] = true
	}
	fmt.Fprintf(out, "\n%d of %d labelled. ", len(done), len(sample))
	if len(done) == len(sample) {
		fmt.Fprintln(out, "Run make label LABEL_FLAGS=-report for the agreement.")
	} else {
		fmt.Fprintln(out, "Run make label again to carry on.")
	}
	return nil
}

// rubric is shown once before labelling, so you and the judge read
// answers_question the same way (agreed 2026-11-12, plan 6.3c).
const rubric = `Answers the question means it covers the expected answer as written.
Details listed there are examples, not a checklist. Named steps must be there,
and so must saying what the documents don't cover, when the expected answer asks for it.
`

func appendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func printReport(out io.Writer, rows []evals.DetailRow, labels []evals.Label) error {
	a, err := evals.Agree(rows, labels)
	if err != nil {
		return err
	}
	pct := func(n int) string { return fmt.Sprintf("%d/%d (%d%%)", n, a.N, 100*n/a.N) }
	fmt.Fprintf(out, "Agreement with the judge over %d answers:\n  verdict: %s\n  answers_question: %s\n  pass or fail: %s\n",
		a.N, pct(a.Verdict), pct(a.Answers), pct(a.PassFail))
	if len(a.Disagreements) > 0 {
		fmt.Fprintln(out, "\nDisagreements:")
		for _, d := range a.Disagreements {
			fmt.Fprintf(out, "  %s\n    judge's reasoning: %s\n", d, d.JudgeReasoning)
		}
	}
	if a.Trusted() {
		fmt.Fprintln(out, "\nThe judge is trusted: at least 80% agreement on both the verdict and answers_question.")
	} else {
		fmt.Fprintln(out, "\nThe judge is not trusted yet: below 80% on the verdict or answers_question. Fix its prompt and re-check on this sample.")
	}
	return nil
}

// rejudgeAll grades every sampled answer again with today's judge,
// retrying after a minute when the judge fails (Groq's free tier allows
// 8,000 tokens a minute), and returns rows carrying the new verdicts. The
// stored sample is left as it was.
func rejudgeAll(ctx context.Context, out io.Writer, sample []sampled, d deps) ([]evals.DetailRow, error) {
	if d.newGrader == nil {
		return nil, errors.New("-rejudge: no judge")
	}
	g, err := d.newGrader(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]evals.DetailRow, len(sample))
	for i, s := range sample {
		in := judge.Input{Question: s.Question, Answer: s.Answer, Expected: s.ExpectedAnswer}
		for _, src := range s.Sources {
			in.Sources = append(in.Sources, judge.Source{ID: src.ID, Title: src.Title, Text: src.Text})
		}
		var v judge.Result
		for attempt := 1; ; attempt++ {
			if v, err = g.Judge(ctx, in); err == nil {
				break
			}
			if attempt > 3 {
				return nil, fmt.Errorf("re-judging %s: %w", s.ID, err)
			}
			d.sleep(ctx, time.Minute)
		}
		r := s.DetailRow
		r.Verdict, r.AnswersQuestion, r.Reasoning, r.UnsupportedClaims = string(v.Verdict), v.AnswersQuestion, v.Reasoning, v.UnsupportedClaims
		r.Passed = evals.Answer{Item: evals.Item{ExpectedChunks: s.ExpectedChunks}, Text: s.Answer, Verdict: v}.Passed()
		rows[i] = r
	}
	fmt.Fprintf(out, "re-judged %d answers with today's judge\n\n", len(rows))
	return rows, nil
}
