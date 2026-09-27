package testkit_test

import (
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestSessionBuilder_ClockAndTurns(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "sess-1").
		Prompt("do the thing").
		Say("ok working on it").
		At("14:30").
		Prompt("also fix x").
		Build()

	if len(s.Events) != 3 {
		t.Fatalf("want 3 events, got %d", len(s.Events))
	}

	want0 := time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)
	want1 := want0.Add(time.Minute)
	want2 := time.Date(2026, 9, 27, 14, 30, 0, 0, time.UTC)
	if !s.Events[0].TS.Equal(want0) {
		t.Fatalf("event0 TS = %v, want %v", s.Events[0].TS, want0)
	}
	if !s.Events[1].TS.Equal(want1) {
		t.Fatalf("event1 TS = %v, want %v", s.Events[1].TS, want1)
	}
	if !s.Events[2].TS.Equal(want2) {
		t.Fatalf("event2 TS = %v, want %v", s.Events[2].TS, want2)
	}

	if s.Events[0].ID != "e1" || s.Events[1].ID != "e2" || s.Events[2].ID != "e3" {
		t.Fatalf("unexpected auto IDs: %s %s %s", s.Events[0].ID, s.Events[1].ID, s.Events[2].ID)
	}

	if s.Events[0].Turn != 1 || s.Events[1].Turn != 1 || s.Events[2].Turn != 2 {
		t.Fatalf("unexpected turns: %d %d %d", s.Events[0].Turn, s.Events[1].Turn, s.Events[2].Turn)
	}
	if s.Events[1].Message == nil || !s.Events[1].Message.Final {
		t.Fatalf("want the message before the second prompt marked final")
	}

	if !s.Start.Equal(want0) || !s.End.Equal(want2) {
		t.Fatalf("Start/End = %v/%v, want %v/%v", s.Start, s.End, want0, want2)
	}
}

func TestExactAttribution(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Model("model-a").
		Edit("a.go", "var agentLine = 1").
		Add(model.Event{
			Kind: model.KindExternalEdit,
			External: &model.ExternalEdit{
				Path: "/repo/a.go", RelPath: "a.go",
				Lines:  []string{"var humanLine = 2"},
				Source: "cc_attachment",
			},
		}).
		Add(model.Event{
			Kind: model.KindEdit,
			Edit: &model.FileEdit{
				Path: "/repo/a.go", RelPath: "a.go", Op: model.OpUpdate, Via: "edit",
				Added:  []string{"var failedLine = 4"},
				Failed: true,
			},
		}).
		At("14:10").
		Model("model-a").
		Edit("a.go", "var laterWins = 5").
		At("15:00").
		Model("model-b").
		Edit("a.go", "var laterWins = 5").
		Build()

	pr := testkit.PR("acme/shop", 1).
		Add("a.go", 1,
			"var agentLine = 1",
			"var humanLine = 2",
			"var uncapturedLine = 3",
			"",
			"var failedLine = 4",
			"var laterWins = 5",
		).
		Build()

	attr := testkit.ExactAttribution(pr, []*model.Session{s})
	if attr.Total != 5 {
		t.Fatalf("Total = %d, want 5", attr.Total)
	}
	if attr.Explained != 3 {
		t.Fatalf("Explained = %d, want 3", attr.Explained)
	}
	if len(attr.Files) != 1 {
		t.Fatalf("want 1 file attribution, got %d", len(attr.Files))
	}

	byLine := map[int]model.LineAttribution{}
	for _, l := range attr.Files[0].Lines {
		byLine[l.Line] = l
	}

	if byLine[1].Label != model.LabelAgent || byLine[1].Model != "model-a" {
		t.Fatalf("line1 = %+v, want agent/model-a", byLine[1])
	}
	if byLine[2].Label != model.LabelHuman {
		t.Fatalf("line2 = %+v, want human_in_session", byLine[2])
	}
	if byLine[3].Label != model.LabelUncaptured {
		t.Fatalf("line3 = %+v, want uncaptured", byLine[3])
	}
	if byLine[4].Label != model.LabelTrivial {
		t.Fatalf("line4 = %+v, want trivial", byLine[4])
	}
	if byLine[5].Label != model.LabelUncaptured {
		t.Fatalf("line5 (failed edit only) = %+v, want uncaptured", byLine[5])
	}
	if byLine[6].Label != model.LabelAgent || byLine[6].Model != "model-b" {
		t.Fatalf("line6 (later edit wins) = %+v, want agent/model-b", byLine[6])
	}
}

func TestPRBuilder_LineNumbers(t *testing.T) {
	pr := testkit.PR("acme/shop", 7).Add("a.go", 10, "x", "y").Build()

	if pr.URL != "https://github.com/acme/shop/pull/7" {
		t.Fatalf("URL = %q", pr.URL)
	}
	if pr.Title != "Test PR" || pr.HeadRef != "feat/test" || pr.BaseRef != "main" {
		t.Fatalf("unexpected defaults: %+v", pr)
	}

	f := pr.File("a.go")
	if f == nil {
		t.Fatalf("file a.go not found")
	}
	added := f.AddedLines()
	if len(added) != 2 {
		t.Fatalf("want 2 added lines, got %d", len(added))
	}
	if added[0].NewNo != 10 || added[1].NewNo != 11 {
		t.Fatalf("NewNo = %d,%d want 10,11", added[0].NewNo, added[1].NewNo)
	}
}
