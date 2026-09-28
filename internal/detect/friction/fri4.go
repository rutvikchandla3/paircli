package friction

import (
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type fri4 struct{}

func init() { engine.Register(fri4{}) }

func (fri4) ID() string { return "FRI-4" }

func (d fri4) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}

	sig := engine.NewSignal(d.ID())

	var findings []model.Finding
	var lookupCount int
	var followedByEditsCount int

	docsMCPPattern := regexp.MustCompile(`(?i)(context7|docs|deepwiki|devdocs|mdn)`)

	// Find all lookups (lookup events and mcp_call events matching pattern)
	lookups := []engine.Item{}
	for _, it := range c.Timeline {
		e := it.E

		if e.Kind == model.KindLookup && e.Lookup != nil {
			lookups = append(lookups, it)
		}

		if e.Kind == model.KindMCP && e.MCP != nil && docsMCPPattern.MatchString(e.MCP.Server) {
			lookups = append(lookups, it)
		}
	}

	// For each lookup, find followed-by edits
	for _, lookupIt := range lookups {
		lookupCount++
		lookupSeq := lookupIt.E.Seq

		// Find file edits in same session, within 10 events or 15 minutes
		var followedEdits []engine.Item
		for _, it := range c.Timeline {
			if it.S.ID != lookupIt.S.ID {
				continue
			}
			if it.E.Kind != model.KindEdit || it.E.Edit == nil {
				continue
			}
			if it.E.Seq <= lookupSeq {
				continue
			}

			// Within 10 events (by Seq)
			if it.E.Seq-lookupSeq > 10 {
				continue
			}

			// Within 15 minutes
			if it.E.TS.Sub(lookupIt.E.TS) > 15*time.Minute {
				continue
			}

			// Must be a PR file
			relPath := it.E.Edit.RelPath
			if relPath != "" && c.IsPRFile(relPath) {
				followedEdits = append(followedEdits, it)
			}
		}

		// Build finding for this lookup
		var text string
		var evidence []model.Evidence
		var anchors []model.Anchor

		if lookupIt.E.Kind == model.KindLookup {
			lookup := lookupIt.E.Lookup
			if lookup.Kind == "search" {
				text = "Searched \"" + model.Clip(lookup.Query, 80) + "\" at " + engine.Clock(lookupIt.E.TS)
			} else if lookup.Kind == "fetch" && len(lookup.URLs) > 0 {
				u, _ := url.Parse(lookup.URLs[0])
				host := u.Host
				path := u.Path
				if len(path) > 40 {
					path = path[:40]
				}
				text = "Fetched " + host + path + " at " + engine.Clock(lookupIt.E.TS)
			}
			evidence = []model.Evidence{c.Evidence(lookupIt, lookup.Query)}
		} else if lookupIt.E.Kind == model.KindMCP {
			mcp := lookupIt.E.MCP
			text = "Looked up docs via `" + mcp.Server + "` at " + engine.Clock(lookupIt.E.TS)
			evidence = []model.Evidence{c.Evidence(lookupIt, mcp.Tool)}
		}

		// Append edits info if any
		if len(followedEdits) > 0 {
			followedByEditsCount++
			firstFile := ""
			if followedEdits[0].E.Edit != nil {
				firstFile = followedEdits[0].E.Edit.RelPath
				if firstFile == "" {
					// Use Path if RelPath is empty
					p := followedEdits[0].E.Edit.Path
					if p != "" && strings.HasPrefix(p, "/repo/") {
						firstFile = p[6:] // Strip "/repo/"
					}
				}
			}
			text += "; edits followed in `" + firstFile + "`"
			anchors = c.AnchorsFor(followedEdits...)
		}

		text += "."

		finding := model.Finding{
			Summary:  text,
			Severity: model.StateInfo,
			Evidence: evidence,
			Anchors:  anchors,
		}
		findings = append(findings, finding)
	}

	// Set summary and state
	if lookupCount == 0 {
		sig.State = model.StateClear
		sig.Summary = "No web or docs lookups."
	} else {
		sig.State = model.StateInfo
		sig.Summary = engine.Plural(lookupCount, "web or docs lookup", "web or docs lookups") + "; " +
			engine.Plural(followedByEditsCount, "was", "were") + " followed by edits to PR files."
	}

	sig.Findings = findings
	sig.Data = map[string]any{
		"lookups":           lookupCount,
		"followed_by_edits": followedByEditsCount,
	}

	return sig
}
