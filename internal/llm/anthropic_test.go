package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// noWait replaces anthropicProvider.wait in tests so retry tests don't
// actually block for seconds; it records the durations it was asked to wait.
func noWait(waited *[]time.Duration) func(ctx context.Context, d time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		*waited = append(*waited, d)
		return nil
	}
}

func newTestAnthropic(baseURL string) *anthropicProvider {
	return &anthropicProvider{
		apiKey:  "sk-ant-test-key",
		baseURL: baseURL,
		model:   "claude-cfg-model",
		client:  http.DefaultClient,
		wait:    contextSleep,
	}
}

func TestAnthropic_Success(t *testing.T) {
	var gotAuth, gotVersion, gotContentType string
	var gotBody anthropicRequestBody

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotContentType = r.Header.Get("content-type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": "hello "},
				{"type": "text", "text": "world"},
			},
			"usage": map[string]int{"input_tokens": 12, "output_tokens": 34},
		})
	}))
	defer server.Close()

	p := newTestAnthropic(server.URL)
	resp, err := p.Complete(context.Background(), Request{
		Model:  "claude-req-model",
		System: "be terse",
		Prompt: "say hi",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuth != "sk-ant-test-key" {
		t.Errorf("x-api-key = %q", gotAuth)
	}
	if gotVersion != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, anthropicVersion)
	}
	if !strings.Contains(gotContentType, "application/json") {
		t.Errorf("content-type = %q", gotContentType)
	}
	if gotBody.Model != "claude-req-model" {
		t.Errorf("body.Model = %q, want claude-req-model", gotBody.Model)
	}
	if gotBody.System != "be terse" {
		t.Errorf("body.System = %q", gotBody.System)
	}
	if gotBody.MaxTokens != defaultMaxTokens {
		t.Errorf("body.MaxTokens = %d, want default %d", gotBody.MaxTokens, defaultMaxTokens)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Role != "user" || gotBody.Messages[0].Content != "say hi" {
		t.Errorf("body.Messages = %+v", gotBody.Messages)
	}

	if resp.Text != "hello world" {
		t.Errorf("resp.Text = %q, want %q", resp.Text, "hello world")
	}
	if resp.InputTokens != 12 || resp.OutputTokens != 34 {
		t.Errorf("resp tokens = %d/%d, want 12/34", resp.InputTokens, resp.OutputTokens)
	}

	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestAnthropic_RetryThenSuccess(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(529)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"message": "overloaded"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "ok"}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	p := newTestAnthropic(server.URL)
	var waited []time.Duration
	p.wait = noWait(&waited)

	resp, err := p.Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("resp.Text = %q, want ok", resp.Text)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("server got %d calls, want 2", calls)
	}
	if len(waited) != 1 || waited[0] != anthropicBackoff[0] {
		t.Fatalf("waited = %v, want [%v]", waited, anthropicBackoff[0])
	}
}

func TestAnthropic_RetryAfterHeader(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("retry-after", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"message": "rate limited"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "ok"}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	p := newTestAnthropic(server.URL)
	var waited []time.Duration
	p.wait = noWait(&waited)

	if _, err := p.Complete(context.Background(), Request{Prompt: "hi"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waited) != 1 || waited[0] != 7*time.Second {
		t.Fatalf("waited = %v, want [7s] (retry-after should override backoff)", waited)
	}
}

func TestAnthropic_NonRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "invalid request: bad model"},
		})
	}))
	defer server.Close()

	p := newTestAnthropic(server.URL)
	var waited []time.Duration
	p.wait = noWait(&waited)

	_, err := p.Complete(context.Background(), Request{Prompt: "hi"})
	if err == nil {
		t.Fatal("want error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "invalid request: bad model") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waited) != 0 {
		t.Fatalf("should not retry a 400: waited = %v", waited)
	}
}

func TestAnthropic_ContextCancel(t *testing.T) {
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
	}))
	defer server.Close()
	defer close(unblock)

	p := newTestAnthropic(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := p.Complete(ctx, Request{Prompt: "hi"})
	if err == nil {
		t.Fatal("want error on context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}
