package consistency

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// conSignal runs one inferred detector alone over ctx.
func conSignal(t *testing.T, ctx *engine.Context, id string) *model.Signal {
	t.Helper()
	sig := testkit.Find(engine.RunIDs(ctx, id), id)
	if sig == nil {
		t.Fatalf("%s signal not found", id)
	}
	return sig
}

// conSession is the one-prompt session the CON-1 and CON-3 tests judge.
func conSession(t *testing.T) *engine.Context {
	t.Helper()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("add retries").
		Edit("a.go", "retry()").
		Build()
	pr := testkit.PR("acme/shop", 1).Add("a.go", 10, "retry()").Build()
	return testkit.Ctx(pr, s)
}

// --- CON-1 ---

func TestCON1_NilJudgments(t *testing.T) {
	sig := conSignal(t, conSession(t), "CON-1")
	if sig.State != model.StateUnknown {
		t.Errorf("state = %q, want unknown", sig.State)
	}
	const want = "LLM pass is off. Run with --llm to fill this in."
	if sig.Summary != want {
		t.Errorf("summary = %q, want %q", sig.Summary, want)
	}
}

// TestCON1_Verdicts covers one finding per verdict, their exact wording, the
// severity of each and the signal state.
func TestCON1_Verdicts(t *testing.T) {
	ctx := conSession(t)
	ctx.Judgments = &model.Judgments{
		Claims: []model.ClaimVerdict{
			{Claim: "All tests pass.", Source: "pr_body", Verdict: "contradicted",
				Reason: "the last run failed", Cites: []string{"ev:claude-code:s1/e2"}},
			{Claim: "Adds tests for the retry path.", Source: "pr_body", Verdict: "supported",
				Cites: []string{"ev:claude-code:s1/e2"}},
			{Claim: "No behaviour change.", Source: "final_message", Verdict: "no_evidence",
				Cites: []string{"ev:claude-code:s1/e1"}},
		},
	}

	sig := conSignal(t, ctx, "CON-1")
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert (a claim is contradicted)", sig.State)
	}
	const want = "1 claims contradicted, 1 supported, 1 without evidence."
	if sig.Summary != want {
		t.Errorf("summary = %q, want %q", sig.Summary, want)
	}
	if len(sig.Findings) != 3 {
		t.Fatalf("findings = %d, want 3", len(sig.Findings))
	}
	wantFindings := []struct {
		summary  string
		severity model.State
	}{
		{"“All tests pass.” is contradicted: the last run failed", model.StateAlert},
		{"“Adds tests for the retry path.” is supported.", model.StateInfo},
		{"“No behaviour change.”: no evidence either way.", model.StateInfo},
	}
	for i, w := range wantFindings {
		f := sig.Findings[i]
		if f.Summary != w.summary {
			t.Errorf("finding %d = %q, want %q", i, f.Summary, w.summary)
		}
		if f.Severity != w.severity {
			t.Errorf("finding %d severity = %q, want %q", i, f.Severity, w.severity)
		}
		if f.Provenance != model.Inferred {
			t.Errorf("finding %d provenance = %q, want inferred", i, f.Provenance)
		}
	}
	if ev := sig.Findings[0].Evidence; len(ev) != 1 || ev[0].Event != "e2" {
		t.Errorf("finding 0 evidence = %+v, want the ev cite to resolve", ev)
	}
}

// TestCON1_SupportedOnly stays in info when nothing is contradicted.
func TestCON1_SupportedOnly(t *testing.T) {
	ctx := conSession(t)
	ctx.Judgments = &model.Judgments{
		Claims: []model.ClaimVerdict{
			{Claim: "Adds a retry loop.", Source: "pr_body", Verdict: "supported", Cites: []string{"ev:claude-code:s1/e2"}},
		},
	}

	sig := conSignal(t, ctx, "CON-1")
	if sig.State != model.StateInfo {
		t.Errorf("state = %q, want info", sig.State)
	}
	if got, want := sig.Summary, "0 claims contradicted, 1 supported, 0 without evidence."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// TestCON1_NoClaims is the empty-judgment case.
func TestCON1_NoClaims(t *testing.T) {
	ctx := conSession(t)
	ctx.Judgments = &model.Judgments{}

	sig := conSignal(t, ctx, "CON-1")
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if got, want := sig.Summary, "No checkable claims found."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// --- CON-3 ---

func TestCON3_NilJudgments(t *testing.T) {
	sig := conSignal(t, conSession(t), "CON-3")
	if sig.State != model.StateUnknown {
		t.Errorf("state = %q, want unknown", sig.State)
	}
	const want = "LLM pass is off. Run with --llm to fill this in."
	if sig.Summary != want {
		t.Errorf("summary = %q, want %q", sig.Summary, want)
	}
}

// TestCON3_Untraced covers the untraced finding, its anchors (from the explicit
// file/lines and from the hunk cite) and the signal summary.
func TestCON3_Untraced(t *testing.T) {
	ctx := conSession(t)
	ctx.Judgments = &model.Judgments{
		Scope: []model.ScopeItem{
			{File: "a.go", Lines: "10", Traced: true, TraceTo: "add retries", Cites: []string{"file:a.go:10-10"}},
			{File: "b.go", Lines: "3-4", Traced: true, TraceTo: "the plan", Cites: []string{"file:b.go:3-4"}},
			{File: "c.go", Lines: "7-9", Traced: false, Cites: []string{"ev:claude-code:s1/e2"}},
		},
	}

	sig := conSignal(t, ctx, "CON-3")
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if got, want := sig.Summary, "2 of 3 hunks trace to the ask or plan; 1 do not."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1 (only untraced items get one)", len(sig.Findings))
	}
	f := sig.Findings[0]
	if got, want := f.Summary, "`c.go` 7-9 does not trace to the ask, plan or a decision."; got != want {
		t.Errorf("finding = %q, want %q", got, want)
	}
	if f.Severity != model.StateAlert || f.Provenance != model.Inferred {
		t.Errorf("finding severity/provenance = %q/%q, want alert/inferred", f.Severity, f.Provenance)
	}
	if len(f.Anchors) != 1 || f.Anchors[0].File != "c.go" || f.Anchors[0].Lines != "7-9" {
		t.Errorf("anchors = %+v, want c.go 7-9", f.Anchors)
	}
}

// TestCON3_AllTraced is the clean case: no findings, clear.
func TestCON3_AllTraced(t *testing.T) {
	ctx := conSession(t)
	ctx.Judgments = &model.Judgments{
		Scope: []model.ScopeItem{
			{File: "a.go", Lines: "10", Traced: true, Cites: []string{"file:a.go:10-10"}},
		},
	}

	sig := conSignal(t, ctx, "CON-3")
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if got, want := sig.Summary, "1 of 1 hunks trace to the ask or plan; 0 do not."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("findings = %d, want 0", len(sig.Findings))
	}
}

// TestCON3_AnchorsFromCites checks that a hunk cite alone anchors the finding
// when the judgment carried no explicit file.
func TestCON3_AnchorsFromCites(t *testing.T) {
	ctx := conSession(t)
	ctx.Judgments = &model.Judgments{
		Scope: []model.ScopeItem{
			{File: "src/webhooks/retry.ts", Traced: false, Cites: []string{"file:src/webhooks/retry.ts:12-13"}},
		},
	}

	sig := conSignal(t, ctx, "CON-3")
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	anchors := sig.Findings[0].Anchors
	if len(anchors) != 1 || anchors[0].File != "src/webhooks/retry.ts" || anchors[0].Lines != "12-13" {
		t.Errorf("anchors = %+v, want the hunk cite", anchors)
	}
	if got, want := sig.Findings[0].Summary, "`src/webhooks/retry.ts` does not trace to the ask, plan or a decision."; got != want {
		t.Errorf("finding = %q, want %q", got, want)
	}
}
