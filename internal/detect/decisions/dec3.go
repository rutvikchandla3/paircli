package decisions

import (
	"fmt"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/enrich"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type dec3 struct{}

func init() { engine.Register(dec3{}) }

func (dec3) ID() string { return "DEC-3" }

// llmOff is the summary DEC-3 reports while the grounded LLM pass is off. The
// three inferred signals carry no deterministic evidence at all, so there is
// nothing honest to say until the pass runs.
const llmOff = "LLM pass is off. Run with --llm to fill this in."

func (d dec3) Detect(c *engine.Context) model.Signal {
	if c.Judgments == nil {
		return engine.Unknown(d.ID(), llmOff)
	}
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	decisions := c.Judgments.Decisions
	if len(decisions) == 0 {
		sig.State = model.StateClear
		sig.Summary = "No decisions were identified."
		return sig
	}

	for _, dec := range decisions {
		sig.Findings = append(sig.Findings, model.Finding{
			Summary:    decisionText(dec),
			Severity:   model.StateInfo,
			Provenance: model.Inferred,
			Evidence:   enrich.Evidence(c, dec.Cites),
		})
	}

	sig.State = model.StateInfo
	sig.Summary = model.Clip(fmt.Sprintf("%d decisions shaped this diff; first: %s.",
		len(decisions), model.Clip(decisions[0].Choice, 80)), 160)
	return sig
}

// decisionText renders one decision as "{choice} — {reason} ({by})". The
// parenthetical parts are dropped when the judgment did not carry them.
func decisionText(d model.DecisionItem) string {
	var b strings.Builder
	b.WriteString(d.Choice)
	if d.Reason != "" {
		b.WriteString(" — ")
		b.WriteString(d.Reason)
	}
	if d.By != "" {
		b.WriteString(" (")
		b.WriteString(d.By)
		b.WriteString(")")
	}
	return b.String()
}
