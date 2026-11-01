package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/fake"
)

func noEnv(string) string { return "" }

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsAreLocalOnly(t *testing.T) {
	c, err := parse(nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.addr, "127.0.0.1:") || c.model != "qwen3:4b" || c.effort != "low" || c.invitee {
		t.Errorf("defaults = %+v", c)
	}
}

func TestSetupServesChatOverTheIndexedFixture(t *testing.T) {
	c, _ := parse([]string{"-embedder", "hash", "-corpus", "../../corpus/testdata/corpus",
		"-index", filepath.Join(t.TempDir(), "dev.idx")}, noEnv)
	m := &fake.Model{Name: "ollama", Events: []model.Event{{Kind: model.Text, Text: "Alpha is Go [1]."}}}
	var log bytes.Buffer
	h, cleanup, err := setup(context.Background(), c, m, &log)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.Contains(log.String(), "indexed 5 chunks") {
		t.Errorf("log = %q", log.String())
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	resp, err = http.Post(srv.URL+"/chat", "application/json", strings.NewReader(`{"message":"What is Alpha?"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	body.ReadFrom(resp.Body)
	for _, want := range []string{"event: citation", `"chunk_id":"proj-alpha#`, "event: done"} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("stream lacks %q:\n%s", want, body.String())
		}
	}
	// The fixed caller is a visitor, so the model never sees the private chunk.
	if prompt := m.Requests()[0].Messages[0].Content; strings.Contains(prompt, "consulting rate") {
		t.Error("visitor's prompt contains the invitee-only chunk")
	}
}

func TestUnknownEmbedder(t *testing.T) {
	c, _ := parse([]string{"-embedder", "magic"}, noEnv)
	if _, _, err := setup(context.Background(), c, &fake.Model{}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
}

var allEnv = map[string]string{
	"LISTEN_ADDR": "127.0.0.1:9999", "CORPUS_DIR": "/c", "INDEX_PATH": "/i.idx",
	"OLLAMA_BASE_URL": "http://gpu-box:11434", "EMBEDDER": "hash", "EMBED_MODEL": "mxbai-embed-large",
	"EMBED_DIM": "1024", "CHAT_MODEL": "llama3.1", "REASONING_EFFORT": "medium", "DEV_INVITEE": "true",
}

func TestEnvironmentSetsEveryOption(t *testing.T) {
	c, err := parse(nil, envOf(allEnv))
	if err != nil {
		t.Fatal(err)
	}
	want := config{addr: "127.0.0.1:9999", corpusDir: "/c", index: "/i.idx", ollamaURL: "http://gpu-box:11434",
		embedder: "hash", embedModel: "mxbai-embed-large", embedDim: 1024, model: "llama3.1", effort: "medium", invitee: true}
	if c != want {
		t.Errorf("config\n got %+v\nwant %+v", c, want)
	}
}

func TestFlagsOverrideTheEnvironment(t *testing.T) {
	c, err := parse([]string{"-addr", "127.0.0.1:7000", "-model", "qwen3:4b", "-invitee=false"}, envOf(allEnv))
	if err != nil {
		t.Fatal(err)
	}
	if c.addr != "127.0.0.1:7000" || c.model != "qwen3:4b" || c.invitee || c.effort != "medium" {
		t.Errorf("config = %+v", c)
	}
}

func TestInvalidEnvironmentStopsStartup(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"dim":     {"EMBED_DIM": "big"},
		"invitee": {"DEV_INVITEE": "maybe"},
	} {
		if _, err := parse(nil, envOf(m)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// OLLAMA_BASE_URL, EMBED_MODEL and EMBED_DIM must reach the embedder: a fake
// Ollama returning 4-dimension vectors lets the index build.
func TestOllamaSettingsReachTheEmbedder(t *testing.T) {
	var model string
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		model = req.Model
		vecs := make([][]float32, len(req.Input))
		for i := range vecs {
			vecs[i] = []float32{1, float32(i), 0, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
	}))
	defer ollama.Close()
	c, err := parse([]string{"-corpus", "../../corpus/testdata/corpus", "-index", filepath.Join(t.TempDir(), "dev.idx")},
		envOf(map[string]string{"OLLAMA_BASE_URL": ollama.URL, "EMBED_MODEL": "tiny-embed", "EMBED_DIM": "4"}))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	_, cleanup, err := setup(context.Background(), c, &fake.Model{}, &log)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	cleanup()
	if model != "tiny-embed" || !strings.Contains(log.String(), "ollama/tiny-embed/4") {
		t.Errorf("embed model %q, log %q", model, log.String())
	}
}
