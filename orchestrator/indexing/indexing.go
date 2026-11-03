// Package indexing holds the settings shared by everything that builds the
// retrieval index (design §5): which corpus folders, where the index goes,
// and which embedder. The indexer and `make dev` both use it, so they read
// the same variables and build the same index.
package indexing

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
)

// Settings is comparable, so a command's whole config can be compared in tests.
type Settings struct {
	CorpusDirs  string // colon-separated, like PATH
	IndexPath   string // the sidecar goes to <IndexPath>.json
	ChunkTarget int    // preferred chunk size in bytes
	Embedder    string // ollama, titan or hash
	OllamaURL   string
	OllamaModel string
	Dim         int // 0: the embedder's default (768 ollama, 512 titan, 256 hash)
	TitanModel  string
}

// Flags registers the settings as flags whose defaults come from env:
// flag > environment > built-in default (design §11).
func Flags(fs *flag.FlagSet, env *config.Env) *Settings {
	env.Renamed("CORPUS_DIR", "CORPUS_DIRS")
	s := &Settings{}
	fs.StringVar(&s.CorpusDirs, "corpus", env.String("CORPUS_DIRS", "orchestrator/corpus/testdata/corpus"), "corpus folders, colon-separated, e.g. corpus/public:corpus/private [CORPUS_DIRS]")
	fs.StringVar(&s.IndexPath, "index", env.String("INDEX_PATH", "build/dev/corpus.idx"), "index file to write; the sidecar goes to <index>.json [INDEX_PATH]")
	fs.IntVar(&s.ChunkTarget, "target", env.Int("CHUNK_TARGET", 1000), "preferred chunk size in bytes [CHUNK_TARGET]")
	fs.StringVar(&s.Embedder, "embedder", env.String("EMBEDDER", "ollama"), "ollama, titan (Bedrock), or hash (no model; tests and offline) [EMBEDDER]")
	fs.StringVar(&s.OllamaURL, "ollama", env.String("OLLAMA_BASE_URL", embed.OllamaBaseURL), "Ollama's base URL [OLLAMA_BASE_URL]")
	fs.StringVar(&s.OllamaModel, "embed-model", env.String("EMBED_MODEL", "nomic-embed-text"), "Ollama embedding model [EMBED_MODEL]")
	fs.IntVar(&s.Dim, "embed-dim", env.Int("EMBED_DIM", 0), "embedding size; 0 = 768 for ollama, 512 for titan, 256 for hash [EMBED_DIM]")
	fs.StringVar(&s.TitanModel, "titan-model", env.String("TITAN_MODEL", embed.TitanModel), "Bedrock embedding model when -embedder=titan [TITAN_MODEL]")
	return s
}

// NewEmbedder builds the configured embedder. newTitan creates the Bedrock
// client and is called only for titan, so other runs never touch AWS.
func (s Settings) NewEmbedder(newTitan func() (embed.Invoker, error)) (embed.Embedder, error) {
	switch s.Embedder {
	case "ollama":
		return embed.Ollama{BaseURL: s.OllamaURL, Name: s.OllamaModel, D: dimOr(s.Dim, 768)}, nil
	case "hash":
		return embed.Hash{D: dimOr(s.Dim, 256)}, nil
	case "titan":
		if newTitan == nil {
			return nil, fmt.Errorf("embedder titan: no Bedrock client")
		}
		api, err := newTitan()
		if err != nil {
			return nil, err
		}
		return embed.NewTitan(api, s.TitanModel, dimOr(s.Dim, 512))
	}
	return nil, fmt.Errorf("unknown embedder %q: want ollama, titan or hash", s.Embedder)
}

func dimOr(d, def int) int {
	if d > 0 {
		return d
	}
	return def
}

// Stats describes a finished build.
type Stats struct {
	Chunks, Invitee int
	Elapsed         time.Duration
	Tokens          int64 // input tokens billed by Titan; 0 for other embedders
}

// Build reads every corpus folder and writes the index with e.
func (s Settings) Build(ctx context.Context, e embed.Embedder) (Stats, error) {
	start := time.Now()
	dirs := corpus.SplitDirs(s.CorpusDirs)
	if len(dirs) == 0 {
		return Stats{}, fmt.Errorf("no corpus folders: set CORPUS_DIRS or -corpus")
	}
	chunks, err := corpus.Chunker{Target: s.ChunkTarget}.ReadDirs(dirs)
	if err != nil {
		return Stats{}, err
	}
	if len(chunks) == 0 {
		return Stats{}, fmt.Errorf("no chunks: %s has no Markdown files with content", s.CorpusDirs)
	}
	if err := os.MkdirAll(filepath.Dir(s.IndexPath), 0o755); err != nil {
		return Stats{}, err
	}
	var before int64
	t, isTitan := e.(*embed.Titan)
	if isTitan {
		before = t.Tokens()
	}
	if err := retrieval.Build(ctx, chunks, e, s.IndexPath); err != nil {
		return Stats{}, err
	}
	st := Stats{Chunks: len(chunks), Elapsed: time.Since(start)}
	if isTitan {
		st.Tokens = t.Tokens() - before
	}
	for _, c := range chunks {
		if c.Access == "invitee" {
			st.Invitee++
		}
	}
	return st, nil
}
