// Command indexer builds the retrieval index from the Markdown corpus
// (design §5, plan step 3.4).
//
//	go run ./orchestrator/cmd/indexer -corpus corpus -out build/corpus.idx
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "indexer:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("indexer", flag.ContinueOnError)
	dir := fs.String("corpus", "", "directory of Markdown files (required)")
	path := fs.String("out", "", "index file to write; the sidecar goes to <out>.json (required)")
	target := fs.Int("target", 1000, "preferred chunk size in bytes")
	embedder := fs.String("embedder", "hash", "embedding model: hash (no download; for tests and offline use)")
	dim := fs.Int("dim", 256, "vector size for the hash embedder")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *path == "" {
		return errors.New("-corpus and -out are required")
	}
	var e embed.Embedder
	switch *embedder {
	case "hash":
		e = embed.Hash{D: *dim}
	default:
		return fmt.Errorf("unknown -embedder %q", *embedder)
	}
	chunks, err := corpus.Chunker{Target: *target}.ReadDir(*dir)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks: %s has no Markdown files with content", *dir)
	}
	if err := retrieval.Build(context.Background(), chunks, e, *path); err != nil {
		return err
	}
	invitee := 0
	for _, c := range chunks {
		if c.Access == "invitee" {
			invitee++
		}
	}
	fmt.Fprintf(out, "indexed %d chunks (%d public, %d invitee) with %s -> %s\n",
		len(chunks), len(chunks)-invitee, invitee, e.Model(), *path)
	return nil
}
