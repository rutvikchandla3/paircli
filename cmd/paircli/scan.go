package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/claudecode"
	"github.com/rutvikchandla3/paircli/internal/codex"
	"github.com/rutvikchandla3/paircli/internal/correlate"
	"github.com/rutvikchandla3/paircli/internal/gitinfo"
	"github.com/rutvikchandla3/paircli/internal/output"
	"github.com/rutvikchandla3/paircli/internal/pi"
	"github.com/rutvikchandla3/paircli/internal/session"
)

// runScan implements `paircli scan <pr-number-or-url>`: fetches the PR via
// gh, gathers sessions from all harnesses (both capture paths), correlates,
// and writes the output folder under the current repo's .paircli/ dir.
func runScan(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: paircli scan <pr-number-or-url>")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	repo, prNumber, err := resolveRepoAndPR(cwd, args[0])
	if err != nil {
		return err
	}

	pr, err := fetchPR(repo, prNumber)
	if err != nil {
		return err
	}

	sessions, err := gatherSessions(repo)
	if err != nil {
		return err
	}

	result := correlate.Correlate(pr, sessions, correlate.Options{})

	report := buildReport(prNumber, repo, sessions, result)

	if err := output.Write(cwd, report, sessions); err != nil {
		return err
	}

	fmt.Printf("paircli: wrote .paircli/pr-%d/ (%d session(s), %d unattributed commit(s))\n",
		prNumber, report.SessionCount, len(report.UnattributedCommits))
	return nil
}

// resolveRepoAndPR accepts either a bare PR number (uses the current repo,
// resolved via gitinfo — no git binary needed) or a full GitHub PR URL.
func resolveRepoAndPR(cwd, arg string) (repo string, number int, err error) {
	urlRe := regexp.MustCompile(`github\.com/([^/]+/[^/]+)/pull/(\d+)`)
	if m := urlRe.FindStringSubmatch(arg); m != nil {
		n, convErr := strconv.Atoi(m[2])
		if convErr != nil {
			return "", 0, convErr
		}
		return m[1], n, nil
	}

	n, convErr := strconv.Atoi(arg)
	if convErr != nil {
		return "", 0, fmt.Errorf("could not parse %q as a PR number or GitHub PR URL", arg)
	}

	info, err := gitinfo.Resolve(cwd)
	if err != nil {
		return "", 0, fmt.Errorf("resolving current repo: %w", err)
	}
	if info.OwnerRepo == "" {
		return "", 0, fmt.Errorf("could not determine owner/repo from git remote; pass a full PR URL instead")
	}
	return info.OwnerRepo, n, nil
}

// ghCommit mirrors the fields we ask gh for via --json commits.
type ghCommit struct {
	OID           string `json:"oid"`
	CommittedDate string `json:"committedDate"`
}

type ghPRView struct {
	Commits     []ghCommit `json:"commits"`
	CreatedAt   string     `json:"createdAt"`
	HeadRefName string     `json:"headRefName"`
}

func fetchPR(repo string, number int) (correlate.PR, error) {
	cmd := exec.Command("gh", "pr", "view", strconv.Itoa(number),
		"--json", "commits,files,createdAt,headRefName", "-R", repo)
	out, err := cmd.Output()
	if err != nil {
		return correlate.PR{}, fmt.Errorf("gh pr view failed: %w", err)
	}

	var parsed ghPRView
	if err := json.Unmarshal(out, &parsed); err != nil {
		return correlate.PR{}, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	pr := correlate.PR{Repo: repo, HeadRefName: parsed.HeadRefName}
	for _, c := range parsed.Commits {
		ts, _ := time.Parse(time.RFC3339, c.CommittedDate)
		pr.Commits = append(pr.Commits, correlate.Commit{
			SHA:       c.OID,
			Timestamp: ts,
			Branch:    parsed.HeadRefName,
		})
	}
	return pr, nil
}

// gatherSessions runs Path B (and merges Path A event-log data, where
// present) for every harness. Codex and Pi are stubbed and contribute no
// sessions yet (see internal/codex, internal/pi).
func gatherSessions(repo string) ([]session.Session, error) {
	var sessions []session.Session

	ccSessions, err := claudecode.ScanPathB(claudecode.DefaultProjectsDir())
	if err != nil {
		return nil, fmt.Errorf("scanning claude-code transcripts: %w", err)
	}
	ccSessions = mergeClaudeCodeHookEvents(ccSessions)
	resolveRepoRemotes(ccSessions)
	sessions = append(sessions, ccSessions...)

	// TODO(codex): wire in once ScanPathB is implemented.
	codexSessions, _ := codex.ScanPathB("")
	sessions = append(sessions, codexSessions...)

	// TODO(pi): wire in once ScanPathB is implemented.
	piSessions, _ := pi.ScanPathB("")
	sessions = append(sessions, piSessions...)

	return sessions, nil
}

// resolveRepoRemotes fills in RepoRemote for each session by resolving its
// recorded cwd through gitinfo, so the correlator's required repo-match
// check has something to compare against.
func resolveRepoRemotes(sessions []session.Session) {
	cache := map[string]string{}
	for i := range sessions {
		cwd := sessions[i].RepoPath
		if cwd == "" {
			continue
		}
		if remote, ok := cache[cwd]; ok {
			sessions[i].RepoRemote = remote
			continue
		}
		info, err := gitinfo.Resolve(cwd)
		remote := ""
		if err == nil {
			remote = info.OwnerRepo
		}
		cache[cwd] = remote
		sessions[i].RepoRemote = remote
	}
}

// mergeClaudeCodeHookEvents reads our own Path-A event log
// (~/.paircli/events/claude-code/*.jsonl) and, for any session that also
// appears there, attaches the exact commit SHA our hook captured and
// upgrades that session's confidence/capture path accordingly.
func mergeClaudeCodeHookEvents(sessions []session.Session) []session.Session {
	dir, err := claudecode.EventsDir()
	if err != nil {
		return sessions
	}
	shaBySession := readHookSHAs(dir)
	if len(shaBySession) == 0 {
		return sessions
	}
	for i := range sessions {
		if sha, ok := shaBySession[sessions[i].SessionID]; ok && sha != "" {
			sessions[i].CommitSHA = sha
			sessions[i].CapturePath = session.CapturePathA
			sessions[i].Confidence = session.ConfidenceExact
		}
	}
	return sessions
}

func readHookSHAs(dir string) map[string]string {
	result := map[string]string{}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec struct {
				SessionID string `json:"session_id"`
				CommitSHA string `json:"commit_sha"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec.SessionID != "" && rec.CommitSHA != "" {
				result[rec.SessionID] = rec.CommitSHA
			}
		}
	}
	return result
}

func buildReport(prNumber int, repo string, sessions []session.Session, result correlate.Result) session.Report {
	harnessSet := map[string]bool{}
	perHarness := map[string]int{}
	correctionsTotal := 0
	var wallStart, wallEnd *time.Time

	for _, s := range sessions {
		harnessSet[s.Harness] = true
		perHarness[s.Harness]++
		correctionsTotal += s.CorrectionSignals

		if !s.StartTime.IsZero() {
			if wallStart == nil || s.StartTime.Before(*wallStart) {
				t := s.StartTime
				wallStart = &t
			}
		}
		if !s.EndTime.IsZero() {
			if wallEnd == nil || s.EndTime.After(*wallEnd) {
				t := s.EndTime
				wallEnd = &t
			}
		}
	}

	harnesses := make([]string, 0, len(harnessSet))
	for h := range harnessSet {
		harnesses = append(harnesses, h)
	}

	completeness := captureCompleteness(sessions, result)

	return session.Report{
		PRNumber:               prNumber,
		Repo:                   repo,
		HarnessesUsed:          harnesses,
		Sessions:               sessions,
		SessionCountPerHarness: perHarness,
		SessionCount:           len(sessions),
		WallClockStart:         wallStart,
		WallClockEnd:           wallEnd,
		Commits:                result.Attributions,
		UnattributedCommits:    result.UnattributedCommits,
		CorrectionSignalsTotal: correctionsTotal,
		CaptureCompleteness:    completeness,
	}
}

// captureCompleteness implements SIGNALS.md's hooked/reconstructed/partial/
// none tagging at the report level, based on the mix of capture paths
// actually present across this PR's sessions.
func captureCompleteness(sessions []session.Session, result correlate.Result) string {
	if len(sessions) == 0 {
		return "none"
	}
	hasA, hasB := false, false
	for _, s := range sessions {
		switch s.CapturePath {
		case session.CapturePathA:
			hasA = true
		case session.CapturePathB:
			hasB = true
		}
	}
	switch {
	case hasA && !hasB:
		return "hooked"
	case hasA && hasB:
		return "partial"
	case hasB:
		return "reconstructed"
	default:
		return "none"
	}
}
