package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/scan"
)

// TestE2E_DefaultScanFolderIsUnderHome runs the whole scenario with no --out:
// the scan's output must land under the home directory, and nothing at all may
// be written into the checkout being scanned. The second half is the point —
// a customer running `git add .` after a scan must not commit paircli's
// output, and after this change the folder holds redacted copies of their own
// changed files.
func TestE2E_DefaultScanFolderIsUnderHome(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)
	home := t.TempDir()
	t.Setenv("HOME", home)

	res, err := scan.Run(context.Background(), scan.Options{
		Repo:            e2eRepo,
		Number:          e2eNumber,
		CWD:             w.repoDir,
		Runner:          w.runner,
		Config:          w.cfg,
		NoCommitPatches: true,
		Now:             e2eAt(e2eNowHHMM),
		Version:         e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: scan.Run with no --out: %v", err)
	}

	want := filepath.Join(home, ".paircli", "scans", "acme__shop", "pr-482", res.Head)
	if res.OutDir != want {
		t.Fatalf("OutDir = %q, want %q", res.OutDir, want)
	}
	for _, name := range []string{"signals.json", "report.md", "comment.md", "authorship.json"} {
		if _, err := os.Stat(filepath.Join(res.OutDir, name)); err != nil {
			t.Errorf("the scan did not write %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(w.repoDir, ".paircli")); !os.IsNotExist(err) {
		t.Errorf("the scan wrote into the checkout: %v", err)
	}
	// 0700 for the whole tree, the sessions subdirectory included: on a publish
	// these hold redacted copies of the user's own source files.
	for _, dir := range []string{res.OutDir, filepath.Join(res.OutDir, "sessions")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, got)
		}
	}

	// The secret planted in the transcript must still be confined to the
	// verbatim session copies, now under the home directory.
	e2eAssertNoSecretIn(t, res.OutDir)

	// The path is not the repository's, so no report may carry it: the comment
	// is posted to the PR as-is.
	comment, err := os.ReadFile(filepath.Join(res.OutDir, "comment.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(comment), home) {
		t.Errorf("comment.md carries the scan folder path:\n%s", comment)
	}
}
