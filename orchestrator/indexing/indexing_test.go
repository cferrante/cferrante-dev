package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
)

func envOf(m map[string]string) config.Getenv { return func(k string) string { return m[k] } }

func parse(t *testing.T, args []string, env map[string]string) (Settings, error) {
	t.Helper()
	e := config.New(envOf(env))
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	s := Flags(fs, e)
	if err := e.Err(); err != nil {
		return Settings{}, err
	}
	if err := fs.Parse(args); err != nil {
		return Settings{}, err
	}
	return *s, nil
}

func TestDefaults(t *testing.T) {
	s, err := parse(t, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{CorpusDirs: "orchestrator/corpus/testdata/corpus", IndexPath: "build/dev/corpus.idx", ChunkTarget: 1000,
		Embedder: "ollama", OllamaURL: embed.OllamaBaseURL, OllamaModel: "nomic-embed-text", TitanModel: embed.TitanModel,
		EmbedCache: "evals/embeddings/titan-512.json"}
	if s != want {
		t.Errorf("defaults\n got %+v\nwant %+v", s, want)
	}
}

var allEnv = map[string]string{
	"CORPUS_DIRS": "a:b", "INDEX_PATH": "/i.idx", "CHUNK_TARGET": "500", "EMBEDDER": "titan",
	"OLLAMA_BASE_URL": "http://gpu:11434", "EMBED_MODEL": "mxbai", "EMBED_DIM": "1024", "TITAN_MODEL": "amazon.titan-x",
	"EMBED_CACHE": "/c.json",
}

func TestEnvironmentSetsEveryOption(t *testing.T) {
	s, err := parse(t, nil, allEnv)
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{CorpusDirs: "a:b", IndexPath: "/i.idx", ChunkTarget: 500, Embedder: "titan",
		OllamaURL: "http://gpu:11434", OllamaModel: "mxbai", Dim: 1024, TitanModel: "amazon.titan-x", EmbedCache: "/c.json"}
	if s != want {
		t.Errorf("settings\n got %+v\nwant %+v", s, want)
	}
}

func TestFlagsOverrideTheEnvironment(t *testing.T) {
	s, err := parse(t, []string{"-corpus", "c", "-index", "x.idx", "-target", "200", "-embedder", "hash", "-embed-dim", "64",
		"-embed-model", "m", "-titan-model", "t", "-ollama", "http://o", "-embed-cache", "k.json"}, allEnv)
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{CorpusDirs: "c", IndexPath: "x.idx", ChunkTarget: 200, Embedder: "hash", OllamaURL: "http://o",
		OllamaModel: "m", Dim: 64, TitanModel: "t", EmbedCache: "k.json"}
	if s != want {
		t.Errorf("settings\n got %+v\nwant %+v", s, want)
	}
}

func TestTheOldCorpusVariableIsAnError(t *testing.T) {
	// CORPUS_DIR (one folder) became CORPUS_DIRS; ignoring it would index the wrong corpus.
	if _, err := parse(t, nil, map[string]string{"CORPUS_DIR": "corpus"}); err == nil || !strings.Contains(err.Error(), "CORPUS_DIRS") {
		t.Errorf("err = %v, want one naming CORPUS_DIRS", err)
	}
}

type fakeTitan struct{ calls int }

func (f *fakeTitan) InvokeModel(_ context.Context, in *bedrockruntime.InvokeModelInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	f.calls++
	var body struct {
		Text string `json:"inputText"`
		Dim  int    `json:"dimensions"`
	}
	json.Unmarshal(in.Body, &body)
	v := make([]float32, body.Dim)
	for _, w := range strings.Fields(strings.ToLower(body.Text)) {
		v[len(w)%body.Dim]++ // enough structure for a valid, non-zero vector
	}
	raw, _ := json.Marshal(map[string]any{"embedding": v, "inputTextTokenCount": len(strings.Fields(body.Text))})
	return &bedrockruntime.InvokeModelOutput{Body: raw}, nil
}

func TestEmbedderForEachKind(t *testing.T) {
	titanCalls := 0
	newTitan := func() (embed.Invoker, error) { titanCalls++; return &fakeTitan{}, nil }
	for _, tc := range []struct {
		env   map[string]string
		model string
	}{
		{map[string]string{"EMBEDDER": "ollama"}, "ollama/nomic-embed-text/768"},
		{map[string]string{"EMBEDDER": "hash"}, "hash-v1-256"},
		{map[string]string{"EMBEDDER": "titan"}, "bedrock/amazon.titan-embed-text-v2:0/512"},
		{map[string]string{"EMBEDDER": "titan", "EMBED_DIM": "256"}, "bedrock/amazon.titan-embed-text-v2:0/256"},
	} {
		s, _ := parse(t, nil, tc.env)
		e, err := s.NewEmbedder(newTitan)
		if err != nil {
			t.Fatalf("%v: %v", tc.env, err)
		}
		if e.Model() != tc.model {
			t.Errorf("%v: model %q, want %q", tc.env, e.Model(), tc.model)
		}
	}
	// Only Titan needs AWS, so the others never create a client.
	if titanCalls != 2 {
		t.Errorf("Titan client created %d times, want 2", titanCalls)
	}
}

func TestEmbedderErrors(t *testing.T) {
	failing := func() (embed.Invoker, error) { return nil, errors.New("no AWS session") }
	for name, env := range map[string]map[string]string{
		"unknown":     {"EMBEDDER": "magic"},
		"titan dim":   {"EMBEDDER": "titan", "EMBED_DIM": "768"},
		"titan login": {"EMBEDDER": "titan"},
	} {
		s, _ := parse(t, nil, env)
		if _, err := s.NewEmbedder(failing); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestBuildIndexesEveryCorpusFolder(t *testing.T) {
	public, private := t.TempDir(), t.TempDir()
	write := func(dir, name, access string) {
		body := "---\ntitle: " + name + "\nurl: /" + name + "\naccess: " + access + "\n---\n## A\n\nSome text about " + name + "."
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(public, "about", "public")
	write(private, "rates", "invitee")
	index := filepath.Join(t.TempDir(), "sub", "corpus.idx")
	s, _ := parse(t, []string{"-corpus", public + ":" + private, "-index", index, "-embedder", "hash"}, nil)
	e, _ := s.NewEmbedder(nil)
	st, err := s.Build(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if st.Chunks != 2 || st.Invitee != 1 {
		t.Errorf("stats %+v, want 2 chunks, 1 invitee", st)
	}
	r, err := retrieval.Load(index, e)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, access, ok := r.Chunk("rates#a"); !ok || access != "invitee" {
		t.Errorf("rates#a: found %v, access %q", ok, access)
	}
}

// An index built with Ollama's nomic-embed-text must not be served with
// Titan, even at the same size: the vectors aren't comparable. The guard
// itself dates from step 3.4 (retrieval.Load); this pins it for the Titan
// switch, so it passed on its first run and was proven by breaking Load.
func TestANomicIndexIsRefusedUnderTitan(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&req)
		vecs := make([][]float32, len(req.Input))
		for i := range vecs {
			vecs[i] = make([]float32, 512)
			vecs[i][i%512] = 1
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
	}))
	defer ollama.Close()
	index := filepath.Join(t.TempDir(), "corpus.idx")
	s, _ := parse(t, []string{"-corpus", "../corpus/testdata/corpus", "-index", index, "-ollama", ollama.URL, "-embed-dim", "512"}, nil)
	nomic, _ := s.NewEmbedder(nil)
	if _, err := s.Build(context.Background(), nomic); err != nil {
		t.Fatal(err)
	}
	s.Embedder = "titan"
	titan, err := s.NewEmbedder(func() (embed.Invoker, error) { return &fakeTitan{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retrieval.Load(index, titan); err == nil || !strings.Contains(err.Error(), "rebuild the index") {
		t.Errorf("err = %v, want a refusal asking to rebuild", err)
	}
}

// The cache embedder reports the model that filled it, so an index built in
// CI records Titan, and it never creates an AWS client.
func TestTheCacheEmbedderReportsItsRecordedModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	rec := embed.NewRecorder(embed.Hash{D: 4})
	if _, err := rec.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Save(path); err != nil {
		t.Fatal(err)
	}
	s, _ := parse(t, nil, map[string]string{"EMBEDDER": "cache", "EMBED_CACHE": path})
	e, err := s.NewEmbedder(func() (embed.Invoker, error) { t.Error("created an AWS client"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if e.Model() != "hash-v1-4" {
		t.Errorf("model %q, want hash-v1-4", e.Model())
	}
}
