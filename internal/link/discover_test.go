package link

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/hooklog"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// copyFixture copies a testdata file to dst, creating parent directories as
// needed.
func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// testWindow is a window wide enough to cover every fixture's events
// (all dated 2026-09-27T14:00-14:11Z).
func testWindow() Window {
	return Window{
		From: time.Date(2026, 9, 27, 13, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 27, 16, 0, 0, 0, time.UTC),
	}
}

func TestDiscover_AllHarnesses(t *testing.T) {
	tmp := t.TempDir()

	ccRoot := filepath.Join(tmp, "claude-projects")
	copyFixture(t, "testdata/claudecode/s1.jsonl", filepath.Join(ccRoot, "-work-shop", "s1.jsonl"))

	// An old fixture whose mtime predates the window: it must never be
	// parsed, even though its own event timestamp (14:10Z) would otherwise
	// fall inside the window.
	oldPath := filepath.Join(ccRoot, "-work-shop", "old.jsonl")
	copyFixture(t, "testdata/claudecode/old.jsonl", oldPath)
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	codexRoot := filepath.Join(tmp, "codex-root")
	copyFixture(t, "testdata/codex/rollout-good.jsonl", filepath.Join(codexRoot, "sessions", "2026", "09", "27", "rollout-good.jsonl"))

	piRoot := filepath.Join(tmp, "pi-root")
	copyFixture(t, "testdata/pi/session-good.jsonl", filepath.Join(piRoot, "--work-shop--", "session-good.jsonl"))

	cfg := config.Default()
	cfg.Roots = config.Roots{ClaudeCode: ccRoot, Codex: codexRoot, Pi: piRoot, HookLog: filepath.Join(tmp, "hooks")}

	sessions, err := Discover(cfg, testWindow(), false)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	var refs []string
	for _, s := range sessions {
		refs = append(refs, s.Ref())
	}
	want := []string{"claude-code:cc-s1", "codex:codex-s1", "pi:pi-s1"}
	if len(refs) != len(want) {
		t.Fatalf("got sessions %v, want %v", refs, want)
	}
	for i, w := range want {
		if refs[i] != w {
			t.Errorf("sessions[%d] = %q, want %q (order: %v)", i, refs[i], w, refs)
		}
	}
	for _, s := range sessions {
		if s.ID == "cc-old" {
			t.Fatalf("old.jsonl (mtime before window) must not be discovered, got session %s", s.Ref())
		}
	}
}

func TestDiscover_MergeDuplicateIDs(t *testing.T) {
	tmp := t.TempDir()
	ccRoot := filepath.Join(tmp, "claude-projects")
	copyFixture(t, "testdata/claudecode/resume-part1.jsonl", filepath.Join(ccRoot, "-work-shop", "resume-part1.jsonl"))
	copyFixture(t, "testdata/claudecode/resume-part2.jsonl", filepath.Join(ccRoot, "-work-shop", "resume-part2.jsonl"))

	cfg := config.Default()
	cfg.Roots = config.Roots{ClaudeCode: ccRoot, Codex: filepath.Join(tmp, "no-codex"), Pi: filepath.Join(tmp, "no-pi"), HookLog: filepath.Join(tmp, "hooks")}

	sessions, err := Discover(cfg, testWindow(), false)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1 merged session", len(sessions))
	}
	s := sessions[0]
	if s.Ref() != "claude-code:cc-resume" {
		t.Fatalf("Ref() = %q, want claude-code:cc-resume", s.Ref())
	}
	if len(s.Events) != 2 {
		t.Fatalf("got %d events, want 2 (duplicate id u1 from part2 dropped): %+v", len(s.Events), s.Events)
	}
	if s.Events[0].Prompt == nil || s.Events[0].Prompt.Text != "Add retry logic." {
		t.Errorf("Events[0] = %+v, want the first occurrence of u1", s.Events[0])
	}
	if s.Events[1].Prompt == nil || s.Events[1].Prompt.Text != "Continue after resume." {
		t.Errorf("Events[1] = %+v, want u2's prompt", s.Events[1])
	}
	for _, e := range s.Events {
		if e.Prompt != nil && e.Prompt.Text == "DUPLICATE: should be dropped, first occurrence wins." {
			t.Fatalf("duplicate-id event from part2 should have been dropped, found: %+v", e)
		}
	}
	if filepath.Base(s.SourcePath) != "resume-part1.jsonl" {
		t.Errorf("SourcePath = %q, want the earliest file (resume-part1.jsonl)", s.SourcePath)
	}
}

func TestDiscover_HooksMerged(t *testing.T) {
	tmp := t.TempDir()

	ccRoot := filepath.Join(tmp, "claude-projects")
	copyFixture(t, "testdata/claudecode/s1.jsonl", filepath.Join(ccRoot, "-work-shop", "s1.jsonl"))

	codexRoot := filepath.Join(tmp, "codex-root")
	copyFixture(t, "testdata/codex/rollout-good.jsonl", filepath.Join(codexRoot, "sessions", "2026", "09", "27", "rollout-good.jsonl"))

	piRoot := filepath.Join(tmp, "pi-root")
	copyFixture(t, "testdata/pi/session-good.jsonl", filepath.Join(piRoot, "--work-shop--", "session-good.jsonl"))

	hookDir := filepath.Join(tmp, "hooks")
	if err := hooklog.Append(hookDir, model.HookRecord{
		Harness:   model.HarnessClaudeCode,
		Event:     "SessionEnd",
		SessionID: "cc-s1",
		TS:        time.Date(2026, 9, 27, 14, 1, 0, 0, time.UTC),
		Reason:    "exit",
	}); err != nil {
		t.Fatal(err)
	}
	if err := hooklog.Append(hookDir, model.HookRecord{
		Harness:   model.HarnessCodex,
		Event:     "SessionEnd",
		SessionID: "codex-s1",
		TS:        time.Date(2026, 9, 27, 14, 1, 0, 0, time.UTC),
		Reason:    "exit",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Roots = config.Roots{ClaudeCode: ccRoot, Codex: codexRoot, Pi: piRoot, HookLog: hookDir}

	sessions, err := Discover(cfg, testWindow(), true)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	byRef := map[string]*model.Session{}
	for _, s := range sessions {
		byRef[s.Ref()] = s
	}

	cc := byRef["claude-code:cc-s1"]
	if cc == nil {
		t.Fatal("claude-code:cc-s1 not found")
	}
	if cc.Capture != model.CaptureHooked {
		t.Errorf("claude-code session Capture = %q, want hooked", cc.Capture)
	}
	foundSessionEnd := false
	for _, e := range cc.Events {
		if e.Kind == model.KindSessionEnd {
			foundSessionEnd = true
		}
	}
	if !foundSessionEnd {
		t.Error("claude-code session missing merged session_end event")
	}

	cx := byRef["codex:codex-s1"]
	if cx == nil {
		t.Fatal("codex:codex-s1 not found")
	}
	if cx.Capture != model.CaptureHooked {
		t.Errorf("codex session Capture = %q, want hooked", cx.Capture)
	}

	pi := byRef["pi:pi-s1"]
	if pi == nil {
		t.Fatal("pi:pi-s1 not found")
	}
	if pi.Capture != model.CaptureHooked {
		t.Errorf("pi session Capture = %q, want hooked (via HookRecords custom entries)", pi.Capture)
	}
	foundGitHead := false
	for _, e := range pi.Events {
		if e.Kind == model.KindGitHead && e.GitHead != nil && e.GitHead.SHA == "2222222222222222222222222222222222bbbb" {
			foundGitHead = true
		}
	}
	if !foundGitHead {
		t.Error("pi session missing git_head event merged from its embedded hook record")
	}
}
