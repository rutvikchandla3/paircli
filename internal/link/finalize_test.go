package link

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestFinalize_Methods(t *testing.T) {
	const shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	pr := testkit.PR("acme/shop", 7).Commit(shaA, "14:00").Commit(shaB, "15:00").Build()

	sExact := testkit.Session(model.HarnessClaudeCode, "exact").
		Add(model.Event{Kind: model.KindGitHead, GitHead: &model.GitHead{SHA: shaA, Trigger: "commit"}}).
		Build()

	// Satisfies both sha_exact (git_head) and sha_ancestor (StartSHA): exact
	// must win because it is checked first.
	sPrecedence := testkit.Session(model.HarnessClaudeCode, "prec").
		Add(model.Event{Kind: model.KindGitHead, GitHead: &model.GitHead{SHA: shaA, Trigger: "commit"}}).
		Build()
	sPrecedence.StartSHA = shaB

	sAncestor := testkit.Session(model.HarnessClaudeCode, "ancestor").Prompt("hi").Build()
	sAncestor.StartSHA = shaB

	sContent := testkit.Session(model.HarnessClaudeCode, "content").Prompt("hi").Build()
	sContent.Branch = "other-branch" // keep it out of the heuristic bucket

	sHeuristic := testkit.Session(model.HarnessClaudeCode, "heuristic").Prompt("hi").Build()
	// Branch stays the testkit default "feat/test", which matches pr.HeadRef,
	// and its single event lands at 14:00, inside shaA's +/-2h window.

	sDropped := testkit.Session(model.HarnessClaudeCode, "dropped").Prompt("hi").Build()
	sDropped.Branch = "unrelated"

	cands := []*model.Session{sExact, sPrecedence, sAncestor, sContent, sHeuristic, sDropped}

	attr := &model.Attribution{Files: []model.FileAttribution{{
		Path: "x.go",
		Lines: []model.LineAttribution{{
			Line: 1, Label: model.LabelAgent,
			Source: &model.EventRef{Session: sContent.Ref(), Event: "e1"},
		}},
	}}}

	res := Finalize(pr, cands, attr, map[string][]string{}, 0)

	want := map[string]model.LinkMethod{
		sExact.Ref():      model.LinkSHAExact,
		sPrecedence.Ref(): model.LinkSHAExact,
		sAncestor.Ref():   model.LinkSHAAncestor,
		sContent.Ref():    model.LinkContent,
		sHeuristic.Ref():  model.LinkHeuristic,
	}
	for ref, method := range want {
		if got := res.SessionLinks[ref]; got != method {
			t.Errorf("SessionLinks[%s] = %q, want %q", ref, got, method)
		}
	}
	if _, ok := res.SessionLinks[sDropped.Ref()]; ok {
		t.Errorf("sDropped should not be linked, got method %q", res.SessionLinks[sDropped.Ref()])
	}
	if res.Dropped != 1 {
		t.Errorf("Dropped = %d, want 1", res.Dropped)
	}
	if len(res.Sessions) != 5 {
		t.Fatalf("len(Sessions) = %d, want 5", len(res.Sessions))
	}
	for i := 1; i < len(res.Sessions); i++ {
		a, b := res.Sessions[i-1], res.Sessions[i]
		if a.Start.After(b.Start) {
			t.Errorf("Sessions not sorted by Start: %s (%v) before %s (%v)", a.Ref(), a.Start, b.Ref(), b.Start)
		}
	}
}

func TestFinalize_CommitLinks(t *testing.T) {
	const shaExact = "sha-exact"
	const shaContent = "sha-content"
	const shaHeuristic = "sha-heuristic"
	const shaNone = "sha-none"

	pr := testkit.PR("acme/shop", 3).
		Commit(shaExact, "14:00").
		Commit(shaContent, "15:00").
		Commit(shaHeuristic, "16:00").
		Commit(shaNone, "23:00").
		Build()

	sExact := testkit.Session(model.HarnessClaudeCode, "exact2").
		Add(model.Event{Kind: model.KindGitHead, GitHead: &model.GitHead{SHA: shaExact, Trigger: "commit"}}).
		Build()
	sExact.Branch = "unrelated" // keep it out of the heuristic bucket for other commits

	sContentSess := testkit.Session(model.HarnessClaudeCode, "content2").Prompt("hi").Build()
	sContentSess.Branch = "unrelated"

	sHeuristicSess := testkit.Session(model.HarnessClaudeCode, "heur2").At("16:00").Prompt("hi").Build()
	// Branch stays the testkit default "feat/test" == pr.HeadRef.

	cands := []*model.Session{sExact, sContentSess, sHeuristicSess}

	attr := &model.Attribution{Files: []model.FileAttribution{{
		Path: "x.go",
		Lines: []model.LineAttribution{{
			Line: 1, Label: model.LabelAgent,
			Source: &model.EventRef{Session: sContentSess.Ref(), Event: "e1"},
		}},
	}}}
	commitSessions := map[string][]string{shaContent: {sContentSess.Ref()}}

	res := Finalize(pr, cands, attr, commitSessions, 0)

	if len(res.Links) != 4 {
		t.Fatalf("len(Links) = %d, want 4", len(res.Links))
	}

	byMethod := map[string]model.CommitLink{}
	for _, l := range res.Links {
		byMethod[l.SHA] = l
	}

	exact := byMethod[shaExact]
	if exact.Method != model.LinkSHAExact || exact.Confidence != "exact" {
		t.Errorf("exact link = %+v, want method sha_exact confidence exact", exact)
	}
	if len(exact.Sessions) != 1 || exact.Sessions[0] != sExact.Ref() {
		t.Errorf("exact link sessions = %v, want [%s]", exact.Sessions, sExact.Ref())
	}

	content := byMethod[shaContent]
	if content.Method != model.LinkContent || content.Confidence != "inferred" {
		t.Errorf("content link = %+v, want method content confidence inferred", content)
	}
	if len(content.Sessions) != 1 || content.Sessions[0] != sContentSess.Ref() {
		t.Errorf("content link sessions = %v, want [%s]", content.Sessions, sContentSess.Ref())
	}

	heuristic := byMethod[shaHeuristic]
	if heuristic.Method != model.LinkHeuristic || heuristic.Confidence != "inferred" {
		t.Errorf("heuristic link = %+v, want method heuristic confidence inferred", heuristic)
	}
	if len(heuristic.Sessions) != 1 || heuristic.Sessions[0] != sHeuristicSess.Ref() {
		t.Errorf("heuristic link sessions = %v, want [%s]", heuristic.Sessions, sHeuristicSess.Ref())
	}

	none := byMethod[shaNone]
	if none.Method != model.LinkNone || none.Confidence != "unknown" {
		t.Errorf("none link = %+v, want method none confidence unknown", none)
	}
	if len(none.Sessions) != 0 {
		t.Errorf("none link sessions = %v, want empty", none.Sessions)
	}

	if len(res.Unattributed) != 1 || res.Unattributed[0] != shaNone {
		t.Errorf("Unattributed = %v, want [%s]", res.Unattributed, shaNone)
	}

	// PR order is preserved.
	wantOrder := []string{shaExact, shaContent, shaHeuristic, shaNone}
	for i, sha := range wantOrder {
		if res.Links[i].SHA != sha {
			t.Errorf("Links[%d].SHA = %q, want %q", i, res.Links[i].SHA, sha)
		}
	}
}
