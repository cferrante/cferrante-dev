package evals

import (
	"os/exec"
	"strings"
)

// GitState is the current commit and whether a tracked file has uncommitted
// changes (a new cache or results file is untracked until committed);
// outside a git checkout the commit is empty. Eval runs log it so results
// compare by commit.
func GitState(dir string) (string, bool) {
	sha, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false
	}
	status, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=no").Output()
	return strings.TrimSpace(string(sha)), err == nil && len(strings.TrimSpace(string(status))) > 0
}
