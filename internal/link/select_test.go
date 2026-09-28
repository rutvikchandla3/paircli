package link

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// selWindow covers testkit's fixed clock epoch (2026-09-27T14:00:00Z).
func selWindow() Window {
	return Window{
		From: time.Date(2026, 9, 27, 13, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC),
	}
}

func TestSelect_RepoMatch(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Build()

	same := testkit.Session(model.HarnessClaudeCode, "same").Prompt("hi").Build() // RepoRemote "acme/shop" by default
	other := testkit.Session(model.HarnessClaudeCode, "other").Prompt("hi").Build()
	other.RepoRemote = "other/repo"

	cands, dropped := Select(pr, []*model.Session{same, other}, selWindow())

	if len(cands) != 1 || cands[0].Ref() != same.Ref() {
		t.Fatalf("cands = %v, want only %s", refsOf(cands), same.Ref())
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
}

func TestSelect_RelPath(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	sess := testkit.Session(model.HarnessClaudeCode, "rp").
		Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{
			Path: filepath.Join(repoDir, "src", "app.go"), Op: model.OpUpdate, Via: "edit", Added: []string{"x"},
		}}).
		Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{
			Path: "lib/util.go", Op: model.OpUpdate, Via: "edit", Added: []string{"y"}, // Pi-style relative path
		}}).
		Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{
			Path: "/somewhere/else/outside.go", Op: model.OpUpdate, Via: "edit", Added: []string{"z"},
		}}).
		Build()
	sess.CWD = repoDir

	pr := testkit.PR("acme/shop", 1).Build() // RepoRemote matches default, so candidacy doesn't depend on edits

	cands, _ := Select(pr, []*model.Session{sess}, selWindow())
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1", len(cands))
	}
	got := cands[0]
	if got.RepoRoot != repoDir {
		t.Errorf("RepoRoot = %q, want %q", got.RepoRoot, repoDir)
	}
	if got.Events[0].Edit.RelPath != "src/app.go" {
		t.Errorf("absolute path inside repo: RelPath = %q, want %q", got.Events[0].Edit.RelPath, "src/app.go")
	}
	if got.Events[1].Edit.RelPath != "lib/util.go" {
		t.Errorf("relative (Pi-style) path joined with CWD: RelPath = %q, want %q", got.Events[1].Edit.RelPath, "lib/util.go")
	}
	if got.Events[2].Edit.RelPath != "" {
		t.Errorf("path outside repo: RelPath = %q, want empty", got.Events[2].Edit.RelPath)
	}
}

func TestSelect_DeletedWorktreeSuffixMatch(t *testing.T) {
	deletedCWD := filepath.Join(t.TempDir(), "no-longer-exists")

	sess := testkit.Session(model.HarnessClaudeCode, "gone").
		Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{
			Path: filepath.Join(deletedCWD, "src", "app.go"), Op: model.OpUpdate, Via: "edit", Added: []string{"x"},
		}}).
		Build()
	sess.CWD = deletedCWD
	sess.RepoRoot = ""   // parsers never set this; testkit's default is only a test convenience
	sess.RepoRemote = "" // simulate a harness that never recorded the remote

	pr := testkit.PR("acme/shop", 1).Add("src/app.go", 1, "line").Build()

	cands, dropped := Select(pr, []*model.Session{sess}, selWindow())
	if len(cands) != 1 {
		t.Fatalf("got %d candidates (dropped=%d), want 1", len(cands), dropped)
	}
	got := cands[0]
	if got.RepoRoot != "" {
		t.Errorf("RepoRoot = %q, want empty (CWD gone)", got.RepoRoot)
	}
	if got.Events[0].Edit.RelPath != "src/app.go" {
		t.Errorf("RelPath = %q, want %q (suffix-matched against PR files)", got.Events[0].Edit.RelPath, "src/app.go")
	}
}

func refsOf(sessions []*model.Session) []string {
	var out []string
	for _, s := range sessions {
		out = append(out, s.Ref())
	}
	return out
}
