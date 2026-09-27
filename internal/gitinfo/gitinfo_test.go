package gitinfo

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a small helper for constructing fixture .git dirs.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolve_PlainRepo(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(gitDir, "config"), `[core]
	repositoryformatversion = 0
[remote "origin"]
	url = git@github.com:rutvikchandla3/paircli.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`)

	info, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if info.Branch != "main" {
		t.Errorf("Branch = %q, want %q", info.Branch, "main")
	}
	if info.OwnerRepo != "rutvikchandla3/paircli" {
		t.Errorf("OwnerRepo = %q, want %q", info.OwnerRepo, "rutvikchandla3/paircli")
	}
	if info.GitDir != gitDir {
		t.Errorf("GitDir = %q, want %q", info.GitDir, gitDir)
	}
}

func TestResolve_HTTPSRemote(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature/foo\n")
	writeFile(t, filepath.Join(gitDir, "config"), `[remote "origin"]
	url = https://github.com/rutvikchandla3/paircli.git
`)

	info, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if info.Branch != "feature/foo" {
		t.Errorf("Branch = %q, want %q", info.Branch, "feature/foo")
	}
	if info.OwnerRepo != "rutvikchandla3/paircli" {
		t.Errorf("OwnerRepo = %q, want %q", info.OwnerRepo, "rutvikchandla3/paircli")
	}
}

func TestResolve_DetachedHead(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n")
	writeFile(t, filepath.Join(gitDir, "config"), `[remote "origin"]
	url = git@github.com:someone/somerepo.git
`)

	info, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if info.Branch != "" {
		t.Errorf("Branch = %q, want empty for detached HEAD", info.Branch)
	}
}

// TestResolve_Worktree constructs a fixture that mimics `git worktree add`'s
// layout: the worktree's .git is a file pointing at
// <main>/.git/worktrees/<name>, which itself has a commondir file pointing
// back at <main>/.git.
func TestResolve_Worktree(t *testing.T) {
	root := t.TempDir()
	mainGitDir := filepath.Join(root, "main", ".git")
	writeFile(t, filepath.Join(mainGitDir, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(mainGitDir, "config"), `[remote "origin"]
	url = git@github.com:rutvikchandla3/paircli.git
`)

	worktreeDir := filepath.Join(root, "wt")
	worktreeGitMeta := filepath.Join(mainGitDir, "worktrees", "wt")
	writeFile(t, filepath.Join(worktreeGitMeta, "HEAD"), "ref: refs/heads/feature/wt-branch\n")
	writeFile(t, filepath.Join(worktreeGitMeta, "commondir"), "../..\n")
	writeFile(t, filepath.Join(worktreeDir, ".git"), "gitdir: "+worktreeGitMeta+"\n")

	info, err := Resolve(worktreeDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if info.Branch != "feature/wt-branch" {
		t.Errorf("Branch = %q, want %q", info.Branch, "feature/wt-branch")
	}
	if info.GitDir != mainGitDir {
		t.Errorf("GitDir (commondir-resolved) = %q, want %q", info.GitDir, mainGitDir)
	}
	if info.OwnerRepo != "rutvikchandla3/paircli" {
		t.Errorf("OwnerRepo = %q, want %q", info.OwnerRepo, "rutvikchandla3/paircli")
	}
}
