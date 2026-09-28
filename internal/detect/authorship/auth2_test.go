package authorship

import (
	"reflect"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// TestAUTH2_Coverage builds an Attribution by hand (Total 20, Explained 8,
// ratio 0.4) to exercise the alert threshold, the unattributed-commit
// finding, and the SessionLinks == nil -> "unknown" default.
func TestAUTH2_Coverage(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "session-one").Build()
	ref := s.Ref() // "claude-code:session-one"

	fa := model.FileAttribution{
		Path:   "a.go",
		Counts: map[model.LineLabel]int{model.LabelAgent: 8, model.LabelUncaptured: 12},
	}
	for i := 1; i <= 20; i++ {
		la := model.LineAttribution{Line: i}
		if i <= 8 {
			la.Label = model.LabelAgent
			evRef := model.EventRef{Session: ref, Event: "e" + string(rune('0'+i))}
			la.Source = &evRef
		} else {
			la.Label = model.LabelUncaptured
		}
		fa.Lines = append(fa.Lines, la)
	}
	attr := &model.Attribution{Total: 20, Explained: 8, Files: []model.FileAttribution{fa}}

	links := []model.CommitLink{
		{SHA: "abc1234567890", Sessions: []string{ref}, Method: model.LinkSHAExact, Confidence: "exact"},
		{SHA: "def0987654321", Sessions: nil, Method: model.LinkNone, Confidence: "unknown"},
	}

	pr := testkit.PR("acme/shop", 1).Build()
	ctx := engine.NewContext(pr, []*model.Session{s}, attr, links, nil)
	// ctx.SessionLinks is left nil by NewContext; that is exactly what this
	// test wants to exercise (auth2LinkMethod must default to "unknown").

	sig := auth2{}.Detect(ctx)

	if sig.State != model.StateAlert {
		t.Fatalf("state = %s, want alert (Total 20 >= 10 and ratio 0.4 < 0.5)", sig.State)
	}

	wantSummary := "1 sessions (1 claude-code) explain 40% of changed lines. 1 commits have no session."
	if sig.Summary != wantSummary {
		t.Fatalf("summary = %q, want %q", sig.Summary, wantSummary)
	}

	if len(sig.Findings) != 2 {
		t.Fatalf("findings = %d, want 2 (one session, one unattributed commit)", len(sig.Findings))
	}
	wantSessionFinding := "`claude-code:session-` (reconstructed) linked by unknown — 8 lines, commits abc1234"
	if got := sig.Findings[0].Summary; got != wantSessionFinding {
		t.Fatalf("session finding = %q, want %q", got, wantSessionFinding)
	}
	if sig.Findings[0].Severity != model.StateInfo {
		t.Fatalf("session finding severity = %s, want info", sig.Findings[0].Severity)
	}
	wantCommitFinding := "Commit def0987 has no matching session."
	if got := sig.Findings[1].Summary; got != wantCommitFinding {
		t.Fatalf("commit finding = %q, want %q", got, wantCommitFinding)
	}
	if sig.Findings[1].Severity != model.StateAlert {
		t.Fatalf("commit finding severity = %s, want alert", sig.Findings[1].Severity)
	}

	data, ok := sig.Data["sessions"].([]map[string]any)
	if !ok || len(data) != 1 {
		t.Fatalf("data sessions = %#v", sig.Data["sessions"])
	}
	if data[0]["link"] != "unknown" {
		t.Fatalf("data session link = %v, want unknown", data[0]["link"])
	}
	if data[0]["lines"] != 8 {
		t.Fatalf("data session lines = %v, want 8", data[0]["lines"])
	}
	if want := []string{"abc1234567890"}; !reflect.DeepEqual(data[0]["commits"], want) {
		t.Fatalf("data session commits = %v, want %v", data[0]["commits"], want)
	}
	if want := []string{"def0987654321"}; !reflect.DeepEqual(sig.Data["unattributed"], want) {
		t.Fatalf("data unattributed = %v, want %v", sig.Data["unattributed"], want)
	}
	if r := sig.Data["ratio"].(float64); r != 0.4 {
		t.Fatalf("data ratio = %v, want 0.4", r)
	}
}

func TestAUTH2_NoSessions(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Build()
	ctx := engine.NewContext(pr, nil, nil, nil, nil)

	sig := auth2{}.Detect(ctx)

	if sig.State != model.StateUnknown {
		t.Fatalf("state = %s, want unknown", sig.State)
	}
}
