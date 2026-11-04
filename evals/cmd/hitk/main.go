// Command hitk runs the golden set's retrieval check (plan step 6.1): it
// indexes the corpus, searches each golden question as its caller would,
// and prints ✓ or ✗ per question with the overall hit@k. It reports only;
// the CI gate and its threshold come in step 6.2.
//
//	go run ./evals/cmd/hitk                       # .env settings, Ollama embeddings
//	go run ./evals/cmd/hitk -embedder hash        # no model, keyword-only in effect
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cferrante/cferrante-dev/evals"
	cfg "github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/indexing"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
)

func main() {
	getenv, err := cfg.WithDotEnv(".env", os.Getenv)
	if err == nil {
		err = run(os.Args[1:], getenv, os.Stdout)
	}
	if err != nil {
		log.Fatalf("hitk: %v", err)
	}
}

func run(args []string, getenv cfg.Getenv, out io.Writer) error {
	env := cfg.New(getenv)
	fs := flag.NewFlagSet("hitk", flag.ContinueOnError)
	idx := indexing.Flags(fs, env)
	golden := fs.String("golden", env.String("GOLDEN_FILES", "evals/golden/*.jsonl:corpus/private/golden/*.jsonl"),
		"golden files, colon-separated globs; invitee questions live in the private repo [GOLDEN_FILES]")
	k := fs.Int("k", 5, "how many results count as found")
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

	ctx := context.Background()
	e, err := idx.NewEmbedder(nil) // ollama or hash; Titan arrives with the 6.2 gate
	if err != nil {
		return err
	}
	st, err := idx.Build(ctx, e)
	if err != nil {
		return err
	}
	r, err := retrieval.Load(idx.IndexPath, e)
	if err != nil {
		return err
	}
	defer r.Close()
	fmt.Fprintf(out, "%d questions from %s; %d chunks from %s with %s\n\n", len(items), strings.Join(paths, ", "), st.Chunks, idx.CorpusDirs, e.Model())

	results, err := evals.HitAtK(ctx, items, r, *k)
	if err != nil {
		return err
	}
	for _, res := range results {
		switch {
		case !res.Scored:
			fmt.Fprintf(out, "– %s  no expected chunk (the answer is scored in 6.3)\n", res.Item.ID)
		case res.Hit:
			fmt.Fprintf(out, "✓ %s  rank %d\n", res.Item.ID, res.Rank)
		default:
			fmt.Fprintf(out, "✗ %s  top %d: %s  (want %s)  %q\n", res.Item.ID, *k, strings.Join(res.Got, ", "),
				strings.Join(res.Item.ExpectedChunks, " or "), res.Item.Question)
		}
	}
	hits, scored := evals.Score(results)
	pct := 0
	if scored > 0 {
		pct = 100 * hits / scored
	}
	fmt.Fprintf(out, "\nhit@%d: %d/%d (%d%%)\n", *k, hits, scored, pct)
	return nil
}
