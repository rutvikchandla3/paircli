package friction

import (
	"fmt"
	"time"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type fri3 struct{}

func init() { engine.Register(fri3{}) }

func (fri3) ID() string { return "FRI-3" }

func (d fri3) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	var resets []*resetFinding
	var totalLinesAfter int

	// For each session, find compactions and resets
	for _, session := range c.Sessions {
		for i, e := range session.Events {
			if e.Kind == model.KindCompaction && e.Compaction != nil {
				reset := &resetFinding{
					isCompaction: true,
					trigger:      e.Compaction.Trigger,
					ts:           e.TS,
					e:            &session.Events[i],
					session:      session,
				}

				// Find non-failed edits after this reset
				var afterItems []engine.Item
				for j := i + 1; j < len(session.Events); j++ {
					ev := &session.Events[j]
					if ev.Kind == model.KindEdit && ev.Edit != nil && !ev.Edit.Failed {
						afterItems = append(afterItems, engine.Item{S: session, E: ev})
					}
				}

				reset.afterItems = afterItems
				anchors := c.AnchorsFor(afterItems...)
				reset.files = countDistinctFiles(anchors)
				resets = append(resets, reset)
			} else if e.Kind == model.KindReset && e.Reset != nil {
				reset := &resetFinding{
					isCompaction: false,
					resetType:    e.Reset.Type,
					ts:           e.TS,
					e:            &session.Events[i],
					session:      session,
				}

				// Find non-failed edits after this reset
				var afterItems []engine.Item
				for j := i + 1; j < len(session.Events); j++ {
					ev := &session.Events[j]
					if ev.Kind == model.KindEdit && ev.Edit != nil && !ev.Edit.Failed {
						afterItems = append(afterItems, engine.Item{S: session, E: ev})
					}
				}

				reset.afterItems = afterItems
				anchors := c.AnchorsFor(afterItems...)
				reset.files = countDistinctFiles(anchors)
				resets = append(resets, reset)
			}
		}
	}

	// Count total lines written after resets
	// Build a set of event refs in after items
	for _, reset := range resets {
		afterRefs := make(map[model.EventRef]bool)
		for _, it := range reset.afterItems {
			afterRefs[model.EventRef{Session: it.S.Ref(), Event: it.E.ID}] = true
		}

		// Count attribution lines whose source is in the after set
		for _, f := range c.Attribution.Files {
			for _, l := range f.Lines {
				if l.Source != nil && afterRefs[*l.Source] {
					totalLinesAfter++
				}
			}
		}
	}

	// Generate findings for each reset
	for _, reset := range resets {
		var summary string
		if reset.isCompaction {
			trigger := reset.trigger
			if trigger == "" {
				trigger = "unknown trigger"
			}
			summary = fmt.Sprintf("Context compacted (%s) at %s; %d PR files edited afterwards.", trigger, engine.Clock(reset.ts), reset.files)
		} else {
			summary = fmt.Sprintf("Session %s at %s; %d PR files edited afterwards.", reset.resetType, engine.Clock(reset.ts), reset.files)
		}

		finding := model.Finding{
			Summary:  summary,
			Severity: model.StateInfo,
			Evidence: []model.Evidence{
				c.Evidence(engine.Item{S: reset.session, E: reset.e}, ""),
			},
			Data: map[string]any{
				"files": reset.files,
			},
		}
		sig.Findings = append(sig.Findings, finding)
	}

	// Determine state and summary
	if len(resets) == 0 {
		sig.State = model.StateClear
		sig.Summary = "No compactions, clears, resumes or forks."
	} else {
		sig.State = model.StateInfo

		// Build "by_type" data
		byType := make(map[string]int)
		var kinds []string
		for _, reset := range resets {
			if reset.isCompaction {
				byType["compaction"]++
				if !contains(kinds, "compaction") {
					kinds = append(kinds, "compaction")
				}
			} else {
				byType[reset.resetType]++
				if !contains(kinds, reset.resetType) {
					kinds = append(kinds, reset.resetType)
				}
			}
		}

		kindsSummary := ""
		for i, k := range kinds {
			if i > 0 {
				kindsSummary += ", "
			}
			kindsSummary += k
		}

		sig.Summary = fmt.Sprintf("%d context resets (%s); %d PR lines were written after a reset.", len(resets), kindsSummary, totalLinesAfter)
	}

	// Build data
	sig.Data = map[string]any{
		"resets":      len(resets),
		"lines_after": totalLinesAfter,
	}

	return sig
}

type resetFinding struct {
	isCompaction bool
	trigger      string
	resetType    string
	ts           time.Time
	e            *model.Event
	session      *model.Session
	afterItems   []engine.Item
	files        int
}

func countDistinctFiles(anchors []model.Anchor) int {
	seen := make(map[string]bool)
	for _, a := range anchors {
		seen[a.File] = true
	}
	return len(seen)
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
