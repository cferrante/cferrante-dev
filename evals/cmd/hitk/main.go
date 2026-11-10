// Command hitk runs the golden set's retrieval check (plan steps 6.1, 6.2):
// it indexes the corpus, searches each golden question as its caller would,
// and prints ✓ or ✗ per question with hit@k overall and per file. With -min
// it is the CI gate; with -write-cache it saves the run's vectors for CI
// (design §10).
//
//	go run ./evals/cmd/hitk                       # .env settings, Ollama embeddings
//	go run ./evals/cmd/hitk -embedder hash        # no model, keyword-only in effect
//	make embed-cache                              # Titan over the public set -> the cache
//	make hitk-gate                                # the cache, the public set, -min (CI)
package main

import (
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
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/cferrante/cferrante-dev/evals"
	cfg "github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/indexing"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
)

func main() {
	getenv, err := cfg.WithDotEnv(".env", os.Getenv)
	if err == nil {
		err = run(os.Args[1:], getenv, os.Stdout, bedrockClient)
	}
	if err != nil {
		log.Fatalf("hitk: %v", err)
	}
}

// bedrockClient creates the Bedrock client Titan uses, through the AWS profile.
func bedrockClient(profile, region string) (embed.Invoker, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithSharedConfigProfile(profile), awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return bedrockruntime.NewFromConfig(awsCfg), nil
}

func run(args []string, getenv cfg.Getenv, out io.Writer, newBedrock func(profile, region string) (embed.Invoker, error)) error {
	env := cfg.New(getenv)
	fs := flag.NewFlagSet("hitk", flag.ContinueOnError)
	idx := indexing.Flags(fs, env)
	golden := fs.String("golden", env.String("GOLDEN_FILES", "evals/golden/*.jsonl:corpus/private/golden/*.jsonl"),
		"golden files, colon-separated globs; invitee questions live in the private repo [GOLDEN_FILES]")
	k := fs.Int("k", 5, "how many results count as found")
	writeCache := fs.String("write-cache", "", "save every vector this run embeds to this file (public text only)")
	minRate := fs.Float64("min", env.Float("HITK_MIN", 0), "fail when hits/scored is below this fraction; 0 = report only [HITK_MIN]")
	results := fs.String("results", env.String("HITK_RESULTS", "evals/results/hitk.jsonl"), `append one line per run to this file; "" = none [HITK_RESULTS]`)
	profile := fs.String("aws-profile", env.String("AWS_PROFILE", "cferrante-dev-admin"), "AWS profile for Titan [AWS_PROFILE]")
	region := fs.String("aws-region", env.String("AWS_REGION", "us-east-1"), "AWS region [AWS_REGION]")
	if err := env.Err(); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if idx.IndexPath == "build/dev/corpus.idx" { // don't overwrite make dev's index
		idx.IndexPath = "build/evals/corpus.idx"
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
	if *writeCache != "" {
		// Checked before any embedding, so a refused run costs nothing.
		if err := publicOnly(corpus.SplitDirs(idx.CorpusDirs), paths, items); err != nil {
			return fmt.Errorf("-write-cache: %w", err)
		}
	}

	ctx := context.Background()
	e, err := idx.NewEmbedder(func() (embed.Invoker, error) {
		if newBedrock == nil {
			return nil, errors.New("no Bedrock client")
		}
		return newBedrock(*profile, *region)
	})
	if err != nil {
		return err
	}
	var rec *embed.Recorder
	if *writeCache != "" {
		rec = embed.NewRecorder(e)
		e = rec
	}
	st, err := idx.Build(ctx, e)
	if err != nil {
		if idx.Embedder == "titan" && errors.Is(err, retrieval.ErrEmbed) {
			return fmt.Errorf("%w (if your AWS session has expired: aws sso login --profile %s)", err, *profile)
		}
		return err
	}
	if rec != nil && st.Invitee > 0 {
		return fmt.Errorf("-write-cache: the corpus has %d invitee chunks; cache only public text", st.Invitee)
	}
	r, err := retrieval.Load(idx.IndexPath, e)
	if err != nil {
		return err
	}
	defer r.Close()
	fmt.Fprintf(out, "%d questions from %s; %d chunks from %s with %s\n\n", len(items), strings.Join(paths, ", "), st.Chunks, idx.CorpusDirs, e.Model())

	res, err := evals.HitAtK(ctx, items, r, *k)
	if err != nil {
		return err
	}
	for _, x := range res {
		switch {
		case !x.Scored:
			fmt.Fprintf(out, "– %s  no expected chunk (the answer is scored in 6.3)\n", x.Item.ID)
		case x.Hit:
			fmt.Fprintf(out, "✓ %s  rank %d\n", x.Item.ID, x.Rank)
		default:
			fmt.Fprintf(out, "✗ %s  top %d: %s  (want %s)  %q\n", x.Item.ID, *k, strings.Join(x.Got, ", "),
				strings.Join(x.Item.ExpectedChunks, " or "), x.Item.Question)
		}
	}
	hits, scored := evals.Score(res)
	fmt.Fprintf(out, "\nhit@%d: %d/%d (%d%%)\n", *k, hits, scored, percent(hits, scored))
	perFile := map[string]fileScore{}
	for _, path := range paths {
		var mine []evals.Result
		for _, x := range res {
			if x.Item.File == path {
				mine = append(mine, x)
			}
		}
		h, s := evals.Score(mine)
		perFile[path] = fileScore{h, s}
		fmt.Fprintf(out, "  %s: %d/%d (%d%%)\n", path, h, s, percent(h, s))
	}

	if rec != nil {
		if err := rec.Save(*writeCache); err != nil {
			return err
		}
		fmt.Fprintf(out, "\nsaved the vectors to %s (%d input tokens billed)\n", *writeCache, st.Tokens+titanTokens(e))
	}
	if *results != "" {
		if err := appendResult(*results, e.Model(), *k, perFile); err != nil {
			return err
		}
	}
	if *minRate > 0 && (scored == 0 || float64(hits)/float64(scored) < *minRate) {
		rate := 0.0
		if scored > 0 {
			rate = float64(hits) / float64(scored)
		}
		return fmt.Errorf("hit@%d %d/%d (%.2f) is below the minimum %.2f", *k, hits, scored, rate, *minRate)
	}
	return nil
}

// publicOnly refuses anything private before it is embedded: a vector of
// private text can be partly turned back into the text, and the cache is
// committed to a public repo.
func publicOnly(dirs, goldenPaths []string, items []evals.Item) error {
	for _, p := range append(append([]string{}, dirs...), goldenPaths...) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if strings.Contains(filepath.ToSlash(abs)+"/", "/corpus/private/") {
			return fmt.Errorf("%s is under corpus/private; cache only public text", p)
		}
	}
	for _, it := range items {
		if it.Caller == "invitee" {
			return fmt.Errorf("%s is an invitee question; cache only public text", it.ID)
		}
	}
	return nil
}

// titanTokens is what the questions cost on top of the build's chunks.
func titanTokens(e embed.Embedder) int64 {
	if r, ok := e.(*embed.Recorder); ok {
		e = r.Embedder
	}
	if t, ok := e.(*embed.Titan); ok {
		return t.Tokens()
	}
	return 0
}

type fileScore struct {
	Hits   int `json:"hits"`
	Scored int `json:"scored"`
}

// appendResult adds one JSON line per run, so later runs (another embedder,
// a corpus edit) compare against earlier ones by commit.
func appendResult(path, model string, k int, files map[string]fileScore) error {
	commit, dirty := evals.GitState(".")
	line, err := json.Marshal(struct {
		Time     string               `json:"time"`
		Commit   string               `json:"commit"`
		Dirty    bool                 `json:"dirty"`
		Embedder string               `json:"embedder"`
		K        int                  `json:"k"`
		Files    map[string]fileScore `json:"files"`
	}{time.Now().UTC().Format(time.RFC3339), commit, dirty, model, k, files})
	if err != nil {
		return err
	}
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

func percent(hits, scored int) int {
	if scored == 0 {
		return 0
	}
	return 100 * hits / scored
}
