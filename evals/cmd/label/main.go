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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cferrante/cferrante-dev/evals"
	cfg "github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
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

func main() {
	getenv, err := cfg.WithDotEnv(".env", os.Getenv)
	if err == nil {
		err = runEnv(os.Args[1:], getenv, os.Stdin, os.Stdout)
	}
	if err != nil {
		log.Fatalf("label: %v", err)
	}
}

func run(args []string, in io.Reader, out io.Writer) error {
	return runEnv(args, func(string) string { return "" }, in, out)
}

func runEnv(args []string, getenv cfg.Getenv, in io.Reader, out io.Writer) error {
	env := cfg.New(getenv)
	fs := flag.NewFlagSet("label", flag.ContinueOnError)
	detail := fs.String("detail", "build/evals/answers.jsonl", "the answer-eval run to sample from (make answers writes it)")
	dir := fs.String("dir", "corpus/private/labels", "where the sample and your labels live (the private repo)")
	corpusDirs := fs.String("corpus", env.String("CORPUS_DIRS", "corpus/public:corpus/private"), "corpus folders, for the chunks' text [CORPUS_DIRS]")
	perSide := fs.Int("per-side", 10, "answers the judge passed, and answers it didn't, to sample")
	report := fs.Bool("report", false, "compare your labels with the judge's verdicts")
	if err := env.Err(); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	samplePath, labelsPath := filepath.Join(*dir, "sample.jsonl"), filepath.Join(*dir, "labels.jsonl")

	sample, err := loadSample(samplePath)
	if errors.Is(err, os.ErrNotExist) {
		if *report {
			return fmt.Errorf("no sample yet: run make label first")
		}
		if sample, err = takeSample(*detail, *corpusDirs, *perSide); err != nil {
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

func takeSample(detailPath, corpusDirs string, perSide int) ([]sampled, error) {
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
		rows = append(rows, r)
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
