package intent

import (
	"fmt"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type int3 struct{}

func init() { engine.Register(int3{}) }

func (int3) ID() string { return "INT-3" }

func (d int3) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	// Collect plan events
	plans := c.Of(model.KindPlan)

	if len(plans) == 0 {
		sig.State = model.StateClear
		sig.Summary = "No plan was recorded."
		sig.Data = map[string]any{"plans": []any{}}
		return sig
	}

	// Process each plan
	var planData []map[string]any
	var latestApprovedPlan *engine.Item
	var latestApprovedPlanData map[string]any

	for _, it := range plans {
		p := it.E.Plan

		// Count non-empty lines or steps
		var linesCount int
		var linesDesc string
		if len(p.Steps) > 0 {
			linesCount = len(p.Steps)
			linesDesc = "steps"
		} else if p.Text != "" {
			// Count non-empty lines
			lines := strings.Split(strings.TrimSpace(p.Text), "\n")
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					linesCount++
				}
			}
			linesDesc = "lines"
		}

		// Determine approval status
		var approvalWord string
		var approvalStatus interface{} = false
		if p.Approved != nil {
			if *p.Approved {
				approvalWord = "approved"
				approvalStatus = true
				latestApprovedPlan = &it
			} else {
				approvalWord = "rejected"
			}
		} else {
			approvalWord = "proposed"
			approvalStatus = nil
		}

		clockStr := engine.Clock(it.E.TS)
		var findingSummary string
		if linesCount > 0 {
			findingSummary = fmt.Sprintf("Plan %s at %s (%d %s).",
				approvalWord, clockStr, linesCount, linesDesc)
		} else {
			findingSummary = fmt.Sprintf("Plan %s at %s.", approvalWord, clockStr)
		}

		// Get excerpt (first line of plan)
		var excerpt string
		if p.Text != "" {
			lines := strings.Split(strings.TrimSpace(p.Text), "\n")
			if len(lines) > 0 && lines[0] != "" {
				excerpt = lines[0]
			}
		}

		finding := model.Finding{
			Summary:  findingSummary,
			Severity: model.StateInfo,
			Evidence: []model.Evidence{c.Evidence(it, excerpt)},
		}

		sig.Findings = append(sig.Findings, finding)

		// Build data entry
		planEntry := map[string]any{
			"session":  it.S.Ref(),
			"event":    it.E.ID,
			"approved": approvalStatus,
			"source":   p.Source,
			"text":     model.Clip(p.Text, 4000),
		}
		planData = append(planData, planEntry)

		// Keep latest approved plan info for summary
		if p.Approved != nil && *p.Approved {
			latestApprovedPlanData = map[string]any{
				"session": it.S.Ref(),
				"ts":      clockStr,
			}
		}
	}

	// Build signal summary
	if latestApprovedPlan != nil && latestApprovedPlanData != nil {
		session := latestApprovedPlanData["session"].(string)
		ts := latestApprovedPlanData["ts"].(string)
		sig.Summary = fmt.Sprintf("Plan approved at %s in %s; %d plans proposed in total.",
			ts, session, len(plans))
		sig.State = model.StateInfo
	} else {
		// No approved plan, but plans exist
		sig.Summary = fmt.Sprintf("%d plans proposed; none recorded as approved.",
			len(plans))
		sig.State = model.StateInfo
	}

	sig.Data = map[string]any{"plans": planData}
	return sig
}
