package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// defaultAnthropicBaseURL is the public API, used when ANTHROPIC_BASE_URL is
// unset.
const defaultAnthropicBaseURL = "https://api.anthropic.com"

// anthropicProvider calls the Anthropic Messages API directly over HTTP
// (POST {base}/v1/messages), with the endpoint and the credential taken from
// the environment. The wire shape and the retry policy it shares with the
// configurable "http" provider live in messages.go. No SDK dependency:
// internal/llm may not add new Go module dependencies.
type anthropicProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client

	// wait sleeps for d, honoring ctx cancellation; overridable in tests so
	// retry tests don't actually block for seconds.
	wait func(ctx context.Context, d time.Duration) error
}

func newAnthropic(cfg config.LLM) *anthropicProvider {
	base := os.Getenv("ANTHROPIC_BASE_URL")
	if base == "" {
		base = defaultAnthropicBaseURL
	}
	return &anthropicProvider{
		apiKey:  os.Getenv("ANTHROPIC_API_KEY"),
		baseURL: base,
		model:   cfg.Model,
		client:  &http.Client{},
		wait:    contextSleep,
	}
}

// Name implements Provider.
func (p *anthropicProvider) Name() string { return "anthropic" }

// Complete implements Provider. It never logs the API key or the request
// body.
func (p *anthropicProvider) Complete(ctx context.Context, req Request) (Response, error) {
	payload, err := json.Marshal(messagesBody(resolveModel(req.Model, p.model), req.System, req.Prompt, req.MaxTokens))
	if err != nil {
		return Response{}, fmt.Errorf("llm: anthropic: encode request: %w", err)
	}
	return retryMessages(ctx, "anthropic", p.wait, func(callCtx context.Context) (Response, error) {
		return postMessages(callCtx, "anthropic", p.client, p.baseURL+"/v1/messages", anthropicHeaders(p.apiKey), payload)
	})
}

// anthropicHeaders is the header set for a request authenticated the Anthropic
// way: the key in x-api-key, plus the version the messages endpoint requires.
func anthropicHeaders(apiKey string) http.Header {
	h := http.Header{}
	h.Set("x-api-key", apiKey)
	h.Set("anthropic-version", anthropicVersion)
	h.Set("content-type", "application/json")
	return h
}
