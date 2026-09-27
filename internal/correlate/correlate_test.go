package correlate

import (
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/session"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestCorrelate_ExactSHAMatch(t *testing.T) {
	commitTime := mustParse(t, "2026-09-27T12:00:00Z")
	pr := PR{
		Repo:        "rutvikchandla3/paircli",
		HeadRefName: "feature/x",
		Commits: []Commit{
			{SHA: "abc123", Timestamp: commitTime, Branch: "feature/x"},
		},
	}
	sessions := []session.Session{
		{
			Harness:    "claude-code",
			SessionID:  "s1",
			RepoRemote: "rutvikchandla3/paircli",
			CommitSHA:  "abc123",
		},
	}

	res := Correlate(pr, sessions, Options{})

	if len(res.UnattributedCommits) != 0 {
		t.Fatalf("expected no unattributed commits, got %v", res.UnattributedCommits)
	}
	if len(res.Attributions) != 1 {
		t.Fatalf("expected 1 attribution, got %d", len(res.Attributions))
	}
	got := res.Attributions[0]
	if got.Method != "sha_exact" || got.Confidence != session.ConfidenceExact {
		t.Errorf("got %+v, want method=sha_exact confidence=exact", got)
	}
	if got.SessionRef != "claude-code-s1" {
		t.Errorf("SessionRef = %q, want claude-code-s1", got.SessionRef)
	}
}

func TestCorrelate_HeuristicMatch(t *testing.T) {
	commitTime := mustParse(t, "2026-09-27T14:00:00Z")
	pr := PR{
		Repo:        "rutvikchandla3/paircli",
		HeadRefName: "feature/y",
		Commits: []Commit{
			{SHA: "def456", Timestamp: commitTime, Branch: "feature/y"},
		},
	}
	// No SHA recorded (e.g. Claude Code Path B), but same repo, same branch,
	// and session window overlaps the commit within the default 2h window.
	sessions := []session.Session{
		{
			Harness:    "claude-code",
			SessionID:  "s2",
			RepoRemote: "rutvikchandla3/paircli",
			Branch:     "feature/y",
			StartTime:  mustParse(t, "2026-09-27T13:00:00Z"),
			EndTime:    mustParse(t, "2026-09-27T13:50:00Z"),
		},
	}

	res := Correlate(pr, sessions, Options{})

	if len(res.UnattributedCommits) != 0 {
		t.Fatalf("expected no unattributed commits, got %v", res.UnattributedCommits)
	}
	got := res.Attributions[0]
	if got.Method != "heuristic" || got.Confidence != session.ConfidenceInferred {
		t.Errorf("got %+v, want method=heuristic confidence=inferred", got)
	}
	if got.SessionRef != "claude-code-s2" {
		t.Errorf("SessionRef = %q, want claude-code-s2", got.SessionRef)
	}
}

func TestCorrelate_Unattributed(t *testing.T) {
	commitTime := mustParse(t, "2026-09-27T14:00:00Z")
	pr := PR{
		Repo:        "rutvikchandla3/paircli",
		HeadRefName: "feature/z",
		Commits: []Commit{
			{SHA: "ghi789", Timestamp: commitTime, Branch: "feature/z"},
		},
	}
	sessions := []session.Session{
		// Different repo entirely: repo match is required, so this can
		// never be a candidate regardless of branch/time.
		{
			Harness:    "codex",
			SessionID:  "s3",
			RepoRemote: "someone/else",
			Branch:     "feature/z",
			StartTime:  commitTime.Add(-time.Minute),
			EndTime:    commitTime.Add(time.Minute),
		},
		// Same repo but no branch match and no time overlap: score 0, below
		// threshold.
		{
			Harness:    "claude-code",
			SessionID:  "s4",
			RepoRemote: "rutvikchandla3/paircli",
			Branch:     "unrelated-branch",
			StartTime:  commitTime.Add(-48 * time.Hour),
			EndTime:    commitTime.Add(-47 * time.Hour),
		},
	}

	res := Correlate(pr, sessions, Options{})

	if len(res.UnattributedCommits) != 1 || res.UnattributedCommits[0] != "ghi789" {
		t.Fatalf("expected ghi789 unattributed, got %v", res.UnattributedCommits)
	}
	if res.Attributions[0].Confidence != session.ConfidenceUnknown {
		t.Errorf("expected unknown confidence for unattributed commit, got %+v", res.Attributions[0])
	}
}
