package decisions

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type dec1 struct{}

func init() { engine.Register(dec1{}) }

func (dec1) ID() string { return "DEC-1" }

// correctionKeywords are checked, lowercased, against a candidate correction
// prompt's text: the text must start with one of these, or one of these must
// appear within the text's first 60 characters.
var correctionKeywords = []string{
	"no,", "no ", "nope", "don't", "dont ", "do not",
	"stop", "revert", "undo", "wrong", "that's not", "thats not",
	"not what", "instead", "why did you", "you broke", "roll back",
	"rollback", "put it back", "that broke",
}

// isCandidateCorrectionText reports whether text (a raw prompt) matches the
// DEC-1 correction-prompt keyword-boundary rule.
func isCandidateCorrectionText(text string) bool {
	if utf8.RuneCountInString(text) > 400 {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	head := t
	if utf8.RuneCountInString(head) > 60 {
		r := []rune(head)
		head = string(r[:60])
	}
	for _, kw := range correctionKeywords {
		if strings.HasPrefix(t, kw) || strings.Contains(head, kw) {
			return true
		}
	}
	return false
}

// hasActionInTurn reports whether s has a command or file_edit event with
// Turn == turn.
func hasActionInTurn(s *model.Session, turn int) bool {
	for i := range s.Events {
		e := &s.Events[i]
		if e.Turn != turn {
			continue
		}
		if e.Kind == model.KindCommand || e.Kind == model.KindEdit {
			return true
		}
	}
	return false
}

// editsInTurn returns non-failed file_edit items of s with the given Turn,
// timestamped before "before".
func editsInTurn(s *model.Session, turn int, before ...func(model.Event) bool) []engine.Item {
	var out []engine.Item
	for i := range s.Events {
		e := &s.Events[i]
		if e.Turn != turn || e.Kind != model.KindEdit || e.Edit == nil || e.Edit.Failed {
			continue
		}
		ok := true
		for _, pred := range before {
			if !pred(*e) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, engine.Item{S: s, E: e})
		}
	}
	return out
}

// distinctEditFiles returns the distinct RelPath values of items' file edits,
// in first-seen order.
func distinctEditFiles(items []engine.Item) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if it.E.Edit == nil || it.E.Edit.RelPath == "" || seen[it.E.Edit.RelPath] {
			continue
		}
		seen[it.E.Edit.RelPath] = true
		out = append(out, it.E.Edit.RelPath)
	}
	return out
}

func (d dec1) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	var findings []model.Finding
	var interrupts, rejections, denials, corrections, touchingPR int

	for _, it := range c.Timeline {
		if it.E.AgentID != "" {
			continue
		}
		switch it.E.Kind {
		case model.KindInterrupt:
			turn := it.E.Turn
			touched := editsInTurn(it.S, turn, func(e model.Event) bool { return e.TS.Before(it.E.TS) })
			f := dec1Finding(c, it, "interrupt", interruptText(it, touched), touched, false)
			findings = append(findings, f)
			interrupts++
			if len(f.Anchors) > 0 {
				touchingPR++
			}

		case model.KindRejection:
			text := fmt.Sprintf("Rejected a `%s` call at %s.", it.E.Rejection.Tool, engine.Clock(it.E.TS))
			f := dec1Finding(c, it, "rejection", text, nil, false)
			findings = append(findings, f)
			rejections++

		case model.KindPermission:
			p := it.E.Permission
			if p == nil || p.Decision != "deny" || p.By == "auto" {
				continue
			}
			text := fmt.Sprintf("Denied a `%s` permission request at %s.", p.Tool, engine.Clock(it.E.TS))
			f := dec1Finding(c, it, "denial", text, nil, false)
			findings = append(findings, f)
			denials++

		case model.KindPrompt:
			p := it.E.Prompt
			if p == nil || !isCandidateCorrectionText(p.Text) {
				continue
			}
			checkTurn := it.E.Turn - 1
			if p.Steering {
				checkTurn = it.E.Turn
			}
			if !hasActionInTurn(it.S, checkTurn) {
				continue
			}
			touched := editsInTurn(it.S, checkTurn, func(e model.Event) bool { return e.TS.Before(it.E.TS) })
			text := correctionText(it, touched)
			f := dec1Finding(c, it, "correction_prompt", text, touched, true)
			findings = append(findings, f)
			corrections++
			if len(f.Anchors) > 0 {
				touchingPR++
			}
		}
	}

	sig.Findings = findings
	n := interrupts + rejections + denials + corrections
	sig.Data = map[string]any{
		"interrupts":         interrupts,
		"rejections":         rejections,
		"denials":            denials,
		"correction_prompts": corrections,
		"touching_pr":        touchingPR,
	}
	if n == 0 {
		sig.State = model.StateClear
		sig.Summary = "No interrupts, rejections or corrections."
		return sig
	}
	var parts []string
	if interrupts > 0 {
		parts = append(parts, engine.Plural(interrupts, "interrupt", "interrupts"))
	}
	if rejections > 0 {
		parts = append(parts, engine.Plural(rejections, "rejection", "rejections"))
	}
	if corrections > 0 {
		parts = append(parts, engine.Plural(corrections, "correction prompt", "correction prompts"))
	}
	var summary string
	if len(parts) > 0 {
		summary = fmt.Sprintf("%s (%s); %s.",
			engine.Plural(n, "human correction", "human corrections"),
			strings.Join(parts, ", "),
			engine.Plural(touchingPR, "touched PR line", "touched PR lines"))
	} else {
		summary = fmt.Sprintf("%s; %s.",
			engine.Plural(n, "human correction", "human corrections"),
			engine.Plural(touchingPR, "touched PR line", "touched PR lines"))
	}
	sig.Summary = summary
	if touchingPR > 0 {
		sig.State = model.StateAlert
	} else {
		sig.State = model.StateInfo
	}
	return sig
}

func interruptText(it engine.Item, touched []engine.Item) string {
	files := distinctEditFiles(touched)
	switch len(files) {
	case 0:
		return fmt.Sprintf("Interrupted at %s.", engine.Clock(it.E.TS))
	case 1:
		return fmt.Sprintf("Interrupted at %s while the agent was editing `%s`.", engine.Clock(it.E.TS), files[0])
	default:
		return fmt.Sprintf("Interrupted at %s while the agent was editing `%s`, `%s`.", engine.Clock(it.E.TS), files[0], files[1])
	}
}

func correctionText(it engine.Item, touched []engine.Item) string {
	clip := model.Clip(it.E.Prompt.Text, 80)
	text := fmt.Sprintf("Correction at %s: “%s”", engine.Clock(it.E.TS), clip)
	files := distinctEditFiles(touched)
	if len(files) > 0 {
		text += fmt.Sprintf(" (changes to `%s`)", files[0])
	}
	return text
}

// dec1Finding builds a Finding for a DEC-1 correction event. touched is the
// (possibly empty) set of file_edit items this correction is believed to
// have affected.
func dec1Finding(c *engine.Context, it engine.Item, kind, summary string, touched []engine.Item, candidate bool) model.Finding {
	anchors := c.AnchorsFor(touched...)
	sev := model.StateInfo
	if len(anchors) > 0 {
		sev = model.StateAlert
	}
	data := map[string]any{"kind": kind}
	if candidate {
		data["candidate"] = true
	}
	excerpt := ""
	switch kind {
	case "interrupt":
		excerpt = it.E.Interrupt.Reason
	case "rejection":
		excerpt = it.E.Rejection.Tool
	case "denial":
		excerpt = it.E.Permission.Tool
	case "correction_prompt":
		excerpt = it.E.Prompt.Text
	}
	evidence := []model.Evidence{c.Evidence(it, excerpt)}
	for i, t := range touched {
		if i >= 2 {
			break
		}
		evidence = append(evidence, c.Evidence(t, t.E.Edit.RelPath))
	}
	return model.Finding{
		Summary:  summary,
		Severity: sev,
		Anchors:  anchors,
		Evidence: evidence,
		Data:     data,
	}
}
