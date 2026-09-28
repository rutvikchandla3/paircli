// Package render turns pipeline results into the files paircli writes:
// signals.json, report.md, comment.md, authorship.json and one JSON file per
// linked session. Output is deterministic: identical input produces
// byte-identical files.
package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

// Options configures Write and CommentMarkdown.
type Options struct {
	OutDir          string // .paircli/pr-<n>
	IncludePrompts  bool
	MaxCommentLines int
}

// CommentMarker is the first line of comment.md; internal/scan upserts the PR
// comment that carries it.
const CommentMarker = "<!-- paircli -->"

// DefaultCommentLines is the comment bullet budget when Options leaves it unset.
const DefaultCommentLines = 5

// Glyphs used in the report and the comment.
const (
	glyphRecorded = "■"
	glyphDerived  = "□"
	glyphInferred = "◇"

	commentAlertRecorded = "▲"
	commentAlertInferred = "◆"
	commentFiller        = "●"
)

// questionHeading maps each review question to its report heading.
var questionHeading = map[model.ReviewQuestion]string{
	model.QIntent:       "What was actually asked for?",
	model.QAuthorship:   "Who wrote which lines?",
	model.QVerification: "Was it verified, and does the proof still hold?",
	model.QDecisions:    "What was tried, rejected or corrected?",
	model.QFriction:     "Where did it struggle?",
	model.QExposure:     "What else did this session touch?",
	model.QOversight:    "How closely was a human watching?",
	model.QConsistency:  "Does the PR's story match the session's?",
}

// maxSignalFindings is how many findings one signal renders before the
// remainder is summarized.
const maxSignalFindings = 15

// maxAlertFindings is how many findings an alert renders in the Alerts section.
const maxAlertFindings = 3

// Write writes every report file into opts.OutDir. Each file ends with a
// newline. Session files left over from an earlier run are deleted first.
func Write(opts Options, rep *model.Report, attr *model.Attribution, sessions []*model.Session) error {
	if rep == nil {
		return fmt.Errorf("render: write: nil report")
	}
	sessDir := filepath.Join(opts.OutDir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		return fmt.Errorf("render: write: %w", err)
	}
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return fmt.Errorf("render: write: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := os.Remove(filepath.Join(sessDir, e.Name())); err != nil {
			return fmt.Errorf("render: write: %w", err)
		}
	}
	if attr == nil {
		attr = &model.Attribution{}
	}
	files := []struct {
		name  string
		value any
	}{
		{"signals.json", rep},
		{"authorship.json", attr},
	}
	for _, f := range files {
		if err := writeJSON(filepath.Join(opts.OutDir, f.name), f.value); err != nil {
			return err
		}
	}
	if err := writeText(filepath.Join(opts.OutDir, "report.md"), ReportMarkdown(rep)); err != nil {
		return err
	}
	if err := writeText(filepath.Join(opts.OutDir, "comment.md"), CommentMarkdown(rep, opts)); err != nil {
		return err
	}
	for _, s := range sessions {
		if s == nil {
			continue
		}
		if err := writeJSON(filepath.Join(sessDir, sessionFileName(s)), s); err != nil {
			return err
		}
	}
	return nil
}

// sessionFileName is "<harness>-<id>.json" with unsafe id characters replaced.
func sessionFileName(s *model.Session) string {
	return string(s.Harness) + "-" + safeName(s.ID) + ".json"
}

// safeName replaces every character outside [A-Za-z0-9._-] with "_".
func safeName(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// writeJSON writes v indented with two spaces and a trailing newline.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("render: write %s: %w", path, err)
	}
	return writeText(path, string(data)+"\n")
}

// writeText writes s, ensuring exactly one trailing newline.
func writeText(path, s string) error {
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		return fmt.Errorf("render: write %s: %w", path, err)
	}
	return nil
}

// ReportMarkdown renders the human report.
func ReportMarkdown(rep *model.Report) string {
	if rep == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# paircli report · PR #%d: %s\n\n", rep.PR.Number, text(rep.PR.Title))
	fmt.Fprintf(&b, "**%s** · `%s` → `%s` · %s · %s · +%d −%d\n\n",
		text(rep.PR.Repo), text(rep.PR.HeadRef), text(rep.PR.BaseRef),
		engine.Plural(rep.PR.Commits, "commit", "commits"),
		engine.Plural(rep.PR.Files, "file", "files"),
		rep.PR.Additions, rep.PR.Deletions)
	fmt.Fprintf(&b, "**Coverage:** %s\n\n", coveragePhrase(rep))

	if alerts := alertSignals(rep.Signals); len(alerts) > 0 {
		b.WriteString("## Alerts\n\n")
		for _, sig := range alerts {
			fmt.Fprintf(&b, "- **%s %s:** %s\n", text(sig.ID), text(sig.Title), text(sig.Summary))
			for i, f := range sig.Findings {
				if i == maxAlertFindings {
					break
				}
				fmt.Fprintf(&b, "  %s\n", findingLine(f, sig))
			}
		}
		b.WriteString("\n")
	}

	for _, q := range model.ReviewQuestions {
		sigs := questionSignals(rep.Signals, q)
		if len(sigs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", questionHeading[q])
		for _, sig := range sigs {
			b.WriteString(signalBlock(sig))
		}
	}

	if unknown := unknownSignals(rep.Signals); len(unknown) > 0 {
		b.WriteString("## Not available\n\n")
		for _, sig := range unknown {
			fmt.Fprintf(&b, "- **%s %s:** %s\n", text(sig.ID), text(sig.Title), text(sig.Summary))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Sessions\n\n")
	b.WriteString("| Session | Link | Capture | Start | End | Models | PR lines |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, s := range rep.Sessions {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %d |\n",
			shortRef(s.Ref), s.Link, s.Capture,
			rfc3339(s.Start), rfc3339(s.End), dashJoin(s.Models), s.LinesAuthored)
	}
	b.WriteString("\n## Commits\n\n")
	b.WriteString("| Commit | Sessions | Method | Confidence |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, c := range rep.Commits {
		refs := make([]string, 0, len(c.Sessions))
		for _, r := range c.Sessions {
			refs = append(refs, shortRef(r))
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			shortSHA(c.SHA), dashJoin(refs), c.Method, text(c.Confidence))
	}

	fmt.Fprintf(&b, "\nGlyphs: %s recorded · %s derived · %s inferred. Generated by %s at %s.\n",
		glyphRecorded, glyphDerived, glyphInferred, text(rep.Tool), rfc3339(rep.GeneratedAt))
	return tidy(b.String())
}

// signalBlock renders one "###" signal section, including its findings and
// support line.
func signalBlock(sig model.Signal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s %s · %s %s · %s\n\n", text(sig.ID), text(sig.Title),
		provenanceGlyph(sig.Provenance), sig.Provenance, sig.State)
	b.WriteString(text(sig.Summary) + "\n")
	for i, f := range sig.Findings {
		if i == maxSignalFindings {
			fmt.Fprintf(&b, "\n- … and %d more in signals.json\n", len(sig.Findings)-maxSignalFindings)
			break
		}
		if i == 0 {
			b.WriteString("\n")
		}
		b.WriteString(findingLine(f, sig) + "\n")
	}
	if len(sig.Support) > 0 {
		fmt.Fprintf(&b, "\nSupport: %s.\n", supportLine(sig.Support))
	}
	b.WriteString("\n")
	return b.String()
}

// findingLine renders one finding bullet.
func findingLine(f model.Finding, sig model.Signal) string {
	line := "- " + text(f.Summary)
	if a := anchorsText(f.Anchors); a != "" {
		line += " · " + a
	}
	if len(f.Evidence) > 0 {
		ev := f.Evidence[0]
		line += fmt.Sprintf(" · `%s` %s · %s", shortRef(ev.Session), text(ev.Event), clock(ev.TS))
	}
	if effectiveProvenance(f, sig) == model.Inferred {
		line += " ◇"
	}
	return line
}

// effectiveProvenance is the finding's own provenance, or the signal's when unset.
func effectiveProvenance(f model.Finding, sig model.Signal) model.Provenance {
	if f.Provenance != "" {
		return f.Provenance
	}
	return sig.Provenance
}

// anchorsText renders anchors as "`file` lines 40-45", joined by ", ".
func anchorsText(anchors []model.Anchor) string {
	var parts []string
	for _, a := range anchors {
		s := "`" + text(a.File) + "`"
		if a.Lines != "" {
			s += " lines " + text(a.Lines)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// supportLine renders support as "claude-code full, codex partial".
func supportLine(support map[string]string) string {
	keys := make([]string, 0, len(support))
	for k := range support {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, text(k)+" "+text(support[k]))
	}
	return strings.Join(parts, ", ")
}

// provenanceGlyph is the report glyph for a provenance.
func provenanceGlyph(p model.Provenance) string {
	switch p {
	case model.Recorded:
		return glyphRecorded
	case model.Inferred:
		return glyphInferred
	default:
		return glyphDerived
	}
}

// coveragePhrase is the "**Coverage:**" body shared by the report header.
func coveragePhrase(rep *model.Report) string {
	parts := []string{sessionCountPhrase(rep)}
	if h := harnessPhrase(rep.Coverage.Harnesses); h != "" {
		parts[0] += " " + h
	}
	parts = append(parts, "capture: "+rep.Coverage.Capture)
	parts = append(parts, engine.Pct(rep.Coverage.LinesExplained, rep.Coverage.LinesTotal)+" of changed lines explained")
	if n := len(rep.Coverage.UnattributedCommits); n > 0 {
		parts = append(parts, engine.Plural(n, "unattributed commit", "unattributed commits"))
	}
	return strings.Join(parts, " · ")
}

// commentCoveragePhrase is the comment header's shorter coverage phrase.
func commentCoveragePhrase(rep *model.Report) string {
	head := sessionCountPhrase(rep)
	if h := harnessPhrase(rep.Coverage.Harnesses); h != "" {
		head += " " + h
	}
	parts := []string{
		head,
		engine.Pct(rep.Coverage.LinesExplained, rep.Coverage.LinesTotal) + " of changed lines explained",
		"capture: " + rep.Coverage.Capture,
	}
	return strings.Join(parts, " · ")
}

// sessionCountPhrase is "3 sessions" or "1 session".
func sessionCountPhrase(rep *model.Report) string {
	return engine.Plural(rep.Coverage.Sessions, "session", "sessions")
}

// harnessPhrase is "(2 claude-code, 1 codex)", or "" when no harness is known.
func harnessPhrase(counts map[model.Harness]int) string {
	keys := make([]string, 0, len(counts))
	for h, n := range counts {
		if n > 0 {
			keys = append(keys, string(h))
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[model.Harness(k)], k))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// alertSignals returns alert signals, P0 first then catalog order.
func alertSignals(sigs []model.Signal) []model.Signal {
	var out []model.Signal
	for _, s := range sigs {
		if s.State == model.StateAlert {
			out = append(out, s)
		}
	}
	sortByPriority(out)
	return out
}

// questionSignals returns the non-unknown signals of one question, in input order.
func questionSignals(sigs []model.Signal, q model.ReviewQuestion) []model.Signal {
	var out []model.Signal
	for _, s := range sigs {
		if s.Question == q && s.State != model.StateUnknown {
			out = append(out, s)
		}
	}
	return out
}

// unknownSignals returns every signal with no data, in input order.
func unknownSignals(sigs []model.Signal) []model.Signal {
	var out []model.Signal
	for _, s := range sigs {
		if s.State == model.StateUnknown {
			out = append(out, s)
		}
	}
	return out
}

// sortByPriority orders signals by priority (P0 first), then catalog order.
func sortByPriority(sigs []model.Signal) {
	order := catalogOrder()
	rank := func(p model.Priority) int {
		switch p {
		case model.P0:
			return 0
		case model.P1:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(sigs, func(i, j int) bool {
		ri, rj := rank(sigs[i].Priority), rank(sigs[j].Priority)
		if ri != rj {
			return ri < rj
		}
		oi, iok := order[sigs[i].ID]
		oj, jok := order[sigs[j].ID]
		if !iok {
			oi = len(order) + 1
		}
		if !jok {
			oj = len(order) + 1
		}
		return oi < oj
	})
}

// catalogOrder maps signal ID to its catalog index.
func catalogOrder() map[string]int {
	metas := engine.Catalog()
	m := make(map[string]int, len(metas))
	for i, meta := range metas {
		m[meta.ID] = i
	}
	return m
}

// CommentMarkdown renders the PR comment body. It uses signal summaries only:
// never finding text, prompt text or evidence.
func CommentMarkdown(rep *model.Report, opts Options) string {
	if rep == nil {
		return CommentMarker + "\n"
	}
	footer := fmt.Sprintf("<sub>Full report: `%s`. Signals describe the change, not the author.</sub>",
		filepath.Join(opts.OutDir, "report.md"))
	var b strings.Builder
	b.WriteString(CommentMarker + "\n")
	if rep.Coverage.Sessions == 0 {
		b.WriteString("\nNo captured AI coding sessions matched this PR.\n\n")
		b.WriteString(footer + "\n")
		return tidy(b.String())
	}
	fmt.Fprintf(&b, "\n**paircli** · %s\n\n", commentCoveragePhrase(rep))
	for _, line := range commentBullets(rep, opts) {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + footer + "\n")
	return tidy(b.String())
}

// commentBullets picks the comment's bullets within the line budget.
func commentBullets(rep *model.Report, opts Options) []string {
	budget := opts.MaxCommentLines
	if budget <= 0 {
		budget = DefaultCommentLines
	}
	var out []string
	for _, sig := range alertSignals(rep.Signals) {
		if len(out) == budget {
			break
		}
		glyph := commentAlertRecorded
		if sig.Provenance == model.Inferred {
			glyph = commentAlertInferred
		}
		out = append(out, fmt.Sprintf("- %s **%s:** %s `%s`",
			glyph, text(sig.Title), text(sig.Summary), text(sig.ID)))
	}
	type filler struct {
		id    string
		label string
	}
	fillers := []filler{
		{"FRI-1", "Start here"},
		{"OVS-1", "Autonomy"},
	}
	if opts.IncludePrompts {
		fillers = append(fillers, filler{"INT-1", "Ask"})
	}
	for _, f := range fillers {
		if len(out) == budget {
			break
		}
		sig, ok := findSignal(rep.Signals, f.id)
		if !ok || sig.State == model.StateUnknown {
			continue
		}
		if f.id == "FRI-1" && len(sig.Findings) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("- %s **%s:** %s `%s`",
			commentFiller, f.label, text(sig.Summary), text(sig.ID)))
	}
	if len(out) == 0 {
		out = append(out, "- No alerts.")
	}
	return out
}

// findSignal returns the signal with the given ID.
func findSignal(sigs []model.Signal, id string) (model.Signal, bool) {
	for _, s := range sigs {
		if s.ID == id {
			return s, true
		}
	}
	return model.Signal{}, false
}

// shortRef shortens the session id inside a "<harness>:<id>" ref to 8 characters.
func shortRef(ref string) string {
	i := strings.Index(ref, ":")
	if i < 0 {
		return text(short(ref, 8))
	}
	return text(ref[:i+1] + short(ref[i+1:], 8))
}

// shortSHA shortens a commit SHA to 7 characters.
func shortSHA(sha string) string { return text(short(sha, 7)) }

// short cuts s to at most n runes.
func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// clock formats t the way report text does.
func clock(t time.Time) string { return engine.Clock(t) }

// rfc3339 formats t in UTC.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// dashJoin joins values, or returns "—" when there are none.
func dashJoin(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	return strings.Join(values, ", ")
}

// text flattens free text to one line and passes it through redact.Text.
func text(s string) string {
	return redact.Text(oneLine(s))
}

// oneLine flattens newlines and tabs and collapses runs of spaces.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// tidy collapses blank-line runs and trims trailing whitespace.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t\r")
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n") + "\n"
}
