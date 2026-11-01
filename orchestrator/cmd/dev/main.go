// Command dev serves the orchestrator on the laptop over Ollama (plan step
// 3.5): it builds the index from the corpus, then serves POST /chat as SSE.
//
//	make dev                      # Ollama: qwen3:4b + nomic-embed-text
//	make dev DEV_FLAGS=-invitee   # as an invitee, so private chunks appear
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/oai"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
	"github.com/cferrante/cferrante-dev/orchestrator/server"
)

type config struct {
	addr, corpusDir, index, embedder, model, effort string
	invitee                                         bool
}

func parse(args []string) (config, error) {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	var c config
	fs.StringVar(&c.addr, "addr", "127.0.0.1:8787", "listen address (localhost only by default)")
	fs.StringVar(&c.corpusDir, "corpus", "orchestrator/corpus/testdata/corpus", "corpus directory")
	fs.StringVar(&c.index, "index", "build/dev/corpus.idx", "where to build the index")
	fs.StringVar(&c.embedder, "embedder", "ollama", "ollama (nomic-embed-text) or hash (no Ollama needed)")
	fs.StringVar(&c.model, "model", "qwen3:4b", "Ollama chat model")
	fs.StringVar(&c.effort, "effort", "low", "reasoning effort; low keeps qwen3's thinking short on a CPU")
	fs.BoolVar(&c.invitee, "invitee", false, "act as an invitee (sees private chunks)")
	return c, fs.Parse(args)
}

func embedderFor(name string) (embed.Embedder, error) {
	switch name {
	case "ollama":
		return embed.Ollama{Name: "nomic-embed-text", D: 768}, nil
	case "hash":
		return embed.Hash{D: 256}, nil
	}
	return nil, fmt.Errorf("unknown -embedder %q", name)
}

// setup builds the index and returns the HTTP handler and a cleanup func.
func setup(ctx context.Context, c config, m model.Model, logw io.Writer) (http.Handler, func(), error) {
	e, err := embedderFor(c.embedder)
	if err != nil {
		return nil, nil, err
	}
	chunks, err := corpus.Chunker{Target: 1000}.ReadDir(c.corpusDir)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(c.index), 0o755); err != nil {
		return nil, nil, err
	}
	start := time.Now()
	if err := retrieval.Build(ctx, chunks, e, c.index); err != nil {
		return nil, nil, fmt.Errorf("building the index (is Ollama running?): %w", err)
	}
	r, err := retrieval.Load(c.index, e)
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(logw, "indexed %d chunks from %s with %s in %v\n", len(chunks), c.corpusDir, e.Model(), time.Since(start).Round(time.Millisecond))

	caller := auth.Caller{ID: "dev-visitor"}
	if c.invitee {
		caller = auth.Caller{ID: "dev-invitee", Groups: []string{auth.InviteeGroup}}
	}
	a := &agent.Agent{Model: m, Retriever: r, Store: agent.NewMemoryStore(), Clock: clock.Real{}}
	mux := http.NewServeMux()
	mux.Handle("/chat", &server.Chat{Agent: a, Auth: auth.Fixed(caller), Clock: clock.Real{}})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	return mux, func() { r.Close() }, nil
}

func main() {
	c, err := parse(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := &oai.Client{Provider: "ollama", BaseURL: oai.OllamaBaseURL, Model: c.model, ReasoningEffort: c.effort}
	h, cleanup, err := setup(ctx, c, m, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()
	srv := &http.Server{Addr: c.addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	fmt.Printf("serving http://%s/chat (model %s via Ollama, invitee=%v); Ctrl+C to stop\n", c.addr, c.model, c.invitee)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	fmt.Println("stopped")
}
