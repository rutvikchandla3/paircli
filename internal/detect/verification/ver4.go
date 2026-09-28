package verification

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() {
	engine.Register(ver4{})
}

type ver4 struct{}

func (ver4) ID() string { return "VER-4" }

// ver4EditsBetween returns non-failed agent file_edit events between two
// instants (exclusive) on paths classified test, in timeline order.
func ver4EditsBetween(c *engine.Context, fromTS, toTS model.Event) []engine.Item {
	var out []engine.Item
	for _, it := range c.Of(model.KindEdit) {
		ed := it.E.Edit
		if ed == nil || ed.Failed || ed.RelPath == "" {
			continue
		}
		if !it.E.TS.After(fromTS.TS) || !it.E.TS.Before(toTS.TS) {
			continue
		}
		if !classify.Has(classify.Path(ed.RelPath, c.Config), classify.TestFile) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// ver4SourceItem returns the timeline item that produced PR line (file, line),
// when the line has an attributed source.
func ver4SourceItem(c *engine.Context, file string, line int) (engine.Item, bool) {
	if c.Attribution == nil {
		return engine.Item{}, false
	}
	for _, f := range c.Attribution.Files {
		if f.Path != file {
			continue
		}
		for _, l := range f.Lines {
			if l.Line != line || l.Source == nil {
				continue
			}
			ref := *l.Source
			for _, it := range c.Timeline {
				if c.Ref(it) == ref {
					return it, true
				}
			}
		}
	}
	return engine.Item{}, false
}

// ver4AssertionCounts returns how many of the lines are assertion-like.
func ver4AssertionCounts(lines []string) int {
	n := 0
	for _, l := range lines {
		if classify.AssertionLike(l) {
			n++
		}
	}
	return n
}

func (ver4) Detect(c *engine.Context) model.Signal {
	sig := engine.NewSignal("VER-4")
	if !c.HasSessions() {
		return engine.NoSessions("VER-4")
	}

	var testRuns []checkRun
	for _, r := range checkRuns(c) {
		if r.Class == classify.Test {
			testRuns = append(testRuns, r)
		}
	}

	var findings []model.Finding
	failEditPass, assertionsRemoved, skipMarkers, snapshotUpdates, deletedTests := 0, 0, 0, 0, 0

	// Rule 1: a test file edited between a failing run and the next passing run.
	for i, f := range testRuns {
		if f.Result.Status != "fail" {
			continue
		}
		var pass *checkRun
		for j := i + 1; j < len(testRuns); j++ {
			if testRuns[j].Result.Status == "pass" {
				pass = &testRuns[j]
				break
			}
		}
		if pass == nil {
			continue
		}
		edits := ver4EditsBetween(c, *f.It.E, *pass.It.E)
		if len(edits) == 0 {
			continue
		}

		file := edits[0].E.Edit.RelPath
		removed, added := 0, 0
		for _, it := range edits {
			removed += ver4AssertionCounts(it.E.Edit.Removed)
			added += ver4AssertionCounts(it.E.Edit.Added)
		}

		summary := fmt.Sprintf("Test file `%s` was edited between a failing run (%s) and the next passing run (%s).",
			file, engine.Clock(f.It.E.TS), engine.Clock(pass.It.E.TS))
		if lost := removed - added; lost > 0 {
			summary += fmt.Sprintf(" It lost %d assertion lines.", lost)
		}

		anchors := c.AnchorsFor(edits...)
		if len(anchors) == 0 {
			anchors = []model.Anchor{{File: file}}
		}

		evidence := []model.Evidence{c.Evidence(f.It, evidenceExcerpt(f.It))}
		for _, it := range edits {
			if len(evidence) > 2 {
				break
			}
			evidence = append(evidence, c.Evidence(it, evidenceExcerpt(it)))
		}
		evidence = append(evidence, c.Evidence(pass.It, evidenceExcerpt(pass.It)))

		findings = append(findings, model.Finding{
			Summary:  summary,
			Severity: model.StateAlert,
			Anchors:  anchors,
			Evidence: evidence,
			Data:     map[string]any{"file": file, "assertions_lost": removed - added},
		})
		failEditPass++
	}

	// Rules 2, 3 and 5 walk the PR's files in PR order.
	if c.PR != nil {
		for i := range c.PR.Files {
			f := &c.PR.Files[i]
			classes := classify.Path(f.Path, c.Config)
			isTest := classify.Has(classes, classify.TestFile)

			// Rule 2: assertions removed in the PR.
			if isTest {
				r := ver4AssertionCounts(ver4RemovedTexts(f))
				a := ver4AssertionCounts(ver4AddedTexts(f))
				if r-a >= 1 {
					findings = append(findings, model.Finding{
						Summary:  fmt.Sprintf("`%s`: %d assertion lines removed, %d added.", f.Path, r, a),
						Severity: model.StateAlert,
						Anchors:  []model.Anchor{{File: f.Path}},
						Data:     map[string]any{"removed": r, "added": a},
					})
					assertionsRemoved++
				}
			}

			// Rule 3: skip/only markers added.
			for _, dl := range f.AddedLines() {
				kind, ok := classify.SkipMarker(dl.Text)
				if !ok {
					continue
				}
				finding := model.Finding{
					Summary: fmt.Sprintf("`%s:%d` adds a `%s` marker: `%s`",
						f.Path, dl.NewNo, kind, model.Clip(strings.TrimSpace(dl.Text), 60)),
					Severity: model.StateAlert,
					Anchors:  []model.Anchor{{File: f.Path, Lines: strconv.Itoa(dl.NewNo)}},
					Data:     map[string]any{"file": f.Path, "line": dl.NewNo, "kind": kind},
				}
				if it, ok := ver4SourceItem(c, f.Path, dl.NewNo); ok {
					finding.Evidence = []model.Evidence{c.Evidence(it, evidenceExcerpt(it))}
				}
				findings = append(findings, finding)
				skipMarkers++
			}

			// Rule 5: deleted tests.
			if isTest && f.Status == model.StatusDeleted {
				findings = append(findings, model.Finding{
					Summary:  fmt.Sprintf("Test file deleted: `%s`.", f.Path),
					Severity: model.StateAlert,
					Anchors:  []model.Anchor{{File: f.Path}},
				})
				deletedTests++
			}
		}
	}

	// Rule 4: snapshot rewrites, from commands and from PR files.
	for _, it := range c.Of(model.KindCommand) {
		cmd := it.E.Command
		if cmd == nil {
			continue
		}
		if !hasCmdClass(classify.Command(cmd.Cmd, c.Config.ExtraChecks), classify.SnapshotUpdate) {
			continue
		}
		findings = append(findings, model.Finding{
			Summary:  fmt.Sprintf("Snapshots regenerated with `%s` at %s.", classify.ShortCmd(cmd.Cmd), engine.Clock(it.E.TS)),
			Severity: model.StateAlert,
			Evidence: []model.Evidence{c.Evidence(it, evidenceExcerpt(it))},
		})
		snapshotUpdates++
	}
	if c.PR != nil {
		for i := range c.PR.Files {
			f := &c.PR.Files[i]
			if f.Status != model.StatusModified && f.Status != model.StatusAdded {
				continue
			}
			if !classify.Has(classify.Path(f.Path, c.Config), classify.SnapshotFile) {
				continue
			}
			findings = append(findings, model.Finding{
				Summary:  fmt.Sprintf("Snapshot file changed: `%s`.", f.Path),
				Severity: model.StateAlert,
				Anchors:  []model.Anchor{{File: f.Path}},
			})
			snapshotUpdates++
		}
	}

	if len(findings) > 0 {
		sig.State = model.StateAlert
	} else {
		sig.State = model.StateClear
		sig.Summary = "No weakened tests, new skips, snapshot rewrites or deleted tests found."
	}
	if len(findings) > 0 && sig.Summary == "" {
		sig.Summary = ver4Summary(failEditPass, assertionsRemoved, skipMarkers, snapshotUpdates, deletedTests)
	}
	sig.Findings = findings
	sig.Data = map[string]any{
		"fail_edit_pass":     failEditPass,
		"assertions_removed": assertionsRemoved,
		"skip_markers":       skipMarkers,
		"snapshot_updates":   snapshotUpdates,
		"deleted_tests":      deletedTests,
	}
	return sig
}

// ver4Summary describes which integrity rules fired.
func ver4Summary(failEditPass, assertionsRemoved, skipMarkers, snapshotUpdates, deletedTests int) string {
	var parts []string
	add := func(n int, one, many string) {
		if n > 0 {
			parts = append(parts, engine.Plural(n, one, many))
		}
	}
	add(failEditPass, "test edit between fail and pass", "test edits between fail and pass")
	add(assertionsRemoved, "file with assertions removed", "files with assertions removed")
	add(skipMarkers, "skip marker", "skip markers")
	add(snapshotUpdates, "snapshot update", "snapshot updates")
	add(deletedTests, "deleted test", "deleted tests")
	if len(parts) == 0 {
		return "Tests were weakened, skipped, regenerated or deleted."
	}
	return "Test integrity issues: " + strings.Join(parts, ", ") + "."
}

// ver4AddedTexts returns the text of a PR file's added lines.
func ver4AddedTexts(f *model.DiffFile) []string {
	var out []string
	for _, l := range f.AddedLines() {
		out = append(out, l.Text)
	}
	return out
}

// ver4RemovedTexts returns the text of a PR file's removed lines.
func ver4RemovedTexts(f *model.DiffFile) []string {
	var out []string
	for _, l := range f.RemovedLines() {
		out = append(out, l.Text)
	}
	return out
}
