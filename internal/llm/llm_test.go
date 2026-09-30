package llm

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/config"
)

func TestNew_None(t *testing.T) {
	for _, provider := range []string{"", "none"} {
		p, err := New(config.LLM{Provider: provider})
		if err != nil {
			t.Fatalf("provider %q: unexpected error: %v", provider, err)
		}
		if p != nil {
			t.Fatalf("provider %q: want nil Provider, got %v", provider, p)
		}
	}
}

func TestNew_AnthropicNeedsKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	if _, err := New(config.LLM{Provider: "anthropic"}); err == nil {
		t.Fatal("want error when ANTHROPIC_API_KEY is unset")
	} else if err.Error() != "llm: ANTHROPIC_API_KEY is not set" {
		t.Fatalf("unexpected error message: %v", err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	p, err := New(config.LLM{Provider: "anthropic"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || p.Name() != "anthropic" {
		t.Fatalf("want anthropic provider, got %v", p)
	}
}

func TestNew_Unknown(t *testing.T) {
	_, err := New(config.LLM{Provider: "bogus"})
	if err == nil {
		t.Fatal("want error for unknown provider")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error should name the provider: %v", err)
	}
}

// TestNew_HTTP covers the configured-gateway provider's two required pieces:
// an endpoint, and the variable a credential would come from.
func TestNew_HTTP(t *testing.T) {
	if _, err := New(config.LLM{Provider: "http"}); err == nil {
		t.Fatal("want error when llm.base_url is unset")
	} else if !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("error should name the missing key: %v", err)
	}

	cfg := config.LLM{Provider: "http", BaseURL: "https://gateway.internal", AuthTokenEnv: "PAIRCLI_TEST_NEW_HTTP_TOKEN"}
	t.Setenv("PAIRCLI_TEST_NEW_HTTP_TOKEN", "")
	if _, err := New(cfg); err == nil {
		t.Fatal("want error when the named token variable is not set")
	} else if !strings.Contains(err.Error(), "PAIRCLI_TEST_NEW_HTTP_TOKEN") {
		t.Fatalf("error should name the variable: %v", err)
	}

	// A gateway that needs no credential leaves auth_token_env empty.
	p, err := New(config.LLM{Provider: "http", BaseURL: "https://gateway.internal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || p.Name() != "http" {
		t.Fatalf("want http provider, got %v", p)
	}

	t.Setenv("PAIRCLI_TEST_NEW_HTTP_TOKEN", "tok")
	p, err = New(cfg)
	if err != nil {
		t.Fatalf("unexpected error with the token set: %v", err)
	}
	if p == nil || p.Name() != "http" {
		t.Fatalf("want http provider, got %v", p)
	}
}

// TestNew_Pi checks the pi provider is built from the binary the environment
// names, the same way claude-cli is.
func TestNew_Pi(t *testing.T) {
	t.Setenv("PAIRCLI_PI_BIN", filepath.Join("testdata", "pi_success.sh"))
	p, err := New(config.LLM{Provider: "pi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || p.Name() != "pi" {
		t.Fatalf("want pi provider, got %v", p)
	}
}

func TestNew_ModelFallback(t *testing.T) {
	tests := []struct {
		name     string
		reqModel string
		cfgModel string
		want     string
	}{
		{"explicit wins", "claude-request-model", "claude-cfg-model", "claude-request-model"},
		{"falls back to cfg", "", "claude-cfg-model", "claude-cfg-model"},
		{"falls back to default", "", "", config.DefaultModel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveModel(tt.reqModel, tt.cfgModel); got != tt.want {
				t.Errorf("resolveModel(%q, %q) = %q, want %q", tt.reqModel, tt.cfgModel, got, tt.want)
			}
		})
	}
}
