package verification

import (
	"fmt"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() {
	engine.Register(ver1{})
}

type ver1 struct{}

func (ver1) ID() string { return "VER-1" }

// ver1Key groups check runs by (class, short command).
type ver1Key struct {
	class classify.CmdClass
	cmd   string
}

func (ver1) Detect(c *engine.Context) model.Signal {
	sig := engine.NewSignal("VER-1")
	if !c.HasSessions() {
		return engine.NoSessions("VER-1")
	}

	runs := checkRuns(c)

	// Group by (Class, Cmd) in order of first appearance.
	var order []ver1Key
	groups := map[ver1Key][]checkRun{}
	for _, r := range runs {
		k := ver1Key{r.Class, r.Cmd}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}

	alert := false
	var findings []model.Finding
	for _, k := range order {
		grp := groups[k]
		last := grp[len(grp)-1]

		statuses := make([]string, 0, len(grp))
		for _, r := range grp {
			statuses = append(statuses, r.Result.Status)
		}
		truncated := false
		show := statuses
		if len(show) > 6 {
			show = show[len(show)-6:]
			truncated = true
		}
		statusStr := strings.Join(show, ", ")
		if truncated {
			statusStr = "…, " + statusStr
		}

		counts := last.Result.Status
		if last.Result.Passed >= 0 || last.Result.Failed >= 0 {
			p, f := last.Result.Passed, last.Result.Failed
			if p < 0 {
				p = 0
			}
			if f < 0 {
				f = 0
			}
			counts = fmt.Sprintf("%d passed, %d failed", p, f)
		}

		sev := model.StateInfo
		if last.Result.Status == "fail" {
			sev = model.StateAlert
			alert = true
		}

		findings = append(findings, model.Finding{
			Summary:  fmt.Sprintf("`%s` ×%d (%s) — last: %s", k.cmd, len(grp), statusStr, counts),
			Severity: sev,
			Evidence: []model.Evidence{c.Evidence(last.It, evidenceExcerpt(last.It))},
			Data: map[string]any{
				"class":       string(k.class),
				"runs":        len(grp),
				"last_status": last.Result.Status,
				"by_user":     last.It.E.Command.ByUser,
			},
		})
	}
	sig.Findings = findings

	if alert {
		sig.State = model.StateAlert
	} else {
		sig.State = model.StateInfo
	}

	// Summary and by_class data, over classes test, typecheck, lint, build.
	type classTally struct {
		n    int
		last string
	}
	byClass := map[classify.CmdClass]*classTally{}
	for _, r := range runs {
		ct, ok := byClass[r.Class]
		if !ok {
			ct = &classTally{}
			byClass[r.Class] = ct
		}
		ct.n++
		ct.last = r.Result.Status
	}

	if len(runs) == 0 {
		sig.Summary = "No test, lint, typecheck or build commands ran."
	} else {
		var parts []string
		for _, cl := range []classify.CmdClass{classify.Test, classify.Typecheck, classify.Lint, classify.Build} {
			ct, ok := byClass[cl]
			if !ok {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s ×%d (last %s)", cl, ct.n, ct.last))
		}
		sig.Summary = fmt.Sprintf("Checks ran %d times: %s", len(runs), strings.Join(parts, ", "))
	}

	dataByClass := map[string]any{}
	for cl, ct := range byClass {
		dataByClass[string(cl)] = map[string]any{"runs": ct.n, "last": ct.last}
	}
	var dataRuns []map[string]any
	for _, r := range runs {
		dataRuns = append(dataRuns, map[string]any{
			"class":   string(r.Class),
			"cmd":     r.Cmd,
			"session": r.It.S.Ref(),
			"event":   r.It.E.ID,
			"ts":      r.It.E.TS,
			"status":  r.Result.Status,
			"passed":  r.Result.Passed,
			"failed":  r.Result.Failed,
			"skipped": r.Result.Skipped,
			"by_user": r.It.E.Command.ByUser,
		})
	}
	sig.Data = map[string]any{
		"runs":     dataRuns,
		"by_class": dataByClass,
	}

	return sig
}
