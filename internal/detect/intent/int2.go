package intent

import (
	"fmt"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type int2 struct{}

func init() { engine.Register(int2{}) }

func (int2) ID() string { return "INT-2" }

func (d int2) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	// Collect question events
	questions := c.Of(model.KindQuestion)

	if len(questions) == 0 {
		sig.State = model.StateClear
		sig.Summary = "The agent asked no questions."
		sig.Data = map[string]any{"count": 0}
		return sig
	}

	// Process each question event and its QA items
	for _, it := range questions {
		q := it.E.Question
		for _, qa := range q.Items {
			// Build finding text
			questionText := model.Clip(qa.Question, 100)
			var findingSummary string

			if qa.Answer == "" {
				findingSummary = fmt.Sprintf("Q: %s (no answer recorded)", questionText)
			} else {
				answerText := model.Clip(qa.Answer, 80)
				findingSummary = fmt.Sprintf("Q: %s → %s", questionText, answerText)
			}

			finding := model.Finding{
				Summary:  findingSummary,
				Severity: model.StateInfo,
				Evidence: []model.Evidence{c.Evidence(it, qa.Question)},
				Data: map[string]any{
					"header":  qa.Header,
					"options": qa.Options,
				},
			}

			sig.Findings = append(sig.Findings, finding)
		}
	}

	// Count total QA decisions
	totalQAs := 0
	for _, it := range questions {
		q := it.E.Question
		totalQAs += len(q.Items)
	}

	// Build signal summary
	if totalQAs > 0 {
		// Find the last question event in timeline
		var lastEvent *engine.Item
		for i := len(questions) - 1; i >= 0; i-- {
			if len(questions[i].E.Question.Items) > 0 {
				lastEvent = &questions[i]
				break
			}
		}

		if lastEvent != nil {
			clockStr := engine.Clock(lastEvent.E.TS)
			sig.Summary = fmt.Sprintf("%d decisions made by a human, latest at %s.",
				totalQAs, clockStr)
		}
		sig.State = model.StateInfo
	}

	sig.Data = map[string]any{"count": totalQAs}
	return sig
}
