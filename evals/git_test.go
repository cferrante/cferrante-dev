package evals

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// make embed-cache writes the new cache before it logs the run, so an
// untracked file must not mark the tree dirty; a changed tracked file must.
func TestDirtyMeansAChangedTrackedFile(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	git("add", "a.txt")
	git("commit", "-q", "-m", "a")
	os.WriteFile(filepath.Join(dir, "new.json"), []byte("{}"), 0o644)
	if commit, dirty := GitState(dir); commit == "" || dirty {
		t.Errorf("untracked file only: commit %q dirty %v, want a commit and clean", commit, dirty)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("b"), 0o644)
	if _, dirty := GitState(dir); !dirty {
		t.Error("changed tracked file: dirty false")
	}
}
