package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/scan"
)

// TestE2E_HTTPGateway judges the scenario's snapshot through the "http"
// provider against a gateway that speaks the Anthropic Messages shape — no
// fake provider, a real HTTP round trip. The report must be the same one
// testdata/golden_llm pins, which is what makes the transport a detail rather
// than a second implementation of the pass.
func TestE2E_HTTPGateway(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	snap := e2eScanDeterministic(t, w)

	// The gateway answers the four jobs in the order internal/judge asks them,
	// with the same replies the fake provider hands over.
	replies := e2eLLMReplies(t)
	var mu sync.Mutex
	var gotAuth []string
	var paths []string
	calls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.URL.Path)
		gotAuth = append(gotAuth, r.Header.Get("authorization"))
		reply := `{}`
		if calls < len(replies) {
			reply = replies[calls]
		}
		calls++
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": reply}},
			"usage":   map[string]int{"input_tokens": 3, "output_tokens": 4},
		})
	}))
	defer gateway.Close()

	t.Setenv("PAIRCLI_TEST_HTTP_TOKEN", "gateway-token")
	provider, err := llm.New(config.LLM{
		Provider:     "http",
		BaseURL:      gateway.URL,
		AuthTokenEnv: "PAIRCLI_TEST_HTTP_TOKEN",
		Headers:      map[string]string{"x-team": "platform"},
	})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}

	res, err := scan.Replay(context.Background(), scan.ReplayOptions{
		Snapshot: snap,
		OutDir:   w.outDir,
		LLM:      provider,
		Now:      e2eAt(e2eNowHHMM),
		Version:  e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: scan.Replay through the gateway: %v", err)
	}

	mu.Lock()
	gotPaths, gotTokens, gotCalls := append([]string(nil), paths...), append([]string(nil), gotAuth...), calls
	mu.Unlock()
	if gotCalls != len(e2eLLMReplyFiles) {
		t.Errorf("gateway calls = %d, want %d (one per judge job)", gotCalls, len(e2eLLMReplyFiles))
	}
	for i, p := range gotPaths {
		if p != "/v1/messages" {
			t.Errorf("call %d path = %q, want /v1/messages", i, p)
		}
	}
	for i, tok := range gotTokens {
		if tok != "Bearer gateway-token" {
			t.Errorf("call %d authorization = %q, want the bearer token", i, tok)
		}
	}

	for _, name := range e2eLLMOutputs {
		raw, err := os.ReadFile(filepath.Join(w.outDir, name))
		if err != nil {
			t.Fatalf("e2e: reading %s: %v", name, err)
		}
		e2eLLMCompareGolden(t, name, e2eNormalize(w, raw))
	}
	e2eAssertLLMRun(t, res)
}

// TestE2E_HTTPGatewayConfigFile checks the last link in the chain: the keys a
// user actually writes in .paircli.json reach the gateway. It reads a real
// config file, so a rename of a key breaks this test rather than a user's
// setup.
func TestE2E_HTTPGatewayConfigFile(t *testing.T) {
	var mu sync.Mutex
	var auth, team string
	gateway := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth, team = r.Header.Get("authorization"), r.Header.Get("x-team")
		mu.Unlock()
		_, _ = rw.Write([]byte(`{"content":[{"type":"text","text":"{}"}]}`))
	}))
	defer gateway.Close()

	dir := t.TempDir()
	body := `{"llm": {"provider": "http", "base_url": "` + gateway.URL + `",
		"auth_token_env": "ADE_GATEWAY_TOKEN", "headers": {"x-team": "platform"}}}`
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", config.FileName, err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	t.Setenv("ADE_GATEWAY_TOKEN", "tok-from-env")
	provider, err := llm.New(cfg.LLM)
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	if _, err := provider.Complete(context.Background(), llm.Request{Prompt: "hi"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer tok-from-env" {
		t.Errorf("authorization = %q, want the token from the variable the config named", auth)
	}
	if team != "platform" {
		t.Errorf("x-team = %q, want the configured header", team)
	}
}
