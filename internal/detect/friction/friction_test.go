package friction

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestFRI1_Hotspot(t *testing.T) {
	// Create a session with edits to one file that stands out
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Edit("main.go", "line1").
		Run("go test ./...", 0, "").
		Edit("main.go", "line2").
		Run("go test ./...", 0, "").
		Edit("main.go", "line3").
		Run("go test ./...", 0, "").
		Edit("main.go", "line4").
		Run("go test ./...", 0, "").
		Edit("main.go", "line5").
		Run("go test ./...", 0, "").
		// Few edits to other file
		Edit("util.go", "line1").
		Run("go test ./...", 0, "").
		Build()

	pr := testkit.PR("acme/shop", 42).
		Add("main.go", 1, "line1", "line2", "line3", "line4", "line5").
		Add("util.go", 1, "line1").
		Build()

	ctx := testkit.Ctx(pr, s)
	sig := engine.RunIDs(ctx, "FRI-1")[0]

	if sig.State != model.StateInfo {
		t.Errorf("expected StateInfo, got %s", sig.State)
	}
	if len(sig.Findings) == 0 {
		t.Errorf("expected findings for hotspot")
	}
	if len(sig.Summary) > 160 {
		t.Errorf("summary too long: %d chars", len(sig.Summary))
	}
}

func TestFRI1_Cycles(t *testing.T) {
	// Test that cycles are counted correctly
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Edit("test.go", "line1").
		Run("go test ./...", 0, "").
		Edit("test.go", "line2").
		Edit("test.go", "line3").
		Run("go test ./...", 0, "").
		Build()

	pr := testkit.PR("acme/shop", 42).
		Add("test.go", 1, "line1", "line2", "line3").
		Build()

	ctx := testkit.Ctx(pr, s)
	sig := engine.RunIDs(ctx, "FRI-1")[0]

	if sig.State != model.StateClear {
		t.Errorf("expected StateClear for no hotspots, got %s", sig.State)
	}
}

func TestFRI1_NoHotspot(t *testing.T) {
	// Create a session with balanced edits
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Edit("a.go", "line1").
		Edit("b.go", "line1").
		Edit("c.go", "line1").
		Build()

	pr := testkit.PR("acme/shop", 42).
		Add("a.go", 1, "line1").
		Add("b.go", 1, "line1").
		Add("c.go", 1, "line1").
		Build()

	ctx := testkit.Ctx(pr, s)
	sig := engine.RunIDs(ctx, "FRI-1")[0]

	if sig.State != model.StateClear {
		t.Errorf("expected StateClear, got %s", sig.State)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("expected no findings")
	}
}

func TestFRI1_FailedEditsIgnored(t *testing.T) {
	// Failed edits should not count
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Build()

	// Manually add a failed edit
	s.Events = append(s.Events, model.Event{
		ID:   "e1",
		Kind: model.KindEdit,
		Edit: &model.FileEdit{
			RelPath: "main.go",
			Op:      model.OpUpdate,
			Failed:  true,
		},
	})

	// Add successful edits
	s.Events = append(s.Events, model.Event{
		ID:   "e2",
		Kind: model.KindEdit,
		Edit: &model.FileEdit{
			RelPath: "main.go",
			Op:      model.OpUpdate,
			Added:   []string{"line1"},
		},
	})

	s.Finalize()

	pr := testkit.PR("acme/shop", 42).
		Add("main.go", 1, "line1").
		Build()

	ctx := testkit.Ctx(pr, s)
	sig := engine.RunIDs(ctx, "FRI-1")[0]

	if sig.State != model.StateClear {
		t.Errorf("expected StateClear, got %s", sig.State)
	}
}

func TestFRI2_LoopOfThree(t *testing.T) {
	// Test detection of 3 consecutive failures
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Run("go test ./...", 1, "fail").
		Run("go test ./...", 1, "fail").
		Run("go test ./...", 1, "fail").
		Build()

	ctx := testkit.Ctx(testkit.PR("acme/shop", 42).Build(), s)
	sig := engine.RunIDs(ctx, "FRI-2")[0]

	if sig.State != model.StateAlert {
		t.Errorf("expected StateAlert for loop, got %s", sig.State)
	}
	if len(sig.Findings) == 0 {
		t.Errorf("expected findings for loop")
	}
}

func TestFRI2_LoopBrokenBySuccess(t *testing.T) {
	// 3 failures followed by success should break the chain
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Run("go test ./...", 1, "fail").
		Run("go test ./...", 1, "fail").
		Run("go test ./...", 1, "fail").
		Run("go test ./...", 0, "pass").
		Build()

	ctx := testkit.Ctx(testkit.PR("acme/shop", 42).Build(), s)
	sig := engine.RunIDs(ctx, "FRI-2")[0]

	// No loop since the failure chain was broken
	if sig.State != model.StateClear {
		t.Errorf("expected StateClear when loop is broken by success, got %s", sig.State)
	}
}

func TestFRI2_UnresolvedCheck(t *testing.T) {
	// A check that fails and never succeeds
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Run("go test ./...", 1, "fail").
		Build()

	ctx := testkit.Ctx(testkit.PR("acme/shop", 42).Build(), s)
	sig := engine.RunIDs(ctx, "FRI-2")[0]

	if sig.State != model.StateAlert {
		t.Errorf("expected StateAlert for unresolved check, got %s", sig.State)
	}
	if len(sig.Findings) == 0 {
		t.Errorf("expected findings for unresolved check")
	}
}

func TestFRI2_ResolvedInLaterSession(t *testing.T) {
	// If a check fails in session 1 but succeeds in session 2, it's resolved
	s1 := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Run("go test ./...", 1, "fail").
		Build()

	s2 := testkit.Session(model.HarnessClaudeCode, "s2").
		Prompt("fix").
		Run("go test ./...", 0, "pass").
		Build()

	ctx := testkit.Ctx(testkit.PR("acme/shop", 42).Build(), s1, s2)
	sig := engine.RunIDs(ctx, "FRI-2")[0]

	// No unresolved check since it succeeded in s2
	if sig.State != model.StateClear {
		t.Errorf("expected StateClear when check resolved in later session, got %s", sig.State)
	}
}

func TestFRI2_NonCheckFailureNotUnresolved(t *testing.T) {
	// A failing non-check command should not be counted as unresolved
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Run("grep foo bar", 1, "not found").
		Build()

	ctx := testkit.Ctx(testkit.PR("acme/shop", 42).Build(), s)
	sig := engine.RunIDs(ctx, "FRI-2")[0]

	if sig.State != model.StateClear {
		t.Errorf("expected StateClear for failing non-check command, got %s", sig.State)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("expected no findings for non-check command")
	}
}

func TestFRI2_TimeoutAndFailure(t *testing.T) {
	// Test timeout and harness failure findings
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Build()

	// Add timeout event
	s.Events = append(s.Events, model.Event{
		ID:   "e1",
		Kind: model.KindCommand,
		Command: &model.Command{
			Cmd:    "long running test",
			Status: model.CmdTimeout,
		},
	})

	// Add failure event
	s.Events = append(s.Events, model.Event{
		ID:   "e2",
		Kind: model.KindFailure,
		Failure: &model.Failure{
			Type: "api_error",
		},
	})

	s.Finalize()

	ctx := testkit.Ctx(testkit.PR("acme/shop", 42).Build(), s)
	sig := engine.RunIDs(ctx, "FRI-2")[0]

	if sig.State != model.StateInfo {
		t.Errorf("expected StateInfo for timeout/failure, got %s", sig.State)
	}
	if len(sig.Findings) < 2 {
		t.Errorf("expected at least 2 findings for timeout and failure")
	}
}

func TestFRI3_CompactionThenEdits(t *testing.T) {
	// Test compaction followed by edits
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Edit("main.go", "line1").
		Run("go test ./...", 0, "").
		Add(model.Event{
			Kind: model.KindCompaction,
			Compaction: &model.Compaction{
				Trigger: "auto",
			},
		}).
		Edit("main.go", "line2").
		Build()

	pr := testkit.PR("acme/shop", 42).
		Add("main.go", 1, "line1", "line2").
		Build()

	ctx := testkit.Ctx(pr, s)
	sig := engine.RunIDs(ctx, "FRI-3")[0]

	if sig.State != model.StateInfo {
		t.Errorf("expected StateInfo for compaction with edits, got %s", sig.State)
	}
	if len(sig.Findings) == 0 {
		t.Errorf("expected findings for compaction")
	}
	if len(sig.Summary) > 160 {
		t.Errorf("summary too long: %d chars", len(sig.Summary))
	}
}

func TestFRI3_ResetTypes(t *testing.T) {
	// Test different reset types
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Edit("main.go", "line1").
		Add(model.Event{
			Kind: model.KindReset,
			Reset: &model.Reset{
				Type: "clear",
			},
		}).
		Edit("main.go", "line2").
		Build()

	pr := testkit.PR("acme/shop", 42).
		Add("main.go", 1, "line1", "line2").
		Build()

	ctx := testkit.Ctx(pr, s)
	sig := engine.RunIDs(ctx, "FRI-3")[0]

	if sig.State != model.StateInfo {
		t.Errorf("expected StateInfo for reset with edits, got %s", sig.State)
	}
	if len(sig.Findings) == 0 {
		t.Errorf("expected findings for reset")
	}
}

func TestFRI3_None(t *testing.T) {
	// Test when there are no resets
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("start").
		Edit("main.go", "line1").
		Build()

	pr := testkit.PR("acme/shop", 42).
		Add("main.go", 1, "line1").
		Build()

	ctx := testkit.Ctx(pr, s)
	sig := engine.RunIDs(ctx, "FRI-3")[0]

	if sig.State != model.StateClear {
		t.Errorf("expected StateClear when no resets, got %s", sig.State)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("expected no findings")
	}
	if len(sig.Summary) > 160 {
		t.Errorf("summary too long: %d chars", len(sig.Summary))
	}
}
