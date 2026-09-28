package authorship

import (
	"fmt"
	"math"
	"sort"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() {
	engine.Register(auth1{})
}

// auth1 is the AUTH-1 detector: line-level authorship.
type auth1 struct{}

func (auth1) ID() string { return "AUTH-1" }

// auth1FileTotal returns the non-trivial line count for a file's counts.
func auth1FileTotal(counts map[model.LineLabel]int) int {
	return counts[model.LabelAgent] + counts[model.LabelMixed] + counts[model.LabelHuman] + counts[model.LabelUncaptured]
}

func (auth1) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions("AUTH-1")
	}
	attr := c.Attribution
	if attr.Total == 0 {
		return engine.Unknown("AUTH-1", "No added lines to attribute.")
	}

	sig := engine.NewSignal("AUTH-1")
	sig.State = model.StateInfo

	counts := map[model.LineLabel]int{}
	reformatted := 0
	for _, f := range attr.Files {
		for _, l := range f.Lines {
			counts[l.Label]++
			if l.Reformatted {
				reformatted++
			}
		}
	}
	a, m, h, u := counts[model.LabelAgent], counts[model.LabelMixed], counts[model.LabelHuman], counts[model.LabelUncaptured]
	pct := int(math.Round(float64(a) / float64(attr.Total) * 100))
	sig.Summary = fmt.Sprintf(
		"Agent wrote %d of %d changed lines (%d%%); %d edited by a human afterwards, %d written by a human in session, %d not captured.",
		a, attr.Total, pct, m, h, u)

	type row struct {
		path   string
		counts map[model.LineLabel]int
		total  int
	}
	var rows []row
	for _, f := range attr.Files {
		total := auth1FileTotal(f.Counts)
		if total == 0 {
			continue
		}
		rows = append(rows, row{f.Path, f.Counts, total})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].total > rows[j].total })
	if len(rows) > 20 {
		rows = rows[:20]
	}

	findings := make([]model.Finding, 0, len(rows))
	for _, r := range rows {
		findings = append(findings, model.Finding{
			Summary: fmt.Sprintf("`%s` — %d agent · %d agent→human · %d human · %d uncaptured",
				r.path, r.counts[model.LabelAgent], r.counts[model.LabelMixed], r.counts[model.LabelHuman], r.counts[model.LabelUncaptured]),
			Severity: model.StateInfo,
			Anchors:  attr.LinesFor(r.path, model.LabelUncaptured),
		})
	}
	sig.Findings = findings

	filesData := make([]map[string]any, 0, len(attr.Files))
	for _, f := range attr.Files {
		filesData = append(filesData, map[string]any{"path": f.Path, "counts": f.Counts})
	}
	sig.Data = map[string]any{
		"counts":      counts,
		"reformatted": reformatted,
		"files":       filesData,
	}
	return sig
}
