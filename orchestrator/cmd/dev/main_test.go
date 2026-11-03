package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/fake"
	"github.com/cferrante/cferrante-dev/orchestrator/model/oai"
)

func noEnv(string) string { return "" }

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsAreLocalOnly(t *testing.T) {
	c, err := parse(nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.addr, "127.0.0.1:") || c.model != "qwen3:4b" || c.effort != "low" || c.invitee ||
		c.deadline != 5*time.Minute {
		t.Errorf("defaults = %+v", c)
	}
}

func TestSetupServesChatOverTheIndexedFixture(t *testing.T) {
	c, _ := parse([]string{"-embedder", "hash", "-corpus", "../../corpus/testdata/corpus", "-suggested", "../../corpus/testdata/suggested.json",
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
	"CONTACT_OPTIONS": "Use the form.", "AVAILABILITY": "Tue–Thu", "TURN_DEADLINE": "90s",
	"WEB_DIR": "/site", "ASSISTANT_NAME": "Concierge", "SUGGESTED_FILE": "/s.json",
	"CHAT_PROVIDER": "groq", "GROQ_MODEL": "openai/gpt-oss-20b", "GROQ_KEY_PARAM": "/k", "AWS_PROFILE": "p", "AWS_REGION": "eu-west-1",
}

func TestEnvironmentSetsEveryOption(t *testing.T) {
	c, err := parse(nil, envOf(allEnv))
	if err != nil {
		t.Fatal(err)
	}
	want := config{addr: "127.0.0.1:9999", corpusDir: "/c", index: "/i.idx", ollamaURL: "http://gpu-box:11434",
		embedder: "hash", embedModel: "mxbai-embed-large", embedDim: 1024, model: "llama3.1", effort: "medium", invitee: true,
		contact: "Use the form.", availability: "Tue–Thu", deadline: 90 * time.Second, webDir: "/site", assistant: "Concierge", suggestedFile: "/s.json",
		provider: "groq", groqModel: "openai/gpt-oss-20b", groqKeyParam: "/k", awsProfile: "p", awsRegion: "eu-west-1"}
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
	c, err := parse([]string{"-corpus", "../../corpus/testdata/corpus", "-suggested", "../../corpus/testdata/suggested.json", "-index", filepath.Join(t.TempDir(), "dev.idx")},
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

func TestDevOffersToolsAndLogsConfirmedActions(t *testing.T) {
	c, _ := parse([]string{"-embedder", "hash", "-corpus", "../../corpus/testdata/corpus", "-suggested", "../../corpus/testdata/suggested.json", "-invitee",
		"-index", filepath.Join(t.TempDir(), "dev.idx")}, envOf(map[string]string{"AVAILABILITY": "Tue–Thu"}))
	m := &fake.Model{Name: "ollama", Turns: [][]model.Event{
		{{Kind: model.ToolCallEvent, ToolCall: &model.ToolCall{ID: "c1", Name: "send_message", Arguments: []byte(`{"message":"hello"}`)}}},
		{{Kind: model.Text, Text: "Please confirm."}},
	}}
	var log bytes.Buffer
	h, cleanup, err := setup(context.Background(), c, m, &log)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	srv := httptest.NewServer(h)
	defer srv.Close()
	body := func(payload string) string {
		resp, err := http.Post(srv.URL+"/chat", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		return b.String()
	}
	first := body(`{"message":"tell him hello"}`)
	if len(m.Requests()[0].Tools) != 4 {
		t.Errorf("invitee shown %d tools, want 4", len(m.Requests()[0].Tools))
	}
	i := strings.Index(first, `"action_id":"`)
	if i < 0 {
		t.Fatalf("no confirm_request in:\n%s", first)
	}
	id := first[i+len(`"action_id":"`):]
	id = id[:strings.Index(id, `"`)]
	if strings.Contains(log.String(), "outbox:") {
		t.Fatal("sent before confirmation")
	}
	second := body(`{"confirm":{"action_id":"` + id + `"}}`)
	if !strings.Contains(second, "Done:") || !strings.Contains(log.String(), `outbox: send_message from dev-invitee: {"message":"hello"}`) {
		t.Errorf("confirm stream:\n%s\nlog:\n%s", second, log.String())
	}
}

// Models without thinking (llama3.1) reject reasoning_effort; "none" leaves it
// out, which an empty variable can't do because empty means unset.
func TestEffortNoneOmitsReasoningEffort(t *testing.T) {
	c, err := parse(nil, envOf(map[string]string{"REASONING_EFFORT": "none"}))
	if err != nil || c.effort != "" {
		t.Errorf("effort = %q, err %v; want empty", c.effort, err)
	}
}

func TestDevServesThePageWithItsCSP(t *testing.T) {
	web := t.TempDir()
	os.WriteFile(filepath.Join(web, "index.html"), []byte("<title>receptionist</title>"), 0o644)
	c, _ := parse([]string{"-embedder", "hash", "-corpus", "../../corpus/testdata/corpus", "-suggested", "../../corpus/testdata/suggested.json",
		"-index", filepath.Join(t.TempDir(), "dev.idx")}, envOf(map[string]string{"WEB_DIR": web}))
	h, cleanup, err := setup(context.Background(), c, &fake.Model{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(b.String(), "receptionist") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("GET / = %d, CSP %q, body %q", resp.StatusCode, resp.Header.Get("Content-Security-Policy"), b.String())
	}
}

func TestDevServesSuggestedQuestionsFromTheFile(t *testing.T) {
	c, _ := parse([]string{"-embedder", "hash", "-corpus", "../../corpus/testdata/corpus",
		"-index", filepath.Join(t.TempDir(), "dev.idx")},
		envOf(map[string]string{"SUGGESTED_FILE": "../../corpus/testdata/suggested.json"}))
	m := &fake.Model{Name: "ollama"}
	h, cleanup, err := setup(context.Background(), c, m, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/suggested")
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]string
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 2 || list[0]["id"] != "alpha-search" {
		t.Fatalf("visitor list = %v", list)
	}
	resp, err = http.Post(srv.URL+"/chat", "application/json", strings.NewReader(`{"suggested_id":"alpha-search"}`))
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	body.ReadFrom(resp.Body)
	resp.Body.Close()
	if !strings.Contains(body.String(), "fuses keyword and vector search") || !strings.Contains(body.String(), "event: citation") ||
		len(m.Requests()) != 0 {
		t.Errorf("model calls %d, stream:\n%s", len(m.Requests()), body.String())
	}
}

func TestDevRefusesABadSuggestionsFile(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(bad, []byte(`[{"id":"x","question":"q","answer":"a [1]","citations":["private-notes#rates"],"access":"public"}]`), 0o600)
	c, _ := parse([]string{"-embedder", "hash", "-corpus", "../../corpus/testdata/corpus",
		"-index", filepath.Join(t.TempDir(), "dev.idx")}, envOf(map[string]string{"SUGGESTED_FILE": bad}))
	if _, _, err := setup(context.Background(), c, &fake.Model{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "invitee-only") {
		t.Errorf("err = %v, want the leak refused", err)
	}
}

func TestGroqSettingsFromTheEnvironment(t *testing.T) {
	c, err := parse(nil, envOf(map[string]string{
		"CHAT_PROVIDER": "groq", "GROQ_MODEL": "openai/gpt-oss-20b", "GROQ_KEY_PARAM": "/x/key",
		"AWS_PROFILE": "someone", "AWS_REGION": "eu-west-1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.provider != "groq" || c.groqModel != "openai/gpt-oss-20b" || c.groqKeyParam != "/x/key" ||
		c.awsProfile != "someone" || c.awsRegion != "eu-west-1" {
		t.Errorf("config = %+v", c)
	}
	d, _ := parse(nil, noEnv)
	if d.provider != "ollama" || d.groqModel != "openai/gpt-oss-120b" || d.groqKeyParam != "/cferrante-dev/groq-api-key" ||
		d.awsProfile != "cferrante-dev-admin" || d.awsRegion != "us-east-1" {
		t.Errorf("defaults = %+v", d)
	}
	if _, err := parse(nil, envOf(map[string]string{"CHAT_PROVIDER": "openai"})); err == nil {
		t.Error("unknown provider: want an error")
	}
}

func TestReasoningEffortDefaultsPerProvider(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{}, "low"},                                                     // qwen3 on a CPU
		{map[string]string{"CHAT_PROVIDER": "groq"}, ""},                                 // Groq's own default
		{map[string]string{"CHAT_PROVIDER": "groq", "REASONING_EFFORT": "high"}, "high"}, // explicit wins
		{map[string]string{"REASONING_EFFORT": "none"}, ""},                              // omit the field
	} {
		c, err := parse(nil, envOf(tc.env))
		if err != nil || c.effort != tc.want {
			t.Errorf("%v: effort %q, err %v; want %q", tc.env, c.effort, err, tc.want)
		}
	}
}

func TestChatModelForEachProvider(t *testing.T) {
	var asked string
	secret := func(_ context.Context, name string) (string, error) { asked = name; return "test-value-not-a-real-key", nil }
	c, _ := parse(nil, envOf(map[string]string{"CHAT_PROVIDER": "groq"}))
	m, err := chatModel(context.Background(), c, secret)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := m.(*oai.Client)
	if !ok || g.Provider != "groq" || g.BaseURL != oai.GroqBaseURL || g.Model != "openai/gpt-oss-120b" ||
		g.APIKey != "test-value-not-a-real-key" || asked != "/cferrante-dev/groq-api-key" {
		t.Errorf("groq model = %+v, asked %q", m, asked)
	}

	asked = ""
	o, _ := parse(nil, envOf(map[string]string{"OLLAMA_BASE_URL": "http://gpu:11434"}))
	m, err = chatModel(context.Background(), o, secret)
	if g, ok := m.(*oai.Client); err != nil || !ok || g.Provider != "ollama" || g.BaseURL != "http://gpu:11434/v1" || g.APIKey != "" || asked != "" {
		t.Errorf("ollama model = %+v, err %v; asked for a secret: %q", m, err, asked)
	}
}

func TestAnUnreadableGroqKeyExplainsHowToLogIn(t *testing.T) {
	failing := func(context.Context, string) (string, error) {
		return "", errors.New("reading SSM parameter /cferrante-dev/groq-api-key: token has expired")
	}
	c, _ := parse(nil, envOf(map[string]string{"CHAT_PROVIDER": "groq"}))
	_, err := chatModel(context.Background(), c, failing)
	if err == nil || !strings.Contains(err.Error(), "aws sso login --profile cferrante-dev-admin") {
		t.Errorf("err = %v, want the login command", err)
	}
}
