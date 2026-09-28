package oversight

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type ovs1 struct{}

func init() { engine.Register(ovs1{}) }

func (ovs1) ID() string { return "OVS-1" }

// Postures a tool action can run under. "unknown" means no mode had been
// recorded yet at the time of the call.
const (
	ovsPostureUnapproved = "unapproved"
	ovsPosturePossible   = "approval_possible"
	ovsPostureAuto       = "auto_approved"
	ovsPostureUnknown    = "unknown"
)

// ovsIsToolAction reports whether e counts as a tool action for T19: a
// command (not ByUser), file_edit, mcp_call, tool_call, lookup or subagent
// event, from the main agent or a subagent.
func ovsIsToolAction(e *model.Event) bool {
	switch e.Kind {
	case model.KindCommand:
		return e.Command != nil && !e.Command.ByUser
	case model.KindEdit, model.KindMCP, model.KindToolCall, model.KindLookup, model.KindSubagent:
		return true
	}
	return false
}

// ovs1Posture classifies one tool action given the raw mode in force.
func ovs1Posture(h model.Harness, mode *model.Mode, kind model.EventKind) string {
	switch h {
	case model.HarnessPi:
		// Pi has no permission layer at all.
		return ovsPostureUnapproved
	case model.HarnessClaudeCode:
		if mode == nil {
			return ovsPostureUnknown
		}
		switch mode.Permission {
		case model.PermBypass:
			return ovsPostureUnapproved
		case model.PermAcceptEdits:
			// Edits are auto-approved; every other tool still prompts.
			if kind == model.KindEdit {
				return ovsPostureUnapproved
			}
			return ovsPosturePossible
		case model.PermAuto:
			return ovsPostureAuto
		case model.PermAsk, model.PermPlan:
			return ovsPosturePossible
		}
		return ovsPostureUnknown
	case model.HarnessCodex:
		if mode == nil {
			return ovsPostureUnknown
		}
		switch mode.Approval {
		case "never":
			return ovsPostureUnapproved
		case "on-request", "on-failure", "untrusted":
			return ovsPosturePossible
		}
		return ovsPostureUnknown
	}
	return ovsPostureUnknown
}

// ovs1Session accumulates one session's autonomy envelope.
type ovs1Session struct {
	ref       string
	harness   model.Harness
	toolCalls int
	counts    map[string]int  // posture -> count
	modes     map[string]bool // distinct raw permission modes
	pairs     map[string]int  // Codex "approval\x00sandbox" -> count
	prompts   int             // permission events with Decision "ask"
}

// ovs1RawModes returns the session's distinct raw permission modes, sorted.
func (s *ovs1Session) ovs1RawModes() string {
	if len(s.modes) == 0 {
		return ""
	}
	out := make([]string, 0, len(s.modes))
	for m := range s.modes {
		out = append(out, m)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// ovs1TopPair returns the most common Codex (approval, sandbox) pair, with
// ties broken lexicographically so output is deterministic.
func (s *ovs1Session) ovs1TopPair() (string, string) {
	bestKey, bestCount := "", -1
	for k, n := range s.pairs {
		if n > bestCount || (n == bestCount && k < bestKey) {
			bestKey, bestCount = k, n
		}
	}
	if bestKey == "" {
		return "", ""
	}
	approval, sandbox, _ := strings.Cut(bestKey, "\x00")
	return approval, sandbox
}

func (d ovs1) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	stats := make(map[string]*ovs1Session, len(c.Sessions))
	for _, s := range c.Sessions {
		stats[s.Ref()] = &ovs1Session{
			ref:     s.Ref(),
			harness: s.Harness,
			counts:  map[string]int{},
			modes:   map[string]bool{},
			pairs:   map[string]int{},
		}
	}

	// Current mode per session; nil until the first mode_change is seen.
	current := make(map[string]*model.Mode, len(c.Sessions))

	for _, it := range c.Timeline {
		st := stats[it.S.Ref()]
		if st == nil {
			continue
		}
		e := it.E

		if e.Kind == model.KindMode && e.AgentID == "" && e.Mode != nil {
			current[it.S.Ref()] = e.Mode
			if e.Mode.RawPermission != "" {
				st.modes[e.Mode.RawPermission] = true
			}
		}

		if e.Kind == model.KindPermission && e.Permission != nil && e.Permission.Decision == "ask" {
			st.prompts++
		}

		if !ovsIsToolAction(e) {
			continue
		}
		st.toolCalls++
		mode := current[it.S.Ref()]
		st.counts[ovs1Posture(st.harness, mode, e.Kind)]++
		// The Codex finding reports the pair that governed the most tool calls.
		if mode != nil && (mode.Approval != "" || mode.Sandbox != "") {
			st.pairs[mode.Approval+"\x00"+mode.Sandbox]++
		}
	}

	// Findings, one per session with tool calls, in linked-session order.
	totals := map[string]int{}
	for _, s := range c.Sessions {
		st := stats[s.Ref()]
		totals["tool_calls"] += st.toolCalls
		totals["unapproved"] += st.counts[ovsPostureUnapproved]
		totals["auto_approved"] += st.counts[ovsPostureAuto]
		totals["approval_possible"] += st.counts[ovsPosturePossible]
		totals["unknown"] += st.counts[ovsPostureUnknown]
		totals["prompts_shown"] += st.prompts

		if st.toolCalls == 0 {
			continue
		}

		var summary string
		switch st.harness {
		case model.HarnessPi:
			summary = fmt.Sprintf("`%s`: no permission layer; %d tool calls.", st.ref, st.toolCalls)
		case model.HarnessClaudeCode:
			summary = fmt.Sprintf("`%s`: %s of %d tool calls ran with no approval step",
				st.ref, engine.Pct(st.counts[ovsPostureUnapproved], st.toolCalls), st.toolCalls)
			if modes := st.ovs1RawModes(); modes != "" {
				summary += " (modes: " + modes + ")"
			}
			summary += "."
		case model.HarnessCodex:
			approval, sandbox := st.ovs1TopPair()
			if approval == "" {
				approval = "unknown"
			}
			if sandbox == "" {
				sandbox = "unknown"
			}
			summary = fmt.Sprintf("`%s`: approvals %s, sandbox %s for %s of %d tool calls.",
				st.ref, approval, sandbox, engine.Pct(st.counts[ovsPostureUnapproved], st.toolCalls), st.toolCalls)
		default:
			continue
		}

		sig.Findings = append(sig.Findings, model.Finding{
			Summary:  summary,
			Severity: model.StateInfo,
			Data: map[string]any{
				"session":           st.ref,
				"tool_calls":        st.toolCalls,
				"unapproved":        st.counts[ovsPostureUnapproved],
				"auto_approved":     st.counts[ovsPostureAuto],
				"approval_possible": st.counts[ovsPosturePossible],
				"unknown":           st.counts[ovsPostureUnknown],
			},
		})
	}

	summary := fmt.Sprintf("%s of %d tool calls ran with no approval step.",
		engine.Pct(totals["unapproved"], totals["tool_calls"]), totals["tool_calls"])
	if totals["prompts_shown"] > 0 {
		summary += fmt.Sprintf(" %d permission prompts were shown.", totals["prompts_shown"])
	}

	sessions := make([]map[string]any, 0, len(c.Sessions))
	for _, s := range c.Sessions {
		st := stats[s.Ref()]
		_, sandbox := st.ovs1TopPair()
		sessions = append(sessions, map[string]any{
			"ref":        st.ref,
			"modes":      st.ovs1RawModes(),
			"sandbox":    sandbox,
			"unapproved": st.counts[ovsPostureUnapproved],
			"total":      st.toolCalls,
		})
	}

	sig.State = model.StateInfo
	sig.Summary = summary
	sig.Data = map[string]any{
		"tool_calls":        totals["tool_calls"],
		"unapproved":        totals["unapproved"],
		"auto_approved":     totals["auto_approved"],
		"approval_possible": totals["approval_possible"],
		"unknown":           totals["unknown"],
		"prompts_shown":     totals["prompts_shown"],
		"sessions":          sessions,
	}
	return sig
}
