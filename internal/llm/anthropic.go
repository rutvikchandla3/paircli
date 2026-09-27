package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
)

const (
	anthropicVersion        = "2023-06-01"
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultMaxTokens        = 4096
	anthropicCallTimeout    = 180 * time.Second
)

// anthropicBackoff is the retry backoff schedule: up to 3 retries (4 total
// attempts) with 1s, 2s, 4s between them, unless a retry-after header says
// otherwise.
var anthropicBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// anthropicRetryableStatus are the HTTP statuses that are retried.
var anthropicRetryableStatus = map[int]bool{
	429: true, 500: true, 502: true, 503: true, 504: true, 529: true,
}

// anthropicProvider calls the Anthropic Messages API directly over HTTP
// (POST {base}/v1/messages). No SDK dependency: internal/llm may not add
// new Go module dependencies.
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

// contextSleep waits for d or until ctx is done, whichever comes first.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Name implements Provider.
func (p *anthropicProvider) Name() string { return "anthropic" }

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequestBody struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponseBody struct {
	Content []anthropicContentBlock `json:"content"`
	Usage   anthropicUsage          `json:"usage"`
}

type anthropicErrorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// retryableError marks an error from a single attempt as retryable, with an
// optional server-requested delay (from a retry-after header).
type retryableError struct {
	err        error
	retryAfter time.Duration
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// Complete implements Provider. It never logs the API key or the request
// body.
func (p *anthropicProvider) Complete(ctx context.Context, req Request) (Response, error) {
	model := resolveModel(req.Model, p.model)
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	body := anthropicRequestBody{
		Model:     model,
		MaxTokens: maxTokens,
		System:    req.System,
		Messages:  []anthropicMessage{{Role: "user", Content: req.Prompt}},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("llm: anthropic: encode request: %w", err)
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, anthropicCallTimeout)
		resp, err := p.doRequest(callCtx, payload)
		cancel()
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		re, ok := err.(*retryableError)
		if !ok {
			return Response{}, err
		}
		lastErr = re.err
		if attempt >= len(anthropicBackoff) {
			return Response{}, lastErr
		}
		wait := anthropicBackoff[attempt]
		if re.retryAfter > 0 {
			wait = re.retryAfter
		}
		if werr := p.wait(ctx, wait); werr != nil {
			return Response{}, werr
		}
	}
}

// doRequest performs a single HTTP attempt. Retryable failures (429, 5xx,
// 529, network errors) are returned as *retryableError.
func (p *anthropicProvider) doRequest(ctx context.Context, payload []byte) (Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("llm: anthropic: build request: %w", err)
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("content-type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return Response{}, &retryableError{err: fmt.Errorf("llm: anthropic: request failed: %w", err)}
	}
	defer httpResp.Body.Close()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, &retryableError{err: fmt.Errorf("llm: anthropic: read response: %w", err)}
	}

	if httpResp.StatusCode == http.StatusOK {
		var parsed anthropicResponseBody
		if err := json.Unmarshal(data, &parsed); err != nil {
			return Response{}, fmt.Errorf("llm: anthropic: decode response: %w", err)
		}
		var text bytes.Buffer
		for _, block := range parsed.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		return Response{
			Text:         text.String(),
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
		}, nil
	}

	statusErr := fmt.Errorf("llm: anthropic: status %d: %s", httpResp.StatusCode, anthropicErrorMessage(data))
	if anthropicRetryableStatus[httpResp.StatusCode] {
		return Response{}, &retryableError{err: statusErr, retryAfter: parseRetryAfter(httpResp.Header.Get("retry-after"))}
	}
	return Response{}, statusErr
}

func anthropicErrorMessage(data []byte) string {
	var eb anthropicErrorBody
	if err := json.Unmarshal(data, &eb); err == nil && eb.Error.Message != "" {
		return eb.Error.Message
	}
	return string(data)
}

// parseRetryAfter parses a retry-after header given in seconds. It returns 0
// (no override) for an empty, non-numeric or non-positive value.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
