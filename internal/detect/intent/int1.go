package intent

import (
	"fmt"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type int1 struct{}

func init() { engine.Register(int1{}) }

func (int1) ID() string { return "INT-1" }

func (d int1) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	// Collect prompts from main agent only
	prompts := c.Of(model.KindPrompt)
	var mainPrompts []engine.Item
	for _, it := range prompts {
		if it.E.AgentID == "" {
			mainPrompts = append(mainPrompts, it)
		}
	}

	if len(mainPrompts) == 0 {
		sig.State = model.StateInfo
		sig.Summary = "No human prompts were recorded."
		sig.Data = map[string]any{
			"prompts":  0,
			"steering": 0,
			"sessions": 0,
		}
		return sig
	}

	// Build findings and collect stats
	steeringCount := 0
	sessionsSet := make(map[string]bool)
	var firstText string

	for _, it := range mainPrompts {
		p := it.E.Prompt

		// Build text: slash command format if present
		var text string
		if p.SlashCommand != "" {
			text = fmt.Sprintf("%s %s", p.SlashCommand, p.Text)
		} else {
			text = p.Text
		}

		// Collapse newlines
		text = strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == '\t' {
				return ' '
			}
			return r
		}, text)
		text = strings.Join(strings.Fields(text), " ")

		if firstText == "" {
			firstText = text
		}

		// Count steering
		if p.Steering {
			steeringCount++
		}

		// Track sessions
		sessionsSet[it.S.Ref()] = true

		// Build finding
		summaryText := model.Clip(text, 150)
		if p.Steering {
			summaryText = "(mid-task) " + summaryText
		}

		finding := model.Finding{
			Summary:  summaryText,
			Severity: model.StateInfo,
			Evidence: []model.Evidence{c.Evidence(it, text)},
			Data: map[string]any{
				"session":  it.S.Ref(),
				"turn":     it.E.Turn,
				"steering": p.Steering,
			},
		}

		// Add slash_command if present
		if p.SlashCommand != "" {
			finding.Data["slash_command"] = p.SlashCommand
		}

		sig.Findings = append(sig.Findings, finding)
	}

	// Build signal summary
	sessionCount := len(sessionsSet)
	promptCount := len(mainPrompts)

	summaryParts := []string{
		engine.Plural(promptCount, "prompt", "prompts"),
		"across",
		engine.Plural(sessionCount, "session", "sessions"),
	}
	summaryText := strings.Join(summaryParts, " ") + fmt.Sprintf("; first: %q.", model.Clip(firstText, 80))

	// Add steering info if it fits
	if steeringCount > 0 {
		steeringStr := fmt.Sprintf(" %d sent mid-task.", steeringCount)
		if len(summaryText)+len(steeringStr) <= 160 {
			summaryText += steeringStr
		}
	}

	sig.State = model.StateInfo
	sig.Summary = summaryText
	sig.Data = map[string]any{
		"prompts":  promptCount,
		"steering": steeringCount,
		"sessions": sessionCount,
	}

	return sig
}
