package embed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeOllama(t *testing.T, handle func(req map[string]any) (int, string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/embed" {
			t.Errorf("%s %s, want POST /api/embed", r.Method, r.URL.Path)
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		status, body := handle(req)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOllamaEmbedsABatch(t *testing.T) {
	var got map[string]any
	srv := fakeOllama(t, func(req map[string]any) (int, string) {
		got = req
		return 200, `{"model":"nomic-embed-text","embeddings":[[0.1,0.2,0.3],[0.4,0.5,0.6]]}`
	})
	o := Ollama{BaseURL: srv.URL, Name: "nomic-embed-text", D: 3}
	if o.Model() != "ollama/nomic-embed-text/3" || o.Dim() != 3 {
		t.Errorf("Model %q Dim %d", o.Model(), o.Dim())
	}
	v, err := o.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 2 || v[1][2] != float32(0.6) {
		t.Errorf("vectors = %v", v)
	}
	if got["model"] != "nomic-embed-text" || len(got["input"].([]any)) != 2 {
		t.Errorf("request = %v", got)
	}
}

func TestOllamaRejectsBadResponses(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"server error": {500, `{"error":"model not found"}`, "500"},
		"wrong count":  {200, `{"embeddings":[[0.1,0.2,0.3]]}`, "1 vectors"},
		"wrong size":   {200, `{"embeddings":[[0.1,0.2],[0.3,0.4]]}`, "size 2"},
		"not json":     {200, `nope`, "decode"},
	} {
		srv := fakeOllama(t, func(map[string]any) (int, string) { return tc.status, tc.body })
		_, err := Ollama{BaseURL: srv.URL, Name: "m", D: 3}.Embed(context.Background(), []string{"a", "b"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func TestOllamaWithNoTextsMakesNoCall(t *testing.T) {
	srv := fakeOllama(t, func(map[string]any) (int, string) {
		t.Error("called for no texts")
		return 200, `{"embeddings":[]}`
	})
	if v, err := (Ollama{BaseURL: srv.URL, Name: "m", D: 3}).Embed(context.Background(), nil); err != nil || len(v) != 0 {
		t.Errorf("got %v, %v", v, err)
	}
}
