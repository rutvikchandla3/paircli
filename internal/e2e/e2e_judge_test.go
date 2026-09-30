package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/judge"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/scan"
	"github.com/rutvikchandla3/paircli/internal/snapshot"
)

// e2eScanDeterministic runs T25's scenario with --snapshot and no LLM pass, and
// returns the replay record it wrote.
func e2eScanDeterministic(t *testing.T, w *world) *snapshot.Snapshot {
	t.Helper()
	if _, err := scan.Run(context.Background(), scan.Options{
		Repo:     e2eRepo,
		Number:   e2eNumber,
		CWD:      w.repoDir,
		OutDir:   w.outDir,
		Runner:   w.runner,
		Config:   w.cfg,
		Snapshot: true,
		Now:      e2eAt(e2eNowHHMM),
		Version:  e2eVersion,
	}); err != nil {
		t.Fatalf("e2e: scan.Run with --snapshot: %v", err)
	}
	snap, err := snapshot.Read(filepath.Join(w.outDir, snapshot.FileName))
	if err != nil {
		t.Fatalf("e2e: reading the snapshot: %v", err)
	}
	return snap
}

// TestE2E_ReplayReproducesScan is the property the snapshot exists for, at the
// level a user sees it: replaying a snapshot with no LLM pass rewrites the whole
// output folder byte for byte — report, signals, attribution, sessions, agent
// trace — and leaves the snapshot itself alone.
//
// The deterministic signals are recomputed rather than stored, so this is what
// proves the recomputation is faithful, not merely close.
func TestE2E_ReplayReproducesScan(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	snap := e2eScanDeterministic(t, w)
	before := e2eReadTree(t, w.outDir)
	if len(before) == 0 {
		t.Fatal("e2e: the scan wrote no output")
	}

	res, err := scan.Replay(context.Background(), scan.ReplayOptions{
		Snapshot: snap,
		OutDir:   w.outDir,
		Now:      e2eAt(e2eNowHHMM),
		Version:  e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: scan.Replay: %v", err)
	}
	if res.Report == nil {
		t.Fatal("e2e: scan.Replay returned a nil report")
	}
	e2eCompareTrees(t, "the deterministic replay", before, e2eReadTree(t, w.outDir))
}

// TestE2E_JudgeSnapshot is the deferred-judging flow end to end: a scan writes
// its snapshot, and judging that snapshot later — with no PR, no `gh`, and none
// of the local session stores — writes exactly the report the same scan would
// have written with --llm.
func TestE2E_JudgeSnapshot(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	snap := e2eScanDeterministic(t, w)

	fake := &llm.Fake{Replies: e2eLLMReplies(t)}
	res, err := scan.Replay(context.Background(), scan.ReplayOptions{
		Snapshot: snap,
		OutDir:   w.outDir,
		LLM:      fake,
		Now:      e2eAt(e2eNowHHMM),
		Version:  e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: scan.Replay with --llm: %v", err)
	}

	t.Run("replies", func(t *testing.T) {
		if got, want := len(fake.Calls), len(e2eLLMReplyFiles); got != want {
			t.Errorf("llm calls = %d, want %d (one per judge job)", got, want)
		}
		if calls := fake.Calls; len(calls) > 0 {
			if !strings.Contains(calls[0].Prompt, "FACT BUNDLE:") {
				t.Errorf("the judge prompt does not carry the fact bundle")
			}
			if calls[0].Model == "" {
				t.Errorf("the judge request carries no model")
			}
		}
	})

	t.Run("golden", func(t *testing.T) {
		for _, name := range e2eLLMOutputs {
			got, err := os.ReadFile(filepath.Join(w.outDir, name))
			if err != nil {
				t.Fatalf("e2e: reading %s: %v", name, err)
			}
			e2eLLMCompareGolden(t, name, e2eNormalize(w, got))
		}
	})

	// The same signal-level assertions the live --llm run has to satisfy: the
	// replay is not a second implementation with its own expectations.
	t.Run("inferred", func(t *testing.T) { e2eAssertLLMRun(t, res) })

	t.Run("untouched snapshot", func(t *testing.T) {
		// Judging writes the report files beside the record, never into it: the
		// snapshot stays the frozen input a second run can be replayed from.
		again, err := snapshot.Read(filepath.Join(w.outDir, snapshot.FileName))
		if err != nil {
			t.Fatalf("e2e: re-reading the snapshot: %v", err)
		}
		if again.Hash != snap.Hash {
			t.Errorf("the judged replay rewrote the snapshot: hash %s, want %s", again.Hash, snap.Hash)
		}
		if again.Judgments != nil {
			t.Errorf("the judged replay stored judgments in the snapshot")
		}
	})
}

// TestE2E_JudgeSnapshotStoredJudgments covers the other input a replay accepts:
// a snapshot that already carries a judgment result. It must be folded in
// exactly as a fresh pass would be, so the two produce the same report.
func TestE2E_JudgeSnapshotStoredJudgments(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	snap := e2eScanDeterministic(t, w)

	// Judge the snapshot's own context, so the record carries what a pass over
	// it produces.
	ec, err := snap.Context()
	if err != nil {
		t.Fatalf("e2e: rebuilding the context: %v", err)
	}
	j, _, err := judge.RunFull(context.Background(), &llm.Fake{Replies: e2eLLMReplies(t)},
		ec, engine.Run(ec), ec.Config)
	if err != nil {
		t.Fatalf("e2e: judge.RunFull: %v", err)
	}
	if j == nil || len(j.Errors) > 0 {
		t.Fatalf("e2e: the pass left judgments unusable: %+v", j)
	}
	snap.Judgments = j

	// No provider at all: the stored result is the input.
	if _, err := scan.Replay(context.Background(), scan.ReplayOptions{
		Snapshot: snap,
		OutDir:   w.outDir,
		Now:      e2eAt(e2eNowHHMM),
		Version:  e2eVersion,
	}); err != nil {
		t.Fatalf("e2e: scan.Replay from stored judgments: %v", err)
	}
	for _, name := range e2eLLMOutputs {
		got, err := os.ReadFile(filepath.Join(w.outDir, name))
		if err != nil {
			t.Fatalf("e2e: reading %s: %v", name, err)
		}
		e2eLLMCompareGolden(t, name, e2eNormalize(w, got))
	}
}

// TestE2E_JudgeSnapshotPartialFailure covers the case a total failure hides,
// through the replay path: one job answers nothing while the others do, so the
// signal fed only by the failed job must report unknown rather than clear.
func TestE2E_JudgeSnapshotPartialFailure(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	snap := e2eScanDeterministic(t, w)

	// Two unusable replies make the claims job fail after its single retry. The
	// story and scope jobs then take the two canned replies after it, and the
	// caveats job finds the provider exhausted.
	all := e2eLLMReplies(t)
	fake := &llm.Fake{Replies: []string{"I cannot help with that.", "Still not JSON.", all[1], all[2]}}
	res, err := scan.Replay(context.Background(), scan.ReplayOptions{
		Snapshot: snap,
		OutDir:   w.outDir,
		LLM:      fake,
		Now:      e2eAt(e2eNowHHMM),
		Version:  e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: a partial LLM failure must not fail the replay: %v", err)
	}

	// CON-1 is fed only by the claims job, which failed.
	con1 := e2eSignal(t, res.Report, "CON-1")
	e2eRequireState(t, con1, model.StateUnknown)

	// The jobs that did answer keep their real results.
	e2eRequireState(t, e2eSignal(t, res.Report, "CON-3"), model.StateAlert)
	e2eRequireState(t, e2eSignal(t, res.Report, "DEC-3"), model.StateInfo)

	// The failure is still recorded where the report footer reads it, and on the
	// result, so the command can say the pass was incomplete.
	auth2 := e2eSignal(t, res.Report, "AUTH-2")
	if _, ok := auth2.Data["llm_errors"]; !ok {
		t.Errorf("AUTH-2: Data[llm_errors] missing: %v", auth2.Data)
	}
	if len(res.LLMErrors) == 0 {
		t.Error("LLMErrors is empty after a partial LLM failure")
	}
}

// e2eReadTree reads every file under dir, keyed by its path relative to dir.
func e2eReadTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("e2e: walking %s: %v", dir, err)
	}
	return out
}

// e2eCompareTrees compares two output trees byte for byte, both ways, so a file
// the second run dropped is a difference too.
func e2eCompareTrees(t *testing.T, label string, want, got map[string]string) {
	t.Helper()
	for name, wantBody := range want {
		gotBody, ok := got[name]
		if !ok {
			t.Errorf("e2e: %s: %s is missing", label, name)
			continue
		}
		if gotBody != wantBody {
			t.Errorf("e2e: %s: %s differs:\n--- before ---\n%s\n--- after ---\n%s", label, name, wantBody, gotBody)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("e2e: %s: %s appeared", label, name)
		}
	}
}
