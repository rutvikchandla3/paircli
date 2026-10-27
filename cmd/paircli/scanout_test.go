package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/scan"
	"github.com/rutvikchandla3/paircli/internal/state"
)

// legacyScanDir plants the layout an earlier paircli wrote into a repository:
// <dir>/.paircli/pr-<n>/<head>/report.md.
func legacyScanDir(t *testing.T, dir string) {
	t.Helper()
	sub := filepath.Join(dir, ".paircli", "pr-7", strings.Repeat("a", 40))
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "report.md"), []byte("# report\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyScanNote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	if got := legacyScanNote(dir); got != "" {
		t.Errorf("legacyScanNote = %q, want empty for a fresh directory", got)
	}

	legacyScanDir(t, dir)
	note := legacyScanNote(dir)
	if note == "" {
		t.Fatal("legacyScanNote found nothing")
	}
	// The note names the old folder and the new one, in home-shortened form so
	// no username is printed.
	for _, want := range []string{
		engine.Home(filepath.Join(dir, ".paircli")),
		engine.Home(state.ScanRoot()),
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q does not mention %q", note, want)
		}
	}
	if strings.Contains(note, os.Getenv("HOME")) {
		t.Errorf("note carries the raw home directory: %q", note)
	}
}

// checkoutAt writes the minimal .git metadata internal/gitinfo reads, with the
// given origin remote, so dir looks like a clone of that repository.
func checkoutAt(t *testing.T, dir, originURL string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	head := []byte("ref: refs/heads/main\n")
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), head, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = " + originURL +
		"\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLegacyScanNoteNeedsNoMatchingRemote is the case the note exists for: a
// checkout of somebody else's PR, where scan.RepoRoot returns "" and only the
// working directory can point at the stale folder.
func TestLegacyScanNoteNeedsNoMatchingRemote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	checkoutAt(t, dir, "git@github.com:someone/other.git")
	legacyScanDir(t, dir)

	// A real checkout, so cwd and the resolved repository root are the same
	// path: if the note were keyed off RepoRoot(cwd, repo) it would find
	// nothing, and this test would fail.
	if root := scan.RepoRoot(dir, "acme/shop"); root != "" {
		t.Fatalf("RepoRoot = %q, want empty for a checkout of another repository", root)
	}
	if legacyScanNote(dir) == "" {
		t.Error("legacyScanNote found nothing in a checkout whose remote does not match")
	}
}

// TestLegacyScanNoteFromASubdirectory covers the second candidate directory: a
// scan run two levels down must still be told about the folder at the root.
func TestLegacyScanNoteFromASubdirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	checkoutAt(t, root, "git@github.com:acme/shop.git")
	legacyScanDir(t, root)
	sub := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	if got := legacyScanNote(sub); got != "" {
		t.Errorf("legacyScanNote(subdir) = %q, want empty: the folder is at the root", got)
	}
	resolved := scan.RepoRoot(sub, "acme/shop")
	if resolved != root {
		t.Fatalf("RepoRoot = %q, want %q", resolved, root)
	}
	if got := legacyScanNote(sub, resolved); got == "" {
		t.Error("legacyScanNote missed the folder at the repository root")
	}
}

func TestLegacyScanNoteWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	dir := t.TempDir()
	legacyScanDir(t, dir)

	note := legacyScanNote(dir)
	if note == "" {
		t.Fatal("legacyScanNote found nothing")
	}
	// No home directory to name, so the half-sentence naming one is dropped.
	if strings.Contains(note, "write to  ") {
		t.Errorf("note has a hole where the scan root belongs: %q", note)
	}
	if !strings.Contains(note, "left untouched") {
		t.Errorf("note = %q", note)
	}
}

// chdir moves the process into dir for the rest of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Errorf("restoring the working directory: %v", err)
		}
	})
}

func TestPrintLegacyScanNote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var quiet bytes.Buffer
	printLegacyScanNote(&quiet, t.TempDir())
	if quiet.Len() != 0 {
		t.Errorf("printed %q for a clean directory", quiet.String())
	}

	dir := t.TempDir()
	legacyScanDir(t, dir)
	var buf bytes.Buffer
	printLegacyScanNote(&buf, dir)
	if !strings.HasPrefix(buf.String(), "paircli: ") || !strings.Contains(buf.String(), ".paircli") {
		t.Errorf("printed %q", buf.String())
	}
	if !strings.HasSuffix(buf.String(), "\n") || strings.Count(buf.String(), "\n") != 1 {
		t.Errorf("the note is not one line: %q", buf.String())
	}
}

func TestDoctor_ScanLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubDoctor(t, nil, nil)

	var buf bytes.Buffer
	if err := doctorReport(&buf, time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("doctorReport: %v", err)
	}
	line := doctorLine(t, strings.Split(buf.String(), "\n"), "scan")
	if !strings.Contains(line, engine.Home(state.ScanRoot())) {
		t.Errorf("scan line = %q, want it to name %q", line, engine.Home(state.ScanRoot()))
	}
	if strings.Contains(line, "legacy") {
		t.Errorf("scan line = %q, want no legacy note in a clean directory", line)
	}
}

func TestDoctor_ScanLineReportsLegacyOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubDoctor(t, nil, nil)

	// doctorReport reads the working directory, which the test owns for the
	// duration: the scan folder check is keyed off cwd, not a repo root.
	dir := t.TempDir()
	legacyScanDir(t, dir)
	chdir(t, dir)

	var buf bytes.Buffer
	if err := doctorReport(&buf, time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("doctorReport: %v", err)
	}
	line := doctorLine(t, strings.Split(buf.String(), "\n"), "scan")
	if !strings.Contains(line, "legacy output in") {
		t.Errorf("scan line = %q, want it to report the legacy folder", line)
	}
}
