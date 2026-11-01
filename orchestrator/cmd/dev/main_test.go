package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/fake"
)

func TestDefaultsAreLocalOnly(t *testing.T) {
	c, err := parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.addr, "127.0.0.1:") || c.model != "qwen3:4b" || c.effort != "low" || c.invitee {
		t.Errorf("defaults = %+v", c)
	}
}

func TestSetupServesChatOverTheIndexedFixture(t *testing.T) {
	c, _ := parse([]string{"-embedder", "hash", "-corpus", "../../corpus/testdata/corpus",
		"-index", filepath.Join(t.TempDir(), "dev.idx")})
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
	c, _ := parse([]string{"-embedder", "magic"})
	if _, _, err := setup(context.Background(), c, &fake.Model{}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
}
