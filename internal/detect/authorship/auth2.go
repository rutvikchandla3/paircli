package authorship

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() {
	engine.Register(auth2{})
}

// auth2 is the AUTH-2 detector: session-commit map and coverage.
type auth2 struct{}

func (auth2) ID() string { return "AUTH-2" }

// auth2Short returns s truncated to n characters (used for short session and
// commit ids in finding text; full ids stay in Data).
func auth2Short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// auth2LinkMethod looks up how ref was linked, defaulting to "unknown" when
// links is nil or ref is absent (see docs/plan/CONTRACTS.md Context.SessionLinks).
func auth2LinkMethod(links map[string]model.LinkMethod, ref string) string {
	if links == nil {
		return "unknown"
	}
	m, ok := links[ref]
	if !ok {
		return "unknown"
	}
	return string(m)
}

// auth2LinesForSession counts attribution lines whose Source.Session is ref.
func auth2LinesForSession(attr *model.Attribution, ref string) int {
	n := 0
	for _, f := range attr.Files {
		for _, l := range f.Lines {
			if l.Source != nil && l.Source.Session == ref {
				n++
			}
		}
	}
	return n
}

// auth2CommitsForSession returns, in PR commit order, the SHAs of commits
// links this session's ref to.
func auth2CommitsForSession(links []model.CommitLink, ref string) []string {
	var out []string
	for _, l := range links {
		for _, s := range l.Sessions {
			if s == ref {
				out = append(out, l.SHA)
				break
			}
		}
	}
	return out
}

// auth2HarnessCounts formats "{n} {harness}" parts, most frequent harness
// first, ties broken alphabetically for determinism.
func auth2HarnessCounts(sessions []*model.Session) string {
	counts := map[model.Harness]int{}
	for _, s := range sessions {
		counts[s.Harness]++
	}
	type hc struct {
		h model.Harness
		n int
	}
	list := make([]hc, 0, len(counts))
	for h, n := range counts {
		list = append(list, hc{h, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].h < list[j].h
	})
	parts := make([]string, len(list))
	for i, x := range list {
		parts[i] = fmt.Sprintf("%d %s", x.n, x.h)
	}
	return strings.Join(parts, ", ")
}

func (auth2) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions("AUTH-2")
	}

	sig := engine.NewSignal("AUTH-2")
	attr := c.Attribution

	sessions := append([]*model.Session(nil), c.Sessions...)
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Start.Before(sessions[j].Start) })

	var unattributed []string
	for _, l := range c.Links {
		if len(l.Sessions) == 0 {
			unattributed = append(unattributed, l.SHA)
		}
	}

	pct := int(math.Round(attr.Ratio() * 100))
	summary := fmt.Sprintf("%d sessions (%s) explain %d%% of changed lines.", len(sessions), auth2HarnessCounts(sessions), pct)
	if k := len(unattributed); k > 0 {
		extra := fmt.Sprintf(" %d commits have no session.", k)
		if len(summary)+len(extra) <= 160 {
			summary += extra
		}
	}
	sig.Summary = summary

	if attr.Total >= 10 && attr.Ratio() < 0.5 {
		sig.State = model.StateAlert
	} else {
		sig.State = model.StateInfo
	}

	findings := make([]model.Finding, 0, len(sessions)+len(unattributed))
	sessionsData := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		ref := s.Ref()
		lines := auth2LinesForSession(attr, ref)
		commits := auth2CommitsForSession(c.Links, ref)
		method := auth2LinkMethod(c.SessionLinks, ref)

		display := fmt.Sprintf("%s:%s", s.Harness, auth2Short(s.ID, 8))
		text := fmt.Sprintf("`%s` (%s) linked by %s — %d lines", display, s.Capture, method, lines)
		if len(commits) > 0 {
			short := make([]string, len(commits))
			for i, sha := range commits {
				short[i] = auth2Short(sha, 7)
			}
			text += ", commits " + strings.Join(short, ", ")
		}
		findings = append(findings, model.Finding{Summary: text, Severity: model.StateInfo})

		sessionsData = append(sessionsData, map[string]any{
			"ref":     ref,
			"link":    method,
			"capture": string(s.Capture),
			"lines":   lines,
			"commits": commits,
		})
	}
	for _, sha := range unattributed {
		findings = append(findings, model.Finding{
			Summary:  fmt.Sprintf("Commit %s has no matching session.", auth2Short(sha, 7)),
			Severity: model.StateAlert,
		})
	}
	sig.Findings = findings

	sig.Data = map[string]any{
		"sessions":     sessionsData,
		"unattributed": unattributed,
		"ratio":        attr.Ratio(),
	}
	return sig
}
