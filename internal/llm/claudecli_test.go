package llm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/config"
)

func TestClaudeCLI_Success(t *testing.T) {
	t.Setenv("PAIRCLI_CLAUDE_BIN", filepath.Join("testdata", "claude_success.sh"))

	p, err := New(config.LLM{Provider: "claude-cli", Model: "claude-cfg-model"})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	if p == nil || p.Name() != "claude-cli" {
		t.Fatalf("want claude-cli provider, got %v", p)
	}

	resp, err := p.Complete(context.Background(), Request{
		Model:  "claude-test-model",
		System: "be terse",
		Prompt: "say hi",
	})
	if err != nil {
		t.Fatalf("Complete: unexpected error: %v", err)
	}
	if resp.Text != "hello from claude cli" {
		t.Errorf("resp.Text = %q", resp.Text)
	}
	if resp.InputTokens != 11 || resp.OutputTokens != 22 {
		t.Errorf("resp tokens = %d/%d, want 11/22", resp.InputTokens, resp.OutputTokens)
	}
}

func TestClaudeCLI_Error(t *testing.T) {
	t.Run("nonzero exit", func(t *testing.T) {
		t.Setenv("PAIRCLI_CLAUDE_BIN", filepath.Join("testdata", "claude_nonzero_exit.sh"))
		p, err := New(config.LLM{Provider: "claude-cli"})
		if err != nil {
			t.Fatalf("New: unexpected error: %v", err)
		}

		_, err = p.Complete(context.Background(), Request{Model: "claude-test-model", Prompt: "say hi"})
		if err == nil {
			t.Fatal("want error for non-zero exit")
		}
		if !strings.Contains(err.Error(), "boom: something went wrong talking to the model") {
			t.Fatalf("error should include stderr: %v", err)
		}
	})

	t.Run("is_error true", func(t *testing.T) {
		t.Setenv("PAIRCLI_CLAUDE_BIN", filepath.Join("testdata", "claude_is_error.sh"))
		p, err := New(config.LLM{Provider: "claude-cli"})
		if err != nil {
			t.Fatalf("New: unexpected error: %v", err)
		}

		_, err = p.Complete(context.Background(), Request{Model: "claude-test-model", Prompt: "say hi"})
		if err == nil {
			t.Fatal("want error when is_error is true")
		}
		if !strings.Contains(err.Error(), "the model declined to answer") {
			t.Fatalf("error should include result text: %v", err)
		}
	})
}
