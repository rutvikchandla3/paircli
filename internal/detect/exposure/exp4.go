package exposure

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() { engine.Register(exp4{}) }

type exp4 struct{}

func (exp4) ID() string { return "EXP-4" }

// exp4ReadFile is one secret file read, aggregated across the timeline.
type exp4ReadFile struct {
	path  string
	it    engine.Item
	count int
}

func (d exp4) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	var findings []model.Finding
	kinds := map[string]bool{}

	// Secrets in the PR diff. Only masked values are ever stored.
	diffCount := 0
	if c.PR != nil {
		for _, f := range c.PR.Files {
			if classify.Has(classify.Path(f.Path, c.Config), classify.Generated) {
				continue
			}
			for _, dl := range f.AddedLines() {
				for _, h := range classify.Secrets(dl.Text) {
					diffCount++
					kinds[h.Kind] = true
					findings = append(findings, model.Finding{
						Summary:  fmt.Sprintf("Possible %s in `%s:%d` (%s).", h.Kind, f.Path, dl.NewNo, h.Masked),
						Severity: model.StateAlert,
						Anchors:  []model.Anchor{{File: f.Path, Lines: strconv.Itoa(dl.NewNo)}},
						Data: map[string]any{
							"kind":   h.Kind,
							"masked": h.Masked,
							"file":   f.Path,
							"line":   dl.NewNo,
						},
					})
				}
			}
		}
	}

	// Secrets in prompts and command output. Evidence excerpts are the masked
	// value only, never the match itself.
	outputCount := 0
	for _, it := range c.Timeline {
		switch it.E.Kind {
		case model.KindPrompt:
			if it.E.Prompt == nil {
				continue
			}
			for _, h := range classify.Secrets(it.E.Prompt.Text) {
				outputCount++
				kinds[h.Kind] = true
				findings = append(findings, model.Finding{
					Summary:  fmt.Sprintf("A %s appeared in a prompt at %s (%s).", h.Kind, engine.Clock(it.E.TS), h.Masked),
					Severity: model.StateAlert,
					Evidence: []model.Evidence{c.Evidence(it, h.Masked)},
					Data: map[string]any{
						"kind":    h.Kind,
						"masked":  h.Masked,
						"source":  "prompt",
						"session": it.S.Ref(),
						"event":   it.E.ID,
					},
				})
			}
		case model.KindCommand:
			if it.E.Command == nil {
				continue
			}
			for _, h := range classify.Secrets(it.E.Command.Output) {
				outputCount++
				kinds[h.Kind] = true
				findings = append(findings, model.Finding{
					Summary:  fmt.Sprintf("A %s appeared in command output at %s (%s).", h.Kind, engine.Clock(it.E.TS), h.Masked),
					Severity: model.StateAlert,
					Evidence: []model.Evidence{c.Evidence(it, h.Masked)},
					Data: map[string]any{
						"kind":    h.Kind,
						"masked":  h.Masked,
						"source":  "output",
						"session": it.S.Ref(),
						"event":   it.E.ID,
					},
				})
			}
		}
	}

	// Secret files read, one finding per file with a read count.
	reads := map[string]*exp4ReadFile{}
	var readOrder []string
	record := func(path string, it engine.Item) {
		if path == "" {
			return
		}
		r, ok := reads[path]
		if !ok {
			r = &exp4ReadFile{path: path, it: it}
			reads[path] = r
			readOrder = append(readOrder, path)
		}
		r.count++
	}
	isSecret := func(p string) bool {
		return p != "" && classify.Has(classify.Path(p, c.Config), classify.SecretFile)
	}
	for _, it := range c.Timeline {
		switch it.E.Kind {
		case model.KindRead:
			if it.E.Read == nil {
				continue
			}
			p := it.E.Read.RelPath
			if p == "" {
				p = it.E.Read.Path
			}
			if isSecret(p) {
				record(p, it)
			}
		case model.KindCommand:
			if it.E.Command == nil {
				continue
			}
			for _, t := range classify.ReadTargets(it.E.Command.Cmd) {
				if isSecret(t) {
					record(t, it)
				}
			}
		}
	}
	for _, p := range readOrder {
		r := reads[p]
		findings = append(findings, model.Finding{
			Summary:  fmt.Sprintf("Read `%s` at %s.", engine.Home(r.path), engine.Clock(r.it.E.TS)),
			Severity: model.StateInfo,
			Evidence: []model.Evidence{c.Evidence(r.it, r.path)},
			Data: map[string]any{
				"path":    r.path,
				"count":   r.count,
				"session": r.it.S.Ref(),
				"event":   r.it.E.ID,
			},
		})
	}

	sig.Findings = findings
	readCount := len(readOrder)

	switch {
	case diffCount+outputCount > 0:
		sig.State = model.StateAlert
	case readCount > 0:
		sig.State = model.StateInfo
	default:
		sig.State = model.StateClear
	}

	if diffCount+outputCount+readCount == 0 {
		sig.Summary = "No secret files read and no secret-shaped strings found."
	} else {
		var head []string
		if diffCount > 0 {
			head = append(head, engine.Plural(diffCount,
				"possible secret in the diff", "possible secrets in the diff"))
		}
		if outputCount > 0 {
			head = append(head, fmt.Sprintf("%d in prompts or output", outputCount))
		}
		var tail string
		if readCount > 0 {
			tail = engine.Plural(readCount, "secret file read", "secret files read")
		}
		summary := strings.Join(head, ", ")
		if tail != "" {
			if summary != "" {
				summary += "; "
			}
			summary += tail
		}
		sig.Summary = model.Clip(summary+".", 160)
	}

	sig.Data = map[string]any{
		"diff":   diffCount,
		"output": outputCount,
		"reads":  readCount,
		"kinds":  exp4SortedKinds(kinds),
	}
	return sig
}

// exp4SortedKinds returns the detected secret kinds in sorted order.
func exp4SortedKinds(kinds map[string]bool) []string {
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
