package authorship

import (
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// auth3Find runs every detector and returns the AUTH-3 signal.
func auth3Find(t *testing.T, ctx *engine.Context) *model.Signal {
	t.Helper()
	sig := testkit.Find(engine.Run(ctx), "AUTH-3")
	if sig == nil {
		t.Fatal("AUTH-3 signal not found")
	}
	return sig
}

// auth3CheckSummary fails when the summary breaks the 160-rune cap.
func auth3CheckSummary(t *testing.T, sig *model.Signal) {
	t.Helper()
	if n := len([]rune(sig.Summary)); n > 160 {
		t.Errorf("AUTH-3 summary too long (%d > 160): %s", n, sig.Summary)
	}
}

func TestAUTH3_TrailerOrder(t *testing.T) {
	pr := testkit.PR("acme/shop", 1)
	pr.Add("file.txt", 1, "a", "b", "c", "d")

	// Claude Code wrote three lines, Codex one: the trailer and summary list
	// the busiest pair first.
	s1 := testkit.Session(model.HarnessClaudeCode, "s1")
	s1.Model("claude-opus").Prompt("go").Edit("file.txt", "a", "b", "c")

	c1 := testkit.Session(model.HarnessCodex, "c1")
	c1.Model("gpt-5").Prompt("go").Edit("file.txt", "d")

	sig := auth3Find(t, testkit.Ctx(pr.Build(), s1.Build(), c1.Build()))

	if sig.State != model.StateInfo {
		t.Errorf("state = %s, want info", sig.State)
	}
	wantTrailer := "Assisted-by: claude-code:claude-opus\nAssisted-by: codex:gpt-5"
	if got, _ := sig.Data["trailer"].(string); got != wantTrailer {
		t.Errorf("trailer =\n%q\nwant\n%q", got, wantTrailer)
	}
	wantSummary := "Assisted-by: claude-code:claude-opus (75%), codex:gpt-5 (25%) of agent lines."
	if sig.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", sig.Summary, wantSummary)
	}

	models, ok := sig.Data["models"].([]map[string]any)
	if !ok || len(models) != 2 {
		t.Fatalf("data[models] = %#v, want 2 entries", sig.Data["models"])
	}
	if models[0]["harness"] != "claude-code" || models[0]["model"] != "claude-opus" || models[0]["lines"] != 3 {
		t.Errorf("models[0] = %#v, want claude-code/claude-opus with 3 lines", models[0])
	}
	auth3CheckSummary(t, sig)
}

func TestAUTH3_SummaryTrim(t *testing.T) {
	// Six pairs, one line each, so the untrimmed summary runs past 160 runes
	// and the tail has to be dropped.
	pr := testkit.PR("acme/shop", 1)
	var sessions []*model.Session
	for i := 1; i <= 6; i++ {
		line := "l" + string(rune('0'+i))
		pr.Add("file.txt", i, line)
		sb := testkit.Session(model.HarnessClaudeCode, "s"+string(rune('0'+i)))
		sb.Model("test-model-"+string(rune('0'+i))).Prompt("go").Edit("file.txt", line)
		sessions = append(sessions, sb.Build())
	}

	sig := auth3Find(t, testkit.Ctx(pr.Build(), sessions...))

	auth3CheckSummary(t, sig)
	if !strings.HasSuffix(sig.Summary, "of agent lines.") {
		t.Errorf("trimmed summary lost its tail: %s", sig.Summary)
	}
	if !strings.Contains(sig.Summary, ", … of agent lines.") {
		t.Errorf("trimmed summary should mark the dropped pairs with ', …': %s", sig.Summary)
	}
	// The trailer is not trimmed: every pair keeps a line.
	trailer, _ := sig.Data["trailer"].(string)
	if n := strings.Count(trailer, "\n") + 1; n != 6 {
		t.Errorf("trailer has %d lines, want 6:\n%s", n, trailer)
	}
}

func TestAUTH3_NoAgentLines(t *testing.T) {
	// The PR has an added line no session accounts for.
	pr := testkit.PR("acme/shop", 1)
	pr.Add("file.txt", 1, "orphan line")

	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("nothing to do").Say("ok")

	sig := auth3Find(t, testkit.Ctx(pr.Build(), sb.Build()))

	if sig.State != model.StateUnknown {
		t.Errorf("state = %s, want unknown", sig.State)
	}
	if sig.Summary != "No agent-written lines in this PR." {
		t.Errorf("summary = %q, want the no-agent-lines message", sig.Summary)
	}
}
