package wording

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func load(t *testing.T, content string) (Wording, error) {
	t.Helper()
	return Load("m.json", func(string) ([]byte, error) { return []byte(content), nil })
}

func TestDefaultsWithoutAFile(t *testing.T) {
	w, err := Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"collection":      "portfolio",
		"retrieval_found": "Found {excerpts} from {sources}: {titles}.",
		"network":         "I can't reach the {assistant} right now. Check your connection, then try again.",
	} {
		if got := w.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if len(w.Overrides()) != 0 {
		t.Errorf("overrides = %v, want none", w.Overrides())
	}
}

func TestAFileOverridesSomeKeys(t *testing.T) {
	w, err := load(t, `{"collection": "work", "excerpt_one": "snippet", "excerpt_other": "snippets"}`)
	if err != nil {
		t.Fatal(err)
	}
	if w.Get("collection") != "work" || w.Get("source_one") != "source" {
		t.Errorf("collection %q, source_one %q", w.Get("collection"), w.Get("source_one"))
	}
	if got := w.Count(1, "excerpt") + " / " + w.Count(4, "excerpt") + " / " + w.Count(0, "source"); got != "1 snippet / 4 snippets / 0 sources" {
		t.Errorf("counts = %q", got)
	}
	if o := w.Overrides(); len(o) != 3 || o["collection"] != "work" {
		t.Errorf("overrides = %v", o)
	}
}

func TestBadFilesAreRefused(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"not JSON":             {`{`, "JSON object"},
		"unknown key":          {`{"pasages": "x"}`, `unknown key "pasages"`},
		"not a string":         {`{"collection": 3}`, `"collection": want a JSON string`},
		"empty":                {`{"collection": "  "}`, `"collection" is empty`},
		"misspelt placeholder": {`{"retrieval_found": "Found {exerpts} from {sources}: {titles}."}`, `{exerpts}`},
		"placeholder not here": {`{"network": "No {titles} today."}`, `{titles}`},
		"braces in a noun":     {`{"source_one": "{collection}"}`, `{collection}`},
		"missing required":     {`{"retrieval_found": "Found {excerpts}."}`, `needs {sources}`},
		"card needs excerpts":  {`{"retrieval_card": "score {score}"}`, `needs {excerpts}`},
	} {
		_, err := load(t, tc.content)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "m.json") {
			t.Errorf("%s: err = %v, want one naming m.json and %q", name, err, tc.want)
		}
	}
	if _, err := Load("m.json", func(string) ([]byte, error) { return nil, errors.New("no such file") }); err == nil {
		t.Error("unreadable file: want an error")
	}
}

func TestFill(t *testing.T) {
	got := Fill("Found {excerpts} from {sources}.", map[string]string{"excerpts": "4 excerpts", "sources": "2 sources"})
	if got != "Found 4 excerpts from 2 sources." {
		t.Errorf("Fill = %q", got)
	}
}

// The page keeps its own copy of the defaults (web/js/messages.js), since
// it's static files; they must equal these, key for key and word for word.
func TestDefaultsMatchThePage(t *testing.T) {
	src, err := os.ReadFile("../../web/js/messages.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "export const defaultMessages = {")
	if start < 0 {
		t.Fatal("defaultMessages not found in messages.js")
	}
	end := strings.Index(s[start:], "\n};")
	page := map[string]string{}
	lines := strings.Split(s[start:start+end], "\n")[1:]
	for i := 0; i < len(lines); i++ {
		key, value, ok := strings.Cut(strings.TrimSpace(lines[i]), ":")
		if !ok || strings.HasPrefix(key, "//") {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" && i+1 < len(lines) { // deno fmt may wrap a long value onto the next line
			i++
			value = strings.TrimSpace(lines[i])
		}
		var v string
		if err := json.Unmarshal([]byte(strings.TrimSuffix(value, ",")), &v); err != nil {
			t.Fatalf("messages.js %s: %v", key, err)
		}
		page[key] = v
	}
	for _, key := range Keys() {
		if page[key] != Default(key) {
			t.Errorf("%s: page %q, server %q", key, page[key], Default(key))
		}
	}
	for key := range page {
		if !slices.Contains(Keys(), key) {
			t.Errorf("%s is on the page but not in the server's keys", key)
		}
	}
}

// The committed example lists every key with its default, and is valid.
func TestExampleFileHasEveryDefault(t *testing.T) {
	w, err := Load("../../web/messages.example.json", os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range Keys() {
		if w.Overrides()[key] != Default(key) {
			t.Errorf("example %s = %q, want the default %q", key, w.Overrides()[key], Default(key))
		}
	}
}
