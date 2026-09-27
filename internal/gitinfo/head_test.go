package gitinfo

import (
	"os/exec"
	"testing"
)

// initTestRepo creates a temp git repo with one commit and returns its
// directory. It skips the test if git is not on PATH.
func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "--allow-empty", "-q", "-m", "initial")
	return dir
}

func TestHeadAndBranch(t *testing.T) {
	dir := initTestRepo(t)

	sha, branch, err := HeadAndBranch(dir)
	if err != nil {
		t.Fatalf("HeadAndBranch: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("sha = %q, want a 40-char hex commit id", sha)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want %q", branch, "main")
	}
}

func TestHeadAndBranch_Detached(t *testing.T) {
	dir := initTestRepo(t)

	sha, _, err := HeadAndBranch(dir)
	if err != nil {
		t.Fatalf("HeadAndBranch: %v", err)
	}

	cmd := exec.Command("git", "checkout", "-q", sha)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout %s: %v\n%s", sha, err, out)
	}

	sha2, branch, err := HeadAndBranch(dir)
	if err != nil {
		t.Fatalf("HeadAndBranch: %v", err)
	}
	if sha2 != sha {
		t.Errorf("sha = %q, want %q", sha2, sha)
	}
	if branch != "" {
		t.Errorf("branch = %q, want empty for detached HEAD", branch)
	}
}

func TestHeadAndBranch_NotAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	_, _, err := HeadAndBranch(dir)
	if err == nil {
		t.Fatal("HeadAndBranch: want error outside a git repo")
	}
}
