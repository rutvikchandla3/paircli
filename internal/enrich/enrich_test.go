package enrich

import (
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// ctxWith builds a one-prompt context carrying j, so the cite helpers resolve
// against a real session and its "e1".."eN" events.
func ctxWith(j *model.Judgments) *engine.Context {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("add retries with backoff").
		Edit("a.go", "retry()").
		Build()
	pr := testkit.PR("acme/shop", 1).Add("a.go", 10, "retry()").Build()
	c := testkit.Ctx(pr, s)
	c.Judgments = j
	return c
}

// sigOf builds one signal fixture.
func sigOf(id string, state model.State, summary string, findings ...model.Finding) model.Signal {
	s := engine.NewSignal(id)
	s.State = state
	s.Summary = summary
	s.Findings = findings
	return s
}

// finding is a short Finding fixture.
func finding(summary string, severity model.State, data map[string]any, events ...string) model.Finding {
	f := model.Finding{Summary: summary, Severity: severity, Data: data}
	for _, ev := range events {
		f.Evidence = append(f.Evidence, model.Evidence{Session: "claude-code:s1", Event: ev})
	}
	return f
}

// TestApply_NilJudgments is the LLM-off contract: the signals come back
// unchanged.
func TestApply_NilJudgments(t *testing.T) {
	sigs := []model.Signal{sigOf("INT-1", model.StateInfo, "1 prompt.", finding("p", model.StateInfo, nil))}
	out := Apply(ctxWith(nil), sigs)
	if len(out) != 1 || out[0].Summary != "1 prompt." || len(out[0].Findings) != 1 {
		t.Fatalf("signals changed with nil judgments: %+v", out)
	}
	// The result is a copy: writing to it must not reach the input.
	out[0].Summary = "changed"
	if sigs[0].Summary == "changed" {
		t.Errorf("Apply returned a slice aliasing its input")
	}
}

func TestApply_NilContext(t *testing.T) {
	sigs := []model.Signal{sigOf("INT-1", model.StateInfo, "1 prompt.")}
	if out := Apply(nil, sigs); len(out) != 1 || out[0].Summary != "1 prompt." {
		t.Fatalf("signals changed with a nil context: %+v", out)
	}
}

// TestApply_AskSummary covers the INT-1 row.
func TestApply_AskSummary(t *testing.T) {
	c := ctxWith(&model.Judgments{AskSummary: &model.AskSummary{
		Ask:        "Add retries with backoff",
		FinalScope: "the retry path only",
		Cites:      []string{"ev:claude-code:s1/e1"},
	}})
	out := Apply(c, []model.Signal{sigOf("INT-1", model.StateInfo, "1 prompt across 1 session; first: \"add retries\".")})

	sig := testkit.Find(out, "INT-1")
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want the condensed ask prepended", len(sig.Findings))
	}
	if got, want := sig.Findings[0].Summary, "Ask (condensed): Add retries with backoff"; got != want {
		t.Errorf("prepended finding = %q, want %q", got, want)
	}
	if sig.Findings[0].Provenance != model.Inferred {
		t.Errorf("prepended finding provenance = %q, want inferred", sig.Findings[0].Provenance)
	}
	if len(sig.Findings[0].Evidence) != 1 || sig.Findings[0].Evidence[0].Event != "e1" {
		t.Errorf("prepended finding evidence = %+v, want the e1 cite", sig.Findings[0].Evidence)
	}
	if _, ok := sig.Data["ask_summary"]; !ok {
		t.Errorf("Data[ask_summary] missing: %v", sig.Data)
	}
	if !strings.HasSuffix(sig.Summary, " (+1 inferred)") {
		t.Errorf("summary = %q, want the inferred suffix", sig.Summary)
	}
	if sig.State != model.StateInfo {
		t.Errorf("state = %q, want info", sig.State)
	}
}

// TestApply_PlanDrift covers the INT-3 row.
func TestApply_PlanDrift(t *testing.T) {
	c := ctxWith(&model.Judgments{PlanDrift: []model.DriftItem{
		{Kind: "missing_step", Text: "add jitter to the backoff", Cites: []string{"ev:claude-code:s1/e1"}},
		{Kind: "unplanned_change", File: "c.go", Lines: "7-9", Text: "metrics", Cites: []string{"file:c.go:7-9"}},
	}})
	base := finding("Plan approved at 14:00 UTC.", model.StateInfo, nil)
	out := Apply(c, []model.Signal{sigOf("INT-3", model.StateInfo, "Plan approved at 14:00 UTC in s1; 1 plans proposed in total.", base)})

	sig := testkit.Find(out, "INT-3")
	if len(sig.Findings) != 3 {
		t.Fatalf("findings = %d, want 3", len(sig.Findings))
	}
	if got, want := sig.Findings[1].Summary, "Planned step with no matching change: add jitter to the backoff"; got != want {
		t.Errorf("missing_step finding = %q, want %q", got, want)
	}
	if got, want := sig.Findings[2].Summary, "Change not in the plan: `c.go` 7-9"; got != want {
		t.Errorf("unplanned_change finding = %q, want %q", got, want)
	}
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if !strings.HasSuffix(sig.Summary, " (+2 inferred)") {
		t.Errorf("summary = %q, want a +2 inferred suffix", sig.Summary)
	}
}

// TestApply_RuleViolations covers the INT-4 row.
func TestApply_RuleViolations(t *testing.T) {
	c := ctxWith(&model.Judgments{RuleViolations: []model.RuleViolation{
		{Rule: "never commit to main", File: "a.go", Lines: "10", Cites: []string{"ev:claude-code:s1/e2"}},
	}})
	out := Apply(c, []model.Signal{sigOf("INT-4", model.StateInfo, "1 rule file was loaded.")})

	sig := testkit.Find(out, "INT-4")
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	if got, want := sig.Findings[0].Summary, "`a.go` 10 may break the rule “never commit to main”."; got != want {
		t.Errorf("finding = %q, want %q", got, want)
	}
	if sig.Findings[0].Severity != model.StateAlert {
		t.Errorf("finding severity = %q, want alert", sig.Findings[0].Severity)
	}
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if !strings.HasSuffix(sig.Summary, " (+1 inferred)") {
		t.Errorf("summary = %q, want the inferred suffix", sig.Summary)
	}
}

// TestApply_CorrectionsRejected covers the DEC-1 row for a candidate the judge
// rejected: it drops to info, and the state and summary are recomputed without
// it.
func TestApply_CorrectionsRejected(t *testing.T) {
	c := ctxWith(&model.Judgments{Corrections: []model.CorrectionItem{
		{Event: model.EventRef{Session: "claude-code:s1", Event: "e5"}, IsCorrection: false, Cites: []string{"ev:claude-code:s1/e5"}},
	}})
	candidate := finding("Correction at 14:01 UTC: “No, use Postgres instead”", model.StateAlert,
		map[string]any{"kind": "correction_prompt", "candidate": true}, "e5")
	candidate.Anchors = []model.Anchor{{File: "a.go", Lines: "10"}}
	sigs := []model.Signal{sigOf("DEC-1", model.StateAlert, "1 human correction (1 correction prompt); 1 touched PR line.", candidate)}

	out := Apply(c, sigs)
	sig := testkit.Find(out, "DEC-1")

	if sig.Findings[0].Severity != model.StateInfo {
		t.Errorf("rejected candidate severity = %q, want info", sig.Findings[0].Severity)
	}
	if sig.Findings[0].Data["confirmed"] != false {
		t.Errorf("Data[confirmed] = %v, want false", sig.Findings[0].Data["confirmed"])
	}
	if got, want := sig.Summary, "No interrupts, rejections or corrections."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if got := sig.Data["touching_pr"]; got != 0 {
		t.Errorf("Data[touching_pr] = %v, want 0", got)
	}
	// The input signal is untouched.
	if sigs[0].Findings[0].Severity != model.StateAlert || sigs[0].Findings[0].Data["confirmed"] != nil {
		t.Errorf("Apply mutated its input: %+v", sigs[0].Findings[0])
	}
}

// TestApply_CorrectionsRejectedInterruptKept checks that a verdict on an
// interrupt only annotates it: a recorded interrupt still counts.
func TestApply_CorrectionsRejectedInterruptKept(t *testing.T) {
	c := ctxWith(&model.Judgments{Corrections: []model.CorrectionItem{
		{Event: model.EventRef{Session: "claude-code:s1", Event: "e5"}, IsCorrection: false, Cites: []string{"ev:claude-code:s1/e5"}},
	}})
	interrupt := finding("Interrupted at 14:05 UTC while the agent was editing `a.go`.", model.StateAlert,
		map[string]any{"kind": "interrupt"}, "e5")
	interrupt.Anchors = []model.Anchor{{File: "a.go", Lines: "10"}}

	out := Apply(c, []model.Signal{sigOf("DEC-1", model.StateAlert, "1 human correction (1 interrupt); 1 touched PR line.", interrupt)})
	sig := testkit.Find(out, "DEC-1")

	if got, want := sig.Summary, "1 human correction (1 interrupt); 1 touched PR line."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if sig.Findings[0].Data["confirmed"] != false {
		t.Errorf("Data[confirmed] = %v, want false", sig.Findings[0].Data["confirmed"])
	}
}

// TestApply_CorrectionsConfirmed covers the DEC-1 row for a confirmed
// candidate: it gains its explanation and keeps its anchors.
func TestApply_CorrectionsConfirmed(t *testing.T) {
	c := ctxWith(&model.Judgments{Corrections: []model.CorrectionItem{
		{Event: model.EventRef{Session: "claude-code:s1", Event: "e5"}, IsCorrection: true,
			About: "the migration was wrong", Cites: []string{"ev:claude-code:s1/e5"}},
	}})
	interrupt := finding("Interrupted at 14:05 UTC while the agent was editing `a.go`.", model.StateAlert,
		map[string]any{"kind": "interrupt"}, "e5")
	interrupt.Anchors = []model.Anchor{{File: "a.go", Lines: "10"}}

	out := Apply(c, []model.Signal{sigOf("DEC-1", model.StateAlert, "1 human correction (1 interrupt); 1 touched PR line.", interrupt)})
	sig := testkit.Find(out, "DEC-1")

	if sig.Findings[0].Data["confirmed"] != true {
		t.Errorf("Data[confirmed] = %v, want true", sig.Findings[0].Data["confirmed"])
	}
	if got, want := sig.Findings[0].Summary, "Interrupted at 14:05 UTC while the agent was editing `a.go`. (the migration was wrong)"; got != want {
		t.Errorf("finding = %q, want %q", got, want)
	}
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if got, want := sig.Summary, "1 human correction (1 interrupt); 1 touched PR line."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// TestApply_Abandoned covers the DEC-2 row: a matched item lands as
// Data["summary"] on its finding, an unmatched one becomes a new info finding.
func TestApply_Abandoned(t *testing.T) {
	c := ctxWith(&model.Judgments{Abandoned: []model.AbandonedItem{
		{Events: []model.EventRef{{Session: "claude-code:s1", Event: "e2"}},
			Summary: "Discarded the per-attempt key approach.", Cites: []string{"ev:claude-code:s1/e2"}},
		{Events: []model.EventRef{{Session: "claude-code:s1", Event: "e9"}},
			Summary: "Tried a Redis-backed counter.", Cites: []string{"ev:claude-code:s1/e9"}},
	}})
	base := finding("Code written in `a.go` at 14:00 UTC was later removed.", model.StateInfo,
		map[string]any{"kind": "reverted_edit"}, "e2")
	sigs := []model.Signal{sigOf("DEC-2", model.StateInfo, "1 abandoned attempt.", base)}

	out := Apply(c, sigs)
	sig := testkit.Find(out, "DEC-2")

	if len(sig.Findings) != 2 {
		t.Fatalf("findings = %d, want the unmatched summary appended", len(sig.Findings))
	}
	if got := sig.Findings[0].Data["summary"]; got != "Discarded the per-attempt key approach." {
		t.Errorf("matched Data[summary] = %v", got)
	}
	if got, want := sig.Findings[1].Summary, "Tried a Redis-backed counter."; got != want {
		t.Errorf("new finding = %q, want %q", got, want)
	}
	if sig.Findings[1].Severity != model.StateInfo || sig.Findings[1].Provenance != model.Inferred {
		t.Errorf("new finding severity/provenance = %q/%q, want info/inferred", sig.Findings[1].Severity, sig.Findings[1].Provenance)
	}
	if sigs[0].Findings[0].Data["summary"] != nil {
		t.Errorf("Apply mutated its input finding: %v", sigs[0].Findings[0].Data)
	}
	if !strings.HasSuffix(sig.Summary, " (+1 inferred)") {
		t.Errorf("summary = %q, want a +1 inferred suffix", sig.Summary)
	}
}

// TestApply_LostConstraints covers the FRI-3 row.
func TestApply_LostConstraints(t *testing.T) {
	c := ctxWith(&model.Judgments{LostConstraints: []model.LostConstraint{
		{Constraint: "don't touch the queue", Cites: []string{"ev:claude-code:s1/e1"}},
	}})
	out := Apply(c, []model.Signal{sigOf("FRI-3", model.StateInfo, "1 context reset (compaction).")})

	sig := testkit.Find(out, "FRI-3")
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	if got, want := sig.Findings[0].Summary, "Constraint missing after compaction: “don't touch the queue”"; got != want {
		t.Errorf("finding = %q, want %q", got, want)
	}
	if sig.Findings[0].Severity != model.StateAlert || sig.State != model.StateAlert {
		t.Errorf("severity/state = %q/%q, want alert/alert", sig.Findings[0].Severity, sig.State)
	}
	if !strings.HasSuffix(sig.Summary, " (+1 inferred)") {
		t.Errorf("summary = %q, want the inferred suffix", sig.Summary)
	}
}

// TestApply_Caveats covers the CON-2 row.
func TestApply_Caveats(t *testing.T) {
	c := ctxWith(&model.Judgments{Caveats: []model.CaveatItem{
		{Text: "the queue path is untested", Cites: []string{"ev:claude-code:s1/e1"}},
	}})
	out := Apply(c, []model.Signal{sigOf("CON-2", model.StateInfo, "1 TODO/FIXME comment added.")})

	sig := testkit.Find(out, "CON-2")
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	if got, want := sig.Findings[0].Summary, "Agent noted: “the queue path is untested”"; got != want {
		t.Errorf("finding = %q, want %q", got, want)
	}
	if sig.Findings[0].Severity != model.StateInfo {
		t.Errorf("finding severity = %q, want info", sig.Findings[0].Severity)
	}
	if !strings.HasSuffix(sig.Summary, " (+1 inferred)") {
		t.Errorf("summary = %q, want the inferred suffix", sig.Summary)
	}
}

// TestApply_LLMErrors checks the judge's errors land on every touched signal.
func TestApply_LLMErrors(t *testing.T) {
	c := ctxWith(&model.Judgments{
		Errors:    []string{"scope: invalid JSON"},
		Caveats:   []model.CaveatItem{{Text: "untested", Cites: []string{"ev:claude-code:s1/e1"}}},
		PlanDrift: []model.DriftItem{{Kind: "missing_step", Text: "add jitter", Cites: []string{"ev:claude-code:s1/e1"}}},
	})
	out := Apply(c, []model.Signal{
		sigOf("CON-2", model.StateInfo, "1 TODO comment added."),
		sigOf("INT-3", model.StateInfo, "1 plan proposed."),
		sigOf("INT-1", model.StateInfo, "1 prompt."),
	})

	for _, id := range []string{"CON-2", "INT-3"} {
		sig := testkit.Find(out, id)
		errs, ok := sig.Data["llm_errors"].([]string)
		if !ok || len(errs) != 1 || errs[0] != "scope: invalid JSON" {
			t.Errorf("%s: Data[llm_errors] = %v, want the judge's error", id, sig.Data["llm_errors"])
		}
	}
	if sig := testkit.Find(out, "INT-1"); sig.Data["llm_errors"] != nil {
		t.Errorf("INT-1 was not touched but carries llm_errors: %v", sig.Data)
	}
}

// TestApply_SummaryLengthKept checks the 160-rune cap: the suffix is only added
// when it fits.
func TestApply_SummaryLengthKept(t *testing.T) {
	j := &model.Judgments{Caveats: []model.CaveatItem{
		{Text: "untested", Cites: []string{"ev:claude-code:s1/e1"}},
	}}
	c := ctxWith(j)

	short := strings.Repeat("a", 20) + "."
	long := strings.Repeat("b", 159) + "."
	out := Apply(c, []model.Signal{
		sigOf("CON-2", model.StateInfo, short),
		sigOf("CON-1", model.StateInfo, long),
	})

	if got, want := testkit.Find(out, "CON-2").Summary, short+" (+1 inferred)"; got != want {
		t.Errorf("short summary = %q, want %q", got, want)
	}
	// CON-1 is not a row of the table, so it is never touched.
	if got := testkit.Find(out, "CON-1").Summary; got != long {
		t.Errorf("untouched summary = %q, want %q", got, long)
	}

	// The same caveat on a summary that is one rune too long leaves it alone.
	tight := strings.Repeat("c", 160-len(" (+1 inferred)")+1) + "."
	out = Apply(c, []model.Signal{sigOf("CON-2", model.StateInfo, tight)})
	if got := testkit.Find(out, "CON-2").Summary; got != tight {
		t.Errorf("tight summary = %q, want it unchanged (%q)", got, tight)
	}
}

// TestEvidence_ResolvesAndSkips checks the cite helper.
func TestEvidence_ResolvesAndSkips(t *testing.T) {
	c := ctxWith(nil)
	ev := Evidence(c, []string{
		"ev:claude-code:s1/e2",
		"sig:INT-1",             // not an event cite
		"ev:codex:nope/e1",      // unknown session
		"ev:claude-code:s1/e99", // unknown event
		"ev:claude-code:s1/e2",  // repeat
	})
	if len(ev) != 1 {
		t.Fatalf("evidence = %+v, want exactly the resolvable cite", ev)
	}
	if ev[0].Session != "claude-code:s1" || ev[0].Event != "e2" || ev[0].TS.IsZero() {
		t.Errorf("evidence = %+v, want session/event/ts filled", ev[0])
	}
}

// TestAnchors_FromFieldsAndCites checks the anchor helper.
func TestAnchors_FromFieldsAndCites(t *testing.T) {
	got := Anchors("a.go", "10-12", []string{"file:b.go:3-4", "file:a.go:10-12", "ev:claude-code:s1/e1"})
	want := []model.Anchor{{File: "a.go", Lines: "10-12"}, {File: "b.go", Lines: "3-4"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("anchors = %+v, want %+v", got, want)
	}
	if got := Anchors("", "", []string{"ev:claude-code:s1/e1"}); got != nil {
		t.Errorf("anchors = %+v, want none", got)
	}
}
