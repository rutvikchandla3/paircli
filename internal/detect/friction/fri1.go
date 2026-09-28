package friction

import (
	"fmt"
	"sort"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type fri1 struct{}

func init() { engine.Register(fri1{}) }

func (fri1) ID() string { return "FRI-1" }

func (d fri1) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	// Build map of file -> (edits, cycles)
	fileStats := make(map[string]*fileEditStats)

	// For each PR file, track edits and cycles
	for _, f := range c.PR.Files {
		fileStats[f.Path] = &fileEditStats{path: f.Path}
	}

	// Count edits and cycles per file
	for _, it := range c.Timeline {
		if it.E.Kind != model.KindEdit || it.E.Edit == nil {
			continue
		}
		edit := it.E.Edit
		if edit.Failed || !c.IsPRFile(edit.RelPath) {
			continue
		}

		stat := fileStats[edit.RelPath]
		if stat == nil {
			stat = &fileEditStats{path: edit.RelPath}
			fileStats[edit.RelPath] = stat
		}
		stat.edits++

		// Check if followed by a check command in the same session before next edit to same file
		isFollowedByCheck := false
		for _, it2 := range c.Timeline {
			if it2.E.TS.Before(it.E.TS) || it2.E.TS.Equal(it.E.TS) {
				continue
			}
			if it2.S.Ref() != it.S.Ref() {
				break // different session
			}
			// Stop if we see another edit to the same file
			if it2.E.Kind == model.KindEdit && it2.E.Edit != nil && it2.E.Edit.RelPath == edit.RelPath {
				break
			}
			// Check if this is a check command
			if it2.E.Kind == model.KindCommand && it2.E.Command != nil {
				classes := classify.Command(it2.E.Command.Cmd, c.Config.ExtraChecks)
				if _, ok := classify.CheckClass(classes); ok {
					isFollowedByCheck = true
					break
				}
			}
		}

		if isFollowedByCheck {
			stat.cycles++
		}
	}

	// Calculate median of edits
	var editCounts []int
	for _, stat := range fileStats {
		if stat.edits >= 1 {
			editCounts = append(editCounts, stat.edits)
		}
	}

	var median int
	if len(editCounts) == 0 {
		median = 1
	} else {
		sort.Ints(editCounts)
		if len(editCounts)%2 == 1 {
			median = editCounts[len(editCounts)/2]
		} else {
			// lower middle for even counts
			median = editCounts[len(editCounts)/2-1]
		}
		if median < 1 {
			median = 1
		}
	}

	// Find hotspots: edits >= 5 and edits >= 3 * median
	var hotspots []*fileEditStats
	for _, stat := range fileStats {
		if stat.edits >= 5 && stat.edits >= 3*median {
			hotspots = append(hotspots, stat)
		}
	}

	// Sort by edits descending
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].edits != hotspots[j].edits {
			return hotspots[i].edits > hotspots[j].edits
		}
		return hotspots[i].path < hotspots[j].path
	})

	// Limit to 5 findings
	if len(hotspots) > 5 {
		hotspots = hotspots[:5]
	}

	// Generate findings
	if len(hotspots) > 0 {
		sig.State = model.StateInfo
		for _, stat := range hotspots {
			finding := model.Finding{
				Summary:  fmt.Sprintf("`%s`: %s.", stat.path, engine.Plural(stat.edits, "edit", "edits")+", "+engine.Plural(stat.cycles, "edit-then-check cycle", "edit-then-check cycles")),
				Severity: model.StateInfo,
				Anchors: []model.Anchor{
					{File: stat.path},
				},
				Data: map[string]any{
					"path":   stat.path,
					"edits":  stat.edits,
					"cycles": stat.cycles,
				},
			}
			sig.Findings = append(sig.Findings, finding)
		}

		// Summary
		if len(hotspots) > 0 {
			top := hotspots[0]
			maxOther := 0
			for _, stat := range fileStats {
				if stat.path != top.path && stat.edits > maxOther {
					maxOther = stat.edits
				}
			}
			sig.Summary = fmt.Sprintf("Most rework: `%s` (%d edits). Other files: %d edits or fewer.", top.path, top.edits, maxOther)
		}
	} else {
		sig.State = model.StateClear
		maxEdits := 0
		for _, stat := range fileStats {
			if stat.edits > maxEdits {
				maxEdits = stat.edits
			}
		}
		sig.Summary = fmt.Sprintf("No file stood out: at most %d edits per file.", maxEdits)
	}

	// Build data for all PR files with edits
	var dataFiles []map[string]any
	for _, stat := range fileStats {
		if stat.edits > 0 {
			dataFiles = append(dataFiles, map[string]any{
				"path":   stat.path,
				"edits":  stat.edits,
				"cycles": stat.cycles,
			})
		}
	}
	sort.Slice(dataFiles, func(i, j int) bool {
		ei, ej := dataFiles[i]["edits"].(int), dataFiles[j]["edits"].(int)
		if ei != ej {
			return ei > ej
		}
		return dataFiles[i]["path"].(string) < dataFiles[j]["path"].(string)
	})

	sig.Data = map[string]any{
		"files": dataFiles,
	}

	return sig
}

type fileEditStats struct {
	path   string
	edits  int
	cycles int
}
