package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func site(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "js"), 0o755)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>t</title>"), 0o644)
	os.WriteFile(filepath.Join(dir, "js", "app.js"), []byte("export {};"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("outside"), 0o644)
	return dir
}

func TestStaticServesThePageWithSecurityHeaders(t *testing.T) {
	srv := httptest.NewServer(Static(site(t)))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "<title>t</title>") {
		t.Fatalf("GET / = %d %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != ContentSecurityPolicy {
		t.Errorf("CSP = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
}

// Module scripts only run when served as JavaScript.
func TestStaticServesModulesAsJavaScript(t *testing.T) {
	srv := httptest.NewServer(Static(site(t)))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("no CSP on assets")
	}
}

func TestStaticDoesNotEscapeItsDirectory(t *testing.T) {
	srv := httptest.NewServer(Static(site(t)))
	defer srv.Close()
	for _, p := range []string{"/../secret.txt", "/%2e%2e/secret.txt", "/js/../../secret.txt"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+p, nil)
		req.URL.Opaque = p // send the path as written, without client-side cleaning
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "outside") {
			t.Errorf("%s served a file outside the site", p)
		}
	}
}
