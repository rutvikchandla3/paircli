package llm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// newTestPi points a pi provider at a fixture script.
func newTestPi(script string, cfgModel string) *piProvider {
	return newPi(filepath.Join("testdata", script), config.LLM{Model: cfgModel})
}

func TestPi_Success(t *testing.T) {
	p := newTestPi("pi_success.sh", "pi-test-model")

	resp, err := p.Complete(context.Background(), Request{
		Model:  "pi-test-model",
		System: "be terse",
		Prompt: "say hi",
	})
	if err != nil {
		t.Fatalf("Complete: unexpected error: %v", err)
	}
	// The fixture's last assistant message wins, and its text parts are joined.
	if resp.Text != "hello from pi" {
		t.Errorf("resp.Text = %q, want %q", resp.Text, "hello from pi")
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 9 {
		t.Errorf("resp tokens = %d/%d, want 7/9", resp.InputTokens, resp.OutputTokens)
	}
}

func TestPi_Error(t *testing.T) {
	t.Run("nonzero exit", func(t *testing.T) {
		_, err := newTestPi("pi_nonzero_exit.sh", "pi-test-model").
			Complete(context.Background(), Request{Prompt: "say hi"})
		if err == nil {
			t.Fatal("want error for non-zero exit")
		}
		if !strings.Contains(err.Error(), "The usage limit has been reached") {
			t.Fatalf("error should carry pi's stderr: %v", err)
		}
	})

	t.Run("error turn at exit 0", func(t *testing.T) {
		_, err := newTestPi("pi_stream_error.sh", "pi-test-model").
			Complete(context.Background(), Request{Prompt: "say hi"})
		if err == nil {
			t.Fatal("want error for a turn pi ended with stopReason error")
		}
		if !strings.Contains(err.Error(), "provider refused the request") {
			t.Fatalf("error should carry the stream's errorMessage: %v", err)
		}
	})

	t.Run("no assistant message", func(t *testing.T) {
		_, err := newTestPi("pi_no_reply.sh", "pi-test-model").
			Complete(context.Background(), Request{Prompt: "say hi"})
		if err == nil {
			t.Fatal("want error when the stream carries no assistant message")
		}
		if !strings.Contains(err.Error(), "no assistant reply") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// TestPiModel pins the model rule: the shared default names a Claude model that
// pi need not serve, so it is treated as "unset" and pi picks for itself.
func TestPiModel(t *testing.T) {
	for _, c := range []struct {
		name             string
		reqModel, cfgMod string
		want             string
	}{
		{"request wins", "anthropic/claude-x", "pi-other", "anthropic/claude-x"},
		{"configured model", "", "pi-other", "pi-other"},
		{"shared default is not passed on", "", config.DefaultModel, ""},
		{"nothing configured", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := piModel(c.reqModel, c.cfgMod); got != c.want {
				t.Errorf("piModel(%q, %q) = %q, want %q", c.reqModel, c.cfgMod, got, c.want)
			}
		})
	}
}

// TestPiContentText covers both shapes a pi message body takes.
func TestPiContentText(t *testing.T) {
	if got := piContentText([]byte(`"plain string"`)); got != "plain string" {
		t.Errorf("string body = %q", got)
	}
	if got := piContentText([]byte(`[{"type":"text","text":"a"},{"type":"thinking","text":"skip"},{"type":"text","text":"b"}]`)); got != "ab" {
		t.Errorf("parts body = %q, want %q", got, "ab")
	}
	if got := piContentText(nil); got != "" {
		t.Errorf("empty body = %q, want empty", got)
	}
}
