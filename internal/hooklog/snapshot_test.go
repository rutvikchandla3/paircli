package hooklog

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
)

func runGitCmd(t *testing.T, dir string, args ...string) {
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

func TestSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	dir := t.TempDir()
	runGitCmd(t, dir, "init", "-q", "-b", "main")

	write := func(rel string, data []byte) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Committed baseline: modified.go (will be modified), deleted.go (will be deleted).
	write("modified.go", []byte("package a\n\nfunc A() {}\n"))
	write("deleted.go", []byte("package a\n"))
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-q", "-m", "initial")

	// Now dirty the tree.
	write("modified.go", []byte("package a\n\nfunc A() { return }\n"))
	os.Remove(filepath.Join(dir, "deleted.go"))
	write("added.go", []byte("package a\n\nfunc B() {}\n"))
	runGitCmd(t, dir, "add", "added.go") // staged add
	write("untracked.txt", []byte("hello\n"))
	write("binary.bin", []byte{0x00, 0x01, 0x02, 0x03})
	big := bytes.Repeat([]byte("x"), (1<<20)+10)
	write("big.txt", big)

	got, err := Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	byPath := map[string]model.FileLines{}
	var paths []string
	for _, fl := range got {
		byPath[fl.Path] = fl
		paths = append(paths, fl.Path)
	}
	sort.Strings(paths)

	wantPresent := []string{"modified.go", "added.go", "untracked.txt"}
	for _, p := range wantPresent {
		if _, ok := byPath[p]; !ok {
			t.Errorf("Snapshot missing %q; got paths %v", p, paths)
		}
	}
	wantAbsent := []string{"deleted.go", "binary.bin", "big.txt"}
	for _, p := range wantAbsent {
		if _, ok := byPath[p]; ok {
			t.Errorf("Snapshot unexpectedly includes %q; got paths %v", p, paths)
		}
	}

	// Result is sorted by path.
	if !sort.StringsAreSorted(pathsOf(got)) {
		t.Errorf("Snapshot result not sorted by path: %v", pathsOf(got))
	}

	// Hashes match model.LineHash over model.SplitLines.
	mod := byPath["modified.go"]
	wantLines := model.SplitLines("package a\n\nfunc A() { return }\n")
	if len(mod.Hashes) != len(wantLines) {
		t.Fatalf("modified.go: got %d hashes, want %d", len(mod.Hashes), len(wantLines))
	}
	for i, l := range wantLines {
		if mod.Hashes[i] != model.LineHash(l) {
			t.Errorf("modified.go line %d hash mismatch", i)
		}
	}
}

func pathsOf(fls []model.FileLines) []string {
	var out []string
	for _, fl := range fls {
		out = append(out, fl.Path)
	}
	return out
}

func TestSnapshot_NotAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if _, err := Snapshot(dir); err == nil {
		t.Fatal("Snapshot: want error outside a git repo")
	}
}
