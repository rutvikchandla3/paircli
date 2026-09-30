package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/snapshot"
)

// replayVersion is the version the fixture scan records and its replays claim.
const replayVersion = "0.0.0-test"

// replayNow stamps the fixture scan and every replay of it.
var replayNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// replayFixture scans the minimal scenario with --snapshot and returns the
// record it wrote, which is the input every replay below runs from.
func replayFixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	repoRoot := t.TempDir()
	scanGitRepo(t, repoRoot, "git@github.com:acme/shop.git")

	projects := t.TempDir()
	scanTranscript(t, projects, repoRoot)

	r := newScanRunner()
	r.responses[scanPRArg] = []byte(scanGHView)
	r.responses[scanDiffArg] = []byte(scanDiff)

	res, err := Run(context.Background(), Options{
		Repo:            "acme/shop",
		Number:          7,
		CWD:             repoRoot,
		OutDir:          filepath.Join(t.TempDir(), "pr-7"),
		Runner:          r,
		Config:          scanConfig(t, projects),
		NoHooks:         true,
		NoCommitPatches: true,
		Snapshot:        true,
		Now:             replayNow,
		Version:         replayVersion,
	})
	if err != nil {
		t.Fatalf("scan.Run with --snapshot: %v", err)
	}
	snap, err := snapshot.Read(filepath.Join(res.OutDir, snapshot.FileName))
	if err != nil {
		t.Fatalf("snapshot.Read: %v", err)
	}
	return snap
}

// replayInto replays snap into a fresh folder that does not exist yet and
// returns the result.
func replayInto(t *testing.T, snap *snapshot.Snapshot) *Result {
	t.Helper()
	outDir := filepath.Join(t.TempDir(), "nested", "judged")
	res, err := Replay(context.Background(), ReplayOptions{
		Snapshot: snap,
		OutDir:   outDir,
		Now:      replayNow,
		Version:  replayVersion,
	})
	if err != nil {
		t.Fatalf("Replay into %s: %v", outDir, err)
	}
	return res
}

// TestReplay_FreshOutDir is the regression test TestRun_SnapshotOnFreshOutDir is
// for the scan side: a replay writes its report before anything else has made
// the folder, so a --out that does not exist yet must still work.
func TestReplay_FreshOutDir(t *testing.T) {
	res := replayInto(t, replayFixture(t))

	for _, name := range []string{"signals.json", "report.md", "comment.md", "authorship.json", "agent-trace.json"} {
		if _, err := os.Stat(filepath.Join(res.OutDir, name)); err != nil {
			t.Errorf("output file %s: %v", name, err)
		}
	}
	if got := len(res.Report.Signals); got != 30 {
		t.Errorf("signals = %d, want 30", got)
	}
	// No LLM pass: the three inferred signals are the ones the scan reported.
	for _, id := range []string{"DEC-3", "CON-1", "CON-3"} {
		sig := scanFindSignal(res.Report.Signals, id)
		if sig == nil {
			t.Errorf("%s missing from the replayed report", id)
			continue
		}
		if sig.State != model.StateUnknown {
			t.Errorf("%s state = %s, want unknown with the LLM pass off", id, sig.State)
		}
	}
}

// TestReplay_Deterministic replays one snapshot twice: the signals are
// recomputed rather than stored, so the two runs must agree byte for byte.
func TestReplay_Deterministic(t *testing.T) {
	snap := replayFixture(t)
	first := replayInto(t, snap)
	second := replayInto(t, snap)

	for _, name := range []string{"signals.json", "report.md", "authorship.json"} {
		a, err := os.ReadFile(filepath.Join(first.OutDir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		b, err := os.ReadFile(filepath.Join(second.OutDir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs between two replays of one snapshot", name)
		}
	}
	if first.Alerts != second.Alerts {
		t.Errorf("alerts = %d then %d", first.Alerts, second.Alerts)
	}
}

// TestReplay_ToolMismatch checks the provenance guard: the snapshot does not
// carry the signals, this binary recomputes them, so a snapshot another paircli
// wrote needs --force before its replay is trusted.
func TestReplay_ToolMismatch(t *testing.T) {
	snap := replayFixture(t)
	outDir := filepath.Join(t.TempDir(), "judged")

	_, err := Replay(context.Background(), ReplayOptions{
		Snapshot: snap,
		OutDir:   outDir,
		Now:      replayNow,
		Version:  "9.9.9-other",
	})
	if err == nil {
		t.Fatal("Replay of a snapshot from another version: expected an error")
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "signals.json")); statErr == nil {
		t.Error("a refused replay still wrote the report")
	}

	if _, err := Replay(context.Background(), ReplayOptions{
		Snapshot: snap,
		OutDir:   outDir,
		Force:    true,
		Now:      replayNow,
		Version:  "9.9.9-other",
	}); err != nil {
		t.Fatalf("Replay with Force: %v", err)
	}
}

// TestReplay_UsageErrors covers the two arguments a replay cannot guess.
func TestReplay_UsageErrors(t *testing.T) {
	snap := replayFixture(t)
	if _, err := Replay(context.Background(), ReplayOptions{OutDir: t.TempDir(), Version: replayVersion}); err == nil {
		t.Error("Replay without a snapshot: expected an error")
	}
	if _, err := Replay(context.Background(), ReplayOptions{Snapshot: snap, Version: replayVersion}); err == nil {
		t.Error("Replay without an output folder: expected an error")
	}
}
