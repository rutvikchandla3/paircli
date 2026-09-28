package verification

import (
	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// checkRun is one check command with its classification and result.
type checkRun struct {
	It     engine.Item
	Class  classify.CmdClass // test | typecheck | lint | build
	Cmd    string            // classify.ShortCmd
	Result classify.CheckResult
}

// checkRuns returns every command in the timeline that classifies as a check
// (classify.Command with c.Config.ExtraChecks, then CheckClass), human-run included.
func checkRuns(c *engine.Context) []checkRun {
	var out []checkRun
	for _, it := range c.Of(model.KindCommand) {
		cmd := it.E.Command
		if cmd == nil {
			continue
		}
		classes := classify.Command(cmd.Cmd, c.Config.ExtraChecks)
		cc, ok := classify.CheckClass(classes)
		if !ok {
			continue
		}
		out = append(out, checkRun{
			It:     it,
			Class:  cc,
			Cmd:    classify.ShortCmd(cmd.Cmd),
			Result: classify.Check(cmd),
		})
	}
	return out
}

// isShapingLabel reports whether l is a label that shows an agent (or a
// human, mid-session) wrote a PR line.
func isShapingLabel(l model.LineLabel) bool {
	switch l {
	case model.LabelAgent, model.LabelMixed, model.LabelHuman:
		return true
	}
	return false
}

// shapingRefs returns the set of event refs that are the Source of at least
// one PR line labeled agent, agent_then_human or human_in_session.
func shapingRefs(attr *model.Attribution) map[model.EventRef]bool {
	refs := map[model.EventRef]bool{}
	if attr == nil {
		return refs
	}
	for _, f := range attr.Files {
		for _, l := range f.Lines {
			if isShapingLabel(l.Label) && l.Source != nil {
				refs[*l.Source] = true
			}
		}
	}
	return refs
}

// itemsForRefs returns the timeline items named by refs, in timeline order.
func itemsForRefs(c *engine.Context, refs map[model.EventRef]bool) []engine.Item {
	if len(refs) == 0 {
		return nil
	}
	var out []engine.Item
	for _, it := range c.Timeline {
		if refs[c.Ref(it)] {
			out = append(out, it)
		}
	}
	return out
}

// shapingItems returns every timeline event that is the Source of at least
// one PR line labeled agent, agent_then_human or human_in_session, in
// timeline order.
func shapingItems(c *engine.Context) []engine.Item {
	return itemsForRefs(c, shapingRefs(c.Attribution))
}

// fileGroup is a PR file together with the shaping events that sourced lines
// in it.
type fileGroup struct {
	path  string
	items []engine.Item
}

// groupByFile groups items (each already known to be the Source of some PR
// line) by the PR file whose line they sourced, in PR file order.
func groupByFile(c *engine.Context, items []engine.Item) []fileGroup {
	if len(items) == 0 {
		return nil
	}
	itemSet := make(map[model.EventRef]engine.Item, len(items))
	for _, it := range items {
		itemSet[c.Ref(it)] = it
	}
	var out []fileGroup
	for _, f := range c.Attribution.Files {
		seen := map[model.EventRef]bool{}
		var its []engine.Item
		for _, l := range f.Lines {
			if l.Source == nil {
				continue
			}
			it, ok := itemSet[*l.Source]
			if !ok || seen[*l.Source] {
				continue
			}
			seen[*l.Source] = true
			its = append(its, it)
		}
		if len(its) > 0 {
			out = append(out, fileGroup{path: f.Path, items: its})
		}
	}
	return out
}

// uncapturedCount returns the number of changed lines labeled uncaptured.
func uncapturedCount(attr *model.Attribution) int {
	n := 0
	if attr == nil {
		return n
	}
	for _, f := range attr.Files {
		n += f.Counts[model.LabelUncaptured]
	}
	return n
}

// lastChars returns the last n runes of s (all of s if shorter).
func lastChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// evidenceExcerpt picks a reasonable excerpt of it for use as Evidence text.
func evidenceExcerpt(it engine.Item) string {
	switch it.E.Kind {
	case model.KindCommand:
		if it.E.Command != nil {
			return lastChars(it.E.Command.Output, 200)
		}
	case model.KindEdit:
		if it.E.Edit != nil {
			return it.E.Edit.RelPath + ": " + joinLines(it.E.Edit.Added)
		}
	}
	return ""
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += " "
		}
		out += l
	}
	return out
}
