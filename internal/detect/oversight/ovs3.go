package oversight

import (
	"fmt"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type ovs3 struct{}

func init() { engine.Register(ovs3{}) }

func (ovs3) ID() string { return "OVS-3" }

// ovs3MaxFiles is how many file names a subagent finding lists before it
// trails off with ", …".
const ovs3MaxFiles = 3

// ovs3Agent is one delegated agent, discovered from a subagent event or from
// the AgentID on a session event.
type ovs3Agent struct {
	id    string
	typ   string
	model string
	lines int
	files []string // files whose PR lines it wrote, first-seen order
	refs  map[model.EventRef]bool
}

// ovs3Describe renders " in `a`, `b`, …" for the files the agent wrote lines
// in, or "" when it wrote none.
func ovs3Describe(files []string) string {
	if len(files) == 0 {
		return ""
	}
	names := files
	ellipsis := false
	if len(names) > ovs3MaxFiles {
		names, ellipsis = names[:ovs3MaxFiles], true
	}
	quoted := make([]string, 0, len(names))
	for _, f := range names {
		quoted = append(quoted, "`"+f+"`")
	}
	out := " in " + strings.Join(quoted, ", ")
	if ellipsis {
		out += ", …"
	}
	return out
}

func (d ovs3) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	// Timeline order, so findings come out in the order the agents appeared.
	var order []*ovs3Agent
	byID := map[string]*ovs3Agent{}
	ensure := func(id string) *ovs3Agent {
		if a, ok := byID[id]; ok {
			return a
		}
		a := &ovs3Agent{id: id, refs: map[model.EventRef]bool{}}
		byID[id] = a
		order = append(order, a)
		return a
	}

	for _, it := range c.Timeline {
		e := it.E
		if e.AgentID != "" {
			ensure(e.AgentID)
			continue
		}
		if e.Kind == model.KindSubagent && e.Subagent != nil {
			id := e.Subagent.AgentID
			if id == "" {
				id = e.ID
			}
			a := ensure(id)
			if a.typ == "" {
				a.typ = e.Subagent.Type
			}
			if a.model == "" {
				a.model = e.Subagent.Model
			}
		}
	}

	total := 0
	attr := c.Attribution
	if attr != nil {
		for _, f := range attr.Files {
			for _, l := range f.Lines {
				if l.AgentID == "" || l.Label == model.LabelTrivial {
					continue
				}
				a, ok := byID[l.AgentID]
				if !ok {
					continue
				}
				a.lines++
				total++
				if l.Source != nil {
					a.refs[*l.Source] = true
				}
				if !ovs3HasFile(a.files, f.Path) {
					a.files = append(a.files, f.Path)
				}
			}
		}
	}

	for _, a := range order {
		display := a.typ
		if display == "" {
			display = a.id
		}
		modelName := a.model
		if modelName == "" {
			modelName = "model unknown"
		}
		sig.Findings = append(sig.Findings, model.Finding{
			Summary: fmt.Sprintf("Subagent `%s` (%s) wrote %s%s.",
				display, modelName, engine.Plural(a.lines, "PR line", "PR lines"), ovs3Describe(a.files)),
			Severity: model.StateInfo,
			Anchors:  attr.AnchorsFor(a.refs),
			Data: map[string]any{
				"id":    a.id,
				"type":  a.typ,
				"model": a.model,
				"lines": a.lines,
			},
		})
	}

	sig.State = model.StateInfo
	if len(order) == 0 {
		sig.State = model.StateClear
		sig.Summary = "No subagents were used."
	} else {
		sig.Summary = fmt.Sprintf("%d subagents; %d PR lines were written by subagents.", len(order), total)
	}

	subagents := make([]map[string]any, 0, len(order))
	for _, a := range order {
		subagents = append(subagents, map[string]any{
			"id":    a.id,
			"type":  a.typ,
			"model": a.model,
			"lines": a.lines,
		})
	}
	sig.Data = map[string]any{"subagents": subagents}
	return sig
}

// ovs3HasFile reports whether files already contains path.
func ovs3HasFile(files []string, path string) bool {
	for _, f := range files {
		if f == path {
			return true
		}
	}
	return false
}
