package consistency

import (
	"fmt"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/enrich"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type con3 struct{}

func init() { engine.Register(con3{}) }

func (con3) ID() string { return "CON-3" }

func (d con3) Detect(c *engine.Context) model.Signal {
	if c.Judgments == nil {
		return engine.Unknown(d.ID(), llmOff)
	}
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	scope := c.Judgments.Scope
	var traced, untraced int
	for _, item := range scope {
		if item.Traced {
			traced++
			continue
		}
		untraced++
		sig.Findings = append(sig.Findings, model.Finding{
			Summary:    fmt.Sprintf("%s does not trace to the ask, plan or a decision.", fileLines(item.File, item.Lines)),
			Severity:   model.StateAlert,
			Provenance: model.Inferred,
			Anchors:    enrich.Anchors(item.File, item.Lines, item.Cites),
			Evidence:   enrich.Evidence(c, item.Cites),
		})
	}

	sig.Summary = fmt.Sprintf("%d of %d hunks trace to the ask or plan; %d do not.",
		traced, len(scope), untraced)
	if untraced > 0 {
		sig.State = model.StateAlert
	} else {
		sig.State = model.StateClear
	}
	return sig
}

// fileLines renders a hunk as "`file` 10-12", or just "`file`" when the
// judgment carried no line range.
func fileLines(file, lines string) string {
	if lines == "" {
		return "`" + file + "`"
	}
	return fmt.Sprintf("`%s` %s", file, lines)
}
