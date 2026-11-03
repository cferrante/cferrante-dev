// Command corpuscheck fails if a commit would publish invitee-only documents
// (design §5, plan step 5.3). This repo is public: the access mask keeps
// private chunks out of the chat, but only this check keeps them off GitHub.
// It looks at the files git tracks (staged included), so the git-ignored
// corpus/private clone is never read:
//
//	go run ./orchestrator/cmd/corpuscheck   # make corpus-check; CI runs it
//
// It fails closed. Under corpus/, every Markdown file must say access: public
// and nothing at all may be tracked in corpus/private/. Anywhere else, a
// Markdown file whose front matter has any access value other than public
// fails, whatever its case or spacing. Test fixtures under testdata/ are
// exempt: their invitee document is generic and exists to test the mask.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if err := run(".", os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "corpuscheck:", err)
		os.Exit(1)
	}
}

func run(repo string, out io.Writer) error {
	raw, err := exec.Command("git", "-C", repo, "ls-files", "-z").Output()
	if err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	var paths []string
	for _, p := range strings.Split(string(raw), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	problems := check(paths, func(p string) ([]byte, error) { return os.ReadFile(filepath.Join(repo, p)) })
	for _, p := range problems {
		fmt.Fprintln(out, p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d file(s) would publish invitee-only content; invitee documents belong in the private repo cloned into corpus/private/", len(problems))
	}
	fmt.Fprintf(out, "corpuscheck: %d tracked files, none invitee-only\n", len(paths))
	return nil
}

// check returns one problem per offending path, sorted.
func check(paths []string, read func(string) ([]byte, error)) []string {
	var problems []string
	for _, p := range paths {
		if msg := checkOne(p, read); msg != "" {
			problems = append(problems, p+": "+msg)
		}
	}
	sort.Strings(problems)
	return problems
}

func checkOne(path string, read func(string) ([]byte, error)) string {
	if strings.HasPrefix(path, "corpus/private/") {
		return "corpus/private/ is the private repo's clone and must never be committed here"
	}
	if !strings.HasSuffix(path, ".md") || strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/testdata/") {
		return ""
	}
	inCorpus := strings.HasPrefix(path, "corpus/") && path != "corpus/README.md"
	content, err := read(path)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	access, found, err := frontMatterAccess(content)
	switch {
	case err != nil && (inCorpus || found):
		return err.Error()
	case !found && inCorpus:
		return "no access line: corpus documents must say access: public"
	case found && access != "public":
		return fmt.Sprintf("access %q: only public documents may be committed", access)
	}
	return ""
}

// frontMatterAccess reads the access value from a front-matter block,
// leniently (any case, any spacing, CRLF, a byte-order mark), because the
// point is to catch what a strict parser would reject too.
func frontMatterAccess(content []byte) (access string, found bool, err error) {
	text := strings.ReplaceAll(string(bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", false, errors.New("no front matter: the file must start with a --- line")
	}
	lines := strings.Split(text[len("---\n"):], "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			return access, found, nil
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "access") {
			v := strings.ToLower(strings.TrimSpace(value))
			if found && v != access {
				return "", true, fmt.Errorf("access appears twice (%q and %q)", access, v)
			}
			access, found = v, true
		}
	}
	// No closing line: everything was front matter, so an access line still counts.
	if found {
		return access, true, nil
	}
	return "", false, errors.New("front matter has no closing --- line")
}
