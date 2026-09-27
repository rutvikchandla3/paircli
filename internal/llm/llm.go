// Package llm provides a small provider layer over grounded LLM backends:
// the Anthropic Messages API, a local `claude` CLI, and a fake for tests.
// internal/judge (T23) is the only intended caller.
package llm

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// Request is a single prompt request to an LLM provider.
type Request struct {
	Model     string
	System    string
	Prompt    string
	MaxTokens int
}

// Response is a completed LLM reply.
type Response struct {
	Text                      string
	InputTokens, OutputTokens int
}

// Provider completes prompts against an LLM backend.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}

// New builds the Provider configured by cfg. A Provider of "" or "none"
// disables the grounded LLM pass: New returns (nil, nil) and callers must
// treat a nil Provider as "skip the LLM pass".
func New(cfg config.LLM) (Provider, error) {
	switch cfg.Provider {
	case "", "none":
		return nil, nil
	case "anthropic":
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return nil, fmt.Errorf("llm: ANTHROPIC_API_KEY is not set")
		}
		return newAnthropic(cfg), nil
	case "claude-cli":
		bin := os.Getenv("PAIRCLI_CLAUDE_BIN")
		if bin == "" {
			found, err := exec.LookPath("claude")
			if err != nil {
				return nil, fmt.Errorf("llm: claude binary not found: %w", err)
			}
			bin = found
		}
		return newClaudeCLI(bin, cfg), nil
	default:
		return nil, fmt.Errorf("llm: unknown provider %q", cfg.Provider)
	}
}

// resolveModel applies the fallback chain: an explicit request model wins,
// then the configured provider model, then config.DefaultModel.
func resolveModel(reqModel, cfgModel string) string {
	if reqModel != "" {
		return reqModel
	}
	if cfgModel != "" {
		return cfgModel
	}
	return config.DefaultModel
}
