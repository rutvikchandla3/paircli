// Package judge is the grounded LLM pass: it builds a compact fact bundle from
// the engine context and the deterministic signals, runs four small LLM jobs
// over it (claims, story, scope, caveats), and returns validated
// model.Judgments in which every item cites ids that appear in the bundle.
// Anything uncited, citing an unknown id, or using an invalid enum is dropped.
package judge

import (
	"context"
	"fmt"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// Run builds the fact bundle from ec and signals, runs the four judge jobs in
// order, and returns their validated judgments.
//
// A nil provider means the LLM pass is disabled: Run returns (nil, nil). A
// provider error on one job is recorded in Judgments.Errors and the remaining
// jobs still run; Run returns an error only when every job failed.
func Run(ctx context.Context, p llm.Provider, ec *engine.Context, signals []model.Signal, cfg *config.Config) (*model.Judgments, error) {
	j, _, err := RunFull(ctx, p, ec, signals, cfg)
	return j, err
}

// RunFull is Run plus the accounting. Stats says which jobs produced nothing
// usable, and per item kind how many items a job offered versus how many
// survived validation, so a caller can tell "nothing was found" apart from
// "nothing could be checked".
//
// It is the provider-driven half of the pass. A caller that runs the prompts
// elsewhere uses BuildPrompts and Set.Apply instead, and gets the same Stats.
func RunFull(ctx context.Context, p llm.Provider, ec *engine.Context, signals []model.Signal, cfg *config.Config) (*model.Judgments, *Stats, error) {
	if p == nil {
		return nil, nil, nil
	}
	set, err := BuildPrompts(ec, signals, cfg)
	if err != nil {
		return nil, nil, err
	}

	results := make([]JobResult, len(set.Prompts))
	for i, jp := range set.Prompts {
		results[i] = fetch(ctx, p, jp)
	}
	stats := set.Apply(results)

	if stats.Failed() == len(set.Prompts) {
		return nil, nil, fmt.Errorf("judge: all %d jobs failed", len(set.Prompts))
	}
	return set.Judgments(), stats, nil
}
