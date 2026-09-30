// The Anthropic Messages wire shape and its retry policy, shared by the two
// providers that speak it: anthropic (endpoint and credential from the
// ANTHROPIC_* environment) and http (endpoint, headers and credential source
// from .paircli.json). Both POST the same body to {base}/v1/messages and read
// the same reply.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	anthropicVersion = "2023-06-01"
	// anthropicCallTimeout bounds a single request.
	anthropicCallTimeout = 180 * time.Second
	// defaultMaxTokens is the completion budget when a request leaves it unset.
	defaultMaxTokens = 4096
)

// anthropicBackoff is the retry backoff schedule: up to 3 retries (4 total
// attempts) with 1s, 2s, 4s between them, unless a retry-after header says
// otherwise.
var anthropicBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// anthropicRetryableStatus are the HTTP statuses that are retried.
var anthropicRetryableStatus = map[int]bool{
	429: true, 500: true, 502: true, 503: true, 504: true, 529: true,
}

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

// messagesBody builds the one-message request body both providers send.
func messagesBody(model, system, prompt string, maxTokens int) anthropicRequestBody {
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	return anthropicRequestBody{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
	}
}

// retryMessages runs do under the shared backoff schedule: the first attempt,
// then up to len(anthropicBackoff) retries of the failures the API marks
// retryable, honoring a server-requested delay when it sends one. provider
// names the caller in error text ("anthropic", "http").
func retryMessages(ctx context.Context, provider string, wait func(context.Context, time.Duration) error,
	do func(context.Context) (Response, error)) (Response, error) {

	var lastErr error
	for attempt := 0; ; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, anthropicCallTimeout)
		resp, err := do(callCtx)
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
		backoff := anthropicBackoff[attempt]
		if re.retryAfter > 0 {
			backoff = re.retryAfter
		}
		if werr := wait(ctx, backoff); werr != nil {
			return Response{}, werr
		}
	}
}

// postMessages performs one HTTP attempt: POST url with header, and decode the
// Anthropic Messages reply. Retryable failures (429, 5xx, 529, network errors,
// an unreadable body) are returned as *retryableError.
func postMessages(ctx context.Context, provider string, client *http.Client, url string,
	header http.Header, payload []byte) (Response, error) {

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("llm: %s: build request: %w", provider, err)
	}
	for name, values := range header {
		for _, v := range values {
			httpReq.Header.Add(name, v)
		}
	}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, &retryableError{err: fmt.Errorf("llm: %s: request failed: %w", provider, err)}
	}
	defer httpResp.Body.Close()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, &retryableError{err: fmt.Errorf("llm: %s: read response: %w", provider, err)}
	}

	if httpResp.StatusCode == http.StatusOK {
		var parsed anthropicResponseBody
		if err := json.Unmarshal(data, &parsed); err != nil {
			return Response{}, fmt.Errorf("llm: %s: decode response: %w", provider, err)
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

	statusErr := fmt.Errorf("llm: %s: status %d: %s", provider, httpResp.StatusCode, anthropicErrorMessage(data))
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

// contextSleep waits for d or until ctx is done, whichever comes first. It is
// the default wait of both HTTP providers; tests replace it to avoid sleeping.
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
