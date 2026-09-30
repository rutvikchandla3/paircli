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
//
// "anthropic" and "http" both speak the Anthropic Messages shape; they differ
// in where their configuration comes from (the ANTHROPIC_* environment versus
// llm.base_url / llm.headers / llm.auth_token_env). "claude-cli" and "pi"
// shell out to the harnesses' own non-interactive modes. Every provider
// reports the configuration it is missing as an error here, rather than
// failing one job at a time later.
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
	case "pi":
		bin := os.Getenv("PAIRCLI_PI_BIN")
		if bin == "" {
			found, err := exec.LookPath("pi")
			if err != nil {
				return nil, fmt.Errorf("llm: pi binary not found: %w", err)
			}
			bin = found
		}
		return newPi(bin, cfg), nil
	case "http":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("llm: http: llm.base_url is not set")
		}
		if cfg.AuthTokenEnv != "" && os.Getenv(cfg.AuthTokenEnv) == "" {
			return nil, fmt.Errorf("llm: http: llm.auth_token_env names %s, which is not set", cfg.AuthTokenEnv)
		}
		return newHTTP(cfg), nil
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
