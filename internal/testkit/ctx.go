package testkit

import (
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// Ctx builds an engine.Context with ExactAttribution and config.Default();
// Links is empty.
func Ctx(pr *model.PR, sessions ...*model.Session) *engine.Context {
	return engine.NewContext(pr, sessions, ExactAttribution(pr, sessions), nil, config.Default())
}

// ExactAttribution computes ground-truth attribution for tests: for every
// added PR line, in file order, it is:
//   - trivial, if model.IsTrivialLine(line);
//   - otherwise agent, sourced from the latest (by timeline order) non-failed
//     file_edit whose RelPath matches the file and whose Added contains the
//     line after NormalizeLine;
//   - otherwise human_in_session, if an external_edit on that file contains
//     the line after NormalizeLine;
//   - otherwise uncaptured.
func ExactAttribution(pr *model.PR, sessions []*model.Session) *model.Attribution {
	attr := &model.Attribution{}
	if pr == nil {
		return attr
	}
	ec := engine.NewContext(pr, sessions, nil, nil, nil)

	for _, f := range pr.Files {
		fa := model.FileAttribution{Path: f.Path, Counts: map[model.LineLabel]int{}}
		for _, dl := range f.AddedLines() {
			la := model.LineAttribution{Line: dl.NewNo}
			switch {
			case model.IsTrivialLine(dl.Text):
				la.Label = model.LabelTrivial
			default:
				norm := model.NormalizeLine(dl.Text)
				if it, ok := latestAgentEdit(ec, f.Path, norm); ok {
					la.Label = model.LabelAgent
					ref := ec.Ref(it)
					la.Source = &ref
					la.Model = it.E.Model
					la.AgentID = it.E.AgentID
				} else if hasExternalEdit(ec, f.Path, norm) {
					la.Label = model.LabelHuman
				} else {
					la.Label = model.LabelUncaptured
				}
			}
			fa.Lines = append(fa.Lines, la)
			fa.Counts[la.Label]++
			if la.Label != model.LabelTrivial {
				attr.Total++
				switch la.Label {
				case model.LabelAgent, model.LabelMixed, model.LabelHuman:
					attr.Explained++
				}
			}
		}
		attr.Files = append(attr.Files, fa)
	}
	return attr
}

// latestAgentEdit returns the last (by timeline order) non-failed file_edit
// on path whose Added contains a line equal to norm after NormalizeLine.
func latestAgentEdit(c *engine.Context, path, norm string) (engine.Item, bool) {
	var found engine.Item
	ok := false
	for _, it := range c.Timeline {
		if it.E.Kind != model.KindEdit || it.E.Edit == nil {
			continue
		}
		ed := it.E.Edit
		if ed.Failed || ed.RelPath != path {
			continue
		}
		for _, a := range ed.Added {
			if model.NormalizeLine(a) == norm {
				found, ok = it, true
				break
			}
		}
	}
	return found, ok
}

// hasExternalEdit reports whether an external_edit on path contains a line
// equal to norm after NormalizeLine.
func hasExternalEdit(c *engine.Context, path, norm string) bool {
	for _, it := range c.Timeline {
		if it.E.Kind != model.KindExternalEdit || it.E.External == nil {
			continue
		}
		ext := it.E.External
		if ext.RelPath != path {
			continue
		}
		for _, l := range ext.Lines {
			if model.NormalizeLine(l) == norm {
				return true
			}
		}
	}
	return false
}

// Find returns a pointer to the signal with the given ID, or nil.
func Find(sigs []model.Signal, id string) *model.Signal {
	for i := range sigs {
		if sigs[i].ID == id {
			return &sigs[i]
		}
	}
	return nil
}
