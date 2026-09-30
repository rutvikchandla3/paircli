package main

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// TestDoctor_Output runs doctor against a temp HOME with every external
// command stubbed: it must print a row per harness plus gh/config/llm lines.
func TestDoctor_Output(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubDoctor(t, nil, errors.New("not found"))

	var buf bytes.Buffer
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if err := doctorReport(&buf, now); err != nil {
		t.Fatalf("doctorReport: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"HARNESS", "SESSIONS (30d)", "HOOKS", "LAST HOOK EVENT"} {
		if !strings.Contains(out, want) {
			t.Errorf("header %q missing from:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 7 {
		t.Fatalf("doctor printed %d lines, want at least 7:\n%s", len(lines), out)
	}
	for i, harness := range []string{"claude-code", "codex", "pi"} {
		row := lines[1+i]
		if !strings.HasPrefix(row, harness) {
			t.Errorf("row %d = %q, want it to start with %q", i+1, row, harness)
		}
		if !strings.Contains(row, "never") && harness != "pi" {
			t.Errorf("%s row = %q, want a LAST HOOK EVENT value", harness, row)
		}
	}
	if !strings.HasPrefix(doctorLine(t, lines, "gh"), "gh") {
		t.Errorf("gh line missing from:\n%s", out)
	}
	if !strings.Contains(doctorLine(t, lines, "gh"), "authenticated") {
		t.Errorf("gh line = %q", doctorLine(t, lines, "gh"))
	}
	if !strings.Contains(doctorLine(t, lines, "config"), "config") {
		t.Errorf("config line missing from:\n%s", out)
	}
	if !strings.Contains(doctorLine(t, lines, "llm"), "llm") {
		t.Errorf("llm line missing from:\n%s", out)
	}
}

// doctorLine returns the first printed line starting with prefix.
func doctorLine(t *testing.T, lines []string, prefix string) string {
	t.Helper()
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no line starts with %q in %q", prefix, strings.Join(lines, "\n"))
	return ""
}

// stubDoctor replaces doctor's two external seams and restores them after the
// test. ghErr stands in for `gh auth status`; lookErr for LookPath.
func stubDoctor(t *testing.T, ghErr, lookErr error) {
	t.Helper()
	prevRun, prevLook := doctorRun, doctorLookPath
	doctorRun = func(string, ...string) error { return ghErr }
	doctorLookPath = func(string) (string, error) { return "", lookErr }
	t.Cleanup(func() { doctorRun, doctorLookPath = prevRun, prevLook })
}

// fakeExitError is an *exec.ExitError, the shape doctor reads as "the command
// ran and failed" (gh present but not authenticated).
func fakeExitError() error { return &exec.ExitError{} }

// doctorTestConfig is a config the doctor line tests mutate.
func doctorTestConfig() *config.Config { return config.Default() }

// TestDoctor_GhStates covers the three gh outcomes doctor reports.
func TestDoctor_GhStates(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"authenticated", nil, "authenticated"},
		{"not authenticated", fakeExitError(), "not authenticated"},
		{"not found", errors.New("exec: gh: executable file not found"), "not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubDoctor(t, c.err, nil)
			if got := doctorGh(); got != c.want {
				t.Errorf("doctorGh() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestDoctor_LLMLine checks the provider/model/credential summary.
func TestDoctor_LLMLine(t *testing.T) {
	stubDoctor(t, nil, errors.New("not found"))
	t.Setenv("ANTHROPIC_API_KEY", "")

	cfg := *doctorTestConfig()
	cfg.LLM.Provider = "none"
	if got := doctorLLM(&cfg); !strings.Contains(got, "none") {
		t.Errorf("none line = %q", got)
	}

	cfg.LLM.Provider = "anthropic"
	cfg.LLM.Model = "claude-opus-5-5"
	got := doctorLLM(&cfg)
	if !strings.Contains(got, "anthropic") || !strings.Contains(got, "claude-opus-5-5") {
		t.Errorf("anthropic line = %q", got)
	}
	if !strings.Contains(got, "not set") {
		t.Errorf("anthropic line should report the missing key: %q", got)
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if got := doctorLLM(&cfg); !strings.Contains(got, "set") {
		t.Errorf("anthropic line with key = %q", got)
	}

	cfg.LLM.Provider = "claude-cli"
	if got := doctorLLM(&cfg); !strings.Contains(got, "claude binary not found") {
		t.Errorf("claude-cli line = %q", got)
	}

	cfg.LLM.Provider = "http"
	cfg.LLM.BaseURL = "https://gateway.internal"
	cfg.LLM.AuthTokenEnv = "PAIRCLI_TEST_HTTP_TOKEN"
	t.Setenv("PAIRCLI_TEST_HTTP_TOKEN", "")
	if got := doctorLLM(&cfg); !strings.Contains(got, "https://gateway.internal") ||
		!strings.Contains(got, "PAIRCLI_TEST_HTTP_TOKEN not set") {
		t.Errorf("http line = %q", got)
	}

	t.Setenv("PAIRCLI_TEST_HTTP_TOKEN", "tok-secret")
	got = doctorLLM(&cfg)
	if !strings.Contains(got, "PAIRCLI_TEST_HTTP_TOKEN set") {
		t.Errorf("http line with token = %q", got)
	}
	// The credential itself must never be printed.
	if strings.Contains(got, "tok-secret") {
		t.Errorf("http line leaks the token: %q", got)
	}
}
