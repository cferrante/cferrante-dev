// Command dev serves the orchestrator on the laptop over Ollama (plan step
// 3.5): it builds the index from the corpus, then serves POST /chat as SSE.
//
//	make dev                      # Ollama: qwen3:4b + nomic-embed-text
//	make dev DEV_FLAGS=-invitee   # as an invitee, so private chunks appear
//
// Every option is also an environment variable, read from the shell or a
// git-ignored .env file (see .env.example); a flag overrides both.
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
	cfg "github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/oai"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
	"github.com/cferrante/cferrante-dev/orchestrator/server"
)

type config struct {
	addr, corpusDir, index, ollamaURL   string
	embedder, embedModel, model, effort string
	embedDim                            int // 0: the embedder's own default
	invitee                             bool
}

// parse reads the configuration. Every option has an environment variable
// (design §11), and a flag overrides it: flag > environment > default.
func parse(args []string, getenv cfg.Getenv) (config, error) {
	env := cfg.New(getenv)
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	var c config
	fs.StringVar(&c.addr, "addr", env.String("LISTEN_ADDR", "127.0.0.1:8787"), "listen address, localhost only by default [LISTEN_ADDR]")
	fs.StringVar(&c.corpusDir, "corpus", env.String("CORPUS_DIR", "orchestrator/corpus/testdata/corpus"), "corpus directory [CORPUS_DIR]")
	fs.StringVar(&c.index, "index", env.String("INDEX_PATH", "build/dev/corpus.idx"), "where to build the index [INDEX_PATH]")
	fs.StringVar(&c.ollamaURL, "ollama", env.String("OLLAMA_BASE_URL", embed.OllamaBaseURL), "Ollama's base URL [OLLAMA_BASE_URL]")
	fs.StringVar(&c.embedder, "embedder", env.String("EMBEDDER", "ollama"), "ollama, or hash (no Ollama needed) [EMBEDDER]")
	fs.StringVar(&c.embedModel, "embed-model", env.String("EMBED_MODEL", "nomic-embed-text"), "Ollama embedding model [EMBED_MODEL]")
	fs.IntVar(&c.embedDim, "embed-dim", env.Int("EMBED_DIM", 0), "embedding size; 0 = 768 for ollama, 256 for hash [EMBED_DIM]")
	fs.StringVar(&c.model, "model", env.String("CHAT_MODEL", "qwen3:4b"), "Ollama chat model [CHAT_MODEL]")
	fs.StringVar(&c.effort, "effort", env.String("REASONING_EFFORT", "low"), "reasoning effort; low keeps qwen3's thinking short on a CPU [REASONING_EFFORT]")
	fs.BoolVar(&c.invitee, "invitee", env.Bool("DEV_INVITEE", false), "act as an invitee, who sees private chunks [DEV_INVITEE]")
	if err := env.Err(); err != nil {
		return c, err
	}
	return c, fs.Parse(args)
}

func embedderFor(c config) (embed.Embedder, error) {
	switch c.embedder {
	case "ollama":
		return embed.Ollama{BaseURL: c.ollamaURL, Name: c.embedModel, D: dimOr(c.embedDim, 768)}, nil
	case "hash":
		return embed.Hash{D: dimOr(c.embedDim, 256)}, nil
	}
	return nil, fmt.Errorf("unknown embedder %q", c.embedder)
}

func dimOr(d, def int) int {
	if d > 0 {
		return d
	}
	return def
}

// setup builds the index and returns the HTTP handler and a cleanup func.
func setup(ctx context.Context, c config, m model.Model, logw io.Writer) (http.Handler, func(), error) {
	e, err := embedderFor(c)
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
	// Settings: flag > shell environment > .env in the working directory > default.
	getenv, err := cfg.WithDotEnv(".env", os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	c, err := parse(os.Args[1:], getenv)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := &oai.Client{Provider: "ollama", BaseURL: c.ollamaURL + "/v1", Model: c.model, ReasoningEffort: c.effort}
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
	fmt.Printf("serving http://%s/chat (model %s, effort %s, via Ollama at %s, invitee=%v); Ctrl+C to stop\n",
		c.addr, c.model, c.effort, c.ollamaURL, c.invitee)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	fmt.Println("stopped")
}
