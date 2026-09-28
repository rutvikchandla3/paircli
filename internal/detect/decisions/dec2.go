package decisions

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type dec2 struct{}

func init() { engine.Register(dec2{}) }

func (dec2) ID() string { return "DEC-2" }

// tfEntry pairs a Finding with the timestamp used to order it among the four
// DEC-2 sources (which are otherwise gathered independently).
type tfEntry struct {
	ts time.Time
	f  model.Finding
}

func (d dec2) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	prAdded := prAddedNorm(c.PR)

	var tf []tfEntry
	var revertedEdits, createdThenDeleted, discards, rollbacks, branches, off int

	for _, s := range c.Sessions {
		for _, g := range revertedGroups(s, prAdded) {
			tf = append(tf, tfEntry{ts: g.firstTS, f: revertedFinding(c, g)})
			revertedEdits++
		}
		for _, e := range createdThenDeletedFindings(c, s) {
			tf = append(tf, e)
			createdThenDeleted++
		}
	}

	for _, it := range c.Timeline {
		switch it.E.Kind {
		case model.KindCommand:
			if it.E.Command == nil {
				continue
			}
			classes := classify.Command(it.E.Command.Cmd, c.Config.ExtraChecks)
			if !hasClass(classes, classify.GitDiscard) && !hasClass(classes, classify.GitResetHard) {
				continue
			}
			if !hasEarlierEdit(it.S, it.E.TS) {
				continue
			}
			text := fmt.Sprintf("Discarded changes with `%s` at %s.", classify.ShortCmd(it.E.Command.Cmd), engine.Clock(it.E.TS))
			f := model.Finding{
				Summary:  text,
				Severity: model.StateInfo,
				Evidence: []model.Evidence{c.Evidence(it, it.E.Command.Cmd)},
				Data:     map[string]any{"kind": "discard"},
			}
			tf = append(tf, tfEntry{ts: it.E.TS, f: f})
			discards++

		case model.KindReset:
			r := it.E.Reset
			if r == nil {
				continue
			}
			switch r.Type {
			case "rollback":
				text := fmt.Sprintf("Rolled back the conversation at %s.", engine.Clock(it.E.TS))
				f := model.Finding{
					Summary:  text,
					Severity: model.StateInfo,
					Evidence: []model.Evidence{c.Evidence(it, r.Summary)},
					Data:     map[string]any{"kind": "rollback"},
				}
				tf = append(tf, tfEntry{ts: it.E.TS, f: f})
				rollbacks++

			case "branch_switch":
				n := offBranchEdits(it.S)
				text := fmt.Sprintf("Left a branch at %s: %s", engine.Clock(it.E.TS), model.Clip(r.Summary, 120))
				f := model.Finding{
					Summary:  text,
					Severity: model.StateInfo,
					Evidence: []model.Evidence{c.Evidence(it, r.Summary)},
					Data:     map[string]any{"kind": "branch_switch", "off_branch_edits": n},
				}
				tf = append(tf, tfEntry{ts: it.E.TS, f: f})
				branches++
				off += n
			}
		}
	}

	sort.SliceStable(tf, func(i, j int) bool { return tf[i].ts.Before(tf[j].ts) })
	findings := make([]model.Finding, 0, len(tf))
	for _, e := range tf {
		findings = append(findings, e.f)
	}
	sig.Findings = findings

	sig.Data = map[string]any{
		"reverted_edits":       revertedEdits,
		"created_then_deleted": createdThenDeleted,
		"discards":             discards,
		"rollbacks":            rollbacks,
		"branches":             branches,
		"off_branch_edits":     off,
	}

	n := revertedEdits + createdThenDeleted + discards + rollbacks + branches
	if n == 0 {
		sig.State = model.StateClear
		sig.Summary = "No reverted code, discards, rollbacks or abandoned branches."
		return sig
	}
	var parts []string
	if revertedEdits > 0 {
		parts = append(parts, engine.Plural(revertedEdits, "reverted edit", "reverted edits"))
	}
	if createdThenDeleted > 0 {
		parts = append(parts, engine.Plural(createdThenDeleted, "created-then-deleted file", "created-then-deleted files"))
	}
	if discards > 0 {
		parts = append(parts, engine.Plural(discards, "discard", "discards"))
	}
	if rollbacks > 0 {
		parts = append(parts, engine.Plural(rollbacks, "rollback", "rollbacks"))
	}
	if branches > 0 {
		parts = append(parts, engine.Plural(branches, "abandoned branch", "abandoned branches"))
	}
	base := engine.Plural(n, "abandoned attempt", "abandoned attempts")
	if len(parts) > 0 {
		sig.Summary = fmt.Sprintf("%s (%s).", base, strings.Join(parts, ", "))
	} else {
		sig.Summary = base + "."
	}
	sig.State = model.StateInfo
	return sig
}

func hasClass(classes []classify.CmdClass, want classify.CmdClass) bool {
	for _, c := range classes {
		if c == want {
			return true
		}
	}
	return false
}

func hasEarlierEdit(s *model.Session, before time.Time) bool {
	for i := range s.Events {
		e := &s.Events[i]
		if e.Kind == model.KindEdit && e.TS.Before(before) {
			return true
		}
	}
	return false
}

func offBranchEdits(s *model.Session) int {
	n := 0
	for i := range s.Events {
		if s.Events[i].Kind == model.KindEdit && s.Events[i].OffBranch {
			n++
		}
	}
	return n
}

// prAddedNorm returns, for every file in pr, the set of NormalizeLine(text)
// values of its added lines.
func prAddedNorm(pr *model.PR) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if pr == nil {
		return out
	}
	for _, f := range pr.Files {
		set := map[string]bool{}
		for _, dl := range f.AddedLines() {
			set[model.NormalizeLine(dl.Text)] = true
		}
		out[f.Path] = set
	}
	return out
}

func nonTrivialLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !model.IsTrivialLine(l) {
			out = append(out, l)
		}
	}
	return out
}

// revertedGroup is one run of consecutive reverted edits to the same file in
// the same session.
type revertedGroup struct {
	s          *model.Session
	file       string
	firstEdit  engine.Item
	removing   engine.Item
	firstTS    time.Time
	turnFirst  int
	turnLast   int
	totalLines int
}

// fileEditEntry is one edit to a single file, in session order, together
// with whether it qualifies as "reverted" per the T16 rule.
type fileEditEntry struct {
	idx      int // index into s.Events
	reverted bool
	lost     int
	removeAt int // index of the edit that removed the lost lines (-1 if none)
}

// revertedGroups finds every merged run of consecutive reverted edits, per
// file, in session s.
func revertedGroups(s *model.Session, prAdded map[string]map[string]bool) []revertedGroup {
	// Collect, per file, the ordered list of edit indices.
	byFile := map[string][]int{}
	for i := range s.Events {
		e := &s.Events[i]
		if e.Kind != model.KindEdit || e.Edit == nil || e.Edit.Failed || e.Edit.RelPath == "" {
			continue
		}
		byFile[e.Edit.RelPath] = append(byFile[e.Edit.RelPath], i)
	}

	var groups []revertedGroup
	for file, idxs := range byFile {
		var entries []fileEditEntry
		for _, idx := range idxs {
			edit := s.Events[idx].Edit
			addedNT := nonTrivialLines(edit.Added)
			if len(addedNT) < 2 {
				entries = append(entries, fileEditEntry{idx: idx, reverted: false})
				continue
			}
			prSet := prAdded[file]
			lost := 0
			removeAt := -1
			for _, line := range addedNT {
				norm := model.NormalizeLine(line)
				if prSet[norm] {
					continue
				}
				for _, j := range idxs {
					if j <= idx {
						continue
					}
					rem := s.Events[j].Edit
					found := false
					for _, r := range rem.Removed {
						if model.NormalizeLine(r) == norm {
							found = true
							break
						}
					}
					if found {
						lost++
						if j > removeAt {
							removeAt = j
						}
						break
					}
				}
			}
			reverted := lost >= 3 || float64(lost) >= 0.5*float64(len(addedNT))
			entries = append(entries, fileEditEntry{idx: idx, reverted: reverted, lost: lost, removeAt: removeAt})
		}

		// Merge consecutive reverted entries into groups.
		i := 0
		for i < len(entries) {
			if !entries[i].reverted {
				i++
				continue
			}
			j := i
			totalLines := 0
			removeAt := -1
			for j < len(entries) && entries[j].reverted {
				totalLines += entries[j].lost
				if entries[j].removeAt > removeAt {
					removeAt = entries[j].removeAt
				}
				j++
			}
			firstIdx := entries[i].idx
			lastIdx := entries[j-1].idx
			g := revertedGroup{
				s:          s,
				file:       file,
				firstEdit:  engine.Item{S: s, E: &s.Events[firstIdx]},
				firstTS:    s.Events[firstIdx].TS,
				turnFirst:  s.Events[firstIdx].Turn,
				turnLast:   s.Events[lastIdx].Turn,
				totalLines: totalLines,
			}
			if removeAt >= 0 {
				g.removing = engine.Item{S: s, E: &s.Events[removeAt]}
			}
			groups = append(groups, g)
			i = j
		}
	}
	sort.SliceStable(groups, func(a, b int) bool { return groups[a].firstTS.Before(groups[b].firstTS) })
	return groups
}

func revertedFinding(c *engine.Context, g revertedGroup) model.Finding {
	text := fmt.Sprintf("Code written in `%s` at %s (%s, turns %d–%d) was later removed.",
		g.file, engine.Clock(g.firstTS), engine.Plural(g.totalLines, "line", "lines"), g.turnFirst, g.turnLast)
	evidence := []model.Evidence{c.Evidence(g.firstEdit, g.file)}
	if g.removing.E != nil {
		evidence = append(evidence, c.Evidence(g.removing, g.file))
	}
	return model.Finding{
		Summary:  text,
		Severity: model.StateInfo,
		Evidence: evidence,
		Data:     map[string]any{"kind": "reverted_edit", "lines": g.totalLines},
	}
}

// createdThenDeletedFindings finds, per session, files created (Op create)
// and later deleted (Op delete, or an rm whose classify.RmTargets includes
// the file) in the same session.
func createdThenDeletedFindings(c *engine.Context, s *model.Session) []tfEntry {
	var out []tfEntry
	consumed := map[string]bool{}
	for i := range s.Events {
		ce := &s.Events[i]
		if ce.Kind != model.KindEdit || ce.Edit == nil || ce.Edit.Op != model.OpCreate || ce.Edit.RelPath == "" {
			continue
		}
		file := ce.Edit.RelPath
		if consumed[file] {
			continue
		}
		for j := i + 1; j < len(s.Events); j++ {
			de := &s.Events[j]
			var match bool
			if de.Kind == model.KindEdit && de.Edit != nil && de.Edit.Op == model.OpDelete && de.Edit.RelPath == file {
				match = true
			} else if de.Kind == model.KindCommand && de.Command != nil {
				for _, t := range classify.RmTargets(de.Command.Cmd) {
					if t == file {
						match = true
						break
					}
				}
			}
			if !match {
				continue
			}
			consumed[file] = true
			text := fmt.Sprintf("Created and later deleted `%s`.", file)
			f := model.Finding{
				Summary:  text,
				Severity: model.StateInfo,
				Evidence: []model.Evidence{c.Evidence(engine.Item{S: s, E: ce}, file), c.Evidence(engine.Item{S: s, E: de}, file)},
				Data:     map[string]any{"kind": "created_then_deleted"},
			}
			out = append(out, tfEntry{ts: ce.TS, f: f})
			break
		}
	}
	return out
}
