package exposure

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() { engine.Register(exp3{}) }

type exp3 struct{}

func (exp3) ID() string { return "EXP-3" }

// exp3MCPRE matches MCP servers and tools that reach outside the repo.
var exp3MCPRE = regexp.MustCompile(`(?i)(github|gitlab|linear|jira|slack|notion|confluence|web|fetch|browser|playwright|search|http|url|issue|drive|gmail|mail|discord)`)

// exp3Read is one external read inside a session.
type exp3Read struct {
	It   engine.Item
	Desc string
}

// exp3Chain is one finding before ordering.
type exp3Chain struct {
	ts      time.Time
	file    string
	finding model.Finding
}

func (d exp3) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	var chains []exp3Chain
	externalReads := 0

	for _, s := range c.Sessions {
		var reads []exp3Read
		for j := range s.Events {
			e := &s.Events[j]
			it := engine.Item{S: s, E: e}
			switch e.Kind {
			case model.KindLookup:
				reads = append(reads, exp3Read{it, exp3DescribeLookup(e.Lookup)})
			case model.KindMCP:
				m := e.MCP
				if m == nil {
					continue
				}
				if exp3MCPRE.MatchString(m.Server) || exp3MCPRE.MatchString(m.Tool) {
					reads = append(reads, exp3Read{it, fmt.Sprintf("`%s.%s`", m.Server, m.Tool)})
				}
			case model.KindCommand:
				cmd := e.Command
				if cmd == nil {
					continue
				}
				classes := classify.Command(cmd.Cmd, c.Config.ExtraChecks)
				if expHasClass(classes, classify.NetworkRead) || expHasClass(classes, classify.GHRead) {
					reads = append(reads, exp3Read{it, fmt.Sprintf("`%s`", classify.ShortCmd(cmd.Cmd))})
				}
			}
		}
		externalReads += len(reads)

		seen := map[string]bool{}
		for j := range s.Events {
			e := &s.Events[j]
			if e.Kind != model.KindEdit || e.Edit == nil {
				continue
			}
			ed := e.Edit
			if ed.Failed || ed.RelPath == "" {
				continue
			}
			classes := classify.Path(ed.RelPath, c.Config)
			if !classify.Has(classes, classify.Sensitive) && !classify.Has(classes, classify.CIConfig) {
				continue
			}

			// Latest external read strictly before this edit, same session.
			last := -1
			for k := range reads {
				if exp3Before(reads[k].It.E, e) {
					last = k
				}
			}
			if last < 0 {
				continue
			}
			key := fmt.Sprintf("%d|%s", last, ed.RelPath)
			if seen[key] {
				continue
			}
			seen[key] = true

			it := engine.Item{S: s, E: e}
			rd := reads[last]
			anchors := c.AnchorsFor(it)
			if len(anchors) == 0 {
				anchors = []model.Anchor{{File: ed.RelPath}}
			}
			chains = append(chains, exp3Chain{
				ts:   e.TS,
				file: ed.RelPath,
				finding: model.Finding{
					Summary: fmt.Sprintf("%s read external content (%s) → %s edited `%s`.",
						engine.Clock(rd.It.E.TS), rd.Desc, engine.Clock(e.TS), ed.RelPath),
					Severity: model.StateAlert,
					Anchors:  anchors,
					Evidence: []model.Evidence{
						c.Evidence(rd.It, exp3ReadExcerpt(rd.It)),
						c.Evidence(it, exp3EditExcerpt(ed)),
					},
					Data: map[string]any{
						"read_session": rd.It.S.Ref(),
						"read_event":   rd.It.E.ID,
						"edit_session": s.Ref(),
						"edit_event":   e.ID,
						"file":         ed.RelPath,
					},
				},
			})
		}
	}

	sort.SliceStable(chains, func(i, j int) bool {
		if !chains[i].ts.Equal(chains[j].ts) {
			return chains[i].ts.Before(chains[j].ts)
		}
		return chains[i].file < chains[j].file
	})

	findings := make([]model.Finding, 0, len(chains))
	for _, ch := range chains {
		findings = append(findings, ch.finding)
	}
	sig.Findings = findings

	switch {
	case len(findings) > 0:
		sig.State = model.StateAlert
		sig.Summary = engine.Plural(len(findings),
			"sensitive file was edited after reading external content in the same session",
			"sensitive files were edited after reading external content in the same session") + "."
	default:
		sig.State = model.StateClear
		sig.Summary = "No sensitive files were edited after reading external content."
	}
	sig.Summary = model.Clip(sig.Summary, 160)

	sig.Data = map[string]any{
		"external_reads": externalReads,
		"chains":         len(findings),
	}
	return sig
}

// exp3Before reports whether a happened before b in the same session.
func exp3Before(a, b *model.Event) bool {
	if !a.TS.Equal(b.TS) {
		return a.TS.Before(b.TS)
	}
	return a.Seq < b.Seq
}

// exp3DescribeLookup renders a web search or fetch as a readable phrase.
func exp3DescribeLookup(l *model.Lookup) string {
	if l == nil {
		return "`lookup`"
	}
	if l.Kind == "search" {
		return fmt.Sprintf("search “%s”", model.Clip(l.Query, 40))
	}
	if len(l.URLs) > 0 {
		return fmt.Sprintf("`%s`", model.Clip(l.URLs[0], 60))
	}
	if l.Query != "" {
		return fmt.Sprintf("search “%s”", model.Clip(l.Query, 40))
	}
	return fmt.Sprintf("`%s`", l.Kind)
}

// exp3ReadExcerpt renders an external read as evidence text.
func exp3ReadExcerpt(it engine.Item) string {
	switch it.E.Kind {
	case model.KindCommand:
		if it.E.Command != nil {
			return it.E.Command.Cmd
		}
	case model.KindMCP:
		if it.E.MCP != nil {
			return it.E.MCP.Server + "." + it.E.MCP.Tool + " " + it.E.MCP.Args
		}
	case model.KindLookup:
		if it.E.Lookup != nil {
			return it.E.Lookup.Kind + " " + strings.Join(append([]string{it.E.Lookup.Query}, it.E.Lookup.URLs...), " ")
		}
	}
	return ""
}

// exp3EditExcerpt renders a sensitive edit as evidence text.
func exp3EditExcerpt(ed *model.FileEdit) string {
	return ed.RelPath + ": " + strings.Join(ed.Added, " ")
}
