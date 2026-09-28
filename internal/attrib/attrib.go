// Package attrib labels every added PR line with who wrote it (AUTH-1) and
// maps PR commits to the sessions whose edits produced them (feeds AUTH-2).
//
// Attribute builds several indexes once over every session's events, in
// timeline order (TS, then Session.Ref(), then Seq), and then matches each
// PR line against them in a fixed priority order: exact agent edit, exact
// external edit, a hook-snapshot-observed human line, a loosely-reformatted
// agent edit, and finally a fuzzy (Levenshtein) match against agent lines.
package attrib

import (
	"sort"
	"time"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// item pairs a session with one of its events. It is the unit the indexes
// below are built from and the unit similarity search returns.
type item struct {
	sess *model.Session
	evt  *model.Event
}

// toRef turns it into the EventRef stored on a LineAttribution.
func toRef(it item) model.EventRef {
	return model.EventRef{Session: it.sess.Ref(), Event: it.evt.ID}
}

// timeline flattens every session's events into one slice ordered by
// (TS, Session.Ref(), Seq), matching the order internal/engine builds its
// own timeline in.
func timeline(sessions []*model.Session) []item {
	var items []item
	for _, s := range sessions {
		for i := range s.Events {
			items = append(items, item{sess: s, evt: &s.Events[i]})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if !a.evt.TS.Equal(b.evt.TS) {
			return a.evt.TS.Before(b.evt.TS)
		}
		if ra, rb := a.sess.Ref(), b.sess.Ref(); ra != rb {
			return ra < rb
		}
		return a.evt.Seq < b.evt.Seq
	})
	return items
}

// indexes are the per-file lookup tables Attribute matches PR lines against.
type indexes struct {
	agentStrict map[string]map[string][]item // file -> NormalizeLine(added) -> edits, in timeline order
	agentLoose  map[string]map[string][]item // file -> LooseLine(added) -> edits, in timeline order
	agentLines  map[string][]string          // file -> distinct NormalizeLine(added), first-seen order
	external    map[string]map[string][]item // file -> NormalizeLine(line) -> external edits, in timeline order
	humanHash   map[string]map[string]item   // file -> LineHash -> the prompt snapshot that introduced it
}

func addIndex(m map[string]map[string][]item, file, key string, it item) {
	if m[file] == nil {
		m[file] = map[string][]item{}
	}
	m[file][key] = append(m[file][key], it)
}

// buildIndexes builds every index in one pass over the sessions' timeline,
// plus a second, per-session pass for the snapshot-derived human index.
func buildIndexes(sessions []*model.Session) *indexes {
	idx := &indexes{
		agentStrict: map[string]map[string][]item{},
		agentLoose:  map[string]map[string][]item{},
		agentLines:  map[string][]string{},
		external:    map[string]map[string][]item{},
	}
	seen := map[string]map[string]bool{}
	agentHashTimes := map[string]map[string][]time.Time{}

	for _, it := range timeline(sessions) {
		e := it.evt
		switch e.Kind {
		case model.KindEdit:
			ed := e.Edit
			if ed == nil || ed.Failed || ed.RelPath == "" {
				continue
			}
			f := ed.RelPath
			for _, added := range ed.Added {
				norm := model.NormalizeLine(added)
				loose := model.LooseLine(added)
				addIndex(idx.agentStrict, f, norm, it)
				addIndex(idx.agentLoose, f, loose, it)
				if seen[f] == nil {
					seen[f] = map[string]bool{}
				}
				if !seen[f][norm] {
					seen[f][norm] = true
					idx.agentLines[f] = append(idx.agentLines[f], norm)
				}
				h := model.LineHash(added)
				if agentHashTimes[f] == nil {
					agentHashTimes[f] = map[string][]time.Time{}
				}
				agentHashTimes[f][h] = append(agentHashTimes[f][h], e.TS)
			}
		case model.KindExternalEdit:
			ext := e.External
			if ext == nil || ext.RelPath == "" {
				continue
			}
			f := ext.RelPath
			for _, line := range ext.Lines {
				addIndex(idx.external, f, model.NormalizeLine(line), it)
			}
		}
	}

	idx.humanHash = buildHumanHash(sessions, agentHashTimes)
	return idx
}

// buildHumanHash walks each session's own snapshot events in order. A line
// hash h seen in a "prompt" snapshot for file f is human when h was absent
// from that session's previous "stop" snapshot for f and no agent edit,
// anywhere, produced a line with hash h before this snapshot's timestamp.
func buildHumanHash(sessions []*model.Session, agentHashTimes map[string]map[string][]time.Time) map[string]map[string]item {
	out := map[string]map[string]item{}
	for _, s := range sessions {
		lastStop := map[string]map[string]bool{}
		for i := range s.Events {
			e := &s.Events[i]
			if e.Kind != model.KindSnapshot || e.Snapshot == nil {
				continue
			}
			snap := e.Snapshot
			if snap.Trigger == "stop" {
				for _, fl := range snap.Files {
					set := make(map[string]bool, len(fl.Hashes))
					for _, h := range fl.Hashes {
						set[h] = true
					}
					lastStop[fl.Path] = set
				}
				continue
			}
			if snap.Trigger != "prompt" {
				continue
			}
			it := item{sess: s, evt: e}
			for _, fl := range snap.Files {
				prev := lastStop[fl.Path]
				for _, h := range fl.Hashes {
					if prev != nil && prev[h] {
						continue
					}
					if earlierAgentEdit(agentHashTimes, fl.Path, h, e.TS) {
						continue
					}
					if out[fl.Path] == nil {
						out[fl.Path] = map[string]item{}
					}
					if _, exists := out[fl.Path][h]; !exists {
						out[fl.Path][h] = it
					}
				}
			}
		}
	}
	return out
}

func earlierAgentEdit(agentHashTimes map[string]map[string][]time.Time, file, hash string, ts time.Time) bool {
	for _, t := range agentHashTimes[file][hash] {
		if t.Before(ts) {
			return true
		}
	}
	return false
}

// maxSimilarityComparisons caps how many Levenshtein comparisons Attribute
// runs per PR line when looking for a fuzzy match.
const maxSimilarityComparisons = 2000

// minSimilarity is the lowest normalized similarity that counts as a match.
const minSimilarity = 0.6

// bestSimilar finds the agent line in file most similar to norm, among
// candidates whose rune-length ratio to norm is within [0.5, 2.0], stopping
// after maxSimilarityComparisons comparisons or a perfect match.
func bestSimilar(idx *indexes, file, norm string) (item, bool) {
	nLen := len([]rune(norm))
	if nLen == 0 {
		return item{}, false
	}
	bestSim := 0.0
	bestLine := ""
	compared := 0
	for _, cand := range idx.agentLines[file] {
		if compared >= maxSimilarityComparisons {
			break
		}
		cLen := len([]rune(cand))
		if cLen == 0 {
			continue
		}
		ratio := float64(nLen) / float64(cLen)
		if ratio < 0.5 || ratio > 2.0 {
			continue
		}
		compared++
		dist := levenshtein(norm, cand)
		maxLen := nLen
		if cLen > maxLen {
			maxLen = cLen
		}
		sim := 1 - float64(dist)/float64(maxLen)
		if sim > bestSim {
			bestSim = sim
			bestLine = cand
		}
		if bestSim >= 1 {
			break
		}
	}
	if bestSim < minSimilarity {
		return item{}, false
	}
	its := idx.agentStrict[file][bestLine]
	if len(its) == 0 {
		return item{}, false
	}
	return its[len(its)-1], true
}

func setSource(la *model.LineAttribution, label model.LineLabel, it item, reformatted bool) {
	la.Label = label
	ref := toRef(it)
	la.Source = &ref
	la.Model = it.evt.Model
	la.AgentID = it.evt.AgentID
	la.Reformatted = reformatted
}

// attributeLine labels one non-trivial added line, following the priority
// order documented on the package and in docs/plan/tasks/T11-attribution.md.
func attributeLine(la *model.LineAttribution, idx *indexes, file, text string) {
	norm := model.NormalizeLine(text)

	if its := idx.agentStrict[file][norm]; len(its) > 0 {
		setSource(la, model.LabelAgent, its[len(its)-1], false)
		return
	}
	if its := idx.external[file][norm]; len(its) > 0 {
		setSource(la, model.LabelHuman, its[0], false)
		return
	}
	if byHash, ok := idx.humanHash[file]; ok {
		if it, ok := byHash[model.LineHash(text)]; ok {
			setSource(la, model.LabelHuman, it, false)
			return
		}
	}
	if its := idx.agentLoose[file][model.LooseLine(text)]; len(its) > 0 {
		setSource(la, model.LabelAgent, its[len(its)-1], true)
		return
	}
	if it, ok := bestSimilar(idx, file, norm); ok {
		setSource(la, model.LabelMixed, it, false)
		return
	}
	la.Label = model.LabelUncaptured
}

// isGenerated reports whether path matches one of cfg's generated-file globs.
func isGenerated(cfg *config.Config, path string) bool {
	for _, pat := range cfg.GeneratedPaths {
		if classify.Glob(pat, path) {
			return true
		}
	}
	return false
}

// Attribute labels every added line of every non-binary PR file. Files
// matching cfg.GeneratedPaths have every line labeled trivial. See the
// package doc comment for the matching order.
func Attribute(pr *model.PR, sessions []*model.Session, cfg *config.Config) *model.Attribution {
	attr := &model.Attribution{}
	if pr == nil {
		return attr
	}
	if cfg == nil {
		cfg = config.Default()
	}
	idx := buildIndexes(sessions)

	for _, f := range pr.Files {
		if f.Binary {
			continue
		}
		fa := model.FileAttribution{Path: f.Path, Counts: map[model.LineLabel]int{}}
		gen := isGenerated(cfg, f.Path)
		for _, dl := range f.AddedLines() {
			la := model.LineAttribution{Line: dl.NewNo}
			switch {
			case gen, model.IsTrivialLine(dl.Text):
				la.Label = model.LabelTrivial
			default:
				attributeLine(&la, idx, f.Path, dl.Text)
			}
			fa.Counts[la.Label]++
			fa.Lines = append(fa.Lines, la)
		}
		attr.Total += fa.Counts[model.LabelAgent] + fa.Counts[model.LabelMixed] +
			fa.Counts[model.LabelHuman] + fa.Counts[model.LabelUncaptured]
		attr.Explained += fa.Counts[model.LabelAgent] + fa.Counts[model.LabelMixed] + fa.Counts[model.LabelHuman]
		attr.Files = append(attr.Files, fa)
	}
	return attr
}
