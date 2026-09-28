package exposure

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() { engine.Register(exp2{}) }

type exp2 struct{}

func (exp2) ID() string { return "EXP-2" }

// exp2FailedInstallRE matches registry output that says a package does not exist.
var exp2FailedInstallRE = regexp.MustCompile(`(?i)(E404|404 Not Found|not found in the npm registry|No matching distribution|Could not find a version|could not resolve|unknown revision|no such package|is not in the npm registry)`)

func (d exp2) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	var findings []model.Finding
	failedNames := []string{}
	failedSeen := map[string]bool{}

	// Failed installs of packages the registry does not have.
	for _, it := range c.Timeline {
		if it.E.Kind != model.KindCommand || it.E.Command == nil {
			continue
		}
		cmd := it.E.Command
		if cmd.Status != model.CmdFailed {
			continue
		}
		if !expHasClass(classify.Command(cmd.Cmd, c.Config.ExtraChecks), classify.Install) {
			continue
		}
		if !exp2FailedInstallRE.MatchString(cmd.Output) {
			continue
		}
		for _, p := range classify.Packages(cmd.Cmd) {
			if p.Name == "" || failedSeen[p.Name] {
				continue
			}
			failedSeen[p.Name] = true
			failedNames = append(failedNames, p.Name)
			findings = append(findings, model.Finding{
				Summary:  fmt.Sprintf("Tried to install `%s`: the registry says it does not exist.", p.Name),
				Severity: model.StateAlert,
				Evidence: []model.Evidence{c.Evidence(it, cmd.Output)},
				Data: map[string]any{
					"name":    p.Name,
					"manager": p.Manager,
					"session": it.S.Ref(),
					"event":   it.E.ID,
				},
			})
		}
	}

	// Install commands that named a package, for evidence on added deps.
	installEvidence := map[string]model.Evidence{}
	for _, it := range c.Of(model.KindCommand) {
		if it.E.Command == nil {
			continue
		}
		cmd := it.E.Command
		if !expHasClass(classify.Command(cmd.Cmd, c.Config.ExtraChecks), classify.Install) {
			continue
		}
		for _, p := range classify.Packages(cmd.Cmd) {
			if p.Name == "" {
				continue
			}
			if _, ok := installEvidence[p.Name]; !ok {
				installEvidence[p.Name] = c.Evidence(it, cmd.Cmd)
			}
		}
	}

	// Dependencies the PR diff adds to a manifest.
	var prompts []string
	for _, it := range c.Of(model.KindPrompt) {
		if it.E.AgentID != "" || it.E.Prompt == nil {
			continue
		}
		prompts = append(prompts, it.E.Prompt.Text)
	}

	var added []map[string]any
	addedCount, agentChoice := 0, 0
	alert := len(failedNames) > 0
	if c.PR != nil {
		for _, f := range c.PR.Files {
			deps := exp2ManifestDeps(f.Path, f.Hunks)
			for _, dep := range deps {
				requested := exp2Mentioned(dep.Name, prompts)
				addedCount++
				ver := ""
				if dep.Version != "" {
					ver = "@" + dep.Version
				}
				sev := model.StateInfo
				summary := fmt.Sprintf("Added `%s%s` (mentioned by the author).", dep.Name, ver)
				if !requested {
					sev = model.StateAlert
					agentChoice++
					alert = true
					summary = fmt.Sprintf("Added `%s%s`: the agent chose it; no prompt mentions it.", dep.Name, ver)
				}
				var evidence []model.Evidence
				if ev, ok := installEvidence[dep.Name]; ok {
					evidence = append(evidence, ev)
				}
				if it, ok := exp2AttributedItem(c, dep.File, dep.Line); ok {
					evidence = append(evidence, c.Evidence(it, exp2EditExcerpt(it)))
				}
				findings = append(findings, model.Finding{
					Summary:  summary,
					Severity: sev,
					Anchors:  []model.Anchor{{File: dep.File, Lines: strconv.Itoa(dep.Line)}},
					Evidence: evidence,
					Data: map[string]any{
						"file":      dep.File,
						"name":      dep.Name,
						"version":   dep.Version,
						"line":      dep.Line,
						"requested": requested,
						"indirect":  dep.Indirect,
					},
				})
				added = append(added, map[string]any{
					"file":      dep.File,
					"name":      dep.Name,
					"version":   dep.Version,
					"requested": requested,
					"indirect":  dep.Indirect,
				})
			}
		}
	}

	sig.Findings = findings
	switch {
	case alert:
		sig.State = model.StateAlert
	case len(findings) > 0:
		sig.State = model.StateInfo
	default:
		sig.State = model.StateClear
	}

	if len(findings) == 0 {
		sig.Summary = "No dependencies added or installed."
	} else {
		var parts []string
		if addedCount > 0 {
			parts = append(parts, fmt.Sprintf("%d dependencies added (%s)",
				addedCount, engine.Plural(agentChoice, "agent choice", "agent choices")))
		}
		if len(failedNames) > 0 {
			parts = append(parts, engine.Plural(len(failedNames),
				"install attempt for a package that does not exist",
				"install attempts for packages that do not exist"))
		}
		sig.Summary = model.Clip(strings.Join(parts, "; ")+".", 160)
	}

	sig.Data = map[string]any{
		"added":           added,
		"failed_installs": failedNames,
	}
	return sig
}

// exp2Mentioned reports whether any prompt text names the package, matched on
// a case-insensitive word boundary.
func exp2Mentioned(name string, prompts []string) bool {
	if name == "" {
		return false
	}
	re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(name) + `\b`)
	if err != nil {
		return false
	}
	for _, p := range prompts {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// exp2AttributedItem resolves the event that sourced the given PR line, when
// attribution knows it.
func exp2AttributedItem(c *engine.Context, file string, line int) (engine.Item, bool) {
	if c.Attribution == nil {
		return engine.Item{}, false
	}
	var ref *model.EventRef
	for _, f := range c.Attribution.Files {
		if f.Path != file {
			continue
		}
		for _, la := range f.Lines {
			if la.Line == line && la.Source != nil {
				ref = la.Source
				break
			}
		}
		if ref != nil {
			break
		}
	}
	if ref == nil {
		return engine.Item{}, false
	}
	for _, it := range c.Timeline {
		if it.S.Ref() == ref.Session && it.E.ID == ref.Event {
			return it, true
		}
	}
	return engine.Item{}, false
}

// exp2EditExcerpt renders a file edit as evidence text.
func exp2EditExcerpt(it engine.Item) string {
	if it.E.Edit == nil {
		return ""
	}
	return it.E.Edit.RelPath + ": " + strings.Join(it.E.Edit.Added, " ")
}
