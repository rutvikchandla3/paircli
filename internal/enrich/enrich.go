package enrich

import (
	"fmt"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// Apply folds c.Judgments into signals and returns the result: a fresh slice in
// which every signal the judgments touch is a new value, so neither the
// caller's slice nor anything reachable from it is modified. A nil context, or
// a nil c.Judgments (the LLM pass is off, or the judge did not run), returns
// the signals unchanged.
func Apply(c *engine.Context, signals []model.Signal) []model.Signal {
	out := append([]model.Signal(nil), signals...)
	if c == nil || c.Judgments == nil {
		return out
	}
	e := &enricher{c: c, j: c.Judgments}

	e.askSummary(out)
	e.planDrift(out)
	e.ruleViolations(out)
	e.corrections(out)
	e.abandoned(out)
	e.lostConstraints(out)
	e.caveats(out)
	return out
}

// enricher carries the context and judgments one Apply call works from.
type enricher struct {
	c *engine.Context
	j *model.Judgments
}

// note records the judge's errors on a touched signal, and appends the
// inferred count to its summary when that keeps the summary within the
// 160-rune cap. added is the number of findings the row contributed.
func (e *enricher) note(out []model.Signal, i, added int) {
	sig := &out[i]
	sig.Data = cloneData(sig.Data)
	if len(e.j.Errors) > 0 {
		if sig.Data == nil {
			sig.Data = map[string]any{}
		}
		sig.Data["llm_errors"] = append([]string(nil), e.j.Errors...)
	}
	if added > 0 {
		sig.Summary = withInferredSuffix(sig.Summary, added)
	}
}

// askSummary prepends the condensed ask to INT-1 and records it under
// Data["ask_summary"].
func (e *enricher) askSummary(out []model.Signal) {
	a := e.j.AskSummary
	if a == nil || a.Ask == "" {
		return
	}
	i := indexOf(out, "INT-1")
	if i < 0 {
		return
	}
	sig := &out[i]
	finding := model.Finding{
		Summary:    "Ask (condensed): " + model.Clip(a.Ask, 120),
		Severity:   model.StateInfo,
		Provenance: model.Inferred,
		Evidence:   Evidence(e.c, a.Cites),
	}
	findings := make([]model.Finding, 0, len(sig.Findings)+1)
	findings = append(findings, finding)
	findings = append(findings, sig.Findings...)
	sig.Findings = findings

	sig.Data = cloneData(sig.Data)
	if sig.Data == nil {
		sig.Data = map[string]any{}
	}
	sig.Data["ask_summary"] = *a
	e.note(out, i, 1)
}

// planDrift adds one finding per INT-3 drift item and alerts the signal.
func (e *enricher) planDrift(out []model.Signal) {
	items := e.j.PlanDrift
	if len(items) == 0 {
		return
	}
	i := indexOf(out, "INT-3")
	if i < 0 {
		return
	}
	sig := &out[i]
	before := len(sig.Findings)
	sig.Findings = ownFindings(sig.Findings)
	for _, d := range items {
		var text string
		switch d.Kind {
		case "missing_step":
			text = "Planned step with no matching change: " + d.Text
		case "unplanned_change":
			text = "Change not in the plan: " + fileLines(d.File, d.Lines)
		default:
			continue
		}
		sig.Findings = append(sig.Findings, model.Finding{
			Summary:    text,
			Severity:   model.StateAlert,
			Provenance: model.Inferred,
			Evidence:   Evidence(e.c, d.Cites),
		})
	}
	sig.State = model.StateAlert
	e.note(out, i, len(sig.Findings)-before)
}

// ruleViolations adds one alert finding per INT-4 rule violation and alerts
// the signal.
func (e *enricher) ruleViolations(out []model.Signal) {
	items := e.j.RuleViolations
	if len(items) == 0 {
		return
	}
	i := indexOf(out, "INT-4")
	if i < 0 {
		return
	}
	sig := &out[i]
	before := len(sig.Findings)
	sig.Findings = ownFindings(sig.Findings)
	for _, rv := range items {
		sig.Findings = append(sig.Findings, model.Finding{
			Summary:    fmt.Sprintf("%s may break the rule “%s”.", fileLines(rv.File, rv.Lines), model.Clip(rv.Rule, 80)),
			Severity:   model.StateAlert,
			Provenance: model.Inferred,
			Evidence:   Evidence(e.c, rv.Cites),
		})
	}
	sig.State = model.StateAlert
	e.note(out, i, len(sig.Findings)-before)
}

// corrections folds the DEC-1 classifications into the candidate correction
// findings: a rejected candidate drops to info and is marked
// Data["confirmed"]=false, a confirmed one is marked true and gains its
// explanation, and the signal's state and summary are recomputed from the
// candidates that are confirmed or were never reviewed.
func (e *enricher) corrections(out []model.Signal) {
	items := e.j.Corrections
	if len(items) == 0 {
		return
	}
	i := indexOf(out, "DEC-1")
	if i < 0 {
		return
	}
	sig := &out[i]
	sig.Findings = ownFindings(sig.Findings)

	// Only "correction_prompt" findings are candidates (the kind internal/detect
	// /decisions marks Data["candidate"] on); an interrupt, rejection or denial
	// is a recorded event whatever the judge says about it, so a verdict on one
	// never removes it from the recomputed counts.
	dismissed := map[int]bool{}
	for _, corr := range items {
		idx := findFindingByEvent(sig.Findings, corr.Event)
		if idx < 0 {
			continue
		}
		f := &sig.Findings[idx]
		f.Data = cloneData(f.Data)
		if f.Data == nil {
			f.Data = map[string]any{}
		}
		if corr.IsCorrection {
			f.Data["confirmed"] = true
			if corr.About != "" {
				f.Summary += fmt.Sprintf(" (%s)", corr.About)
			}
			continue
		}
		f.Severity = model.StateInfo
		f.Data["confirmed"] = false
		if findingKind(*f) == "correction_prompt" {
			dismissed[idx] = true
		}
	}
	e.recountCorrections(sig, dismissed)
	e.note(out, i, 0)
}

// recountCorrections rewrites DEC-1's state and summary from the candidates
// that survive review, in the wording internal/detect/decisions uses. The
// counts backing that summary are rewritten with it.
func (e *enricher) recountCorrections(sig *model.Signal, dismissed map[int]bool) {
	var interrupts, rejections, denials, corrections, touching int
	for i, f := range sig.Findings {
		if dismissed[i] {
			continue
		}
		switch findingKind(f) {
		case "interrupt":
			interrupts++
		case "rejection":
			rejections++
		case "denial":
			denials++
		case "correction_prompt":
			corrections++
		}
		if len(f.Anchors) > 0 {
			touching++
		}
	}

	sig.Data = cloneData(sig.Data)
	if sig.Data == nil {
		sig.Data = map[string]any{}
	}
	sig.Data["correction_prompts"] = corrections
	sig.Data["touching_pr"] = touching

	n := interrupts + rejections + denials + corrections
	if n == 0 {
		sig.State = model.StateClear
		sig.Summary = "No interrupts, rejections or corrections."
		return
	}
	var parts []string
	if interrupts > 0 {
		parts = append(parts, engine.Plural(interrupts, "interrupt", "interrupts"))
	}
	if rejections > 0 {
		parts = append(parts, engine.Plural(rejections, "rejection", "rejections"))
	}
	if corrections > 0 {
		parts = append(parts, engine.Plural(corrections, "correction prompt", "correction prompts"))
	}
	if len(parts) > 0 {
		sig.Summary = fmt.Sprintf("%s (%s); %s.",
			engine.Plural(n, "human correction", "human corrections"),
			strings.Join(parts, ", "),
			engine.Plural(touching, "touched PR line", "touched PR lines"))
	} else {
		sig.Summary = fmt.Sprintf("%s; %s.",
			engine.Plural(n, "human correction", "human corrections"),
			engine.Plural(touching, "touched PR line", "touched PR lines"))
	}
	if touching > 0 {
		sig.State = model.StateAlert
	} else {
		sig.State = model.StateInfo
	}
}

// abandoned attaches each DEC-2 summary to the finding it belongs to, or adds
// a new info finding when no finding matches.
func (e *enricher) abandoned(out []model.Signal) {
	items := e.j.Abandoned
	if len(items) == 0 {
		return
	}
	i := indexOf(out, "DEC-2")
	if i < 0 {
		return
	}
	sig := &out[i]
	sig.Findings = ownFindings(sig.Findings)

	added := 0
	for _, a := range items {
		if idx := findFindingByEvents(sig.Findings, a.Events); idx >= 0 {
			f := &sig.Findings[idx]
			f.Data = cloneData(f.Data)
			if f.Data == nil {
				f.Data = map[string]any{}
			}
			f.Data["summary"] = a.Summary
			continue
		}
		sig.Findings = append(sig.Findings, model.Finding{
			Summary:    a.Summary,
			Severity:   model.StateInfo,
			Provenance: model.Inferred,
			Evidence:   Evidence(e.c, a.Cites),
		})
		added++
	}
	e.note(out, i, added)
}

// lostConstraints adds one alert finding per FRI-3 lost constraint and alerts
// the signal.
func (e *enricher) lostConstraints(out []model.Signal) {
	items := e.j.LostConstraints
	if len(items) == 0 {
		return
	}
	i := indexOf(out, "FRI-3")
	if i < 0 {
		return
	}
	sig := &out[i]
	sig.Findings = ownFindings(sig.Findings)
	for _, l := range items {
		sig.Findings = append(sig.Findings, model.Finding{
			Summary:    fmt.Sprintf("Constraint missing after compaction: “%s”", model.Clip(l.Constraint, 100)),
			Severity:   model.StateAlert,
			Provenance: model.Inferred,
			Evidence:   Evidence(e.c, l.Cites),
		})
	}
	sig.State = model.StateAlert
	e.note(out, i, len(items))
}

// caveats adds one info finding per caveat the agent stated to CON-2.
func (e *enricher) caveats(out []model.Signal) {
	items := e.j.Caveats
	if len(items) == 0 {
		return
	}
	i := indexOf(out, "CON-2")
	if i < 0 {
		return
	}
	sig := &out[i]
	sig.Findings = ownFindings(sig.Findings)
	for _, cv := range items {
		sig.Findings = append(sig.Findings, model.Finding{
			Summary:    fmt.Sprintf("Agent noted: “%s”", model.Clip(cv.Text, 120)),
			Severity:   model.StateInfo,
			Provenance: model.Inferred,
			Evidence:   Evidence(e.c, cv.Cites),
		})
	}
	e.note(out, i, len(items))
}

// indexOf returns the index of the signal with id in sigs, or -1.
func indexOf(sigs []model.Signal, id string) int {
	for i := range sigs {
		if sigs[i].ID == id {
			return i
		}
	}
	return -1
}

// ownFindings returns findings backed by a fresh array, so appending to the
// result never writes into the array the caller handed us.
func ownFindings(findings []model.Finding) []model.Finding {
	return append(make([]model.Finding, 0, len(findings)+1), findings...)
}

// cloneData copies a finding or signal Data map, so writing to the copy never
// touches the map the caller handed us. A nil map stays nil.
func cloneData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	out := make(map[string]any, len(data)+1)
	for k, v := range data {
		out[k] = v
	}
	return out
}

// findingKind returns Finding.Data["kind"], or "".
func findingKind(f model.Finding) string {
	s, _ := f.Data["kind"].(string)
	return s
}

// findFindingByEvent returns the index of the finding whose first evidence is
// the given event, or -1.
func findFindingByEvent(findings []model.Finding, ref model.EventRef) int {
	for i, f := range findings {
		if len(f.Evidence) == 0 {
			continue
		}
		if f.Evidence[0].Session == ref.Session && f.Evidence[0].Event == ref.Event {
			return i
		}
	}
	return -1
}

// findFindingByEvents returns the index of the finding whose first evidence is
// one of refs, or -1.
func findFindingByEvents(findings []model.Finding, refs []model.EventRef) int {
	for i, f := range findings {
		if len(f.Evidence) == 0 {
			continue
		}
		for _, ref := range refs {
			if f.Evidence[0].Session == ref.Session && f.Evidence[0].Event == ref.Event {
				return i
			}
		}
	}
	return -1
}

// fileLines renders a file, optionally with its line range, as the finding
// texts quote it: "`a.go` 10-12", or just "`a.go`" when there is no range.
func fileLines(file, lines string) string {
	if file == "" {
		return ""
	}
	s := "`" + file + "`"
	if lines != "" {
		s += " " + lines
	}
	return s
}

// withInferredSuffix appends " (+k inferred)" to summary, unless that would
// push it past the 160-rune cap on Signal.Summary.
func withInferredSuffix(summary string, k int) string {
	if k <= 0 {
		return summary
	}
	suffix := fmt.Sprintf(" (+%d inferred)", k)
	if len([]rune(summary+suffix)) > 160 {
		return summary
	}
	return summary + suffix
}
