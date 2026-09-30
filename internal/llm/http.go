package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// httpProvider posts the Anthropic Messages shape to an endpoint named in
// .paircli.json: POST {BaseURL}/v1/messages, the credential read from the
// environment variable AuthTokenEnv names, plus whatever literal headers
// Headers adds. It is the provider for an internal gateway — no vendor SDK, no
// fixed endpoint, and the token stays out of the config file.
type httpProvider struct {
	baseURL  string
	model    string
	headers  map[string]string
	tokenEnv string
	client   *http.Client

	// wait sleeps for d, honoring ctx cancellation; overridable in tests so
	// retry tests don't actually block for seconds.
	wait func(ctx context.Context, d time.Duration) error
}

func newHTTP(cfg config.LLM) *httpProvider {
	return &httpProvider{
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		model:    cfg.Model,
		headers:  cfg.Headers,
		tokenEnv: cfg.AuthTokenEnv,
		client:   &http.Client{},
		wait:     contextSleep,
	}
}

// Name implements Provider.
func (p *httpProvider) Name() string { return "http" }

// Complete implements Provider. It never logs the token or the request body.
func (p *httpProvider) Complete(ctx context.Context, req Request) (Response, error) {
	payload, err := json.Marshal(messagesBody(resolveModel(req.Model, p.model), req.System, req.Prompt, req.MaxTokens))
	if err != nil {
		return Response{}, fmt.Errorf("llm: http: encode request: %w", err)
	}
	return retryMessages(ctx, "http", p.wait, func(callCtx context.Context) (Response, error) {
		return postMessages(callCtx, "http", p.client, p.baseURL+"/v1/messages", p.header(), payload)
	})
}

// header builds one request's headers: the wire headers the messages endpoint
// expects, the bearer credential when one is configured, and the configured
// headers last, so a gateway that wants its token somewhere else can say so.
func (p *httpProvider) header() http.Header {
	h := http.Header{}
	h.Set("content-type", "application/json")
	h.Set("anthropic-version", anthropicVersion)
	if token := p.token(); token != "" {
		h.Set("authorization", "Bearer "+token)
	}
	for name, value := range p.headers {
		h.Set(name, value)
	}
	return h
}

// token reads the credential at request time rather than at construction, so a
// rotated environment variable is picked up without rebuilding the provider.
func (p *httpProvider) token() string {
	if p.tokenEnv == "" {
		return ""
	}
	return os.Getenv(p.tokenEnv)
}
