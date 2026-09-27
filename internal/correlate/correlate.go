// Package correlate implements the PR-to-session correlation algorithm from
// docs/ARCHITECTURE.md: exact SHA match first, then a repo+branch+time-window
// heuristic fallback for anything unmatched, with a threshold below which
// commits are left unattributed. This algorithm has no analog in
// agent-beacon (confirmed: it has no historical PR-correlation logic at
// all) — it's the novel part of paircli.
package correlate

import (
	"time"

	"github.com/rutvikchandla3/paircli/internal/session"
)

// DefaultTimeWindow is the +/- window (N in ARCHITECTURE.md) around a
// commit's timestamp that a session's [start,end] must overlap to be a
// heuristic candidate. Configurable via Options.TimeWindow.
const DefaultTimeWindow = 2 * time.Hour

// ScoreThreshold is the minimum heuristic score (see scoreCandidate) above
// which a session is attached to a commit with confidence "inferred".
// Literal choice, not specified numerically in ARCHITECTURE.md: repo match
// is required (weight folded into eligibility, not the score itself),
// branch match is the strongest optional signal, time overlap is
// corroborating. Threshold picked so branch-match-alone or
// strong-time-overlap-alone both qualify, but bare repo match alone does not.
const ScoreThreshold = 0.5

const (
	weightBranchMatch = 0.6
	weightTimeOverlap = 0.5
)

// Commit is the subset of a PR commit's fields the correlator needs, as
// returned by `gh pr view <n> --json commits`.
type Commit struct {
	SHA       string
	Timestamp time.Time
	Branch    string // PR's headRefName, applied to every commit in the PR
}

// PR is the subset of `gh pr view --json commits,files,createdAt,headRefName`
// the correlator needs.
type PR struct {
	Repo        string // "owner/repo"
	HeadRefName string
	Commits     []Commit
}

// Options configures the heuristic fallback.
type Options struct {
	TimeWindow time.Duration // defaults to DefaultTimeWindow if zero
}

// Result is the correlator's output: per-commit attribution plus the
// leftover unattributed list, per SIGNALS.md section 3.
type Result struct {
	Attributions        []session.CommitAttribution
	UnattributedCommits []string
}

// Correlate runs SHA-exact match, then the heuristic fallback, against pr's
// commits using the given sessions (already normalized, from any harness or
// capture path).
func Correlate(pr PR, sessions []session.Session, opts Options) Result {
	window := opts.TimeWindow
	if window == 0 {
		window = DefaultTimeWindow
	}

	res := Result{}
	matchedCommit := map[string]bool{}

	// Step 1: exact SHA match.
	shaIndex := map[string]session.Session{}
	for _, s := range sessions {
		if s.CommitSHA != "" {
			shaIndex[s.CommitSHA] = s
		}
	}
	for _, c := range pr.Commits {
		if s, ok := shaIndex[c.SHA]; ok {
			res.Attributions = append(res.Attributions, session.CommitAttribution{
				SHA:        c.SHA,
				SessionRef: sessionRef(s),
				Method:     "sha_exact",
				Confidence: session.ConfidenceExact,
			})
			matchedCommit[c.SHA] = true
		}
	}

	// Step 2: heuristic fallback for unmatched commits, against sessions
	// that didn't already win an exact match (a session can still be reused
	// heuristically for other commits).
	for _, c := range pr.Commits {
		if matchedCommit[c.SHA] {
			continue
		}
		best := session.Session{}
		bestScore := -1.0
		found := false
		for _, s := range sessions {
			if !repoMatches(pr.Repo, s) {
				continue // required, not scored
			}
			score := scoreCandidate(c, s, pr.HeadRefName, window)
			if score > bestScore {
				bestScore = score
				best = s
				found = true
			}
		}
		if found && bestScore >= ScoreThreshold {
			res.Attributions = append(res.Attributions, session.CommitAttribution{
				SHA:        c.SHA,
				SessionRef: sessionRef(best),
				Method:     "heuristic",
				Confidence: session.ConfidenceInferred,
			})
			matchedCommit[c.SHA] = true
		}
	}

	// Step 3: anything left over is unattributed, always surfaced.
	for _, c := range pr.Commits {
		if !matchedCommit[c.SHA] {
			res.UnattributedCommits = append(res.UnattributedCommits, c.SHA)
			res.Attributions = append(res.Attributions, session.CommitAttribution{
				SHA:        c.SHA,
				Confidence: session.ConfidenceUnknown,
			})
		}
	}

	return res
}

// repoMatches implements the "required, not just scored" repo identity
// check from ARCHITECTURE.md step 2.
func repoMatches(prRepo string, s session.Session) bool {
	if prRepo == "" || s.RepoRemote == "" {
		// No repo identity recorded for this session: can't confirm, so it
		// isn't eligible for heuristic matching (avoids false positives
		// across unrelated repos).
		return false
	}
	return prRepo == s.RepoRemote
}

// scoreCandidate scores a session against a commit per ARCHITECTURE.md step
// 2: branch match is a strong optional signal, time-window overlap is
// corroborating; repo match is checked separately (required) before this is
// called.
func scoreCandidate(c Commit, s session.Session, headRef string, window time.Duration) float64 {
	score := 0.0
	if s.Branch != "" && (s.Branch == c.Branch || s.Branch == headRef) {
		score += weightBranchMatch
	}
	if timeOverlaps(c.Timestamp, s.StartTime, s.EndTime, window) {
		score += weightTimeOverlap
	}
	return score
}

func timeOverlaps(commitTime, sessionStart, sessionEnd time.Time, window time.Duration) bool {
	if sessionStart.IsZero() && sessionEnd.IsZero() {
		return false
	}
	windowStart := commitTime.Add(-window)
	windowEnd := commitTime.Add(window)
	// [sessionStart, sessionEnd] intersects [windowStart, windowEnd]
	return !sessionEnd.Before(windowStart) && !sessionStart.After(windowEnd)
}

func sessionRef(s session.Session) string {
	return s.Harness + "-" + s.SessionID
}
