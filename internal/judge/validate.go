package judge

import (
	"encoding/json"
	"regexp"
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

// validator checks a reply against the fact bundle. Every item must cite at
// least one id that appears in the bundle; anything else is dropped.
type validator struct {
	ids map[string]bool
	ec  *engine.Context
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
	}
}

func applyStory(j *model.Judgments, reply any, v *validator) {
	r := reply.(*storyReply)

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
	}

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
	}

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
	}

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
	}
}

func applyScope(j *model.Judgments, reply any, v *validator) {
	r := reply.(*scopeReply)

	for _, s := range r.Scope {
		if !v.isPRFile(s.File) || !v.linesOK(s.Lines) || !v.citesOK(s.Cites) {
			continue
		}
		j.Scope = append(j.Scope, model.ScopeItem{
			File:    s.File,
			Lines:   s.Lines,
			Traced:  s.Traced,
			TraceTo: clip(s.TraceTo, 200),
			Cites:   renderCites(s.Cites),
		})
	}

	for _, d := range r.PlanDrift {
		if !driftKinds[d.Kind] || !v.linesOK(d.Lines) || !v.citesOK(d.Cites) {
			continue
		}
		if d.File != "" && !v.isPRFile(d.File) {
			continue
		}
		j.PlanDrift = append(j.PlanDrift, model.DriftItem{
			Kind:  d.Kind,
			Text:  clip(d.Text, 200),
			File:  d.File,
			Lines: d.Lines,
			Cites: renderCites(d.Cites),
		})
	}

	for _, rv := range r.RuleViolations {
		if !v.isPRFile(rv.File) || !v.linesOK(rv.Lines) || !v.citesOK(rv.Cites) {
			continue
		}
		j.RuleViolations = append(j.RuleViolations, model.RuleViolation{
			Rule:  clip(rv.Rule, 200),
			File:  rv.File,
			Lines: rv.Lines,
			Cites: renderCites(rv.Cites),
		})
	}
}

func applyCaveats(j *model.Judgments, reply any, v *validator) {
	r := reply.(*caveatsReply)
	for _, c := range r.Caveats {
		if !v.citesOK(c.Cites) {
			continue
		}
		j.Caveats = append(j.Caveats, model.CaveatItem{Text: clip(c.Text, 200), Cites: renderCites(c.Cites)})
	}
	for _, l := range r.LostConstraints {
		if !v.citesOK(l.Cites) {
			continue
		}
		j.LostConstraints = append(j.LostConstraints, model.LostConstraint{Constraint: clip(l.Constraint, 200), Cites: renderCites(l.Cites)})
	}
}
