// Command answers runs the answer evals (plan step 6.3b, design §10): every
// golden question through the real turn on Groq, judged, with counts
// appended to evals/results/answers.jsonl and the answers themselves written
// to build/evals/answers.jsonl. It reads the same owner, style, wording and
// tool settings as make dev, so the prompt hash it prints should match the
// one make dev logs.
//
//	make answers                        # after aws sso login
//	make answers ANSWERS_FLAGS='-ids g-001,g-019'
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/cferrante/cferrante-dev/evals"
	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	cfg "github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/indexing"
	"github.com/cferrante/cferrante-dev/orchestrator/judge"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/oai"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
	"github.com/cferrante/cferrante-dev/orchestrator/secrets"
	"github.com/cferrante/cferrante-dev/orchestrator/tools"
	"github.com/cferrante/cferrante-dev/orchestrator/wording"
)

// deps are what the run needs from outside: real AWS and Groq in main, fakes
// in tests.
type deps struct {
	newBedrock func(profile, region string) (embed.Invoker, error)
	// newModels returns the answering and judging models.
	newModels func(ctx context.Context, s settings) (answer, judge model.Model, err error)
}

type settings struct {
	answerModel, judgeModel, effort, keyParam, profile, region string
}

func main() {
	getenv, err := cfg.WithDotEnv(".env", os.Getenv)
	if err == nil {
		err = run(os.Args[1:], getenv, os.Stdout, deps{newBedrock: bedrockClient, newModels: groqModels})
	}
	if err != nil {
		log.Fatalf("answers: %v", err)
	}
}

func awsConfig(ctx context.Context, profile, region string) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithSharedConfigProfile(profile), awsconfig.WithRegion(region))
}

func bedrockClient(profile, region string) (embed.Invoker, error) {
	c, err := awsConfig(context.Background(), profile, region)
	if err != nil {
		return nil, err
	}
	return bedrockruntime.NewFromConfig(c), nil
}

// groqModels reads the Groq key from SSM and returns two Groq clients, with
// no fallback: an answer from another model would score the wrong one.
func groqModels(ctx context.Context, s settings) (model.Model, model.Model, error) {
	c, err := awsConfig(ctx, s.profile, s.region)
	if err != nil {
		return nil, nil, err
	}
	key, err := (&secrets.SSM{API: ssm.NewFromConfig(c)}).Get(ctx, s.keyParam)
	if err != nil {
		return nil, nil, fmt.Errorf("%w (if your AWS session has expired: aws sso login --profile %s)", err, s.profile)
	}
	return &oai.Client{Provider: "groq", BaseURL: oai.GroqBaseURL, APIKey: key, Model: s.answerModel, ReasoningEffort: s.effort},
		&oai.Client{Provider: "groq", BaseURL: oai.GroqBaseURL, APIKey: key, Model: s.judgeModel}, nil
}

func run(args []string, getenv cfg.Getenv, out io.Writer, d deps) error {
	env := cfg.New(getenv)
	fs := flag.NewFlagSet("answers", flag.ContinueOnError)
	idx := indexing.Flags(fs, env)
	var s settings
	golden := fs.String("golden", env.String("GOLDEN_FILES", "evals/golden/*.jsonl:corpus/private/golden/*.jsonl"), "golden files, colon-separated globs [GOLDEN_FILES]")
	ids := fs.String("ids", "", "only these question ids, comma-separated (for a retry or a quick check)")
	results := fs.String("results", "evals/results/answers.jsonl", `append one counts-only line per run; "" = none`)
	detail := fs.String("detail", "build/evals/answers.jsonl", "every answer's text and verdict (git-ignored)")
	pause := fs.Duration("pause", env.Duration("ANSWERS_PAUSE", 0), "wait between questions, to stay under a rate limit [ANSWERS_PAUSE]")
	retries := fs.Int("retries", env.Int("ANSWERS_RETRIES", 3), "retries for a failed turn or judgement [ANSWERS_RETRIES]")
	retryWait := fs.Duration("retry-wait", env.Duration("ANSWERS_RETRY_WAIT", time.Minute), "wait before a retry; Groq's free tier limits tokens per minute [ANSWERS_RETRY_WAIT]")
	fs.StringVar(&s.answerModel, "groq-model", env.String("GROQ_MODEL", "openai/gpt-oss-120b"), "answering model on Groq [GROQ_MODEL]")
	fs.StringVar(&s.judgeModel, "judge-model", env.String("JUDGE_MODEL", "openai/gpt-oss-20b"), "judging model on Groq [JUDGE_MODEL]")
	fs.StringVar(&s.effort, "effort", env.String("REASONING_EFFORT", ""), "reasoning effort for answers; unset = Groq's default [REASONING_EFFORT]")
	fs.StringVar(&s.keyParam, "groq-key-param", env.String("GROQ_KEY_PARAM", "/cferrante-dev/groq-api-key"), "SSM SecureString holding the Groq key [GROQ_KEY_PARAM]")
	fs.StringVar(&s.profile, "aws-profile", env.String("AWS_PROFILE", "cferrante-dev-admin"), "AWS profile for SSM and Titan [AWS_PROFILE]")
	fs.StringVar(&s.region, "aws-region", env.String("AWS_REGION", "us-east-1"), "AWS region [AWS_REGION]")
	// The same settings make dev reads, so the prompt is the one visitors get.
	assistant := fs.String("assistant", env.String("ASSISTANT_NAME", agent.DefaultAssistantName), "[ASSISTANT_NAME]")
	ownerName := fs.String("owner", env.String("OWNER_NAME", ""), "[OWNER_NAME]")
	ownerFirst := fs.String("owner-first", env.String("OWNER_FIRST_NAME", ""), "[OWNER_FIRST_NAME]")
	pronouns := fs.String("pronouns", env.String("OWNER_PRONOUNS", ""), "[OWNER_PRONOUNS]")
	styleFile := fs.String("style", env.String("PROMPT_STYLE_FILE", ""), "[PROMPT_STYLE_FILE]")
	messagesFile := fs.String("messages", env.String("MESSAGES_FILE", ""), "[MESSAGES_FILE]")
	contact := fs.String("contact", env.String("CONTACT_OPTIONS", "Invitees can send a message through this chat."), "[CONTACT_OPTIONS]")
	availability := fs.String("availability", env.String("AVAILABILITY", "Weekdays, office hours (not configured)."), "[AVAILABILITY]")
	deadline := fs.Duration("turn-deadline", env.Duration("TURN_DEADLINE", 5*time.Minute), "whole-turn limit [TURN_DEADLINE]")
	if err := env.Err(); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if idx.IndexPath == "build/dev/corpus.idx" { // don't overwrite make dev's index
		idx.IndexPath = "build/evals/answers.idx"
	}

	var paths []string
	for _, pattern := range strings.Split(*golden, ":") {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("-golden %q: %w", pattern, err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no golden files match %q", *golden)
	}
	items, err := evals.Load(paths, os.ReadFile)
	if err != nil {
		return err
	}
	if *ids != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(*ids, ",") {
			want[strings.TrimSpace(id)] = true
		}
		var some []evals.Item
		for _, it := range items {
			if want[it.ID] {
				some = append(some, it)
				delete(want, it.ID)
			}
		}
		if len(want) > 0 {
			return fmt.Errorf("-ids: no such questions: %v", keys(want))
		}
		items = some
	}

	words, err := wording.Load(*messagesFile, os.ReadFile)
	if err != nil {
		return err
	}
	persona, err := agent.NewPersona(*ownerName, *ownerFirst, *pronouns, *styleFile, os.ReadFile)
	if err != nil {
		return err
	}
	ctx := context.Background()
	e, err := idx.NewEmbedder(func() (embed.Invoker, error) {
		if d.newBedrock == nil {
			return nil, errors.New("no Bedrock client")
		}
		return d.newBedrock(s.profile, s.region)
	})
	if err != nil {
		return err
	}
	if _, err := idx.Build(ctx, e); err != nil {
		return err
	}
	r, err := retrieval.Load(idx.IndexPath, e)
	if err != nil {
		return err
	}
	defer r.Close()
	answerModel, judgeModel, err := d.newModels(ctx, s)
	if err != nil {
		return err
	}
	rec := &evals.RecordingRetriever{Retriever: r}
	a := &agent.Agent{Model: answerModel, Retriever: rec, Store: agent.NewMemoryStore(), Clock: clock.Real{},
		Tools:   tools.New(tools.Static{ContactOptions: *contact, Availability: *availability}, &tools.MemoryOutbox{Log: io.Discard}),
		Pending: tools.NewMemoryPending(), Deadline: *deadline, AssistantName: *assistant, Wording: words, Persona: persona}
	fmt.Fprintf(out, "%d questions from %s; prompt sha256=%s; answers by %s, judged by %s; embedder %s\n\n",
		len(items), strings.Join(paths, ", "), a.PromptHash(), s.answerModel, s.judgeModel, e.Model())

	start := time.Now()
	answers := (&evals.AnswerRunner{Turn: a, Retrieved: rec, Judge: judge.Judge{Model: judgeModel}, Pause: *pause,
		Retries: *retries, RetryWait: *retryWait}).Run(ctx, items)
	for _, x := range answers {
		mark := "✗"
		if x.Passed() {
			mark = "✓"
		}
		switch {
		case x.Err != "":
			fmt.Fprintf(out, "! %s  %.1fs  %s\n", x.Item.ID, x.Elapsed.Seconds(), x.Err)
		default:
			fmt.Fprintf(out, "%s %s  %.1fs%s  %s, answers %s%s\n", mark, x.Item.ID, x.Elapsed.Seconds(), tries(x.Attempts),
				x.Verdict.Verdict, x.Verdict.AnswersQuestion, claims(x.Verdict.UnsupportedClaims))
		}
	}
	fmt.Fprintf(out, "\n%d questions in %v\n", len(answers), time.Since(start).Round(time.Second))
	sums := evals.Summarize(answers)
	for _, p := range paths {
		if sm, ok := sums[p]; ok {
			fmt.Fprintf(out, "  %s: %d/%d passed, %d errors, verdicts %v, answers %v\n", p, sm.Passed, sm.Questions, sm.Errors, sm.Verdicts, sm.Answers)
		}
	}
	commit, dirty := evals.GitState(".")
	meta := evals.RunMeta{Commit: commit, Dirty: dirty, AnswerModel: "groq/" + s.answerModel, JudgeModel: "groq/" + s.judgeModel,
		Embedder: e.Model(), PromptHash: a.PromptHash()}
	if err := evals.WriteAnswers(*results, *detail, meta, answers); err != nil {
		return err
	}
	fmt.Fprintf(out, "answers and verdicts: %s\n", *detail)
	return nil
}

func claims(c []string) string {
	if len(c) == 0 {
		return ""
	}
	return fmt.Sprintf("; unsupported: %q", c)
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func tries(n int) string {
	if n <= 1 {
		return ""
	}
	return fmt.Sprintf(" (%d tries)", n)
}
