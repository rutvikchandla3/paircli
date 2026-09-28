package verification

import (
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func verSig(t *testing.T, c *engine.Context, id string) *model.Signal {
	t.Helper()
	sig := testkit.Find(engine.RunIDs(c, id), id)
	if sig == nil {
		t.Fatalf("%s signal not found", id)
	}
	return sig
}

// ---------------------------------------------------------------- VER-3

func TestVER3_FullSuiteClear(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("src/main.go", 1, "package main").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("src/main.go", "package main").
		At("14:05").Run("npm test", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-3")
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
	want := "A full test run (`npm test`) ran; every changed file was likely exercised."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	if sig.Data["full_suite"] != true {
		t.Errorf("Data[full_suite] = %v, want true", sig.Data["full_suite"])
	}
}

func TestVER3_TargetedCovered(t *testing.T) {
	// One file per matching rule:
	//   (a) directory prefix, Go package pattern
	//   (b) shared first two path segments
	//   (c) base name without extension inside the argument's base name
	pr := testkit.PR("acme/shop", 1).
		Add("pkg/api/server.go", 1, "package api").
		Add("internal/foo/data.go", 1, "package foo").
		Add("src/lib/retry.ts", 1, "export const retry = 1").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Run("go test ./pkg/api/...", 0, "").
		At("14:02").Run("npx vitest run src/retry.test.ts", 0, "").
		At("14:04").Run("npx jest internal/foo/data_test.go", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-3")
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
	want := "Every changed source file matched a targeted test run."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	if got := sig.Data["targeted_runs"]; got != 3 {
		t.Errorf("Data[targeted_runs] = %v, want 3", got)
	}
	if got := sig.Data["full_suite"]; got != false {
		t.Errorf("Data[full_suite] = %v, want false", got)
	}
	if unc, ok := sig.Data["uncovered"].([]string); !ok || len(unc) != 0 {
		t.Errorf("Data[uncovered] = %v, want empty", sig.Data["uncovered"])
	}
}

func TestVER3_GoPackagePattern(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("pkg/x.go", 1, "package pkg").
		Add("pkg/a/b.go", 1, "package a").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Run("go test ./pkg/...", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-3")
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; Summary=%q", sig.State, sig.Summary)
	}
	if got := sig.Data["targeted_runs"]; got != 1 {
		t.Errorf("Data[targeted_runs] = %v, want 1", got)
	}
}

func TestVER3_Uncovered(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("src/other/thing.rs", 1, "fn thing() {}").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Run("go test ./pkg/api/...", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-3")
	if sig.State != model.StateInfo {
		t.Fatalf("State = %s, want info; Summary=%q", sig.State, sig.Summary)
	}
	want := "Likely not exercised by any test run: 1 changed files (e.g. `src/other/thing.rs`)."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1", len(sig.Findings))
	}
	wantF := "`src/other/thing.rs` — no targeted test run matched it (likely)."
	if sig.Findings[0].Summary != wantF {
		t.Errorf("Findings[0].Summary = %q, want %q", sig.Findings[0].Summary, wantF)
	}
	if sig.Findings[0].Severity != model.StateInfo {
		t.Errorf("Findings[0].Severity = %s, want info", sig.Findings[0].Severity)
	}
	if len(sig.Findings[0].Anchors) != 1 || sig.Findings[0].Anchors[0].File != "src/other/thing.rs" {
		t.Errorf("Anchors = %+v, want [{src/other/thing.rs }]", sig.Findings[0].Anchors)
	}
	unc, ok := sig.Data["uncovered"].([]string)
	if !ok || len(unc) != 1 || unc[0] != "src/other/thing.rs" {
		t.Errorf("Data[uncovered] = %v, want [src/other/thing.rs]", sig.Data["uncovered"])
	}
}

func TestVER3_NoTestRuns(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("src/main.go", 1, "package main").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("src/main.go", "package main").
		Run("go build ./...", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-3")
	if sig.State != model.StateUnknown {
		t.Fatalf("State = %s, want unknown", sig.State)
	}
	want := "No test runs to compare against."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
}

// ---------------------------------------------------------------- VER-4

func TestVER4_FailEditPass(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("src/util.ts", 1, "export const util = 1").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("src/util.ts", "export const util = 1"). // e1
		At("14:05").Run("npm test", 1, "").                       // e2: failing run
		At("14:07").Replace("src/retry.test.ts",                  // e3: weakened test
		[]string{"expect(a).toBe(1)", "assert(b)"},
		[]string{"// nothing"}).
		At("14:10").Run("npm test", 0, ""). // e4: next passing run
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-4")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "Test file `src/retry.test.ts` was edited between a failing run (14:05 UTC) and the next passing run (14:10 UTC). It lost 2 assertion lines."
	found := false
	for _, f := range sig.Findings {
		if f.Summary == want {
			found = true
			if f.Severity != model.StateAlert {
				t.Errorf("Severity = %s, want alert", f.Severity)
			}
			if len(f.Anchors) != 1 || f.Anchors[0].File != "src/retry.test.ts" {
				t.Errorf("Anchors = %+v, want fallback to the test file", f.Anchors)
			}
			if len(f.Evidence) != 3 {
				t.Fatalf("len(Evidence) = %d, want 3 (failing run, edit, passing run)", len(f.Evidence))
			}
			if f.Evidence[0].Event != "e2" || f.Evidence[1].Event != "e3" || f.Evidence[2].Event != "e4" {
				t.Errorf("Evidence events = %q,%q,%q, want e2,e3,e4",
					f.Evidence[0].Event, f.Evidence[1].Event, f.Evidence[2].Event)
			}
		}
	}
	if !found {
		t.Errorf("Findings = %+v, want one with Summary %q", sig.Findings, want)
	}
	if sig.Data["fail_edit_pass"] != 1 {
		t.Errorf("Data[fail_edit_pass] = %v, want 1", sig.Data["fail_edit_pass"])
	}
}

func TestVER4_AssertionsRemovedInPR(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Remove("src/foo.test.ts", 1, "expect(a).toBe(1)", "expect(b).toBe(2)").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("clean up tests").Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-4")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "`src/foo.test.ts`: 2 assertion lines removed, 0 added."
	if len(sig.Findings) != 1 || sig.Findings[0].Summary != want {
		t.Fatalf("Findings = %+v, want one with Summary %q", sig.Findings, want)
	}
	if sig.Findings[0].Anchors[0].File != "src/foo.test.ts" {
		t.Errorf("Anchors = %+v, want anchor on src/foo.test.ts", sig.Findings[0].Anchors)
	}
	if sig.Data["assertions_removed"] != 1 {
		t.Errorf("Data[assertions_removed] = %v, want 1", sig.Data["assertions_removed"])
	}
}

func TestVER4_SkipAndOnly(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("src/foo.test.ts", 10, "it.only('x', () => {").
		Add("src/bar.spec.ts", 3, "test.skip('y', () => {})").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("src/foo.test.ts", "it.only('x', () => {").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-4")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	if len(sig.Findings) != 2 {
		t.Fatalf("len(Findings) = %d, want 2; findings: %+v", len(sig.Findings), sig.Findings)
	}
	want0 := "`src/foo.test.ts:10` adds a `only` marker: `it.only('x', () => {`"
	if sig.Findings[0].Summary != want0 {
		t.Errorf("Findings[0].Summary = %q, want %q", sig.Findings[0].Summary, want0)
	}
	if len(sig.Findings[0].Anchors) != 1 ||
		sig.Findings[0].Anchors[0].File != "src/foo.test.ts" ||
		sig.Findings[0].Anchors[0].Lines != "10" {
		t.Errorf("Findings[0].Anchors = %+v, want [{src/foo.test.ts 10}]", sig.Findings[0].Anchors)
	}
	if len(sig.Findings[0].Evidence) != 1 || sig.Findings[0].Evidence[0].Event != "e1" {
		t.Errorf("Findings[0].Evidence = %+v, want the attributed edit e1", sig.Findings[0].Evidence)
	}
	want1 := "`src/bar.spec.ts:3` adds a `skip` marker: `test.skip('y', () => {})`"
	if sig.Findings[1].Summary != want1 {
		t.Errorf("Findings[1].Summary = %q, want %q", sig.Findings[1].Summary, want1)
	}
	if sig.Data["skip_markers"] != 2 {
		t.Errorf("Data[skip_markers] = %v, want 2", sig.Data["skip_markers"])
	}
}

func TestVER4_SnapshotUpdate(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("src/__snapshots__/foo.test.ts.snap", 1, "exports[`a 1`] = `x`;").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Run("npx jest -u", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-4")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	var cmd, file bool
	for _, f := range sig.Findings {
		switch f.Summary {
		case "Snapshots regenerated with `npx jest -u` at 14:00 UTC.":
			cmd = true
		case "Snapshot file changed: `src/__snapshots__/foo.test.ts.snap`.":
			file = true
		}
	}
	if !cmd || !file {
		t.Errorf("Findings = %+v, want both the snapshot command and the snapshot file", sig.Findings)
	}
	if sig.Data["snapshot_updates"] != 2 {
		t.Errorf("Data[snapshot_updates] = %v, want 2", sig.Data["snapshot_updates"])
	}
}

func TestVER4_DeletedTest(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Status("src/old.test.ts", model.StatusDeleted).
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("remove old tests").Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-4")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "Test file deleted: `src/old.test.ts`."
	if len(sig.Findings) != 1 || sig.Findings[0].Summary != want {
		t.Fatalf("Findings = %+v, want one with Summary %q", sig.Findings, want)
	}
	if sig.Data["deleted_tests"] != 1 {
		t.Errorf("Data[deleted_tests] = %v, want 1", sig.Data["deleted_tests"])
	}
}

func TestVER4_Clear(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("src/foo.test.ts", 1, "expect(1).toBe(1)").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("src/foo.test.ts", "expect(1).toBe(1)").
		At("14:05").Run("npm test", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-4")
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; findings=%+v", sig.State, sig.Findings)
	}
	want := "No weakened tests, new skips, snapshot rewrites or deleted tests found."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("len(Findings) = %d, want 0", len(sig.Findings))
	}
}

// ---------------------------------------------------------------- VER-5

func TestVER5_NoVerifyAfterHookFailure(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("src/a.go", 1, "package a").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Run("git commit -m 'wip'", 1, "pre-commit hook failed"). // e1
		At("14:05").Run("git commit --no-verify -m 'wip'", 0, "").           // e2
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-5")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "Commit made with `git commit --no-verify -m 'wip'` after the pre-commit hook failed."
	if len(sig.Findings) != 1 || sig.Findings[0].Summary != want {
		t.Fatalf("Findings = %+v, want one with Summary %q", sig.Findings, want)
	}
	if sig.Findings[0].Severity != model.StateAlert {
		t.Errorf("Severity = %s, want alert", sig.Findings[0].Severity)
	}
	if len(sig.Findings[0].Evidence) != 2 ||
		sig.Findings[0].Evidence[0].Event != "e1" ||
		sig.Findings[0].Evidence[1].Event != "e2" {
		t.Errorf("Evidence = %+v, want the failed commit then the bypass", sig.Findings[0].Evidence)
	}
	if sig.Data["no_verify"] != 1 {
		t.Errorf("Data[no_verify] = %v, want 1", sig.Data["no_verify"])
	}
}

func TestVER5_HuskyEnv(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("src/a.go", 1, "package a").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Run("HUSKY=0 git commit -m 'skip hooks'", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-5")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "Commit made with `HUSKY=0 git commit -m 'skip hooks'`."
	if len(sig.Findings) != 1 || sig.Findings[0].Summary != want {
		t.Fatalf("Findings = %+v, want one with Summary %q", sig.Findings, want)
	}
	if len(sig.Findings[0].Evidence) != 1 {
		t.Errorf("Evidence = %+v, want only the bypass command", sig.Findings[0].Evidence)
	}
	if sig.Data["no_verify"] != 1 {
		t.Errorf("Data[no_verify] = %v, want 1", sig.Data["no_verify"])
	}
}

func TestVER5_Suppressions(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("src/foo.ts", 1, "// eslint-disable-next-line", "const x = 1", "// @ts-ignore").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("shush the linter").Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-5")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "`src/foo.ts` adds 2 suppressions (eslint-disable, @ts-ignore)."
	if len(sig.Findings) != 1 || sig.Findings[0].Summary != want {
		t.Fatalf("Findings = %+v, want one with Summary %q", sig.Findings, want)
	}
	anchors := sig.Findings[0].Anchors
	if len(anchors) != 2 || anchors[0].Lines != "1" || anchors[1].Lines != "3" {
		t.Errorf("Anchors = %+v, want lines 1 and 3 of src/foo.ts", anchors)
	}
	if sig.Data["suppressions"] != 1 {
		t.Errorf("Data[suppressions] = %v, want 1", sig.Data["suppressions"])
	}
}

func TestVER5_GeneratedIgnored(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("dist/bundle.js", 1, "// eslint-disable").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("build output").Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-5")
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; findings=%+v", sig.State, sig.Findings)
	}
	if sig.Data["suppressions"] != 0 {
		t.Errorf("Data[suppressions] = %v, want 0 (generated files are skipped)", sig.Data["suppressions"])
	}
}

func TestVER5_CIConfig(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add(".github/workflows/ci.yml", 1, "name: CI").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("tweak CI").Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-5")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	want := "CI config changed: `.github/workflows/ci.yml`."
	if len(sig.Findings) != 1 || sig.Findings[0].Summary != want {
		t.Fatalf("Findings = %+v, want one with Summary %q", sig.Findings, want)
	}
	if sig.Data["ci_config"] != 1 {
		t.Errorf("Data[ci_config] = %v, want 1", sig.Data["ci_config"])
	}
}

func TestVER5_CoverageThreshold(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).
		Add("jest.config.js", 5, "  coverageThreshold: { global: { lines: 90 } },").
		Remove("codecov.yml", 1, "fail_under: 80").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("lower the bar").Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-5")
	if sig.State != model.StateAlert {
		t.Fatalf("State = %s, want alert", sig.State)
	}
	var jest, codecov bool
	for _, f := range sig.Findings {
		switch f.Summary {
		case "Coverage threshold changed in `jest.config.js`.":
			jest = true
		case "Coverage threshold changed in `codecov.yml`.":
			codecov = true
		}
	}
	if !jest || !codecov {
		t.Errorf("Findings = %+v, want both jest.config.js and codecov.yml", sig.Findings)
	}
	if sig.Data["coverage_config"] != 2 {
		t.Errorf("Data[coverage_config] = %v, want 2", sig.Data["coverage_config"])
	}
	if sig.Data["ci_config"] != 0 {
		t.Errorf("Data[ci_config] = %v, want 0", sig.Data["ci_config"])
	}
}

func TestVER5_Clear(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("src/a.go", 1, "package a").Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		At("14:00").Edit("src/a.go", "package a").
		At("14:05").Run("go test ./...", 0, "").
		At("14:06").Run("git commit -m 'a'", 0, "").
		Build()
	c := testkit.Ctx(pr, s)

	sig := verSig(t, c, "VER-5")
	if sig.State != model.StateClear {
		t.Fatalf("State = %s, want clear; findings=%+v", sig.State, sig.Findings)
	}
	want := "No skipped hooks, new suppressions, or CI/coverage config changes."
	if sig.Summary != want {
		t.Errorf("Summary = %q, want %q", sig.Summary, want)
	}
	for _, k := range []string{"no_verify", "suppressions", "ci_config", "coverage_config"} {
		if sig.Data[k] != 0 {
			t.Errorf("Data[%s] = %v, want 0", k, sig.Data[k])
		}
	}
}

// TestVER345_NoSessions pins the no-session behaviour of all three detectors.
func TestVER345_NoSessions(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("src/a.go", 1, "package a").Build()
	c := testkit.Ctx(pr)

	for _, id := range []string{"VER-3", "VER-4", "VER-5"} {
		sig := verSig(t, c, id)
		if sig.State != model.StateUnknown {
			t.Errorf("%s State = %s, want unknown", id, sig.State)
		}
		if !strings.Contains(sig.Summary, "No captured sessions") {
			t.Errorf("%s Summary = %q, want the no-sessions reason", id, sig.Summary)
		}
	}
}
