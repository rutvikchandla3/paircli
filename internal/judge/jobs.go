package judge

import (
	"context"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// maxTokens is the output budget of every judge job.
const maxTokens = 4096

// jobSpec is one LLM job: its instructions, the reply shape it must produce,
// and how a validated reply is merged into model.Judgments.
type jobSpec struct {
	name     string
	prompt   string
	newReply func() any
	apply    func(j *model.Judgments, reply any, v *validator)
}

// jobSpecs returns the four jobs in the order they run.
func jobSpecs() []jobSpec {
	return []jobSpec{
		{
			name:     "claims",
			prompt:   claimsPrompt,
			newReply: func() any { return &claimsReply{} },
			apply:    applyClaims,
		},
		{
			name:     "story",
			prompt:   storyPrompt,
			newReply: func() any { return &storyReply{} },
			apply:    applyStory,
		},
		{
			name:     "scope",
			prompt:   scopePrompt,
			newReply: func() any { return &scopeReply{} },
			apply:    applyScope,
		},
		{
			name:     "caveats",
			prompt:   caveatsPrompt,
			newReply: func() any { return &caveatsReply{} },
			apply:    applyCaveats,
		},
	}
}

// request builds the llm.Request for one job over the marshalled bundle.
func (jb jobSpec) request(cfg *config.Config, raw []byte) llm.Request {
	return llm.Request{
		Model:     cfg.LLM.Model,
		System:    systemPrompt,
		Prompt:    jb.prompt + "\n\nFACT BUNDLE:\n" + string(raw),
		MaxTokens: maxTokens,
	}
}

// run executes one job through a provider. A reply that is not valid JSON is
// retried exactly once with retryPrompt appended; a provider error, or two
// invalid replies, is recorded in j.Errors. It reports whether the job produced
// a result.
//
// It is the single-job form of Set.Apply, kept so one job can be driven
// directly; the fetch-and-merge rule itself lives in fetch/applyResult.
func (jb jobSpec) run(ctx context.Context, p llm.Provider, raw []byte, cfg *config.Config,
	j *model.Judgments, v *validator) bool {

	jp := newJobPrompt(jb, cfg, raw)
	return applyResult(j, v, jp, fetch(ctx, p, jp))
}
