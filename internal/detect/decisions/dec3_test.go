package decisions

import (
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// dec3Signal runs DEC-3 alone over ctx.
func dec3Signal(t *testing.T, ctx *engine.Context) *model.Signal {
	t.Helper()
	sig := testkit.Find(engine.RunIDs(ctx, "DEC-3"), "DEC-3")
	if sig == nil {
		t.Fatal("DEC-3 signal not found")
	}
	return sig
}

// TestDEC3_NilJudgments is the LLM-off contract: no judgments, no signal.
func TestDEC3_NilJudgments(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("do it").Build()

	sig := dec3Signal(t, testkit.Ctx(nil, s))
	if sig.State != model.StateUnknown {
		t.Errorf("state = %q, want unknown", sig.State)
	}
	const want = "LLM pass is off. Run with --llm to fill this in."
	if sig.Summary != want {
		t.Errorf("summary = %q, want %q", sig.Summary, want)
	}
}

// TestDEC3_Decisions covers one finding per decision item, its text and its
// evidence, and the signal summary.
func TestDEC3_Decisions(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("add retries").
		Edit("a.go", "retry()").
		Build()
	ctx := testkit.Ctx(nil, s)
	ctx.Judgments = &model.Judgments{
		Decisions: []model.DecisionItem{
			{Choice: "Cap attempts at five", Reason: "the API rate limit", By: "human", Cites: []string{"ev:claude-code:s1/e1"}},
			{Choice: "Exponential backoff", Reason: "jitter was dropped", By: "agent", Cites: []string{"ev:claude-code:s1/e2"}},
		},
	}

	sig := dec3Signal(t, ctx)
	if sig.State != model.StateInfo {
		t.Errorf("state = %q, want info", sig.State)
	}
	const want = "2 decisions shaped this diff; first: Cap attempts at five."
	if sig.Summary != want {
		t.Errorf("summary = %q, want %q", sig.Summary, want)
	}
	if len(sig.Findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(sig.Findings))
	}
	if got, want := sig.Findings[0].Summary, "Cap attempts at five — the API rate limit (human)"; got != want {
		t.Errorf("finding 0 = %q, want %q", got, want)
	}
	if sig.Findings[0].Provenance != model.Inferred {
		t.Errorf("finding provenance = %q, want inferred", sig.Findings[0].Provenance)
	}
	if sig.Findings[0].Severity != model.StateInfo {
		t.Errorf("finding severity = %q, want info", sig.Findings[0].Severity)
	}
	ev := sig.Findings[0].Evidence
	if len(ev) != 1 || ev[0].Session != "claude-code:s1" || ev[0].Event != "e1" {
		t.Errorf("evidence = %+v, want the ev cite to resolve", ev)
	}
}

// TestDEC3_ClipAndEmptyParts checks the {choice} — {reason} ({by}) rendering
// when the judgment carried no reason or author.
func TestDEC3_ClipAndEmptyParts(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("do it").Build()
	ctx := testkit.Ctx(nil, s)
	ctx.Judgments = &model.Judgments{
		Decisions: []model.DecisionItem{{Choice: "Keep the queue", Cites: []string{"ev:claude-code:s1/e1"}}},
	}

	sig := dec3Signal(t, ctx)
	if got, want := sig.Findings[0].Summary, "Keep the queue"; got != want {
		t.Errorf("finding = %q, want %q", got, want)
	}
}

// TestDEC3_NoDecisions is the empty-judgment case.
func TestDEC3_NoDecisions(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("do it").Build()
	ctx := testkit.Ctx(nil, s)
	ctx.Judgments = &model.Judgments{}

	sig := dec3Signal(t, ctx)
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if got, want := sig.Summary, "No decisions were identified."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("findings = %d, want 0", len(sig.Findings))
	}
}

// TestDEC3_SummaryCap keeps the summary inside the 160-rune budget even for a
// long first choice.
func TestDEC3_SummaryCap(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").Prompt("do it").Build()
	ctx := testkit.Ctx(nil, s)
	ctx.Judgments = &model.Judgments{
		Decisions: []model.DecisionItem{{Choice: strings.Repeat("z", 400), Cites: []string{"ev:claude-code:s1/e1"}}},
	}

	sig := dec3Signal(t, ctx)
	if n := len([]rune(sig.Summary)); n > 160 {
		t.Errorf("summary is %d runes, want at most 160: %s", n, sig.Summary)
	}
}

// TestDEC3_LLMOnNoSessions keeps the "no sessions" answer for the LLM-on case.
func TestDEC3_LLMOnNoSessions(t *testing.T) {
	ctx := testkit.Ctx(nil)
	ctx.Judgments = &model.Judgments{}

	sig := dec3Signal(t, ctx)
	if sig.State != model.StateUnknown {
		t.Errorf("state = %q, want unknown", sig.State)
	}
	if got, want := sig.Summary, "No captured sessions for this PR."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}
