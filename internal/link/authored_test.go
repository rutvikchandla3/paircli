package link

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

const (
	subjectA = "fix(redact): keep JSON escapes whole"
	subjectB = "feat(link): link a session by the commits it made"
	shaAut   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaBut   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func authorPR() *model.PR {
	pr := testkit.PR("acme/shop", 7).Commit(shaAut, "14:00").Commit(shaBut, "14:10").Build()
	pr.Commits[0].Message = subjectA + "\n\nbody that is not matched"
	pr.Commits[1].Message = subjectB
	return pr
}

func TestAuthoredCommits(t *testing.T) {
	pr := authorPR()
	tests := []struct {
		name string
		s    *model.Session
		want []string
	}{
		{"message typed after -m",
			testkit.Session(model.HarnessClaudeCode, "m").
				Run(`git commit -q -m "`+subjectA+`"`, 0, "").Build(), []string{shaAut}},
		{"heredoc message",
			testkit.Session(model.HarnessClaudeCode, "h").
				Run("git commit -F - <<'EOF'\n"+subjectB+"\n\nbody\nEOF", 0, "").Build(), []string{shaBut}},
		{"message written to a file the commit reads",
			testkit.Session(model.HarnessClaudeCode, "f").
				Create("/tmp/msg.txt", subjectA, "", "body").
				Run("git commit -q -F /tmp/msg.txt", 0, "").Build(), []string{shaAut}},
		{"git -C dir commit, two commits",
			testkit.Session(model.HarnessClaudeCode, "c").
				Run(`git -C /repo commit -m "`+subjectA+`"`, 0, "").
				Run(`git -C /repo commit -m "`+subjectB+`"`, 0, "").Build(), []string{shaAut, shaBut}},
		{"whitespace in the typed message does not matter",
			testkit.Session(model.HarnessClaudeCode, "w").
				Run("git commit -m 'fix(redact):   keep JSON\n  escapes whole'", 0, "").Build(), []string{shaAut}},
		{"a headline GitHub cut short with an ellipsis",
			testkit.Session(model.HarnessClaudeCode, "t").
				Run("git commit -F - <<'EOF'\nfix(redact): keep JSON escapes whole, and refuse to publish scan JSON that redaction broke\nEOF", 0, "").Build(), []string{shaAut}},
		{"quoted without any git commit is not authorship",
			testkit.Session(model.HarnessClaudeCode, "q").
				Run(`echo "`+subjectA+`"`, 0, "").Build(), nil},
		{"read in a commit log is not authorship",
			testkit.Session(model.HarnessClaudeCode, "l").
				Run("git log --oneline", 0, "abc1234 "+subjectA).
				Run(`git commit -m "an unrelated change"`, 0, "").Build(), nil},
		{"a short subject proves nothing",
			testkit.Session(model.HarnessClaudeCode, "s").
				Run(`git commit -m "fix"`, 0, "").Build(), nil},
	}
	short := authorPR()
	short.Commits[0].Message = "fix"
	truncated := authorPR()
	truncated.Commits[0].Message = "fix(redact): keep JSON escapes whole, and refuse to publish scan JSON…\n\n… that redaction broke"

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := pr
			switch tc.name {
			case "a short subject proves nothing":
				p = short
			case "a headline GitHub cut short with an ellipsis":
				p = truncated
			}
			got := authoredCommits(p, tc.s)
			if len(got) != len(tc.want) {
				t.Fatalf("authoredCommits = %v, want %v", got, tc.want)
			}
			for _, sha := range tc.want {
				if !got[sha] {
					t.Errorf("missing %s in %v", sha, got)
				}
			}
		})
	}
}

// An agent started in one repo that commits into another must still be found.
func TestSelect_KeepsASessionThatAuthoredTheCommitInAnotherRepo(t *testing.T) {
	pr := authorPR()

	author := testkit.Session(model.HarnessClaudeCode, "author").
		Run(`git commit -m "`+subjectA+`"`, 0, "").Build()
	author.RepoRemote = "acme/elsewhere"
	bystander := testkit.Session(model.HarnessClaudeCode, "bystander").Prompt("hi").Build()
	bystander.RepoRemote = "acme/elsewhere"

	cands, dropped := Select(pr, []*model.Session{author, bystander}, selWindow())
	if len(cands) != 1 || cands[0].Ref() != author.Ref() || dropped != 1 {
		t.Fatalf("cands = %v dropped = %d, want only the author kept", cands, dropped)
	}
}

func TestFinalize_AuthoredPrecedenceAndCommitLinks(t *testing.T) {
	pr := authorPR()

	author := testkit.Session(model.HarnessClaudeCode, "author").
		Run(`git commit -m "`+subjectA+`"`, 0, "").Build()
	author.Branch = "other" // not the heuristic's doing

	exact := testkit.Session(model.HarnessClaudeCode, "exact").
		Run(`git commit -m "`+subjectA+`"`, 0, "").
		Add(model.Event{Kind: model.KindGitHead, GitHead: &model.GitHead{SHA: shaAut, Trigger: "commit"}}).Build()

	exact.Branch = "other"

	res := Finalize(pr, []*model.Session{author, exact}, nil, map[string][]string{}, 0)

	if got := res.SessionLinks[author.Ref()]; got != model.LinkAuthored {
		t.Errorf("author link = %q, want %q", got, model.LinkAuthored)
	}
	if got := res.SessionLinks[exact.Ref()]; got != model.LinkSHAExact {
		t.Errorf("exact link = %q, want exact to win over authored", got)
	}
	var first model.CommitLink
	for _, l := range res.Links {
		if l.SHA == shaAut {
			first = l
		}
	}
	// A hook's exact SHA outranks authorship for that commit; the second commit has none.
	if first.Method != model.LinkSHAExact {
		t.Errorf("commit %s method = %q", shaAut, first.Method)
	}
	if len(res.Unattributed) != 1 || res.Unattributed[0] != shaBut {
		t.Errorf("unattributed = %v, want [%s]", res.Unattributed, shaBut)
	}
}

func TestFinalize_AuthoredCommitLinkWithoutHooks(t *testing.T) {
	pr := authorPR()
	author := testkit.Session(model.HarnessClaudeCode, "author").
		Run(`git commit -m "`+subjectA+`"`, 0, "").Build()
	author.Branch = "other"

	res := Finalize(pr, []*model.Session{author}, nil, map[string][]string{}, 0)
	for _, l := range res.Links {
		if l.SHA == shaAut && (l.Method != model.LinkAuthored || l.Confidence != "inferred" || len(l.Sessions) != 1) {
			t.Errorf("commit link = %+v", l)
		}
	}
}

// Checking out main after the work leaves Branch at the last recorded name;
// the branch the session was on earlier must still count.
func TestFinalize_HeuristicUsesEveryBranchTheSessionWasOn(t *testing.T) {
	pr := testkit.PR("acme/shop", 7).Commit(shaAut, "14:00").Build()
	s := testkit.Session(model.HarnessClaudeCode, "later-on-main").Prompt("hi").Build()
	s.Branch = "main"
	s.Branches = []string{pr.HeadRef, "main"}

	res := Finalize(pr, []*model.Session{s}, nil, map[string][]string{}, 0)
	if got := res.SessionLinks[s.Ref()]; got != model.LinkHeuristic {
		t.Errorf("link = %q, want heuristic", got)
	}

	s.Branches = nil
	res = Finalize(pr, []*model.Session{s}, nil, map[string][]string{}, 0)
	if _, ok := res.SessionLinks[s.Ref()]; ok {
		t.Error("a session never on the PR branch must not link")
	}
}
