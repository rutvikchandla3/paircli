package verification

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() {
	engine.Register(ver5{})
}

type ver5 struct{}

func (ver5) ID() string { return "VER-5" }

// ver5HookWindow is how far back a failed git_commit explains a bypass.
const ver5HookWindow = 10 * time.Minute

// hasCmdClass reports whether classes contains want (classify.Has covers path
// classes only). Used by VER-4 and VER-5.
func hasCmdClass(classes []classify.CmdClass, want classify.CmdClass) bool {
	for _, c := range classes {
		if c == want {
			return true
		}
	}
	return false
}

// ver5CoverageFileRE matches base names whose coverage thresholds matter.
var ver5CoverageFileRE = regexp.MustCompile(`^(jest|vitest|karma)\.config\.|^\.coveragerc$|^setup\.cfg$|^pyproject\.toml$|^codecov\.ya?ml$|^\.nycrc`)

// ver5CoverageLineRE matches a coverage-threshold setting.
var ver5CoverageLineRE = regexp.MustCompile(`(?i)(coverageThreshold|fail_under|cov-fail-under|\b(branches|lines|functions|statements)\s*:)`)

// ver5Base returns the last path segment.
func ver5Base(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ver5FailedCommitBefore returns the nearest preceding `git_commit` command in
// the same session as it, when it ran within ver5HookWindow and failed.
func ver5FailedCommitBefore(c *engine.Context, it engine.Item) (engine.Item, bool) {
	var found engine.Item
	ok := false
	for _, prev := range c.Of(model.KindCommand) {
		if prev.S != it.S {
			continue
		}
		if !prev.E.TS.Before(it.E.TS) {
			continue
		}
		cmd := prev.E.Command
		if cmd == nil || !hasCmdClass(classify.Command(cmd.Cmd, c.Config.ExtraChecks), classify.GitCommit) {
			continue
		}
		found, ok = prev, true
	}
	if !ok {
		return engine.Item{}, false
	}
	if found.E.Command.Status != model.CmdFailed {
		return engine.Item{}, false
	}
	if it.E.TS.Sub(found.E.TS) > ver5HookWindow {
		return engine.Item{}, false
	}
	return found, true
}

func (ver5) Detect(c *engine.Context) model.Signal {
	sig := engine.NewSignal("VER-5")
	if !c.HasSessions() {
		return engine.NoSessions("VER-5")
	}

	var findings []model.Finding
	noVerify, suppressions, ciConfig, coverageConfig := 0, 0, 0, 0

	// Rule 1: commits made with --no-verify or hook-skip env vars.
	for _, it := range c.Of(model.KindCommand) {
		cmd := it.E.Command
		if cmd == nil {
			continue
		}
		classes := classify.Command(cmd.Cmd, c.Config.ExtraChecks)
		if !hasCmdClass(classes, classify.NoVerify) && !hasCmdClass(classes, classify.HookSkipEnv) {
			continue
		}

		summary := fmt.Sprintf("Commit made with `%s`", classify.ShortCmd(cmd.Cmd))
		var evidence []model.Evidence
		if prev, ok := ver5FailedCommitBefore(c, it); ok {
			summary += " after the pre-commit hook failed"
			evidence = append(evidence, c.Evidence(prev, evidenceExcerpt(prev)))
		}
		summary += "."
		evidence = append(evidence, c.Evidence(it, evidenceExcerpt(it)))

		findings = append(findings, model.Finding{
			Summary:  summary,
			Severity: model.StateAlert,
			Evidence: evidence,
		})
		noVerify++
	}

	// Rules 2, 3 and 4 walk the PR's files in PR order.
	if c.PR != nil {
		for i := range c.PR.Files {
			f := &c.PR.Files[i]
			classes := classify.Path(f.Path, c.Config)

			// Rule 2: new suppression comments.
			if !classify.Has(classes, classify.Generated) {
				n := 0
				var kinds []string
				var anchors []model.Anchor
				seen := map[string]bool{}
				for _, dl := range f.AddedLines() {
					kind, ok := classify.Suppression(dl.Text)
					if !ok {
						continue
					}
					n++
					if !seen[kind] {
						seen[kind] = true
						kinds = append(kinds, kind)
					}
					anchors = append(anchors, model.Anchor{File: f.Path, Lines: strconv.Itoa(dl.NewNo)})
				}
				if n > 0 {
					findings = append(findings, model.Finding{
						Summary:  fmt.Sprintf("`%s` adds %d suppressions (%s).", f.Path, n, strings.Join(kinds, ", ")),
						Severity: model.StateAlert,
						Anchors:  anchors,
						Data:     map[string]any{"file": f.Path, "count": n, "kinds": kinds},
					})
					suppressions++
				}
			}

			// Rule 3: CI config changed.
			if classify.Has(classes, classify.CIConfig) {
				findings = append(findings, model.Finding{
					Summary:  fmt.Sprintf("CI config changed: `%s`.", f.Path),
					Severity: model.StateAlert,
					Anchors:  []model.Anchor{{File: f.Path}},
				})
				ciConfig++
			}

			// Rule 4: coverage thresholds changed.
			if ver5CoverageFileRE.MatchString(ver5Base(f.Path)) {
				changed := false
				for _, dl := range f.AddedLines() {
					if ver5CoverageLineRE.MatchString(dl.Text) {
						changed = true
						break
					}
				}
				if !changed {
					for _, dl := range f.RemovedLines() {
						if ver5CoverageLineRE.MatchString(dl.Text) {
							changed = true
							break
						}
					}
				}
				if changed {
					findings = append(findings, model.Finding{
						Summary:  fmt.Sprintf("Coverage threshold changed in `%s`.", f.Path),
						Severity: model.StateAlert,
						Anchors:  []model.Anchor{{File: f.Path}},
					})
					coverageConfig++
				}
			}
		}
	}

	if len(findings) > 0 {
		sig.State = model.StateAlert
		sig.Summary = ver5Summary(noVerify, suppressions, ciConfig, coverageConfig)
	} else {
		sig.State = model.StateClear
		sig.Summary = "No skipped hooks, new suppressions, or CI/coverage config changes."
	}
	sig.Findings = findings
	sig.Data = map[string]any{
		"no_verify":       noVerify,
		"suppressions":    suppressions,
		"ci_config":       ciConfig,
		"coverage_config": coverageConfig,
	}
	return sig
}

// ver5Summary describes which bypass rules fired.
func ver5Summary(noVerify, suppressions, ciConfig, coverageConfig int) string {
	var parts []string
	add := func(n int, one, many string) {
		if n > 0 {
			parts = append(parts, engine.Plural(n, one, many))
		}
	}
	add(noVerify, "commit that skipped hooks", "commits that skipped hooks")
	add(suppressions, "file with new suppressions", "files with new suppressions")
	add(ciConfig, "CI config change", "CI config changes")
	add(coverageConfig, "coverage threshold change", "coverage threshold changes")
	if len(parts) == 0 {
		return "Quality gates were bypassed."
	}
	return "Quality-gate bypasses: " + strings.Join(parts, ", ") + "."
}
