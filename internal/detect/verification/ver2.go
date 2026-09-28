package verification

import (
	"fmt"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() {
	engine.Register(ver2{})
}

type ver2 struct{}

func (ver2) ID() string { return "VER-2" }

// ver2RefClass returns the reference class for VER-2: test, if any test run
// exists; else the first of typecheck, build, lint that has runs.
func ver2RefClass(runs []checkRun) classify.CmdClass {
	for _, r := range runs {
		if r.Class == classify.Test {
			return classify.Test
		}
	}
	for _, cl := range []classify.CmdClass{classify.Typecheck, classify.Build, classify.Lint} {
		for _, r := range runs {
			if r.Class == cl {
				return cl
			}
		}
	}
	return ""
}

func runsOfClass(runs []checkRun, class classify.CmdClass) []checkRun {
	var out []checkRun
	for _, r := range runs {
		if r.Class == class {
			out = append(out, r)
		}
	}
	return out
}

// lastPassOf returns the latest (timeline-order) run in runs with a passing
// result.
func lastPassOf(runs []checkRun) (checkRun, bool) {
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Result.Status == "pass" {
			return runs[i], true
		}
	}
	return checkRun{}, false
}

func (ver2) Detect(c *engine.Context) model.Signal {
	sig := engine.NewSignal("VER-2")

	// Case 1: no sessions.
	if !c.HasSessions() {
		return engine.NoSessions("VER-2")
	}

	shaping := shapingItems(c)

	// Case 2: no PR lines came from captured sessions.
	if len(shaping) == 0 {
		return engine.Unknown("VER-2", "No PR lines came from captured sessions.")
	}

	runs := checkRuns(c)
	refClass := ver2RefClass(runs)
	u := uncapturedCount(c.Attribution)

	addUncapturedFinding := func(findings []model.Finding) []model.Finding {
		if u <= 0 {
			return findings
		}
		return append(findings, model.Finding{
			Summary:  fmt.Sprintf("%d changed lines were written outside captured sessions, so when they were written relative to checks is unknown.", u),
			Severity: model.StateInfo,
		})
	}

	// Case 3: no check runs at all.
	if refClass == "" {
		sig.State = model.StateAlert
		sig.Summary = "No test, lint, typecheck or build ran after the agent's edits in any captured session."

		groups := groupByFile(c, shaping)
		if len(groups) > 20 {
			groups = groups[:20]
		}
		var findings []model.Finding
		for _, g := range groups {
			findings = append(findings, model.Finding{
				Summary:  fmt.Sprintf("`%s` was never checked in a captured session.", g.path),
				Severity: model.StateAlert,
				Anchors:  c.AnchorsFor(g.items...),
				Data:     map[string]any{"never_verified": true},
			})
		}
		findings = addUncapturedFinding(findings)
		sig.Findings = findings
		sig.Data = map[string]any{
			"class":            "",
			"last_pass":        nil,
			"stale_events":     0,
			"stale_hunks":      0,
			"never_verified":   true,
			"uncaptured_lines": u,
		}
		return sig
	}

	classRuns := runsOfClass(runs, refClass)
	last, hasPass := lastPassOf(classRuns)

	// Case 4: reference class has runs but none passed.
	if !hasPass {
		lastRun := classRuns[len(classRuns)-1]
		sig.State = model.StateAlert
		sig.Summary = fmt.Sprintf("No passing %s run in captured sessions; the last one failed at %s.", refClass, engine.Clock(lastRun.It.E.TS))
		findings := []model.Finding{{
			Summary:  fmt.Sprintf("`%s` %s at %s.", lastRun.Cmd, lastRun.Result.Status, engine.Clock(lastRun.It.E.TS)),
			Severity: model.StateAlert,
			Evidence: []model.Evidence{c.Evidence(lastRun.It, evidenceExcerpt(lastRun.It))},
		}}
		findings = addUncapturedFinding(findings)
		sig.Findings = findings
		sig.Data = map[string]any{
			"class":            string(refClass),
			"last_pass":        nil,
			"stale_events":     0,
			"stale_hunks":      0,
			"never_verified":   false,
			"uncaptured_lines": u,
		}
		return sig
	}

	// Case 5: there is a last passing run of the reference class.
	var stale []engine.Item
	for _, it := range shaping {
		if it.E.TS.After(last.It.E.TS) {
			stale = append(stale, it)
		}
	}

	lastPassData := map[string]any{
		"session": last.It.S.Ref(),
		"event":   last.It.E.ID,
		"ts":      last.It.E.TS,
		"cmd":     last.Cmd,
	}

	if len(stale) == 0 {
		sig.State = model.StateClear
		sig.Summary = fmt.Sprintf("The last passing %s run (`%s`, %s) came after every captured edit to PR lines.", refClass, last.Cmd, engine.Clock(last.It.E.TS))
		findings := addUncapturedFinding(nil)
		sig.Findings = findings
		sig.Data = map[string]any{
			"class":            string(refClass),
			"last_pass":        lastPassData,
			"stale_events":     0,
			"stale_hunks":      0,
			"never_verified":   false,
			"uncaptured_lines": u,
		}
		return sig
	}

	anchors := c.AnchorsFor(stale...)
	h := len(anchors)

	sig.State = model.StateAlert
	sig.Summary = fmt.Sprintf("%d PR hunks were edited after the last passing %s run (%s).", h, refClass, engine.Clock(last.It.E.TS))

	groups := groupByFile(c, stale)
	var findings []model.Finding
	for i, g := range groups {
		fileAnchors := c.AnchorsFor(g.items...)
		var ranges []string
		for _, a := range fileAnchors {
			ranges = append(ranges, a.Lines)
		}
		latest := g.items[0].E.TS
		for _, it := range g.items {
			if it.E.TS.After(latest) {
				latest = it.E.TS
			}
		}

		evItems := g.items
		if len(evItems) > 3 {
			evItems = evItems[:3]
		}
		var evidence []model.Evidence
		if i == 0 {
			evidence = append(evidence, c.Evidence(last.It, evidenceExcerpt(last.It)))
		}
		for _, it := range evItems {
			evidence = append(evidence, c.Evidence(it, evidenceExcerpt(it)))
		}

		findings = append(findings, model.Finding{
			Summary:  fmt.Sprintf("`%s` lines %s changed at %s, after the last passing run.", g.path, strings.Join(ranges, ", "), engine.Clock(latest)),
			Severity: model.StateAlert,
			Anchors:  fileAnchors,
			Evidence: evidence,
		})
	}
	findings = addUncapturedFinding(findings)
	sig.Findings = findings
	sig.Data = map[string]any{
		"class":            string(refClass),
		"last_pass":        lastPassData,
		"stale_events":     len(stale),
		"stale_hunks":      h,
		"never_verified":   false,
		"uncaptured_lines": u,
	}
	return sig
}
