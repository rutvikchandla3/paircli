package attrib

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestAttribute_AgentLatestWins(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("a.go", "shared line").
		At("15:00").
		Edit("a.go", "shared line").
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "shared line").Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	la := attr.Files[0].Lines[0]
	if la.Label != model.LabelAgent {
		t.Fatalf("label = %s, want agent", la.Label)
	}
	want := s.Events[1].ID
	if la.Source == nil || la.Source.Event != want {
		t.Fatalf("source = %+v, want event %s (the later edit)", la.Source, want)
	}
}

func TestAttribute_ExternalHuman(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Add(model.Event{Kind: model.KindExternalEdit, External: &model.ExternalEdit{
			Path: "/repo/a.go", RelPath: "a.go", Lines: []string{"human wrote this"}, Source: "cc_attachment",
		}}).
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "human wrote this").Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	la := attr.Files[0].Lines[0]
	if la.Label != model.LabelHuman {
		t.Fatalf("label = %s, want human_in_session", la.Label)
	}
	if la.Source == nil || la.Source.Event != s.Events[0].ID {
		t.Fatalf("source = %+v, want event %s", la.Source, s.Events[0].ID)
	}
}

func TestAttribute_SnapshotHuman(t *testing.T) {
	line := "snapshot introduced this"
	h := model.LineHash(line)

	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Add(model.Event{Kind: model.KindSnapshot, Snapshot: &model.Snapshot{
			Trigger: "stop", Files: []model.FileLines{{Path: "a.go", Hashes: nil}},
		}}).
		Prompt("next").
		Add(model.Event{Kind: model.KindSnapshot, Snapshot: &model.Snapshot{
			Trigger: "prompt", Files: []model.FileLines{{Path: "a.go", Hashes: []string{h}}},
		}}).
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, line).Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	la := attr.Files[0].Lines[0]
	if la.Label != model.LabelHuman {
		t.Fatalf("label = %s, want human_in_session", la.Label)
	}
	want := s.Events[2].ID
	if la.Source == nil || la.Source.Event != want {
		t.Fatalf("source = %+v, want the prompt snapshot event %s", la.Source, want)
	}
}

func TestAttribute_SnapshotNotHumanWhenAgentWrote(t *testing.T) {
	line := "agent wrote this first"
	h := model.LineHash(line)

	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("a.go", line).
		Add(model.Event{Kind: model.KindSnapshot, Snapshot: &model.Snapshot{
			Trigger: "stop", Files: []model.FileLines{{Path: "a.go", Hashes: nil}},
		}}).
		Prompt("next").
		Add(model.Event{Kind: model.KindSnapshot, Snapshot: &model.Snapshot{
			Trigger: "prompt", Files: []model.FileLines{{Path: "a.go", Hashes: []string{h}}},
		}}).
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, line).Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	la := attr.Files[0].Lines[0]
	if la.Label != model.LabelAgent {
		t.Fatalf("label = %s, want agent: an agent edit before the snapshot must block the human-hash channel", la.Label)
	}
	want := s.Events[0].ID
	if la.Source == nil || la.Source.Event != want {
		t.Fatalf("source = %+v, want the original agent edit %s", la.Source, want)
	}
}

func TestAttribute_Reformatted(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("a.go", "const x = 'y';").
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, `const x = "y"`).Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	la := attr.Files[0].Lines[0]
	if la.Label != model.LabelAgent || !la.Reformatted {
		t.Fatalf("label/reformatted = %s/%v, want agent/true", la.Label, la.Reformatted)
	}
}

func TestAttribute_Mixed(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("a.go", "const max = 5;").
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "const max = 6;").Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	la := attr.Files[0].Lines[0]
	if la.Label != model.LabelMixed {
		t.Fatalf("label = %s, want agent_then_human", la.Label)
	}
	if la.Source == nil || la.Source.Event != s.Events[0].ID {
		t.Fatalf("source = %+v, want event %s", la.Source, s.Events[0].ID)
	}
}

func TestAttribute_Uncaptured(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").Say("hi").Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "nobody wrote this line").Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	if la := attr.Files[0].Lines[0]; la.Label != model.LabelUncaptured {
		t.Fatalf("label = %s, want uncaptured", la.Label)
	}
}

func TestAttribute_Trivial(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").Say("hi").Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "", "{}", ";").Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	for i, la := range attr.Files[0].Lines {
		if la.Label != model.LabelTrivial {
			t.Fatalf("line %d label = %s, want trivial", i, la.Label)
		}
	}
	if attr.Total != 0 {
		t.Fatalf("total = %d, want 0", attr.Total)
	}
}

func TestAttribute_GeneratedFile(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("api.pb.go", "type Foo struct{}").
		Build()

	pr := testkit.PR("acme/shop", 1).Add("api.pb.go", 1, "type Foo struct{}").Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	if la := attr.Files[0].Lines[0]; la.Label != model.LabelTrivial {
		t.Fatalf("label = %s, want trivial (generated file)", la.Label)
	}
}

func TestAttribute_FailedEditIgnored(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{
			Path: "/repo/a.go", RelPath: "a.go", Op: model.OpUpdate, Via: "edit",
			Added: []string{"failed line"}, Failed: true,
		}}).
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "failed line").Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	if la := attr.Files[0].Lines[0]; la.Label != model.LabelUncaptured {
		t.Fatalf("label = %s, want uncaptured: a failed edit must not count as agent authorship", la.Label)
	}
}

func TestAttribute_OffBranchCounts(t *testing.T) {
	s := testkit.Session(model.HarnessPi, "s1").
		Add(model.Event{Kind: model.KindEdit, OffBranch: true, Edit: &model.FileEdit{
			Path: "/repo/a.go", RelPath: "a.go", Op: model.OpUpdate, Via: "edit",
			Added: []string{"off branch line"},
		}}).
		Agent("sub-1").
		Edit("a.go", "subagent line").
		Build()

	pr := testkit.PR("acme/shop", 1).
		Add("a.go", 1, "off branch line", "subagent line").
		Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	for i, la := range attr.Files[0].Lines {
		if la.Label != model.LabelAgent {
			t.Fatalf("line %d label = %s, want agent (off-branch and subagent edits still count)", i, la.Label)
		}
	}
	if got := attr.Files[0].Lines[1].AgentID; got != "sub-1" {
		t.Fatalf("agent id = %q, want sub-1", got)
	}
}

func TestAttribute_Counts(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("a.go", "agent line").
		Add(model.Event{Kind: model.KindExternalEdit, External: &model.ExternalEdit{
			Path: "/repo/a.go", RelPath: "a.go", Lines: []string{"human line"}, Source: "cc_attachment",
		}}).
		Build()

	pr := testkit.PR("acme/shop", 1).
		Add("a.go", 1, "agent line", "human line", "uncaptured line", "").
		Build()

	attr := Attribute(pr, []*model.Session{s}, config.Default())

	if attr.Total != 3 {
		t.Fatalf("total = %d, want 3", attr.Total)
	}
	if attr.Explained != 2 {
		t.Fatalf("explained = %d, want 2", attr.Explained)
	}
	if got := attr.Ratio(); got < 0.666 || got > 0.667 {
		t.Fatalf("ratio = %v, want ~0.667", got)
	}
}

func TestCommitSessions(t *testing.T) {
	s1 := testkit.Session(model.HarnessClaudeCode, "s1").
		Edit("a.go", "this is a long enough agent line").
		Build()
	s2 := testkit.Session(model.HarnessCodex, "s2").
		Edit("b.go", "another sufficiently long line").
		Build()

	pr := testkit.PR("acme/shop", 1).Build()
	pr.Commits = []model.Commit{
		{
			SHA: "commit1",
			Files: []model.DiffFile{{
				Path: "a.go", Status: model.StatusModified,
				Hunks: []model.DiffHunk{{NewStart: 1, NewLines: 2, Lines: []model.DiffLine{
					{Kind: model.LineAdd, Text: "this is a long enough agent line", NewNo: 1},
					{Kind: model.LineAdd, Text: "hi", NewNo: 2}, // too short, ignored
				}}},
			}},
		},
		{
			SHA: "commit2",
			Files: []model.DiffFile{{
				Path: "b.go", Status: model.StatusModified,
				Hunks: []model.DiffHunk{{NewStart: 1, NewLines: 1, Lines: []model.DiffLine{
					{Kind: model.LineAdd, Text: "another sufficiently long line", NewNo: 1},
				}}},
			}},
		},
		{
			SHA: "commit3",
			Files: []model.DiffFile{{
				Path: "c.go", Status: model.StatusModified,
				Hunks: []model.DiffHunk{{NewStart: 1, NewLines: 1, Lines: []model.DiffLine{
					{Kind: model.LineAdd, Text: "nobody wrote this one either", NewNo: 1},
				}}},
			}},
		},
	}

	got := CommitSessions(pr, []*model.Session{s1, s2})

	if want := []string{"claude-code:s1"}; !reflect.DeepEqual(got["commit1"], want) {
		t.Fatalf("commit1 sessions = %v, want %v", got["commit1"], want)
	}
	if want := []string{"codex:s2"}; !reflect.DeepEqual(got["commit2"], want) {
		t.Fatalf("commit2 sessions = %v, want %v", got["commit2"], want)
	}
	if _, ok := got["commit3"]; ok {
		t.Fatalf("commit3 should be absent: no line matched an agent edit")
	}
}

// BenchmarkAttribute exercises Attribute at roughly 50 files x 400 lines
// (20000 PR lines) against a session with 200 edit events. It only reports
// ns/op; there is no pass/fail threshold.
func BenchmarkAttribute(b *testing.B) {
	pb := testkit.PR("acme/shop", 1)
	sb := testkit.Session(model.HarnessClaudeCode, "bench")

	const files, linesPerFile, edits = 50, 400, 200

	for f := 0; f < files; f++ {
		path := fmt.Sprintf("pkg%d/file.go", f)
		lines := make([]string, linesPerFile)
		for l := range lines {
			lines[l] = fmt.Sprintf("line %d content in file %d", l, f)
		}
		pb.Add(path, 1, lines...)
	}
	pr := pb.Build()

	for e := 0; e < edits; e++ {
		f := e % files
		path := fmt.Sprintf("pkg%d/file.go", f)
		added := make([]string, 0, 4)
		for k := 0; k < 4; k++ {
			l := (e*4 + k) % linesPerFile
			added = append(added, fmt.Sprintf("line %d content in file %d", l, f))
		}
		sb.Edit(path, added...)
	}
	sess := sb.Build()
	cfg := config.Default()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Attribute(pr, []*model.Session{sess}, cfg)
	}
}
