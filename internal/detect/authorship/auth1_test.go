package authorship

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/attrib"
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestAUTH1_Summary(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("a.go", "line agent one", "line agent two").
		Add(model.Event{Kind: model.KindExternalEdit, External: &model.ExternalEdit{
			Path: "/repo/a.go", RelPath: "a.go", Lines: []string{"line human one"}, Source: "cc_attachment",
		}}).
		Build()

	pr := testkit.PR("acme/shop", 1).
		Add("a.go", 1, "line agent one", "line agent two", "line human one", "line uncaptured one").
		Build()

	attr := attrib.Attribute(pr, []*model.Session{s}, config.Default())
	ctx := engine.NewContext(pr, []*model.Session{s}, attr, nil, nil)

	sig := auth1{}.Detect(ctx)

	if sig.State != model.StateInfo {
		t.Fatalf("state = %s, want info", sig.State)
	}
	wantSummary := "Agent wrote 2 of 4 changed lines (50%); 0 edited by a human afterwards, 1 written by a human in session, 1 not captured."
	if sig.Summary != wantSummary {
		t.Fatalf("summary = %q, want %q", sig.Summary, wantSummary)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	wantFinding := "`a.go` — 2 agent · 0 agent→human · 1 human · 1 uncaptured"
	if got := sig.Findings[0].Summary; got != wantFinding {
		t.Fatalf("finding summary = %q, want %q", got, wantFinding)
	}
	if anchors := sig.Findings[0].Anchors; len(anchors) != 1 || anchors[0].Lines != "4" {
		t.Fatalf("anchors = %+v, want line 4 (the uncaptured line)", anchors)
	}
}

func TestAUTH1_NoSessions(t *testing.T) {
	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line one").Build()
	ctx := engine.NewContext(pr, nil, nil, nil, nil)

	sig := auth1{}.Detect(ctx)

	if sig.State != model.StateUnknown {
		t.Fatalf("state = %s, want unknown", sig.State)
	}
}

func TestAUTH1_NoLines(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").Say("hi").Build()
	pr := testkit.PR("acme/shop", 1).Build() // no files

	attr := attrib.Attribute(pr, []*model.Session{s}, config.Default())
	ctx := engine.NewContext(pr, []*model.Session{s}, attr, nil, nil)

	sig := auth1{}.Detect(ctx)

	if sig.State != model.StateUnknown {
		t.Fatalf("state = %s, want unknown", sig.State)
	}
	if sig.Summary != "No added lines to attribute." {
		t.Fatalf("summary = %q", sig.Summary)
	}
}
