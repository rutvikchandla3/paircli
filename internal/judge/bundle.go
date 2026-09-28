package judge

import (
	"encoding/json"
	"fmt"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// DefaultMaxChars is the fact-bundle budget used when the config does not set
// one (config.LLM.MaxInputChars).
const DefaultMaxChars = 200000

// Bundle is the compact JSON fact bundle the four judge jobs read. Every item
// carries an id the jobs must cite:
//
//	events:  ev:<session ref>/<event id>
//	signals: sig:<signal id>
//	hunks:   file:<path>:<new start>-<new end>
type Bundle struct {
	PR            BundlePR         `json:"pr"`
	Asks          []BundleText     `json:"asks"`
	Decisions     []BundleQA       `json:"decisions"`
	Plans         []BundlePlan     `json:"plans"`
	FinalMessages []BundleText     `json:"final_messages"`
	Corrections   []BundleText     `json:"corrections"`
	Abandoned     []BundleText     `json:"abandoned"`
	Compactions   []BundleText     `json:"compactions"`
	Rules         []BundleText     `json:"rules"`
	Signals       []BundleSignal   `json:"signals"`
	Diff          []BundleDiffFile `json:"diff"`
}

// BundlePR is the pull request header.
type BundlePR struct {
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Files []BundlePRFile `json:"files"`
}

// BundlePRFile is one file touched by the PR.
type BundlePRFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// BundleText is a text item with an event id.
type BundleText struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Steering marks an ask that was sent while the agent was mid-turn.
	Steering bool `json:"steering,omitempty"`
}

// BundleQA is one question the agent asked and the human's answer.
type BundleQA struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Answer   string   `json:"answer"`
}

// BundlePlan is a plan the agent proposed.
type BundlePlan struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Approved bool   `json:"approved"`
}

// BundleSignal is one non-unknown deterministic signal.
type BundleSignal struct {
	ID       string   `json:"id"`
	State    string   `json:"state"`
	Summary  string   `json:"summary"`
	Findings []string `json:"findings,omitempty"`
}

// BundleDiffFile is one PR file's hunks.
type BundleDiffFile struct {
	Path  string       `json:"path"`
	Hunks []BundleHunk `json:"hunks,omitempty"`
}

// BundleHunk is one hunk of the PR diff.
type BundleHunk struct {
	ID     string   `json:"id"`
	Header string   `json:"header"`
	Added  []string `json:"added,omitempty"`
}

// Fixed content limits of the fact bundle.
const (
	bundlePRBodyChars      = 4000
	bundlePlanChars        = 4000
	bundleQAChars          = 500
	bundleFindingSummaries = 10
)

// bundleCaps are the per-item limits applied while building the bundle. They
// start at the documented defaults and shrink (in the documented order) until
// the marshalled bundle fits the budget.
type bundleCaps struct {
	hunkLines  int // added lines kept per hunk
	ruleChars  int // instruction contents
	compChars  int // compaction summaries
	askChars   int // ask texts
	diffFiles  int // PR files with hunks
	finalChars int // final message texts
}

// defaultCaps are the documented per-item limits.
func defaultCaps() bundleCaps {
	return bundleCaps{hunkLines: 40, ruleChars: 3000, compChars: 3000, askChars: 1000, diffFiles: 30, finalChars: 2000}
}

// capsLadder is the trimming sequence: the documented order first, then a few
// progressively harsher fallbacks so that any budget can be met.
func capsLadder() []bundleCaps {
	return []bundleCaps{
		defaultCaps(),
		{hunkLines: 10, ruleChars: 3000, compChars: 3000, askChars: 1000, diffFiles: 30, finalChars: 2000},
		{hunkLines: 10, ruleChars: 500, compChars: 3000, askChars: 1000, diffFiles: 30, finalChars: 2000},
		{hunkLines: 10, ruleChars: 500, compChars: 1000, askChars: 1000, diffFiles: 30, finalChars: 2000},
		{hunkLines: 10, ruleChars: 500, compChars: 1000, askChars: 300, diffFiles: 30, finalChars: 2000},
		{hunkLines: 10, ruleChars: 500, compChars: 1000, askChars: 300, diffFiles: 15, finalChars: 2000},
		{hunkLines: 10, ruleChars: 500, compChars: 1000, askChars: 300, diffFiles: 15, finalChars: 500},
		{hunkLines: 5, ruleChars: 300, compChars: 500, askChars: 200, diffFiles: 10, finalChars: 300},
		{hunkLines: 2, ruleChars: 200, compChars: 300, askChars: 100, diffFiles: 5, finalChars: 200},
		{hunkLines: 1, ruleChars: 100, compChars: 100, askChars: 50, diffFiles: 2, finalChars: 100},
		{hunkLines: 0, ruleChars: 0, compChars: 0, askChars: 0, diffFiles: 0, finalChars: 0},
	}
}

// BuildBundle builds the fact bundle from the engine context and the
// deterministic signals, trimming it until json.Marshal fits maxChars, in the
// documented order: hunk lines to 10 each, rule contents to 500, compaction
// summaries to 1000, asks to 300, diff files to 15, final messages to 500.
// It also returns the set of valid ids appearing in the returned bundle.
func BuildBundle(ec *engine.Context, signals []model.Signal, maxChars int) (*Bundle, map[string]bool) {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	ladder := capsLadder()
	b := buildBundle(ec, signals, ladder[0])
	for _, c := range ladder[1:] {
		if bundleSize(b) <= maxChars {
			break
		}
		b = buildBundle(ec, signals, c)
	}
	return b, bundleIDs(b)
}

func buildBundle(ec *engine.Context, signals []model.Signal, c bundleCaps) *Bundle {
	b := &Bundle{}
	if ec == nil {
		return b
	}

	if ec.PR != nil {
		b.PR = BundlePR{Title: ec.PR.Title, Body: clip(ec.PR.Body, bundlePRBodyChars)}
		for _, f := range ec.PR.Files {
			b.PR.Files = append(b.PR.Files, BundlePRFile{
				Path:    f.Path,
				Status:  string(f.Status),
				Added:   len(f.AddedLines()),
				Removed: len(f.RemovedLines()),
			})
		}
	}

	// asks: main-agent prompts, in order.
	for _, it := range ec.Of(model.KindPrompt) {
		p := it.E.Prompt
		if p == nil || it.E.AgentID != "" || p.Text == "" {
			continue
		}
		b.Asks = append(b.Asks, BundleText{ID: eventID(it.S, it.E), Text: clip(p.Text, c.askChars), Steering: p.Steering})
	}

	// decisions: question QAs.
	for _, it := range ec.Of(model.KindQuestion) {
		q := it.E.Question
		if q == nil {
			continue
		}
		for _, qa := range q.Items {
			b.Decisions = append(b.Decisions, BundleQA{
				ID:       eventID(it.S, it.E),
				Question: clip(qa.Question, bundleQAChars),
				Options:  qa.Options,
				Answer:   clip(qa.Answer, bundleQAChars),
			})
		}
	}

	// plans.
	for _, it := range ec.Of(model.KindPlan) {
		p := it.E.Plan
		if p == nil || p.Text == "" {
			continue
		}
		b.Plans = append(b.Plans, BundlePlan{
			ID:       eventID(it.S, it.E),
			Text:     clip(p.Text, bundlePlanChars),
			Approved: p.Approved != nil && *p.Approved,
		})
	}

	// final messages: the last main-agent message before a prompt or the end.
	for _, it := range ec.Of(model.KindMessage) {
		m := it.E.Message
		if m == nil || !m.Final || m.Text == "" {
			continue
		}
		b.FinalMessages = append(b.FinalMessages, BundleText{ID: eventID(it.S, it.E), Text: clip(m.Text, c.finalChars)})
	}

	// corrections: DEC-1 correction prompts and interrupts, keyed by their
	// evidence event ids.
	if sig := findSignal(signals, "DEC-1"); sig != nil {
		for _, f := range sig.Findings {
			switch findingKind(f) {
			case "correction_prompt", "interrupt":
			default:
				continue
			}
			if len(f.Evidence) == 0 {
				continue
			}
			b.Corrections = append(b.Corrections, BundleText{ID: evidenceID(f.Evidence[0]), Text: f.Summary})
		}
	}

	// abandoned: DEC-2 findings, keyed by their first evidence event.
	if sig := findSignal(signals, "DEC-2"); sig != nil {
		for _, f := range sig.Findings {
			if len(f.Evidence) == 0 {
				continue
			}
			b.Abandoned = append(b.Abandoned, BundleText{ID: evidenceID(f.Evidence[0]), Text: f.Summary})
		}
	}

	// compactions with summaries.
	for _, it := range ec.Of(model.KindCompaction) {
		cp := it.E.Compaction
		if cp == nil || cp.Summary == "" {
			continue
		}
		b.Compactions = append(b.Compactions, BundleText{ID: eventID(it.S, it.E), Text: clip(cp.Summary, c.compChars)})
	}

	// rules: instruction contents.
	for _, it := range ec.Of(model.KindInstructions) {
		in := it.E.Instructions
		if in == nil || in.Content == "" {
			continue
		}
		b.Rules = append(b.Rules, BundleText{ID: eventID(it.S, it.E), Text: clip(in.Content, c.ruleChars)})
	}

	// signals: every non-unknown signal.
	for _, s := range signals {
		if s.State == model.StateUnknown {
			continue
		}
		bs := BundleSignal{ID: "sig:" + s.ID, State: string(s.State), Summary: s.Summary}
		for i, f := range s.Findings {
			if i >= bundleFindingSummaries {
				break
			}
			bs.Findings = append(bs.Findings, f.Summary)
		}
		b.Signals = append(b.Signals, bs)
	}

	// diff: up to diffFiles PR files, each hunk with its first hunkLines added lines.
	if ec.PR != nil {
		for i, f := range ec.PR.Files {
			if i >= c.diffFiles {
				break
			}
			var bf BundleDiffFile
			bf.Path = f.Path
			for _, h := range f.Hunks {
				var added []string
				for _, l := range h.Lines {
					if l.Kind != model.LineAdd {
						continue
					}
					if len(added) >= c.hunkLines {
						break
					}
					added = append(added, l.Text)
				}
				bf.Hunks = append(bf.Hunks, BundleHunk{ID: hunkID(f.Path, h), Header: hunkHeader(h), Added: added})
			}
			b.Diff = append(b.Diff, bf)
		}
	}

	return b
}

// bundleSize is the marshalled size of b; an unmarshalable bundle counts as
// over budget.
func bundleSize(b *Bundle) int {
	raw, err := json.Marshal(b)
	if err != nil {
		return 1 << 30
	}
	return len(raw)
}

// bundleIDs collects every id that appears in b.
func bundleIDs(b *Bundle) map[string]bool {
	ids := map[string]bool{}
	add := func(s string) {
		if s != "" {
			ids[s] = true
		}
	}
	for _, x := range b.Asks {
		add(x.ID)
	}
	for _, x := range b.Decisions {
		add(x.ID)
	}
	for _, x := range b.Plans {
		add(x.ID)
	}
	for _, x := range b.FinalMessages {
		add(x.ID)
	}
	for _, x := range b.Corrections {
		add(x.ID)
	}
	for _, x := range b.Abandoned {
		add(x.ID)
	}
	for _, x := range b.Compactions {
		add(x.ID)
	}
	for _, x := range b.Rules {
		add(x.ID)
	}
	for _, s := range b.Signals {
		add(s.ID)
	}
	for _, f := range b.Diff {
		for _, h := range f.Hunks {
			add(h.ID)
		}
	}
	return ids
}

// eventID is the bundle id of one event.
func eventID(s *model.Session, e *model.Event) string {
	if s == nil || e == nil {
		return ""
	}
	return "ev:" + s.Ref() + "/" + e.ID
}

// evidenceID is the bundle id of an Evidence.
func evidenceID(ev model.Evidence) string {
	if ev.Session == "" || ev.Event == "" {
		return ""
	}
	return "ev:" + ev.Session + "/" + ev.Event
}

// hunkID is the bundle id of one PR hunk.
func hunkID(path string, h model.DiffHunk) string {
	end := h.NewStart + h.NewLines - 1
	if end < h.NewStart {
		end = h.NewStart
	}
	return fmt.Sprintf("file:%s:%d-%d", path, h.NewStart, end)
}

// hunkHeader renders the unified-diff hunk header.
func hunkHeader(h model.DiffHunk) string {
	head := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Section != "" {
		head += " " + h.Section
	}
	return head
}

// findSignal returns the signal with the given id, or nil.
func findSignal(signals []model.Signal, id string) *model.Signal {
	for i := range signals {
		if signals[i].ID == id {
			return &signals[i]
		}
	}
	return nil
}

// findingKind returns Finding.Data["kind"], or "".
func findingKind(f model.Finding) string {
	if f.Data == nil {
		return ""
	}
	s, _ := f.Data["kind"].(string)
	return s
}

// clip cuts s to n runes; a non-positive n means "no text".
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return model.Clip(s, n)
}
