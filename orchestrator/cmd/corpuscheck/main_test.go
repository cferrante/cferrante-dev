package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func doc(access string) string {
	return "---\ntitle: T\nurl: /t\naccess: " + access + "\n---\n## A\n\nText.\n"
}

func problemsFor(files map[string]string) []string {
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	return check(paths, func(p string) ([]byte, error) { return []byte(files[p]), nil })
}

func TestACommittedInviteeFileFails(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"invitee in corpus/public":  {"corpus/public/rates.md": doc("invitee")},
		"invitee anywhere else":     {"docs/notes.md": doc("invitee")},
		"case and spacing":          {"docs/x.md": "---\r\ntitle: T\r\n  Access :  Invitee \r\n---\r\nText"},
		"a typo is not public":      {"corpus/public/x.md": doc("invite")},
		"byte order mark":           {"docs/bom.md": "\xef\xbb\xbf" + doc("invitee")},
		"anything in private":       {"corpus/private/notes.md": doc("public")},
		"non-Markdown in private":   {"corpus/private/data.json": "{}"},
		"no front matter in corpus": {"corpus/public/x.md": "## Just text\n"},
		"no access in corpus":       {"corpus/public/x.md": "---\ntitle: T\nurl: /t\n---\nText"},
	} {
		got := problemsFor(files)
		if len(got) != 1 {
			t.Errorf("%s: problems %q, want one", name, got)
			continue
		}
		for path := range files {
			if !strings.Contains(got[0], path) {
				t.Errorf("%s: problem %q doesn't name %s", name, got[0], path)
			}
		}
	}
}

func TestPublicAndOrdinaryFilesPass(t *testing.T) {
	got := problemsFor(map[string]string{
		"corpus/public/about.md":                         doc("public"),
		"corpus/README.md":                               "# Corpus\n\nHow the folders work. access: invitee is explained here.\n",
		"corpus/public/.gitkeep":                         "",
		"docs/design.md":                                 "# Design\n\n---\n\naccess: invitee\n",
		"orchestrator/corpus/testdata/corpus/private.md": doc("invitee"), // test fixture, deliberately invitee
		"README.md":                                      "---\nlayout: page\n---\nHi",
	})
	if len(got) != 0 {
		t.Errorf("problems %q, want none", got)
	}
}

// End to end against a real repository: a staged invitee file fails, and
// git-ignored files in corpus/private are never looked at.
func TestRunOnARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, path)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write(".gitignore", "/corpus/private/\n")
	write("corpus/public/about.md", doc("public"))
	write("corpus/private/rates.md", doc("invitee")) // ignored: fine
	git("add", ".")
	var out bytes.Buffer
	if err := run(dir, &out); err != nil {
		t.Fatalf("clean repo: %v\n%s", err, out.String())
	}

	write("corpus/public/rates.md", doc("invitee"))
	git("add", "corpus/public/rates.md")
	out.Reset()
	if err := run(dir, &out); err == nil || !strings.Contains(out.String(), "corpus/public/rates.md") {
		t.Errorf("err = %v, output %q: want a failure naming the invitee file", err, out.String())
	}
}
