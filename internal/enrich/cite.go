// Package enrich folds the validated LLM judgments (internal/judge) into the
// deterministic signals: it recomputes the three inferred signals from
// model.Judgments, and it adds the inferred findings and state changes the
// judgment tables in docs/plan/tasks/T26-inferred-enrich.md call for.
//
// Every item the judge kept carries at least one bundle id in Cites; the two
// helpers here turn those ids back into the anchors and evidence the report
// renders.
package enrich

import (
	"regexp"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// eventCiteRe matches an event cite, "ev:<session-ref>/<event-id>". Like the
// judge's own pattern it is greedy, so a ref containing "/" still splits at the
// last slash.
var eventCiteRe = regexp.MustCompile(`^ev:(.+)/(.+)$`)

// fileCiteRe matches a hunk cite, "file:<path>:<lines>" where lines is "N" or
// "N-M".
var fileCiteRe = regexp.MustCompile(`^file:(.+):(\d+(?:-\d+)?)$`)

// Evidence turns "ev:<ref>/<event>" cites into Evidence (via c.Session and the
// event ID); other cites are skipped. Cites that name a session or an event
// this context does not hold are skipped too, so the result only ever points at
// events that exist. The order of cites is kept, and repeats are collapsed.
func Evidence(c *engine.Context, cites []string) []model.Evidence {
	if c == nil {
		return nil
	}
	var out []model.Evidence
	seen := map[string]bool{}
	for _, cite := range cites {
		m := eventCiteRe.FindStringSubmatch(cite)
		if m == nil || seen[cite] {
			continue
		}
		ref, eventID := m[1], m[2]
		s := c.Session(ref)
		if s == nil {
			continue
		}
		for i := range s.Events {
			if s.Events[i].ID != eventID {
				continue
			}
			seen[cite] = true
			out = append(out, c.Evidence(engine.Item{S: s, E: &s.Events[i]}, ""))
			break
		}
	}
	return out
}

// Anchors turns "file:<path>:<a>-<b>" cites and explicit file/lines fields
// into Anchors. The explicit file/lines pair is kept first, then any hunk cite
// in cite order; duplicates are dropped, and so is an explicit pair that names
// a whole file a hunk cite already points into by line range.
func Anchors(file, lines string, cites []string) []model.Anchor {
	var out []model.Anchor
	seen := map[model.Anchor]bool{}
	add := func(f, l string) {
		if f == "" {
			return
		}
		a := model.Anchor{File: f, Lines: l}
		if seen[a] {
			return
		}
		seen[a] = true
		out = append(out, a)
	}
	if lines == "" && citeCoversFile(cites, file) {
		file = ""
	}
	add(file, lines)
	for _, cite := range cites {
		if m := fileCiteRe.FindStringSubmatch(cite); m != nil {
			add(m[1], m[2])
		}
	}
	return out
}

// citeCoversFile reports whether one of cites names path.
func citeCoversFile(cites []string, path string) bool {
	if path == "" {
		return false
	}
	for _, cite := range cites {
		if m := fileCiteRe.FindStringSubmatch(cite); m != nil && m[1] == path {
			return true
		}
	}
	return false
}
