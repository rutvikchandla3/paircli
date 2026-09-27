package gitinfo

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// gitTimeout bounds every git shellout in this file: hooks must stay fast
// and never hang the calling agent.
const gitTimeout = 2 * time.Second

// HeadAndBranch shells out to `git -C dir rev-parse HEAD` and
// `git -C dir rev-parse --abbrev-ref HEAD`, each bounded by a 2-second
// timeout. A detached HEAD (branch "HEAD") is reported as branch "".
// Errors are returned; callers at hook time ignore them and record the
// failure instead of aborting.
func HeadAndBranch(dir string) (sha, branch string, err error) {
	sha, err = runGitTrim(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	branch, err = runGitTrim(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return sha, "", err
	}
	if branch == "HEAD" {
		branch = ""
	}
	return sha, branch, nil
}

// runGitTrim runs `git -C dir <args>` with a 2-second timeout and returns
// its trimmed stdout.
func runGitTrim(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
