package render

import (
	"time"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// BuildInput is everything the pipeline hands to BuildReport.
type BuildInput struct {
	PR           *model.PR
	Sessions     []*model.Session
	Links        []model.CommitLink
	SessionLinks map[string]model.LinkMethod
	Attribution  *model.Attribution
	Signals      []model.Signal
	Dropped      int
	Unattributed []string
	Version      string
	Now          time.Time
}

// BuildReport assembles the model.Report that becomes signals.json.
func BuildReport(in BuildInput) *model.Report {
	rep := &model.Report{
		SchemaVersion: model.SchemaVersion,
		GeneratedAt:   in.Now.UTC(),
		Tool:          "paircli " + in.Version,
		PR:            prSummary(in.PR),
		Coverage:      buildCoverage(in),
		Sessions:      []model.SessionSummary{},
		Commits:       nonEmpty(in.Links),
		Signals:       nonEmpty(in.Signals),
	}
	authored := authoredLines(in.Attribution)
	for _, s := range in.Sessions {
		if s == nil {
			continue
		}
		rep.Sessions = append(rep.Sessions, sessionSummary(s, in.Links, in.SessionLinks, authored))
	}
	return rep
}

// prSummary turns the PR into the report's header summary.
func prSummary(pr *model.PR) model.PRSummary {
	if pr == nil {
		return model.PRSummary{}
	}
	sum := model.PRSummary{
		Repo:    pr.Repo,
		Number:  pr.Number,
		URL:     pr.URL,
		Title:   pr.Title,
		HeadRef: pr.HeadRef,
		BaseRef: pr.BaseRef,
		Commits: len(pr.Commits),
		Files:   len(pr.Files),
	}
	for i := range pr.Files {
		sum.Additions += len(pr.Files[i].AddedLines())
		sum.Deletions += len(pr.Files[i].RemovedLines())
	}
	return sum
}

// buildCoverage reports how much of the PR the captured sessions explain.
func buildCoverage(in BuildInput) model.Coverage {
	cov := model.Coverage{
		Sessions:            len(in.Sessions),
		Harnesses:           map[model.Harness]int{},
		UnattributedCommits: nonEmpty(in.Unattributed),
		DroppedSessions:     in.Dropped,
	}
	hooked, total := 0, 0
	for _, s := range in.Sessions {
		if s == nil {
			continue
		}
		total++
		cov.Harnesses[s.Harness]++
		if s.Capture == model.CaptureHooked {
			hooked++
		}
	}
	switch {
	case total == 0:
		cov.Capture = "none"
	case hooked == total:
		cov.Capture = "hooked"
	case hooked == 0:
		cov.Capture = "reconstructed"
	default:
		cov.Capture = "partial"
	}
	attr := in.Attribution
	if attr == nil {
		attr = &model.Attribution{}
	}
	cov.LinesTotal = attr.Total
	cov.LinesExplained = attr.Explained
	cov.Ratio = attr.Ratio()
	return cov
}

// authoredLines counts PR lines per session ref.
func authoredLines(attr *model.Attribution) map[string]int {
	counts := map[string]int{}
	if attr == nil {
		return counts
	}
	for _, f := range attr.Files {
		for _, l := range f.Lines {
			if l.Source != nil {
				counts[l.Source.Session]++
			}
		}
	}
	return counts
}

// sessionSummary builds one row of the report's session list.
func sessionSummary(s *model.Session, links []model.CommitLink,
	sessionLinks map[string]model.LinkMethod, authored map[string]int) model.SessionSummary {
	ref := s.Ref()
	sum := model.SessionSummary{
		Ref:           ref,
		Harness:       s.Harness,
		ID:            s.ID,
		Title:         s.Title,
		Start:         s.Start.UTC(),
		End:           s.End.UTC(),
		Models:        models(s),
		Capture:       s.Capture,
		Link:          model.LinkNone,
		SourcePath:    engine.Home(s.SourcePath),
		Counts:        map[model.EventKind]int{},
		LinesAuthored: authored[ref],
	}
	if m, ok := sessionLinks[ref]; ok {
		sum.Link = m
	}
	for _, l := range links {
		for _, lref := range l.Sessions {
			if lref == ref {
				sum.Commits = append(sum.Commits, l.SHA)
				break
			}
		}
	}
	for i := range s.Events {
		e := &s.Events[i]
		sum.Counts[e.Kind]++
		if e.Kind == model.KindMessage && e.Message != nil && e.Message.Usage != nil {
			u := e.Message.Usage
			sum.Usage.Input += u.Input
			sum.Usage.Output += u.Output
			sum.Usage.CacheRead += u.CacheRead
			sum.Usage.CacheWrite += u.CacheWrite
			sum.Usage.CostUSD += u.CostUSD
		}
	}
	return sum
}

// models returns the distinct non-empty event models in first-appearance order.
func models(s *model.Session) []string {
	seen := map[string]bool{}
	out := []string{}
	for i := range s.Events {
		m := s.Events[i].Model
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// nonEmpty returns a non-nil copy of in, so JSON never renders a null slice.
func nonEmpty[T any](in []T) []T {
	if len(in) == 0 {
		return []T{}
	}
	return append([]T(nil), in...)
}
