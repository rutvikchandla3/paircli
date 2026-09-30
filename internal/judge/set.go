package judge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// jobPrompt is one job's precomputed requests: the first attempt and the single
// retry. The retry is the first request with retryPrompt appended, computed up
// front so a caller that only runs prompts never has to know the retry rule.
type jobPrompt struct {
	spec  jobSpec
	first llm.Request
	retry llm.Request
}

// newJobPrompt builds the two requests one job can need over the marshalled
// fact bundle.
func newJobPrompt(jb jobSpec, cfg *config.Config, raw []byte) jobPrompt {
	first := jb.request(cfg, raw)
	retry := first
	retry.Prompt += "\n\n" + retryPrompt
	return jobPrompt{spec: jb, first: first, retry: retry}
}

// JobResult is the raw material one job produced: either a transport error, or
// the reply texts in attempt order (one text, or two when the first was
// retried). It is what a remote judge backend returns for one job.
type JobResult struct {
	Err   error
	Texts []string
}

// Stats records what the judge pass could not use. It rides beside
// model.Judgments rather than inside it: model is a frozen contract, so the
// accounting lives here.
type Stats struct {
	// Jobs marks a job that produced no usable result, either because the
	// provider errored or because both replies were invalid JSON.
	Jobs map[string]bool
	// Items counts, per item kind, how many items a job offered and how many
	// survived citation validation. Attempted>0 with kept==0 means the reply
	// was well-formed but nothing in it could be grounded.
	Items map[string][2]int
}

func newStats() *Stats {
	return &Stats{Jobs: map[string]bool{}, Items: map[string][2]int{}}
}

// Failed reports how many jobs produced no usable result.
func (s *Stats) Failed() int {
	if s == nil {
		return 0
	}
	n := 0
	for _, bad := range s.Jobs {
		if bad {
			n++
		}
	}
	return n
}

// UnresolvedItem is an inferred signal whose judgment input was unusable: its
// job failed, or the job answered with items none of which could be grounded.
type UnresolvedItem struct {
	Signal    string
	Kind      string
	Attempted int
	Reason    string
}

// inferredSources ties each signal computed from the judgments alone to the job
// that feeds it and the item kind that carries it. Only these three are listed:
// every other signal has a deterministic measurement behind it, and overwriting
// that with unknown would discard real evidence.
var inferredSources = []struct {
	job    string
	kind   string
	signal string
}{
	{"claims", kindClaims, "CON-1"},
	{"scope", kindScope, "CON-3"},
	{"story", kindDecisions, "DEC-3"},
}

// Unresolved returns the inferred signals that must report unknown rather than
// clear, because nothing was actually checked: the job failed outright, or it
// offered items and none survived validation.
//
// A job that legitimately returned an empty list is NOT listed. "We found no
// claims" and "we could not check any claims" are different answers, and only
// the second is unknown.
func (s *Stats) Unresolved() []UnresolvedItem {
	if s == nil {
		return nil
	}
	var out []UnresolvedItem
	for _, src := range inferredSources {
		if s.Jobs[src.job] {
			out = append(out, UnresolvedItem{
				Signal: src.signal,
				Kind:   src.kind,
				Reason: src.job + " job failed; nothing was checked.",
			})
			continue
		}
		if n := s.Items[src.kind]; n[0] > 0 && n[1] == 0 {
			out = append(out, UnresolvedItem{
				Signal:    src.signal,
				Kind:      src.kind,
				Attempted: n[0],
				Reason:    fmt.Sprintf("all %d %s items were dropped; nothing was checked.", n[0], src.kind),
			})
		}
	}
	return out
}

// Unresolved is Unresolved over the Set's own accounting, for a caller that
// drives the prompts itself rather than through RunFull.
func (s *Set) Unresolved() []UnresolvedItem { return s.stats.Unresolved() }

// Set is the judge's prepared work: the marshalled fact bundle, the four job
// prompts, and the state a validated reply is merged into. BuildPrompts
// produces it; a caller either runs the prompts through a provider
// (RunFull) or ships them elsewhere and hands the replies back to Apply.
type Set struct {
	// Raw is the marshalled fact bundle every prompt embeds.
	Raw     []byte
	Prompts []jobPrompt

	j     *model.Judgments
	v     *validator
	stats *Stats
}

// BuildPrompts builds the fact bundle from ec and signals and prepares the four
// job prompts. A nil cfg means defaults.
func BuildPrompts(ec *engine.Context, signals []model.Signal, cfg *config.Config) (*Set, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	bundle, ids := BuildBundle(ec, signals, cfg.LLM.MaxInputChars)
	raw, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("judge: marshal fact bundle: %w", err)
	}
	// One Stats is shared by the Set and the validator, so the validator's
	// per-item-kind counting lands where the caller reads it.
	stats := newStats()
	s := &Set{
		Raw:   raw,
		j:     &model.Judgments{Model: cfg.LLM.Model},
		v:     &validator{ids: ids, ec: ec, stats: stats},
		stats: stats,
	}
	for _, jb := range jobSpecs() {
		s.Prompts = append(s.Prompts, newJobPrompt(jb, cfg, raw))
	}
	return s, nil
}

// Judgments returns the judgments accumulated so far. It is the same value
// Apply merges into, so call it after Apply.
func (s *Set) Judgments() *model.Judgments { return s.j }

// Stats returns the accounting for the jobs applied so far.
func (s *Set) Stats() *Stats { return s.stats }

// Decode reports whether text is a valid reply for job i, without merging it.
// A caller that wants to retry a malformed reply itself can use it to decide.
func (s *Set) Decode(i int, text string) (any, bool) {
	if i < 0 || i >= len(s.Prompts) {
		return nil, false
	}
	reply := s.Prompts[i].spec.newReply()
	if !decode(text, reply) {
		return nil, false
	}
	return reply, true
}

// Apply validates and merges one JobResult per prompt, in prompt order, and
// returns the accounting. results must have one entry per prompt; a short slice
// is treated as missing replies rather than silently ignored.
//
// It records exactly one entry in Judgments.Errors per job that produced
// nothing usable, so the error list stays one-per-job in job order regardless
// of whether the failure was a transport error or an unusable reply.
func (s *Set) Apply(results []JobResult) *Stats {
	for i, jp := range s.Prompts {
		if i >= len(results) {
			s.j.Errors = append(s.j.Errors, jp.spec.name+": no reply")
			s.stats.Jobs[jp.spec.name] = true
			continue
		}
		if !applyResult(s.j, s.v, jp, results[i]) {
			s.stats.Jobs[jp.spec.name] = true
		}
	}
	return s.stats
}

// fetch runs one job's prompts through p: the first attempt, then the retry
// only if the first reply was not valid JSON.
func fetch(ctx context.Context, p llm.Provider, jp jobPrompt) JobResult {
	resp, err := p.Complete(ctx, jp.first)
	if err != nil {
		return JobResult{Err: err}
	}
	if decodable(jp.spec, resp.Text) {
		return JobResult{Texts: []string{resp.Text}}
	}
	resp2, err := p.Complete(ctx, jp.retry)
	if err != nil {
		return JobResult{Err: err}
	}
	return JobResult{Texts: []string{resp.Text, resp2.Text}}
}

// decodable reports whether text parses as the job's reply shape.
func decodable(jb jobSpec, text string) bool {
	return decode(text, jb.newReply())
}

// applyResult merges the first decodable text of res into j, or records why it
// could not. It reports whether the job produced a result.
func applyResult(j *model.Judgments, v *validator, jp jobPrompt, res JobResult) bool {
	if res.Err != nil {
		j.Errors = append(j.Errors, jp.spec.name+": "+res.Err.Error())
		return false
	}
	for _, text := range res.Texts {
		reply := jp.spec.newReply()
		if decode(text, reply) {
			jp.spec.apply(j, reply, v)
			return true
		}
	}
	j.Errors = append(j.Errors, jp.spec.name+": invalid JSON")
	return false
}
