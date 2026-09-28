package verification

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func ver1Sig(t *testing.T, c *engine.Context) *model.Signal {
	t.Helper()
	sig := testkit.Find(engine.RunIDs(c, "VER-1"), "VER-1")
	if sig == nil {
		t.Fatal("VER-1 signal not found")
	}
	return sig
}

func ver2Sig(t *testing.T, c *engine.Context) *model.Signal {
	t.Helper()
	sig := testkit.Find(engine.RunIDs(c, "VER-2"), "VER-2")
	if sig == nil {
		t.Fatal("VER-2 signal not found")
	}
	return sig
}

func TestVER1_GroupsAndLastStatus(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("file.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("file.go", "line1").
		Run("npm test", 1, "").
		Run("npm test", 1, "").
		Run("npm test", 0, "").
		Run("npx tsc --noEmit", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver1Sig(t, c)
	if sig.State != model.StateInfo {
		t.Fatalf("State = %s, want info", sig.State)
	}
	if len(sig.Findings) != 2 {
		t.Fatalf("len(Findings) = %d, want 2", len(sig.Findings))
	}
	wantF0 := "`npm test` ×3 (fail, fail, pass) — last: pass"
	if sig.Findings[0].Summary != wantF0 {
		t.Errorf("Findings[0].Summary = %q, want %q", sig.Findings[0].Summary, wantF0)
	}
	wantF1 := "`npx tsc --noEmit` ×1 (pass) — last: pass"
	if sig.Findings[1].Summary != wantF1 {
		t.Errorf("Findings[1].Summary = %q, want %q", sig.Findings[1].Summary, wantF1)
	}
	wantSummary := "Checks ran 4 times: test ×3 (last pass), typecheck ×1 (last pass)"
	if sig.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", sig.Summary, wantSummary)
	}
}

func TestVER1_LastFailedAlerts(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("file.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("file.go", "line1").
		Run("npm test", 1, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver1Sig(t, c)
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	if len(sig.Findings) != 1 || sig.Findings[0].Severity != model.StateAlert {
		t.Fatalf("Findings = %+v, want one alert finding", sig.Findings)
	}
}

func TestVER1_NoChecks(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("file.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("file.go", "line1").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver1Sig(t, c)
	if sig.State != model.StateInfo {
		t.Fatalf("State = %s, want info", sig.State)
	}
	if len(sig.Findings) != 0 {
		t.Fatalf("len(Findings) = %d, want 0", len(sig.Findings))
	}
	want := "No test, lint, typecheck or build commands ran."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
}

func TestVER1_CountsParsed(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("file.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("file.go", "line1").
		Run("npx jest", 0, "Tests: 5 passed, 1 failed, 6 total").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver1Sig(t, c)
	if len(sig.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1", len(sig.Findings))
	}
	if !strings.Contains(sig.Findings[0].Summary, "5 passed, 1 failed") {
		t.Errorf("Summary = %q, want it to contain %q", sig.Findings[0].Summary, "5 passed, 1 failed")
	}
}

func TestVER2_Clear(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("a.go", "line1").
		At("14:05").Run("npm test", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
	want := "The last passing test run (`npm test`, 14:05 UTC) came after every captured edit to PR lines."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
}

func TestVER2_EditAfterLastPass(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("a.go", 1, "line1").
		Add("a.go", 5, "line2").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("a.go", "line1").  // e1: before the pass, not stale
		At("14:02").Run("npm test", 0, ""). // e2: last pass
		At("14:07").Edit("a.go", "line2").  // e3: after the pass, stale
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert; Summary=%q", sig.State, sig.Summary)
	}
	want := "1 PR hunks were edited after the last passing test run (14:02 UTC)."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1", len(sig.Findings))
	}
	f := sig.Findings[0]
	if len(f.Anchors) != 1 || f.Anchors[0].File != "a.go" || f.Anchors[0].Lines != "5" {
		t.Errorf("Anchors = %+v, want [{a.go 5}]", f.Anchors)
	}
	if len(f.Evidence) != 2 {
		t.Fatalf("len(Evidence) = %d, want 2 (last-pass run + stale edit)", len(f.Evidence))
	}
	if f.Evidence[0].Event != "e2" {
		t.Errorf("Evidence[0].Event = %q, want e2 (the last-pass run)", f.Evidence[0].Event)
	}
	if f.Evidence[1].Event != "e3" {
		t.Errorf("Evidence[1].Event = %q, want e3 (the stale edit)", f.Evidence[1].Event)
	}
	if sig.Data["stale_events"] != 1 || sig.Data["stale_hunks"] != 1 {
		t.Errorf("Data = %+v, want stale_events=1 stale_hunks=1", sig.Data)
	}
}

func TestVER2_EditToNonPRFileIgnored(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("a.go", "line1").
		At("14:02").Run("npm test", 0, "").
		At("14:05").Edit("other.go", "junk"). // not a PR file; no PR line sources it
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
}

func TestVER2_NeverVerified(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("a.go", "line1").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "No test, lint, typecheck or build ran after the agent's edits in any captured session."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1", len(sig.Findings))
	}
	wantF := "`a.go` was never checked in a captured session."
	if sig.Findings[0].Summary != wantF {
		t.Errorf("Findings[0].Summary = %q, want %q", sig.Findings[0].Summary, wantF)
	}
	if sig.Findings[0].Data["never_verified"] != true {
		t.Errorf("Findings[0].Data = %+v, want never_verified: true", sig.Findings[0].Data)
	}
}

func TestVER2_NoPassingRun(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("a.go", "line1").
		At("14:03").Run("npm test", 1, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "No passing test run in captured sessions; the last one failed at 14:03 UTC."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1", len(sig.Findings))
	}
}

func TestVER2_FallbackClassTypecheck(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("a.go", "line1").
		At("14:04").Run("npx tsc --noEmit", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
	want := "The last passing typecheck run (`npx tsc --noEmit`, 14:04 UTC) came after every captured edit to PR lines."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	if sig.Data["class"] != "typecheck" {
		t.Errorf("Data[class] = %v, want typecheck", sig.Data["class"])
	}
}

func TestVER2_CrossSession(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line1").Build()
	s1 := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("a.go", "line1").
		Build()
	s2 := testkit.Session(model.HarnessCodex, "s2").
		At("14:05").Run("npm test", 0, "").
		Build()
	c := testkit.Ctx(pr, s1, s2)

	sig := ver2Sig(t, c)
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
}

func TestVER2_NoShapingEvents(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "orphanline").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("do something unrelated").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateUnknown {
		t.Fatalf("State = %s, want unknown", sig.State)
	}
	want := "No PR lines came from captured sessions."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
}

func TestVER2_UncapturedNote(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("a.go", 1, "line1").
		Add("b.go", 1, "orphan").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("a.go", "line1").
		At("14:05").Run("npm test", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
	if sig.Data["uncaptured_lines"] != 1 {
		t.Fatalf("Data[uncaptured_lines] = %v, want 1", sig.Data["uncaptured_lines"])
	}
	var found bool
	want := fmt.Sprintf("%d changed lines were written outside captured sessions, so when they were written relative to checks is unknown.", 1)
	for _, f := range sig.Findings {
		if f.Summary == want {
			found = true
			if f.Severity != model.StateInfo {
				t.Errorf("uncaptured finding Severity = %s, want info", f.Severity)
			}
		}
	}
	if !found {
		t.Errorf("Findings = %+v, want one with Summary %q", sig.Findings, want)
	}
}

func TestVER2_HumanRunCounts(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line1").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("a.go", "line1").
		At("14:05").UserRun("npm test", 0).
		Build()
	c := testkit.Ctx(pr, s)

	sig := ver2Sig(t, c)
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
	lp, ok := sig.Data["last_pass"].(map[string]any)
	if !ok {
		t.Fatalf("Data[last_pass] = %v, want a map", sig.Data["last_pass"])
	}
	if lp["cmd"] != "npm test" {
		t.Errorf("last_pass[cmd] = %v, want npm test", lp["cmd"])
	}
}
