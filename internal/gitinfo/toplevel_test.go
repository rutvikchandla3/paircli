package gitinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTopLevel(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := TopLevel(nested)
	if err != nil {
		t.Fatalf("TopLevel: %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	gotEval, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	if gotEval != wantRoot {
		t.Errorf("TopLevel(%q) = %q, want %q", nested, got, wantRoot)
	}
}

func TestTopLevel_GitFile(t *testing.T) {
	// A worktree's .git is a file, not a directory; TopLevel should still
	// treat that directory as the top level.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /somewhere/else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "sub")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := TopLevel(nested)
	if err != nil {
		t.Fatalf("TopLevel: %v", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(root)
	gotEval, _ := filepath.EvalSymlinks(got)
	if gotEval != wantRoot {
		t.Errorf("TopLevel(%q) = %q, want %q", nested, got, wantRoot)
	}
}

func TestTopLevel_NotFound(t *testing.T) {
	// A directory with no .git anywhere above it up to the filesystem root.
	// Use a fresh temp dir tree; os.TempDir() itself is not expected to be a
	// git repo in CI/sandbox environments.
	root := t.TempDir()
	nested := filepath.Join(root, "x", "y")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only assert this if none of the ancestors happen to contain .git
	// (defensive skip for unusual sandboxes).
	if _, err := TopLevel(nested); err == nil {
		// Walk up manually to see if some ancestor legitimately has .git;
		// if so, this environment makes the test meaningless.
		dir := nested
		for {
			if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
				t.Skip("ancestor directory unexpectedly contains .git; skipping in this environment")
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		t.Fatal("TopLevel: want error when no .git exists above dir")
	}
}

func TestOwnerRepoFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"git@github.com:acme/shop.git", "acme/shop"},
		{"https://github.com/acme/shop.git", "acme/shop"},
		{"https://github.com/acme/shop", "acme/shop"},
		{"https://gitlab.com/acme/shop.git", ""},
	}
	for _, tc := range tests {
		if got := OwnerRepoFromURL(tc.url); got != tc.want {
			t.Errorf("OwnerRepoFromURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}
