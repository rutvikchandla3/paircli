package oversight

import (
	"fmt"
	"time"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type ovs2 struct{}

func init() { engine.Register(ovs2{}) }

func (ovs2) ID() string { return "OVS-2" }

// ovs2IsHuman reports whether e is a human touchpoint: a main-agent prompt, a
// question with an answer, a user-caused permission event, a human-run
// command, a rejection or an interrupt.
func ovs2IsHuman(e *model.Event) bool {
	if e.AgentID != "" {
		return false
	}
	switch e.Kind {
	case model.KindPrompt:
		return e.Prompt != nil
	case model.KindQuestion:
		if e.Question == nil {
			return false
		}
		for _, qa := range e.Question.Items {
			if qa.Answer != "" {
				return true
			}
		}
		return false
	case model.KindPermission:
		return e.Permission != nil && e.Permission.By == "user"
	case model.KindCommand:
		return e.Command != nil && e.Command.ByUser
	case model.KindRejection, model.KindInterrupt:
		return true
	}
	return false
}

// ovs2HumanIndices returns the indices (== Seq) of a session's human events,
// ascending.
func ovs2HumanIndices(s *model.Session) []int {
	var out []int
	for i := range s.Events {
		if ovs2IsHuman(&s.Events[i]) {
			out = append(out, i)
		}
	}
	return out
}

// ovs2Stretch is one unattended run of a session: the events between two
// human touchpoints (or a session boundary), plus the PR lines they wrote.
type ovs2Stretch struct {
	ref       string
	from, to  time.Time
	toolCalls int
	lines     int
	sources   map[model.EventRef]bool // events that produced a counted line
}

// ovs2Bounds splits a session at human indices into index ranges [start, end]
// (inclusive) over s.Events: session start to the first human event, between
// human events, and the last human event to the session end. A session with no
// human events is one range covering the whole session. Ranges are empty
// (start > end) when two human events are adjacent.
func ovs2Bounds(s *model.Session, human []int) [][2]int {
	if len(human) == 0 {
		return [][2]int{{0, len(s.Events) - 1}}
	}
	bounds := [][2]int{{0, human[0] - 1}}
	for i := 0; i+1 < len(human); i++ {
		bounds = append(bounds, [2]int{human[i] + 1, human[i+1] - 1})
	}
	return append(bounds, [2]int{human[len(human)-1] + 1, len(s.Events) - 1})
}

// ovs2Stretches builds the stretches of one session. Bound i runs from the
// previous human touchpoint's timestamp (or the session start) to the next
// one's (or the session end).
func ovs2Stretches(s *model.Session, human []int) []ovs2Stretch {
	bounds := ovs2Bounds(s, human)
	out := make([]ovs2Stretch, 0, len(bounds))
	for i, b := range bounds {
		st := ovs2Stretch{ref: s.Ref(), sources: map[model.EventRef]bool{}}

		// from: the previous human event's TS, else the session start.
		if i == 0 {
			st.from = s.Start
		} else {
			st.from = s.Events[human[i-1]].TS
		}
		// to: the next human event's TS, else the session end.
		if i < len(human) {
			st.to = s.Events[human[i]].TS
		} else {
			st.to = s.End
		}

		for j := b[0]; j <= b[1] && j < len(s.Events); j++ {
			if j >= 0 && ovsIsToolAction(&s.Events[j]) {
				st.toolCalls++
			}
		}
		out = append(out, st)
	}
	return out
}

// ovs2FillLines counts the PR lines each stretch's events produced and records
// the events that produced them, for anchoring.
func ovs2FillLines(stretches []ovs2Stretch, s *model.Session, attr *model.Attribution) {
	if attr == nil || len(stretches) == 0 {
		return
	}
	idxOf := make(map[string]int, len(s.Events))
	for i := range s.Events {
		idxOf[s.Events[i].ID] = i
	}
	bounds := ovs2Bounds(s, ovs2HumanIndices(s))

	for _, f := range attr.Files {
		for _, l := range f.Lines {
			if l.Source == nil || l.Source.Session != s.Ref() {
				continue
			}
			i, ok := idxOf[l.Source.Event]
			if !ok {
				continue
			}
			for k := range bounds {
				if k >= len(stretches) {
					break
				}
				if bounds[k][0] <= i && i <= bounds[k][1] {
					stretches[k].lines++
					stretches[k].sources[*l.Source] = true
					break
				}
			}
		}
	}
}

// ovs2Longest returns the stretch with the most tool actions, breaking ties on
// the longer duration.
func ovs2Longest(stretches []ovs2Stretch) *ovs2Stretch {
	var best *ovs2Stretch
	for i := range stretches {
		s := &stretches[i]
		switch {
		case best == nil,
			s.toolCalls > best.toolCalls,
			s.toolCalls == best.toolCalls && s.to.Sub(s.from) > best.to.Sub(best.from):
			best = s
		}
	}
	return best
}

func (d ovs2) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	humanTotal := 0
	var overall *ovs2Stretch

	for _, s := range c.Sessions {
		if len(s.Events) == 0 {
			continue
		}
		human := ovs2HumanIndices(s)
		humanTotal += len(human)

		stretches := ovs2Stretches(s, human)
		ovs2FillLines(stretches, s, c.Attribution)

		longest := ovs2Longest(stretches)
		if longest == nil {
			continue
		}
		if overall == nil ||
			longest.toolCalls > overall.toolCalls ||
			(longest.toolCalls == overall.toolCalls && longest.to.Sub(longest.from) > overall.to.Sub(overall.from)) {
			overall = longest
		}

		minutes := ovs2Minutes(*longest)
		sig.Findings = append(sig.Findings, model.Finding{
			Summary: fmt.Sprintf("`%s`: longest unattended stretch %d min, %d tool calls, %s written.",
				s.Ref(), minutes, longest.toolCalls, engine.Plural(longest.lines, "PR line", "PR lines")),
			Severity: model.StateInfo,
			Anchors:  c.AnchorsFor(ovs2Items(c, longest.sources)...),
			Data: map[string]any{
				"session":    s.Ref(),
				"from":       longest.from,
				"to":         longest.to,
				"minutes":    minutes,
				"tool_calls": longest.toolCalls,
				"lines":      longest.lines,
			},
		})
	}

	sig.State = model.StateInfo
	longest := map[string]any{}
	if overall != nil {
		minutes := ovs2Minutes(*overall)
		sig.Summary = fmt.Sprintf("Longest unattended stretch: %d min and %d tool calls in `%s`; %d PR lines were written in it.",
			minutes, overall.toolCalls, overall.ref, overall.lines)
		longest = map[string]any{
			"session":    overall.ref,
			"from":       overall.from,
			"to":         overall.to,
			"minutes":    minutes,
			"tool_calls": overall.toolCalls,
			"lines":      overall.lines,
		}
	} else {
		sig.Summary = "No unattended stretches were recorded."
	}
	sig.Data = map[string]any{"human_events": humanTotal, "longest": longest}
	return sig
}

// ovs2Minutes truncates a stretch's duration to whole minutes.
func ovs2Minutes(s ovs2Stretch) int { return int(s.to.Sub(s.from).Minutes()) }

// ovs2Items resolves event references back to timeline items so anchors can be
// built for them.
func ovs2Items(c *engine.Context, refs map[model.EventRef]bool) []engine.Item {
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
