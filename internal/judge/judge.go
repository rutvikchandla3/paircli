// Package judge is the grounded LLM pass: it builds a compact fact bundle from
// the engine context and the deterministic signals, runs four small LLM jobs
// over it (claims, story, scope, caveats), and returns validated
// model.Judgments in which every item cites ids that appear in the bundle.
// Anything uncited, citing an unknown id, or using an invalid enum is dropped.
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

// Run builds the fact bundle from ec and signals, runs the four judge jobs in
// order, and returns their validated judgments.
//
// A nil provider means the LLM pass is disabled: Run returns (nil, nil). A
// provider error on one job is recorded in Judgments.Errors and the remaining
// jobs still run; Run returns an error only when every job failed.
func Run(ctx context.Context, p llm.Provider, ec *engine.Context, signals []model.Signal, cfg *config.Config) (*model.Judgments, error) {
	if p == nil {
		return nil, nil
	}
	if cfg == nil {
		cfg = config.Default()
	}

	bundle, ids := BuildBundle(ec, signals, cfg.LLM.MaxInputChars)
	raw, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("judge: marshal fact bundle: %w", err)
	}

	j := &model.Judgments{Model: cfg.LLM.Model}
	v := &validator{ids: ids, ec: ec}

	specs := jobSpecs()
	failed := 0
	for _, jb := range specs {
		if !jb.run(ctx, p, raw, cfg, j, v) {
			failed++
		}
	}
	if failed == len(specs) {
		return nil, fmt.Errorf("judge: all %d jobs failed", failed)
	}
	return j, nil
}
