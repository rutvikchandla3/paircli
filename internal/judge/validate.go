package judge

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// The raw reply shapes, one per job. They mirror the schemas in prompts.go.
type claimsReply struct {
	Claims []claimReply `json:"claims"`
}

type claimReply struct {
	Claim   string   `json:"claim"`
	Source  string   `json:"source"`
	Verdict string   `json:"verdict"`
	Reason  string   `json:"reason"`
	Cites   []string `json:"cites"`
}

type storyReply struct {
	AskSummary  *askSummaryReply  `json:"ask_summary"`
	Decisions   []decisionReply   `json:"decisions"`
	Abandoned   []abandonedReply  `json:"abandoned"`
	Corrections []correctionReply `json:"corrections"`
}

type askSummaryReply struct {
	Ask         string   `json:"ask"`
	Refinements []string `json:"refinements"`
	FinalScope  string   `json:"final_scope"`
	Cites       []string `json:"cites"`
}

type decisionReply struct {
	Choice string   `json:"choice"`
	Reason string   `json:"reason"`
	By     string   `json:"by"`
	Cites  []string `json:"cites"`
}

type abandonedReply struct {
	Events  []string `json:"events"`
	Summary string   `json:"summary"`
	Cites   []string `json:"cites"`
}

type correctionReply struct {
	Event        string   `json:"event"`
	IsCorrection bool     `json:"is_correction"`
	About        string   `json:"about"`
	Cites        []string `json:"cites"`
}

type scopeReply struct {
	Scope          []scopeItemReply     `json:"scope"`
	PlanDrift      []driftReply         `json:"plan_drift"`
	RuleViolations []ruleViolationReply `json:"rule_violations"`
}

type scopeItemReply struct {
	File    string   `json:"file"`
	Lines   string   `json:"lines"`
	Traced  bool     `json:"traced"`
	TraceTo string   `json:"trace_to"`
	Cites   []string `json:"cites"`
}

type driftReply struct {
	Kind  string   `json:"kind"`
	Text  string   `json:"text"`
	File  string   `json:"file"`
	Lines string   `json:"lines"`
	Cites []string `json:"cites"`
}

type ruleViolationReply struct {
	Rule  string   `json:"rule"`
	File  string   `json:"file"`
	Lines string   `json:"lines"`
	Cites []string `json:"cites"`
}

type caveatsReply struct {
	Caveats         []caveatReply         `json:"caveats"`
	LostConstraints []lostConstraintReply `json:"lost_constraints"`
}

type caveatReply struct {
	Text  string   `json:"text"`
	Cites []string `json:"cites"`
}

type lostConstraintReply struct {
	Constraint string   `json:"constraint"`
	Cites      []string `json:"cites"`
}

// Enum values accepted by the validator.
var (
	claimSources   = map[string]bool{"pr_body": true, "final_message": true}
	claimVerdicts  = map[string]bool{"supported": true, "contradicted": true, "no_evidence": true}
	decisionBys    = map[string]bool{"": true, "human": true, "agent": true}
	driftKinds     = map[string]bool{"missing_step": true, "unplanned_change": true}
	linesPattern   = regexp.MustCompile(`^\d+(-\d+)?$`)
	eventIDPattern = regexp.MustCompile(`^ev:(.+)/(.+)$`)
)

// Item kinds are the units Stats.Items counts. One job can offer items of more
// than one kind (the scope job feeds scope, plan_drift and rule_violations), so
// the accounting is per kind rather than per job.
const (
	kindClaims         = "claims"
	kindAskSummary     = "ask_summary"
	kindDecisions      = "decisions"
	kindAbandoned      = "abandoned"
	kindCorrections    = "corrections"
	kindScope          = "scope"
	kindPlanDrift      = "plan_drift"
	kindRuleViolations = "rule_violations"
	kindCaveats        = "caveats"
	kindLostConstraint = "lost_constraints"
)

// validator checks a reply against the fact bundle. Every item must cite at
// least one id that appears in the bundle; anything else is dropped.
type validator struct {
	ids map[string]bool
	ec  *engine.Context
	// stats is optional: a nil Stats means "do not count". Direct tests build
	// validators without it.
	stats *Stats
}

// attempted records that a job offered n items of one kind.
func (v *validator) attempted(kind string, n int) {
	if v.stats == nil || n == 0 {
		return
	}
	cur := v.stats.Items[kind]
	cur[0] += n
	v.stats.Items[kind] = cur
}

// kept records that one item of kind survived validation.
func (v *validator) kept(kind string) {
	if v.stats == nil {
		return
	}
	cur := v.stats.Items[kind]
	cur[1]++
	v.stats.Items[kind] = cur
}

// citesOK reports whether cites is non-empty and every id is in the bundle.
func (v *validator) citesOK(cites []string) bool {
	if len(cites) == 0 {
		return false
	}
	for _, c := range cites {
		if !v.ids[c] {
			return false
		}
	}
	return true
}

// eventRef converts a bundle event id to a model.EventRef.
func (v *validator) eventRef(id string) (model.EventRef, bool) {
	if !v.ids[id] {
		return model.EventRef{}, false
	}
	m := eventIDPattern.FindStringSubmatch(id)
	if m == nil {
		return model.EventRef{}, false
	}
	return model.EventRef{Session: m[1], Event: m[2]}, true
}

// eventRefs converts a list of bundle event ids, keeping the valid ones. An
// empty list is accepted; a non-empty list with no valid id at all is not.
func (v *validator) eventRefs(ids []string) ([]model.EventRef, bool) {
	if len(ids) == 0 {
		return nil, true
	}
	var out []model.EventRef
	for _, id := range ids {
		if ref, ok := v.eventRef(id); ok {
			out = append(out, ref)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// linesOK reports whether lines is empty or looks like "N" or "N-M".
func (v *validator) linesOK(lines string) bool {
	return lines == "" || linesPattern.MatchString(lines)
}

// hunkLinesOK reports whether file and lines name a range that lies inside a
// real hunk of the PR diff. An empty file or lines is not checked: a
// missing_step drift item legitimately carries neither.
//
// linesOK only checks the shape of the string, so without this a model could
// return a well-formed range that matches no hunk and still produce an anchor —
// one pointing at lines the PR never touched. The check is containment rather
// than equality against hunkID because the prompt allows a bare line ("13" for
// a hunk spanning 12-15) and because a deletion-only hunk has NewLines 0 and so
// a one-line span, which a bare number should still match.
//
// It reads ec.PR rather than the bundle's hunk ids on purpose: the bundle
// trims whole files away as its budget shrinks, so validating against it would
// make the answer depend on how big the prompt happened to be.
func (v *validator) hunkLinesOK(file, lines string) bool {
	if file == "" || lines == "" {
		return true
	}
	lo, hi, ok := parseLines(lines)
	if !ok {
		return false
	}
	var f *model.DiffFile
	if v.ec != nil && v.ec.PR != nil {
		f = v.ec.PR.File(file)
	}
	if f == nil {
		return false
	}
	for _, h := range f.Hunks {
		start := h.NewStart
		end := start + h.NewLines - 1
		if end < start {
			end = start
		}
		if lo >= start && hi <= end {
			return true
		}
	}
	return false
}

// parseLines parses "N" or "N-M" into an inclusive range. It is called after
// linesOK, so the shape is already known to be one of those two.
func parseLines(lines string) (int, int, bool) {
	if i := strings.IndexByte(lines, '-'); i >= 0 {
		lo, errLo := strconv.Atoi(lines[:i])
		hi, errHi := strconv.Atoi(lines[i+1:])
		if errLo != nil || errHi != nil || hi < lo {
			return 0, 0, false
		}
		return lo, hi, true
	}
	n, err := strconv.Atoi(lines)
	if err != nil {
		return 0, 0, false
	}
	return n, n, true
}

// isPRFile reports whether f names a file in the PR diff.
func (v *validator) isPRFile(f string) bool {
	if f == "" || v.ec == nil || v.ec.PR == nil {
		return false
	}
	return v.ec.IsPRFile(f)
}

// decode strips a surrounding Markdown code fence and unmarshals text into v.
func decode(text string, v any) bool {
	t := stripFence(text)
	if t == "" {
		return false
	}
	return json.Unmarshal([]byte(t), v) == nil
}

// stripFence removes a surrounding ```/```json fence and trims whitespace.
func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	i := strings.IndexByte(t, '\n')
	if i < 0 {
		return ""
	}
	t = t[i+1:]
	if j := strings.LastIndex(t, "```"); j >= 0 {
		t = t[:j]
	}
	return strings.TrimSpace(t)
}

// renderCites returns cites as-is; validated items keep the reply's order.
func renderCites(cites []string) []string {
	return append([]string(nil), cites...)
}

func applyClaims(j *model.Judgments, reply any, v *validator) {
	r := reply.(*claimsReply)
	v.attempted(kindClaims, len(r.Claims))
	for _, c := range r.Claims {
		if !claimSources[c.Source] || !claimVerdicts[c.Verdict] {
			continue
		}
		if !v.citesOK(c.Cites) {
			continue
		}
		j.Claims = append(j.Claims, model.ClaimVerdict{
			Claim:   clip(c.Claim, 160),
			Source:  c.Source,
			Verdict: c.Verdict,
			Reason:  clip(c.Reason, 300),
			Cites:   renderCites(c.Cites),
		})
		v.kept(kindClaims)
	}
}

func applyStory(j *model.Judgments, reply any, v *validator) {
	r := reply.(*storyReply)

	if r.AskSummary != nil {
		v.attempted(kindAskSummary, 1)
	}
	if a := r.AskSummary; a != nil && v.citesOK(a.Cites) {
		as := &model.AskSummary{
			Ask:        clip(a.Ask, 200),
			FinalScope: clip(a.FinalScope, 200),
			Cites:      renderCites(a.Cites),
		}
		for _, ref := range a.Refinements {
			as.Refinements = append(as.Refinements, clip(ref, 200))
		}
		j.AskSummary = as
		v.kept(kindAskSummary)
	}

	v.attempted(kindDecisions, len(r.Decisions))
	for _, d := range r.Decisions {
		if !decisionBys[d.By] || !v.citesOK(d.Cites) {
			continue
		}
		j.Decisions = append(j.Decisions, model.DecisionItem{
			Choice: clip(d.Choice, 160),
			Reason: clip(d.Reason, 300),
			By:     d.By,
			Cites:  renderCites(d.Cites),
		})
		v.kept(kindDecisions)
	}

	v.attempted(kindAbandoned, len(r.Abandoned))
	for _, a := range r.Abandoned {
		if !v.citesOK(a.Cites) {
			continue
		}
		refs, ok := v.eventRefs(a.Events)
		if !ok {
			continue
		}
		j.Abandoned = append(j.Abandoned, model.AbandonedItem{
			Events:  refs,
			Summary: clip(a.Summary, 200),
			Cites:   renderCites(a.Cites),
		})
		v.kept(kindAbandoned)
	}

	v.attempted(kindCorrections, len(r.Corrections))
	for _, c := range r.Corrections {
		ref, ok := v.eventRef(c.Event)
		if !ok || !v.citesOK(c.Cites) {
			continue
		}
		j.Corrections = append(j.Corrections, model.CorrectionItem{
			Event:        ref,
			IsCorrection: c.IsCorrection,
			About:        clip(c.About, 200),
			Cites:        renderCites(c.Cites),
		})
		v.kept(kindCorrections)
	}
}

func applyScope(j *model.Judgments, reply any, v *validator) {
	r := reply.(*scopeReply)

	v.attempted(kindScope, len(r.Scope))
	for _, s := range r.Scope {
		if !v.isPRFile(s.File) || !v.linesOK(s.Lines) ||
			!v.hunkLinesOK(s.File, s.Lines) || !v.citesOK(s.Cites) {
			continue
		}
		j.Scope = append(j.Scope, model.ScopeItem{
			File:    s.File,
			Lines:   s.Lines,
			Traced:  s.Traced,
			TraceTo: clip(s.TraceTo, 200),
			Cites:   renderCites(s.Cites),
		})
		v.kept(kindScope)
	}

	v.attempted(kindPlanDrift, len(r.PlanDrift))
	for _, d := range r.PlanDrift {
		if !driftKinds[d.Kind] || !v.linesOK(d.Lines) || !v.citesOK(d.Cites) {
			continue
		}
		if d.File != "" && (!v.isPRFile(d.File) || !v.hunkLinesOK(d.File, d.Lines)) {
			continue
		}
		j.PlanDrift = append(j.PlanDrift, model.DriftItem{
			Kind:  d.Kind,
			Text:  clip(d.Text, 200),
			File:  d.File,
			Lines: d.Lines,
			Cites: renderCites(d.Cites),
		})
		v.kept(kindPlanDrift)
	}

	v.attempted(kindRuleViolations, len(r.RuleViolations))
	for _, rv := range r.RuleViolations {
		if !v.isPRFile(rv.File) || !v.linesOK(rv.Lines) ||
			!v.hunkLinesOK(rv.File, rv.Lines) || !v.citesOK(rv.Cites) {
			continue
		}
		j.RuleViolations = append(j.RuleViolations, model.RuleViolation{
			Rule:  clip(rv.Rule, 200),
			File:  rv.File,
			Lines: rv.Lines,
			Cites: renderCites(rv.Cites),
		})
		v.kept(kindRuleViolations)
	}
}

func applyCaveats(j *model.Judgments, reply any, v *validator) {
	r := reply.(*caveatsReply)
	v.attempted(kindCaveats, len(r.Caveats))
	for _, c := range r.Caveats {
		if !v.citesOK(c.Cites) {
			continue
		}
		j.Caveats = append(j.Caveats, model.CaveatItem{Text: clip(c.Text, 200), Cites: renderCites(c.Cites)})
		v.kept(kindCaveats)
	}
	v.attempted(kindLostConstraint, len(r.LostConstraints))
	for _, l := range r.LostConstraints {
		if !v.citesOK(l.Cites) {
			continue
		}
		j.LostConstraints = append(j.LostConstraints, model.LostConstraint{Constraint: clip(l.Constraint, 200), Cites: renderCites(l.Cites)})
		v.kept(kindLostConstraint)
	}
}
