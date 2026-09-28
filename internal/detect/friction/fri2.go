package friction

import (
	"fmt"
	"sort"
	"time"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type fri2 struct{}

func init() { engine.Register(fri2{}) }

func (fri2) ID() string { return "FRI-2" }

func (d fri2) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	var loops []*loopFinding
	var unresolvedChecks []*unresolvedCheckFinding
	var timeouts []*timeoutFinding
	var failures []*failureFinding

	// First pass: find loops in each session. A loop is a run of >=3
	// consecutive failing executions of the same normalized command, still
	// unbroken (no success of it yet) at session end.
	for _, session := range c.Sessions {
		sessionCmdFailures := make(map[string][]*cmdFailureEvent) // cmd -> current run

		for i, e := range session.Events {
			if e.Kind != model.KindCommand || e.Command == nil {
				continue
			}

			cmd := e.Command
			normalized := classify.ShortCmd(cmd.Cmd)

			if cmd.Status == model.CmdFailed {
				sessionCmdFailures[normalized] = append(sessionCmdFailures[normalized], &cmdFailureEvent{ts: e.TS, e: &session.Events[i]})
			} else if cmd.Status == model.CmdOK {
				sessionCmdFailures[normalized] = nil
			}
		}

		// Report whatever run is still active (unbroken) at session end.
		// Map iteration order is nondeterministic, so collect then sort.
		var sessionLoops []*loopFinding
		for normalized, run := range sessionCmdFailures {
			if len(run) >= 3 {
				sessionLoops = append(sessionLoops, &loopFinding{
					cmd:        normalized,
					normalized: normalized,
					count:      len(run),
					first:      run[0].e,
					last:       run[len(run)-1].e,
					session:    session,
				})
			}
		}
		sort.Slice(sessionLoops, func(i, j int) bool { return sessionLoops[i].normalized < sessionLoops[j].normalized })
		loops = append(loops, sessionLoops...)
	}

	// Second pass: find timeouts and harness failures
	for _, it := range c.Timeline {
		if it.E.Kind == model.KindCommand && it.E.Command != nil {
			if it.E.Command.Status == model.CmdTimeout {
				timeouts = append(timeouts, &timeoutFinding{
					cmd: classify.ShortCmd(it.E.Command.Cmd),
					e:   it.E,
					s:   it.S,
				})
			}
		}
		if it.E.Kind == model.KindFailure {
			failures = append(failures, &failureFinding{
				e: it.E,
				s: it.S,
			})
		}
	}

	// Third pass: find unresolved checks
	// A check that failed and never succeeded in any session
	seenCheckCommands := make(map[string]*checkCmdStatus) // normalized cmd -> status

	for _, it := range c.Timeline {
		if it.E.Kind != model.KindCommand || it.E.Command == nil {
			continue
		}

		cmd := it.E.Command
		normalized := classify.ShortCmd(cmd.Cmd)
		classes := classify.Command(cmd.Cmd, c.Config.ExtraChecks)
		_, isCheck := classify.CheckClass(classes)

		if !isCheck {
			continue
		}

		status := seenCheckCommands[normalized]
		if status == nil {
			status = &checkCmdStatus{cmd: normalized}
			seenCheckCommands[normalized] = status
		}

		if cmd.Status == model.CmdOK {
			status.succeeded = true
			status.lastSuccess = it.E
		} else if cmd.Status == model.CmdFailed {
			if status.firstFail == nil {
				status.firstFail = it.E
			}
			status.lastFail = it.E
		}
	}

	// Unresolved = failed but never succeeded. Map iteration order is
	// nondeterministic, so sort by normalized command before appending.
	unresolvedCmds := make([]string, 0, len(seenCheckCommands))
	for cmd := range seenCheckCommands {
		unresolvedCmds = append(unresolvedCmds, cmd)
	}
	sort.Strings(unresolvedCmds)
	for _, cmd := range unresolvedCmds {
		status := seenCheckCommands[cmd]
		if status.firstFail != nil && !status.succeeded {
			unresolvedChecks = append(unresolvedChecks, &unresolvedCheckFinding{
				cmd: status.cmd,
				e:   status.lastFail,
			})
		}
	}

	// Generate findings for loops
	for _, l := range loops {
		finding := model.Finding{
			Summary:  fmt.Sprintf("`%s` failed %d times in a row (%s–%s).", l.cmd, l.count, engine.Clock(l.first.TS), engine.Clock(l.last.TS)),
			Severity: model.StateAlert,
			Evidence: []model.Evidence{
				c.Evidence(engine.Item{S: l.session, E: l.first}, l.cmd),
			},
			Data: map[string]any{
				"cmd":   l.cmd,
				"count": l.count,
			},
		}
		sig.Findings = append(sig.Findings, finding)
	}

	// Generate findings for unresolved checks
	seenUnresolved := make(map[string]bool)
	for _, u := range unresolvedChecks {
		if seenUnresolved[u.cmd] {
			continue
		}
		seenUnresolved[u.cmd] = true

		// Find the session for this event
		var foundSession *model.Session
		for _, it := range c.Timeline {
			if it.E.ID == u.e.ID {
				foundSession = it.S
				break
			}
		}

		evidence := []model.Evidence{}
		if foundSession != nil {
			evidence = []model.Evidence{
				c.Evidence(engine.Item{S: foundSession, E: u.e}, u.cmd),
			}
		}

		finding := model.Finding{
			Summary:  fmt.Sprintf("`%s` was still failing at the end (%s).", u.cmd, engine.Clock(u.e.TS)),
			Severity: model.StateAlert,
			Evidence: evidence,
			Data: map[string]any{
				"cmd": u.cmd,
			},
		}
		sig.Findings = append(sig.Findings, finding)
	}

	// Generate findings for timeouts
	for _, t := range timeouts {
		finding := model.Finding{
			Summary:  fmt.Sprintf("`%s` timed out at %s.", t.cmd, engine.Clock(t.e.TS)),
			Severity: model.StateInfo,
			Evidence: []model.Evidence{
				c.Evidence(engine.Item{S: t.s, E: t.e}, t.cmd),
			},
			Data: map[string]any{
				"cmd": t.cmd,
			},
		}
		sig.Findings = append(sig.Findings, finding)
	}

	// Generate findings for harness failures
	for _, f := range failures {
		finding := model.Finding{
			Summary:  fmt.Sprintf("Turn cut short by %s at %s.", f.e.Failure.Type, engine.Clock(f.e.TS)),
			Severity: model.StateInfo,
			Evidence: []model.Evidence{
				c.Evidence(engine.Item{S: f.s, E: f.e}, f.e.Failure.Type),
			},
			Data: map[string]any{
				"type": f.e.Failure.Type,
			},
		}
		sig.Findings = append(sig.Findings, finding)
	}

	// Determine state and summary
	loopCount := len(loops)
	unresolvedCount := len(seenUnresolved)
	timeoutCount := len(timeouts)
	failureCount := len(failures)

	if loopCount == 0 && unresolvedCount == 0 && timeoutCount == 0 && failureCount == 0 {
		sig.State = model.StateClear
		sig.Summary = "No repeated failures, unresolved check failures or timeouts."
	} else {
		parts := []string{}
		if loopCount > 0 {
			parts = append(parts, fmt.Sprintf("%d repeated failures", loopCount))
		}
		if unresolvedCount > 0 {
			parts = append(parts, fmt.Sprintf("%d checks still failing at the end", unresolvedCount))
		}
		if timeoutCount > 0 {
			parts = append(parts, fmt.Sprintf("%d timeouts", timeoutCount))
		}

		if loopCount > 0 || unresolvedCount > 0 {
			sig.State = model.StateAlert
		} else {
			sig.State = model.StateInfo
		}

		summary := ""
		for i, p := range parts {
			if i > 0 {
				summary += ", "
			}
			summary += p
		}
		sig.Summary = summary + "."
	}

	sig.Data = map[string]any{
		"loops":      loopCount,
		"unresolved": unresolvedCount,
		"timeouts":   timeoutCount,
		"failures":   failureCount,
	}

	return sig
}

type cmdFailureEvent struct {
	ts time.Time
	e  *model.Event
}

type loopFinding struct {
	cmd        string
	normalized string
	count      int
	first      *model.Event
	last       *model.Event
	session    *model.Session
}

type unresolvedCheckFinding struct {
	cmd string
	e   *model.Event
}

type timeoutFinding struct {
	cmd string
	e   *model.Event
	s   *model.Session
}

type failureFinding struct {
	e *model.Event
	s *model.Session
}

type checkCmdStatus struct {
	cmd         string
	firstFail   *model.Event
	lastFail    *model.Event
	lastSuccess *model.Event
	succeeded   bool
}
