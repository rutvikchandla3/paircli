package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/scan"
)

// e2eLLMOutputs are the reviewer-facing files the LLM run writes.
var e2eLLMOutputs = []string{"signals.json", "report.md", "comment.md"}

// e2eLLMReplyFiles are the canned judge replies, one per job, in the order
// internal/judge runs them: claims, story, scope, caveats. They are read from
// testdata/golden_llm/replies/, where they double as the fixtures that pin
// which bundle ids the judge is allowed to cite.
var e2eLLMReplyFiles = []string{"claims.json", "story.json", "scope.json", "caveats.json"}

// TestE2E_WithLLM runs T25's scenario again with the LLM pass on: the same
// world, the same fake gh runner, but a llm.Fake whose replies come from
// testdata/golden_llm/replies/. The judge's output must land in the three
// inferred signals, must enrich the deterministic ones and must leave the
// report deterministic, so the whole run is compared against
// testdata/golden_llm byte for byte.
func TestE2E_WithLLM(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	fake := &llm.Fake{Replies: e2eLLMReplies(t)}
	res, err := scan.Run(context.Background(), scan.Options{
		Repo:    e2eRepo,
		Number:  e2eNumber,
		CWD:     w.repoDir,
		OutDir:  w.outDir,
		Runner:  w.runner,
		Config:  w.cfg,
		LLM:     fake,
		Now:     e2eAt(e2eNowHHMM),
		Version: e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: scan.Run with --llm: %v", err)
	}
	if res.Report == nil {
		t.Fatal("e2e: scan.Run returned a nil report")
	}

	t.Run("replies", func(t *testing.T) {
		// One reply per job, and no job needed its retry.
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

	t.Run("inferred", func(t *testing.T) {
		e2eAssertLLMRun(t, res)
	})
}

// e2eAssertLLMRun checks the signal-level effects of the LLM pass: the three
// inferred signals are real, the judge kept every reply intact, and the
// enrich rows that this scenario can exercise all landed.
func e2eAssertLLMRun(t *testing.T, res *scan.Result) {
	rep := res.Report

	// No judge job failed, and no signal carries a leftover error.
	for _, id := range []string{"DEC-1", "DEC-2", "INT-1", "INT-3", "CON-2", "FRI-3"} {
		sig := e2eFindSignal(rep, id)
		if sig == nil {
			t.Fatalf("%s missing from the report", id)
		}
		if errs := sig.Data["llm_errors"]; errs != nil {
			t.Errorf("%s: Data[llm_errors] = %v, want none", id, errs)
		}
	}

	// DEC-3: one finding per decision, in info state.
	dec3 := e2eFindSignal(rep, "DEC-3")
	if dec3 == nil {
		t.Fatal("DEC-3 missing with the LLM pass on")
	}
	if dec3.State != model.StateInfo {
		t.Errorf("DEC-3: state = %q, want info (%s)", dec3.State, dec3.Summary)
	}
	if len(dec3.Findings) != 3 {
		t.Errorf("DEC-3: findings = %d, want 3 (%s)", len(dec3.Findings), dec3.Summary)
	}
	if got, want := dec3.Summary, "3 decisions shaped this diff; first: Keep the retry attempt count in Postgres."; got != want {
		t.Errorf("DEC-3: summary = %q, want %q", got, want)
	}
	if !e2eFindingMentions(*dec3, "Postgres") {
		t.Errorf("DEC-3: no finding names the Postgres decision")
	}
	if strings.Contains(dec3.Summary, "inferred") {
		// DEC-3 is computed from the judgments, not enriched: no suffix.
		t.Errorf("DEC-3: summary carries an inferred suffix: %q", dec3.Summary)
	}

	// CON-1: the two contradicted claims and the one supported claim.
	con1 := e2eFindSignal(rep, "CON-1")
	if con1 == nil {
		t.Fatal("CON-1 missing with the LLM pass on")
	}
	e2eRequireState(t, *con1, model.StateAlert)
	if got, want := con1.Summary, "2 claims contradicted, 1 supported, 0 without evidence."; got != want {
		t.Errorf("CON-1: summary = %q, want %q", got, want)
	}
	if !e2eFindingMentions(*con1, "is contradicted") {
		t.Errorf("CON-1: no contradicted finding (%s)", con1.Summary)
	}

	// CON-3: the two hunks that trace to nothing.
	con3 := e2eFindSignal(rep, "CON-3")
	if con3 == nil {
		t.Fatal("CON-3 missing with the LLM pass on")
	}
	e2eRequireState(t, *con3, model.StateAlert)
	if got, want := con3.Summary, "7 of 9 hunks trace to the ask or plan; 2 do not."; got != want {
		t.Errorf("CON-3: summary = %q, want %q", got, want)
	}
	if !e2eHasAnchor(*con3, "README.md") {
		t.Errorf("CON-3: no anchor on README.md (%s)", con3.Summary)
	}

	// Every inferred finding says so in its provenance, and points at the bundle
	// ids it used: DEC-3 cites events, CON-3 cites hunks (and so carries
	// anchors).
	for _, sig := range []*model.Signal{dec3, con1, con3} {
		for i, f := range sig.Findings {
			if f.Provenance != model.Inferred {
				t.Errorf("%s finding %d: provenance = %q, want inferred", sig.ID, i, f.Provenance)
			}
		}
	}
	for i, f := range dec3.Findings {
		if len(f.Evidence) == 0 {
			t.Errorf("DEC-3 finding %d: no evidence (%s)", i, f.Summary)
		}
	}
	for i, f := range con3.Findings {
		if len(f.Anchors) == 0 {
			t.Errorf("CON-3 finding %d: no anchors (%s)", i, f.Summary)
		}
	}

	// INT-1: the condensed ask, prepended.
	int1 := e2eFindSignal(rep, "INT-1")
	if !e2eFindingMentions(*int1, "Ask (condensed):") {
		t.Errorf("INT-1: no condensed ask finding (%s)", int1.Summary)
	}
	if _, ok := int1.Data["ask_summary"]; !ok {
		t.Errorf("INT-1: Data[ask_summary] missing: %v", int1.Data)
	}
	if !strings.HasSuffix(int1.Summary, " (+1 inferred)") {
		t.Errorf("INT-1: summary = %q, want the inferred suffix", int1.Summary)
	}

	// INT-3: the missing plan step and the unplanned README change.
	int3 := e2eFindSignal(rep, "INT-3")
	e2eRequireState(t, *int3, model.StateAlert)
	if !e2eFindingMentions(*int3, "Planned step with no matching change: add jitter to the backoff") {
		t.Errorf("INT-3: no missing_step finding (%s)", int3.Summary)
	}
	if !e2eFindingMentions(*int3, "Change not in the plan: `README.md` 4-6") {
		t.Errorf("INT-3: no unplanned_change finding (%s)", int3.Summary)
	}
	if !strings.HasSuffix(int3.Summary, " (+2 inferred)") {
		t.Errorf("INT-3: summary = %q, want a +2 inferred suffix", int3.Summary)
	}

	// DEC-1: the interrupt is still counted, but the judge's verdict that it
	// did not correct earlier work is recorded on the finding.
	dec1 := e2eFindSignal(rep, "DEC-1")
	found := false
	for _, f := range dec1.Findings {
		if f.Data["kind"] != "interrupt" {
			continue
		}
		found = true
		if f.Data["confirmed"] != false {
			t.Errorf("DEC-1: interrupt Data[confirmed] = %v, want false", f.Data["confirmed"])
		}
		if f.Severity != model.StateInfo {
			t.Errorf("DEC-1: interrupt severity = %q, want info", f.Severity)
		}
	}
	if !found {
		t.Errorf("DEC-1: no interrupt finding (%s)", dec1.Summary)
	}

	// CON-2: the caveat the agent stated.
	con2 := e2eFindSignal(rep, "CON-2")
	if !e2eFindingMentions(*con2, "Agent noted:") {
		t.Errorf("CON-2: no caveat finding (%s)", con2.Summary)
	}
}

// TestE2E_WithLLM_Failure covers the wiring's error branch: when every judge
// job fails the report keeps the deterministic output, the three inferred
// signals stay out of it, and the failure is noted on AUTH-2.
func TestE2E_WithLLM_Failure(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	// No replies at all: every job fails, and so does its retry.
	fake := &llm.Fake{}
	res, err := scan.Run(context.Background(), scan.Options{
		Repo:    e2eRepo,
		Number:  e2eNumber,
		CWD:     w.repoDir,
		OutDir:  w.outDir,
		Runner:  w.runner,
		Config:  w.cfg,
		LLM:     fake,
		Now:     e2eAt(e2eNowHHMM),
		Version: e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: a failed LLM pass must not fail the scan: %v", err)
	}
	// A provider error is not retried (only an unparsable reply is), so the
	// judge makes exactly one call per job.
	if got, want := len(fake.Calls), len(e2eLLMReplyFiles); got != want {
		t.Errorf("llm calls = %d, want %d (one per judge job)", got, want)
	}

	if got, want := len(res.Report.Signals), 27; got != want {
		t.Errorf("signals = %d, want %d: a failed LLM pass reports no inferred signals", got, want)
	}
	for _, id := range []string{"DEC-3", "CON-1", "CON-3"} {
		if sig := e2eFindSignal(res.Report, id); sig != nil {
			t.Errorf("%s present (%s) although the LLM pass failed", id, sig.State)
		}
	}

	auth2 := e2eSignal(t, res.Report, "AUTH-2")
	errs, ok := auth2.Data["llm_errors"].([]string)
	if !ok || len(errs) == 0 {
		t.Errorf("AUTH-2: Data[llm_errors] = %v, want the judge's failure", auth2.Data["llm_errors"])
	}
	for _, sig := range res.Report.Signals {
		if sig.ID == "AUTH-2" {
			continue
		}
		if v, ok := sig.Data["llm_errors"]; ok {
			t.Errorf("%s: Data[llm_errors] = %v, but the pass produced no judgments", sig.ID, v)
		}
	}

	// The rendered report is the deterministic one, byte for byte: the failure
	// note lives in signals.json only.
	for _, name := range []string{"report.md", "comment.md"} {
		got, err := os.ReadFile(filepath.Join(w.outDir, name))
		if err != nil {
			t.Fatalf("e2e: reading %s: %v", name, err)
		}
		raw, err := os.ReadFile(filepath.Join("testdata", "golden", name))
		if err != nil {
			t.Fatalf("e2e: reading golden %s: %v", name, err)
		}
		if a, b := e2eNormalize(w, got), string(raw); a != b {
			t.Errorf("e2e: %s after a failed LLM pass differs from the deterministic golden:\n--- golden ---\n%s\n--- got ---\n%s", name, b, a)
		}
	}
}

// e2eLLMReplies reads the canned judge replies in job order.
func e2eLLMReplies(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, name := range e2eLLMReplyFiles {
		raw, err := os.ReadFile(filepath.Join("testdata", "golden_llm", "replies", name))
		if err != nil {
			t.Fatalf("e2e: reading reply %s: %v", name, err)
		}
		out = append(out, string(raw))
	}
	return out
}

// e2eLLMCompareGolden compares got with testdata/golden_llm/name, or rewrites
// it when -update is set. Like T25's goldens these are compared byte for byte:
// the LLM pass is deterministic once the replies are fixed.
func e2eLLMCompareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden_llm", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("e2e: mkdir testdata/golden_llm: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("e2e: writing golden %s: %v", name, err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("e2e: reading golden_llm/%s: %v (run `go test ./internal/e2e/... -run E2E -update`)", name, err)
	}
	if want := string(raw); got != want {
		t.Errorf("e2e: golden_llm/%s differs:\n--- golden ---\n%s\n--- got ---\n%s", name, want, got)
	}
}
