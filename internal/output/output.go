// Package output writes the structured signal folder described in
// docs/ARCHITECTURE.md's "Output" section:
//
//	.paircli/pr-<number>/
//	  report.md
//	  signals.json
//	  sessions/<harness>-<session_id>.json
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/session"
)

// Write creates .paircli/pr-<number>/ under root and populates it from
// report and sessions.
func Write(root string, report session.Report, sessions []session.Session) error {
	prDir := filepath.Join(root, ".paircli", fmt.Sprintf("pr-%d", report.PRNumber))
	sessionsDir := filepath.Join(prDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return err
	}

	if err := writeSignalsJSON(prDir, report); err != nil {
		return err
	}
	if err := writeSessions(sessionsDir, sessions); err != nil {
		return err
	}
	if err := writeReportMD(prDir, report); err != nil {
		return err
	}
	return nil
}

func writeSignalsJSON(prDir string, report session.Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(prDir, "signals.json"), data, 0o644)
}

func writeSessions(sessionsDir string, sessions []session.Session) error {
	for _, s := range sessions {
		name := fmt.Sprintf("%s-%s.json", s.Harness, s.SessionID)
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sessionsDir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeReportMD renders a genuinely readable summary, not a JSON dump.
func writeReportMD(prDir string, r session.Report) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# paircli report: PR #%d (%s)\n\n", r.PRNumber, r.Repo)

	fmt.Fprintf(&b, "**Capture completeness:** %s\n\n", r.CaptureCompleteness)

	if len(r.UnattributedCommits) > 0 {
		fmt.Fprintf(&b, "> **%d unattributed commit(s)** — no session data could be linked. "+
			"This is the strongest \"we have no signal here\" flag; treat this PR's process "+
			"signals below as incomplete.\n>\n", len(r.UnattributedCommits))
		for _, sha := range r.UnattributedCommits {
			fmt.Fprintf(&b, "> - `%s`\n", shortSHA(sha))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Harnesses used\n\n")
	if len(r.HarnessesUsed) == 0 {
		b.WriteString("_none detected_\n\n")
	} else {
		for _, h := range r.HarnessesUsed {
			count := r.SessionCountPerHarness[h]
			fmt.Fprintf(&b, "- **%s** — %d session(s)\n", h, count)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Session summary\n\n")
	fmt.Fprintf(&b, "- Total sessions: %d\n", r.SessionCount)
	fmt.Fprintf(&b, "- Total correction signals: %d\n", r.CorrectionSignalsTotal)
	if r.WallClockStart != nil && r.WallClockEnd != nil {
		fmt.Fprintf(&b, "- Wall-clock span: %s to %s\n",
			r.WallClockStart.Format(time.RFC3339), r.WallClockEnd.Format(time.RFC3339))
	}
	b.WriteString("\n")

	if len(r.Sessions) > 0 {
		b.WriteString("## Sessions\n\n")
		b.WriteString("| Harness | Session ID | Confidence | Capture | Iterations | Corrections | Models |\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		sorted := append([]session.Session{}, r.Sessions...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].StartTime.Before(sorted[j].StartTime) })
		for _, s := range sorted {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %d | %s |\n",
				s.Harness, s.SessionID, s.Confidence, s.CapturePath,
				s.IterationCount, s.CorrectionSignals, strings.Join(s.Models, ", "))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Commits\n\n")
	b.WriteString("| SHA | Method | Confidence | Session |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, c := range r.Commits {
		method := c.Method
		if method == "" {
			method = "-"
		}
		ref := c.SessionRef
		if ref == "" {
			ref = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", shortSHA(c.SHA), method, c.Confidence, ref)
	}
	b.WriteString("\n")

	b.WriteString("See `signals.json` for the full structured signal set, and `sessions/` for per-session detail.\n")

	return os.WriteFile(filepath.Join(prDir, "report.md"), []byte(b.String()), 0o644)
}

func shortSHA(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}
