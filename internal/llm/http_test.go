package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// newTestHTTP builds the configured-gateway provider against a test server.
func newTestHTTP(baseURL string, cfg config.LLM) *httpProvider {
	cfg.BaseURL = baseURL
	p := newHTTP(cfg)
	p.client = http.DefaultClient
	return p
}

// TestHTTP_Success checks the whole shape of a gateway request: the path, the
// bearer credential named by the config's env var, the literal headers, the
// Anthropic body, and the parsed reply.
func TestHTTP_Success(t *testing.T) {
	t.Setenv("PAIRCLI_TEST_GATEWAY_TOKEN", "tok-123")

	var gotPath, gotAuth, gotTeam, gotVersion, gotContentType string
	var gotBody anthropicRequestBody

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("authorization")
		gotTeam = r.Header.Get("x-team")
		gotVersion = r.Header.Get("anthropic-version")
		gotContentType = r.Header.Get("content-type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "hello from "}, {"type": "text", "text": "the gateway"}},
			"usage":   map[string]int{"input_tokens": 5, "output_tokens": 6},
		})
	}))
	defer server.Close()

	// A trailing slash in the config must not double up in the path.
	p := newTestHTTP(server.URL+"/", config.LLM{
		Model:        "gateway-model",
		Headers:      map[string]string{"x-team": "platform"},
		AuthTokenEnv: "PAIRCLI_TEST_GATEWAY_TOKEN",
	})

	resp, err := p.Complete(context.Background(), Request{System: "be terse", Prompt: "say hi"})
	if err != nil {
		t.Fatalf("Complete: unexpected error: %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", gotPath)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("authorization = %q, want the bearer token from the named env var", gotAuth)
	}
	if gotTeam != "platform" {
		t.Errorf("x-team = %q, want the configured header", gotTeam)
	}
	if gotVersion != anthropicVersion || gotContentType != "application/json" {
		t.Errorf("wire headers = %q/%q", gotVersion, gotContentType)
	}
	if gotBody.Model != "gateway-model" || gotBody.System != "be terse" || gotBody.MaxTokens != defaultMaxTokens {
		t.Errorf("body = %+v", gotBody)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Content != "say hi" {
		t.Errorf("body messages = %+v", gotBody.Messages)
	}
	if resp.Text != "hello from the gateway" {
		t.Errorf("resp.Text = %q", resp.Text)
	}
	if resp.InputTokens != 5 || resp.OutputTokens != 6 {
		t.Errorf("resp tokens = %d/%d, want 5/6", resp.InputTokens, resp.OutputTokens)
	}
}

// TestHTTP_HeadersOverrideAuth checks the escape hatch: a gateway that wants
// its credential somewhere other than Authorization says so in headers.
func TestHTTP_HeadersOverrideAuth(t *testing.T) {
	t.Setenv("PAIRCLI_TEST_GATEWAY_TOKEN", "tok-123")

	var gotAuth, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		gotKey = r.Header.Get("x-api-key")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	p := newTestHTTP(server.URL, config.LLM{
		Headers:      map[string]string{"x-api-key": "static-key"},
		AuthTokenEnv: "PAIRCLI_TEST_GATEWAY_TOKEN",
	})
	if _, err := p.Complete(context.Background(), Request{Prompt: "hi"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotKey != "static-key" {
		t.Errorf("x-api-key = %q, want the configured value", gotKey)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("authorization = %q, want the bearer token still sent", gotAuth)
	}
}

// TestHTTP_NoToken covers an internal endpoint that needs no credential.
func TestHTTP_NoToken(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	p := newTestHTTP(server.URL, config.LLM{})
	if _, err := p.Complete(context.Background(), Request{Prompt: "hi"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if sawAuth {
		t.Error("an Authorization header was sent with no auth_token_env configured")
	}
}

// TestHTTP_RetryThenSuccess checks the gateway inherits the shared retry rule.
func TestHTTP_RetryThenSuccess(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(529)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	p := newTestHTTP(server.URL, config.LLM{})
	var waited []time.Duration
	p.wait = noWait(&waited)

	resp, err := p.Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("resp.Text = %q, want ok", resp.Text)
	}
	if len(waited) != 1 || waited[0] != anthropicBackoff[0] {
		t.Fatalf("waited = %v, want [%v]", waited, anthropicBackoff[0])
	}
}

// TestHTTP_NonRetryable checks the error names the provider that failed, so a
// gateway problem is not mistaken for an Anthropic one.
func TestHTTP_NonRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad token"}}`))
	}))
	defer server.Close()

	p := newTestHTTP(server.URL, config.LLM{})
	var waited []time.Duration
	p.wait = noWait(&waited)

	_, err := p.Complete(context.Background(), Request{Prompt: "hi"})
	if err == nil {
		t.Fatal("want error for a 401 response")
	}
	if !strings.Contains(err.Error(), "llm: http:") || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waited) != 0 {
		t.Fatalf("should not retry a 401: waited = %v", waited)
	}
}
