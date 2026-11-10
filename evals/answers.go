package evals

// Answer evals (design §10, step 6.3b): run each golden question through the
// real turn, then have the judge grade the answer against the chunks that
// turn retrieved and against the expected answer.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	"github.com/cferrante/cferrante-dev/orchestrator/judge"
	"github.com/cferrante/cferrante-dev/orchestrator/sse"
)

// Turner runs one answer turn; *agent.Agent satisfies it.
type Turner interface {
	Turn(ctx context.Context, caller auth.Caller, req agent.TurnRequest, w *sse.Writer) error
}

// Grader judges one answer; judge.Judge satisfies it.
type Grader interface {
	Judge(ctx context.Context, in judge.Input) (judge.Result, error)
}

// RecordingRetriever keeps the chunks of the latest search, in the order the
// turn numbered them, so the judge sees exactly what the answer could cite.
type RecordingRetriever struct {
	agent.Retriever
	mu   sync.Mutex
	last []agent.Chunk
}

func (r *RecordingRetriever) Retrieve(ctx context.Context, query string, mask uint32, k int) ([]agent.Chunk, error) {
	chunks, err := r.Retriever.Retrieve(ctx, query, mask, k)
	r.mu.Lock()
	r.last = chunks
	r.mu.Unlock()
	return chunks, err
}

func (r *RecordingRetriever) take() []agent.Chunk {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.last
	r.last = nil
	return c
}

// AnswerRunner answers and judges golden questions one at a time. Pause, if
// set, waits between questions to stay under a provider's rate limit.
type AnswerRunner struct {
	Turn      Turner
	Retrieved *RecordingRetriever // must be the retriever Turn uses
	Judge     Grader
	Pause     time.Duration
}

// Answer is one question's outcome. Err is set when the turn or the judge
// failed; such an answer never passes.
type Answer struct {
	Item      Item
	Text      string
	Retrieved []string // chunk ids, in the turn's order
	Verdict   judge.Result
	Err       string
	Elapsed   time.Duration // the turn and the judge together
}

// Passed: grounded and answering the question as expected. For a decline
// question the expected answer is the decline.
func (a Answer) Passed() bool {
	return a.Err == "" && a.Verdict.Verdict == judge.Grounded && a.Verdict.AnswersQuestion == "yes"
}

// Run answers every item in order.
func (r *AnswerRunner) Run(ctx context.Context, items []Item) []Answer {
	out := make([]Answer, 0, len(items))
	for i, it := range items {
		if i > 0 && r.Pause > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(r.Pause):
			}
		}
		out = append(out, r.one(ctx, it))
	}
	return out
}

func (r *AnswerRunner) one(ctx context.Context, it Item) (a Answer) {
	start := time.Now()
	a.Item = it
	defer func() { a.Elapsed = time.Since(start) }()
	r.Retrieved.take() // drop anything left from an earlier question
	var buf bytes.Buffer
	w := sse.New(&buf, nil, clock.Real{})
	err := r.Turn.Turn(ctx, callerFor(it.Caller), agent.TurnRequest{Message: it.Question}, w)
	w.Close()
	text, failure := readStream(buf.Bytes())
	switch {
	case err != nil:
		a.Err = "turn: " + err.Error()
	case failure != "":
		a.Err = "turn: " + failure
	}
	a.Text = text
	chunks := r.Retrieved.take()
	sources := make([]judge.Source, len(chunks))
	for i, c := range chunks {
		a.Retrieved = append(a.Retrieved, c.ID)
		sources[i] = judge.Source{ID: c.ID, Text: c.Text}
	}
	if a.Err != "" {
		return a
	}
	v, err := r.Judge.Judge(ctx, judge.Input{Question: it.Question, Answer: text, Sources: sources, Expected: it.ExpectedAnswer})
	if err != nil {
		a.Err = "judge: " + err.Error()
		return a
	}
	a.Verdict = v
	return a
}

// readStream joins the answer's tokens and reports an error event's code.
func readStream(raw []byte) (text, failure string) {
	var b strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(nil, 1<<20)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var d struct{ Text, Code, Message string }
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d) != nil {
				continue
			}
			switch event {
			case "token":
				b.WriteString(d.Text)
			case "error":
				failure = d.Code + ": " + d.Message
			}
		}
	}
	return b.String(), failure
}

// FileSummary counts one golden file's outcomes.
type FileSummary struct {
	Questions int            `json:"questions"`
	Passed    int            `json:"passed"`
	Errors    int            `json:"errors"`
	Verdicts  map[string]int `json:"verdicts"`
	Answers   map[string]int `json:"answers_question"`
}

// Summarize counts outcomes per golden file.
func Summarize(answers []Answer) map[string]FileSummary {
	out := map[string]FileSummary{}
	for _, a := range answers {
		s, ok := out[a.Item.File]
		if !ok {
			s = FileSummary{Verdicts: map[string]int{}, Answers: map[string]int{}}
		}
		s.Questions++
		switch {
		case a.Err != "":
			s.Errors++
		default:
			s.Verdicts[string(a.Verdict.Verdict)]++
			s.Answers[a.Verdict.AnswersQuestion]++
			if a.Passed() {
				s.Passed++
			}
		}
		out[a.Item.File] = s
	}
	return out
}

// RunMeta identifies what produced a run, so runs compare by commit, models
// and prompt.
type RunMeta struct {
	Commit      string `json:"commit"`
	Dirty       bool   `json:"dirty"`
	AnswerModel string `json:"answer_model"`
	JudgeModel  string `json:"judge_model"`
	Embedder    string `json:"embedder"`
	PromptHash  string `json:"prompt_sha"`
}

// WriteAnswers appends one counts-only line to results (committed) and
// rewrites detail (git-ignored) with every answer's text and verdict.
func WriteAnswers(results, detail string, meta RunMeta, answers []Answer) error {
	line, err := json.Marshal(struct {
		Time string `json:"time"`
		RunMeta
		Files map[string]FileSummary `json:"files"`
	}{time.Now().UTC().Format(time.RFC3339), meta, Summarize(answers)})
	if err != nil {
		return err
	}
	if err := appendLine(results, line); err != nil {
		return err
	}
	var b bytes.Buffer
	for _, a := range answers {
		row, err := json.Marshal(struct {
			ID                string   `json:"id"`
			File              string   `json:"file"`
			Caller            string   `json:"caller"`
			Question          string   `json:"question"`
			ExpectedChunks    []string `json:"expected_chunks"`
			ExpectedAnswer    string   `json:"expected_answer"`
			Answer            string   `json:"answer"`
			Retrieved         []string `json:"retrieved"`
			Verdict           string   `json:"verdict"`
			UnsupportedClaims []string `json:"unsupported_claims"`
			AnswersQuestion   string   `json:"answers_question"`
			Reasoning         string   `json:"reasoning"`
			Passed            bool     `json:"passed"`
			Err               string   `json:"error,omitempty"`
			Seconds           float64  `json:"seconds"`
		}{a.Item.ID, a.Item.File, a.Item.Caller, a.Item.Question, a.Item.ExpectedChunks, a.Item.ExpectedAnswer,
			a.Text, a.Retrieved, string(a.Verdict.Verdict), a.Verdict.UnsupportedClaims, a.Verdict.AnswersQuestion,
			a.Verdict.Reasoning, a.Passed(), a.Err, a.Elapsed.Seconds()})
		if err != nil {
			return err
		}
		b.Write(append(row, '\n'))
	}
	if err := os.MkdirAll(filepath.Dir(detail), 0o755); err != nil {
		return err
	}
	return os.WriteFile(detail, b.Bytes(), 0o600)
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
		return fmt.Errorf("%s: %w", path, err)
	}
	return f.Close()
}
