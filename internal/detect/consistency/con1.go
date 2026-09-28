package consistency

import (
	"fmt"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/enrich"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type con1 struct{}

func init() { engine.Register(con1{}) }

func (con1) ID() string { return "CON-1" }

// llmOff is the summary CON-1 reports while the grounded LLM pass is off.
const llmOff = "LLM pass is off. Run with --llm to fill this in."

func (d con1) Detect(c *engine.Context) model.Signal {
	if c.Judgments == nil {
		return engine.Unknown(d.ID(), llmOff)
	}
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	claims := c.Judgments.Claims
	if len(claims) == 0 {
		sig.State = model.StateClear
		sig.Summary = "No checkable claims found."
		return sig
	}

	var contradicted, supported, noEvidence int
	for _, claim := range claims {
		f := model.Finding{
			Provenance: model.Inferred,
			Evidence:   enrich.Evidence(c, claim.Cites),
		}
		switch claim.Verdict {
		case "contradicted":
			contradicted++
			f.Severity = model.StateAlert
			f.Summary = fmt.Sprintf("“%s” is contradicted: %s", model.Clip(claim.Claim, 80), claim.Reason)
		case "supported":
			supported++
			f.Severity = model.StateInfo
			f.Summary = fmt.Sprintf("“%s” is supported.", claim.Claim)
		default:
			noEvidence++
			f.Severity = model.StateInfo
			f.Summary = fmt.Sprintf("“%s”: no evidence either way.", claim.Claim)
		}
		f.Data = map[string]any{"verdict": claim.Verdict, "source": claim.Source}
		sig.Findings = append(sig.Findings, f)
	}

	sig.Summary = fmt.Sprintf("%d claims contradicted, %d supported, %d without evidence.",
		contradicted, supported, noEvidence)
	if contradicted > 0 {
		sig.State = model.StateAlert
	} else {
		sig.State = model.StateInfo
	}
	return sig
}
