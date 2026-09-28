package authorship

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type auth3 struct{}

func init() { engine.Register(auth3{}) }

func (auth3) ID() string { return "AUTH-3" }

// auth3MaxSummary is the hard cap on Signal.Summary length, in runes.
const auth3MaxSummary = 160

// auth3Key identifies one (harness, model) pair behind the agent-written lines.
type auth3Key struct {
	harness string
	model   string
}

// auth3Model is one counted pair, ready to render.
type auth3Model struct {
	key   auth3Key
	lines int
}

// auth3Harness extracts the harness from a session reference ("<harness>:<id>").
// A line with no source event has no known harness.
func auth3Harness(ref *model.EventRef) string {
	if ref == nil {
		return "unknown"
	}
	harness, _, _ := strings.Cut(ref.Session, ":")
	if harness == "" {
		return "unknown"
	}
	return harness
}

func (d auth3) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	counts := map[auth3Key]int{}
	total := 0
	if attr := c.Attribution; attr != nil {
		for _, f := range attr.Files {
			for _, l := range f.Lines {
				if l.Label != model.LabelAgent && l.Label != model.LabelMixed {
					continue
				}
				name := l.Model
				if name == "" {
					name = "unknown"
				}
				counts[auth3Key{harness: auth3Harness(l.Source), model: name}]++
				total++
			}
		}
	}

	if total == 0 {
		return engine.Unknown(d.ID(), "No agent-written lines in this PR.")
	}

	models := make([]auth3Model, 0, len(counts))
	for k, n := range counts {
		models = append(models, auth3Model{key: k, lines: n})
	}
	// Most lines first; harness then model break ties, so output is stable.
	sort.Slice(models, func(i, j int) bool {
		if models[i].lines != models[j].lines {
			return models[i].lines > models[j].lines
		}
		if models[i].key.harness != models[j].key.harness {
			return models[i].key.harness < models[j].key.harness
		}
		return models[i].key.model < models[j].key.model
	})

	parts := make([]string, 0, len(models))
	for _, m := range models {
		parts = append(parts, fmt.Sprintf("%s:%s (%s)", m.key.harness, m.key.model, engine.Pct(m.lines, total)))
	}

	summary := "Assisted-by: " + strings.Join(parts, ", ") + " of agent lines."
	if len([]rune(summary)) > auth3MaxSummary {
		for k := len(parts) - 1; k >= 1; k-- {
			summary = "Assisted-by: " + strings.Join(parts[:k], ", ") + ", … of agent lines."
			if len([]rune(summary)) <= auth3MaxSummary {
				break
			}
		}
	}

	trailer := make([]string, 0, len(models))
	for _, m := range models {
		trailer = append(trailer, fmt.Sprintf("Assisted-by: %s:%s", m.key.harness, m.key.model))
	}

	dataModels := make([]map[string]any, 0, len(models))
	for _, m := range models {
		dataModels = append(dataModels, map[string]any{
			"harness": m.key.harness,
			"model":   m.key.model,
			"lines":   m.lines,
			"share":   float64(m.lines) / float64(total),
		})
	}

	sig.State = model.StateInfo
	sig.Summary = summary
	sig.Data = map[string]any{
		"models":  dataModels,
		"trailer": strings.Join(trailer, "\n"),
		"efforts": auth3Efforts(c),
	}
	return sig
}

// auth3Efforts collects the distinct reasoning efforts seen per model in
// model_change events, in timeline order.
func auth3Efforts(c *engine.Context) map[string][]string {
	out := map[string][]string{}
	for _, it := range c.Of(model.KindModelChange) {
		mc := it.E.ModelChange
		if mc == nil || mc.Model == "" || mc.Effort == "" {
			continue
		}
		if auth3HasEffort(out[mc.Model], mc.Effort) {
			continue
		}
		out[mc.Model] = append(out[mc.Model], mc.Effort)
	}
	return out
}

// auth3HasEffort reports whether efforts already contains e.
func auth3HasEffort(efforts []string, e string) bool {
	for _, x := range efforts {
		if x == e {
			return true
		}
	}
	return false
}
