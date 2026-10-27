package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rutvikchandla3/paircli/internal/deviceauth"
	"github.com/rutvikchandla3/paircli/internal/publish"
	"github.com/rutvikchandla3/paircli/internal/state"
)

// statusReport is the `paircli status --json` document. It never holds a
// credential, only whether one is usable.
type statusReport struct {
	SchemaVersion int                 `json:"schema_version"`
	CLIVersion    string              `json:"cli_version"`
	ServerURL     string              `json:"server_url"`
	LoggedIn      bool                `json:"logged_in"`
	Hooks         map[string]bool     `json:"hooks"`
	Setup         setupSummary        `json:"setup"`
	Publications  []state.Publication `json:"publications"`
}

// setupSummary is the part of setup.json `paircli status` reports.
type setupSummary struct {
	State     string     `json:"state"`
	Error     *string    `json:"error"`
	Log       string     `json:"log,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

var prRefRe = regexp.MustCompile(`^https?://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)/?$`)

func runStatus(args []string) error { return statusRun(args, os.Stdout) }

// statusRun implements `paircli status [--json] [--pr <url>]`. It reads local
// state, plus one best-effort refresh of publications still waiting on their
// analysis (see refreshPublications), so it stays quick enough for a
// SessionStart hook. It exits zero whatever it finds: being logged out, being
// offline or having a failed publication is data, not an error.
func statusRun(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "print the status as JSON")
	pr := fs.String("pr", "", "show only this pull request's publication (GitHub URL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	report, err := buildStatus(*pr)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return printStatus(w, report)
}

func buildStatus(prFilter string) (statusReport, error) {
	server := resolveServer("")
	report := statusReport{
		SchemaVersion: state.SchemaVersion,
		CLIVersion:    version,
		ServerURL:     server,
		LoggedIn:      loggedInFor(server),
		Hooks:         map[string]bool{},
		Setup:         currentSetup(),
		Publications:  []state.Publication{},
	}
	for _, name := range harnessOrder {
		report.Hooks[name] = harnessTable[name].Installed()
	}

	pubs, err := state.ListPublications()
	if err != nil {
		return report, err
	}
	if prFilter != "" {
		owner, repo, number, err := parsePRRef(prFilter)
		if err != nil {
			return report, err
		}
		var matching []state.Publication
		for _, p := range pubs {
			if strings.EqualFold(p.Owner, owner) && strings.EqualFold(p.Repo, repo) && p.Number == number {
				matching = append(matching, p)
			}
		}
		pubs = matching
	}
	report.Publications = append(report.Publications, refreshPublications(pubs)...)
	return report, nil
}

const (
	// refreshLimit caps how many publications one `paircli status` asks the
	// server about, most recently updated first.
	refreshLimit = 5
	// refreshTimeout bounds each of those requests.
	refreshTimeout = 3 * time.Second
)

// analysisReader is the part of the publication client status needs.
type analysisReader interface {
	AnalysisStatus(ctx context.Context, publicationID string) (*publish.Analysis, error)
}

// statusAnalysisReader returns a client for the stored credential, or false
// when there is none (not logged in, or a credential the client rejects). It is
// a variable so tests need no server.
var statusAnalysisReader = func() (analysisReader, bool) {
	server := resolveServer("")
	auth, ok := deviceauth.Resolve()
	if !ok || !loggedInFor(server) {
		return nil, false
	}
	if auth.ServerURL != "" {
		server = strings.TrimRight(auth.ServerURL, "/")
	}
	client, err := publish.NewClient(server, auth.AccessToken)
	if err != nil {
		return nil, false
	}
	return client, true
}

// refreshPublications asks the server about up to refreshLimit publications
// that still wait on their analysis, because only `publish --wait` follows the
// job and a publish without it (or past its timeout) leaves the record
// "processing" for good. A record is rewritten only when the server's answer
// changes it; any error (not logged in, offline, a slow or unknown job) keeps
// the stored record. The result is pubs with the refreshed records.
func refreshPublications(pubs []state.Publication) []state.Publication {
	var pending []int
	for i, p := range pubs {
		if (p.State == state.StateProcessing || p.State == state.StateUploaded) && p.PublicationID != "" {
			if pending = append(pending, i); len(pending) == refreshLimit {
				break
			}
		}
	}
	if len(pending) == 0 {
		return pubs
	}
	reader, ok := statusAnalysisReader()
	if !ok {
		return pubs
	}

	out := append([]state.Publication(nil), pubs...)
	var wg sync.WaitGroup
	for _, i := range pending {
		wg.Add(1)
		go func(p *state.Publication) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
			defer cancel()
			an, err := reader.AnalysisStatus(ctx, p.PublicationID)
			if err != nil {
				return
			}
			if updated := withAnalysis(*p, an); updated != *p {
				if state.WritePublication(updated) == nil {
					*p = updated
				}
			}
		}(&out[i])
	}
	wg.Wait()
	return out
}

// withAnalysis maps an analysis state onto a publication record the way
// `publish --wait` does: succeeded is ready, failed is failed with the error
// code, queued and running are processing.
func withAnalysis(p state.Publication, an *publish.Analysis) state.Publication {
	p.AnalysisJobID, p.AnalysisState = an.JobID, an.State
	switch an.State {
	case publish.AnalysisSucceeded:
		p.State, p.Error = state.StateReady, ""
	case publish.AnalysisFailed:
		code := "unknown"
		if an.ErrorCode != nil && *an.ErrorCode != "" {
			code = *an.ErrorCode
		}
		p.State, p.Error = state.StateFailed, fmt.Sprintf("the analysis failed (%s)", code)
	default:
		p.State = state.StateProcessing
	}
	return p
}

// loggedInFor reports whether a credential for server is usable, the same
// test `paircli publish` applies before it would start a device login.
func loggedInFor(server string) bool {
	a, ok := deviceauth.Resolve()
	return ok && (a.ServerURL == "" || strings.TrimRight(a.ServerURL, "/") == strings.TrimRight(server, "/"))
}

// currentSetup is setup.json, except that a "running" record whose lock no
// process holds belongs to a run that died, and is reported as failed.
func currentSetup() setupSummary {
	s := readSetupStatus()
	if s.State == setupRunning {
		if lock, err := state.TryLock("setup"); err == nil {
			lock.Release()
			msg := "the previous setup run did not finish"
			s.State, s.Error = setupFailed, &msg
		}
	}
	summary := setupSummary{State: s.State, Error: s.Error, Log: s.Log}
	if !s.UpdatedAt.IsZero() {
		summary.UpdatedAt = &s.UpdatedAt
	}
	return summary
}

func parsePRRef(ref string) (owner, repo string, number int, err error) {
	m := prRefRe.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", "", 0, fmt.Errorf("--pr wants a GitHub pull request URL, got %q", ref)
	}
	number, _ = strconv.Atoi(m[3])
	return m[1], m[2], number, nil
}

func printStatus(w io.Writer, r statusReport) error {
	var b strings.Builder
	fmt.Fprintf(&b, "paircli %s, server %s\n", r.CLIVersion, r.ServerURL)
	if r.LoggedIn {
		b.WriteString("login:  logged in\n")
	} else {
		b.WriteString("login:  not logged in (run paircli login)\n")
	}
	var hooks []string
	for _, name := range harnessOrder {
		hooks = append(hooks, fmt.Sprintf("%s %s", name, yesNo(r.Hooks[name])))
	}
	fmt.Fprintf(&b, "hooks:  %s\n", strings.Join(hooks, ", "))
	fmt.Fprintf(&b, "setup:  %s", r.Setup.State)
	if r.Setup.Error != nil {
		fmt.Fprintf(&b, " (%s)", *r.Setup.Error)
	}
	if r.Setup.State == setupFailed && r.Setup.Log != "" {
		fmt.Fprintf(&b, "\n        log: %s", r.Setup.Log)
	}
	b.WriteString("\n")

	if len(r.Publications) == 0 {
		b.WriteString("publications: none\n")
	} else {
		b.WriteString("publications:\n")
		for _, p := range r.Publications {
			fmt.Fprintf(&b, "  %s/%s#%d  %s", p.Owner, p.Repo, p.Number, p.State)
			if p.Step != "" {
				fmt.Fprintf(&b, " (%s", p.Step)
				if p.AnalysisState != "" {
					fmt.Fprintf(&b, ", analysis %s", p.AnalysisState)
				}
				b.WriteString(")")
			}
			fmt.Fprintf(&b, "  %s  %s\n", ago(p.UpdatedAt), p.ReviewURL)
			if p.Error != "" {
				fmt.Fprintf(&b, "      error: %s\n", p.Error)
			}
			if p.State == state.StateFailed && p.Log != "" {
				fmt.Fprintf(&b, "      log: %s\n", p.Log)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// ago is a coarse "5m ago" for a record's last update.
func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
