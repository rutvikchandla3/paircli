package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/rutvikchandla3/paircli/internal/claudecode"
	"github.com/rutvikchandla3/paircli/internal/codex"
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/hooklog"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/pi"
	"github.com/rutvikchandla3/paircli/internal/scan"
)

// doctorRun and doctorLookPath are the only ways doctor touches the outside
// world, so tests can run it without a gh binary or a claude CLI.
var (
	doctorRun      = func(name string, args ...string) error { return exec.Command(name, args...).Run() }
	doctorLookPath = exec.LookPath
)

// runDoctor implements `paircli doctor`: one row per harness plus the state of
// gh, the repo configuration and the LLM provider.
func runDoctor(args []string) error {
	return doctorReport(os.Stdout, time.Now())
}

// doctorRow is one harness's line in the doctor table.
type doctorRow struct {
	harness  string
	sessions int
	hooks    string
	last     string
}

// doctorReport writes the doctor table to w. now is injected so tests can pin
// the 30-day window and the "last hook event" formatting.
func doctorReport(w io.Writer, now time.Time) error {
	cwd, _ := os.Getwd()
	root := scan.RepoRoot(cwd, "")
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	since := now.Add(-30 * 24 * time.Hour)
	hookDir := cfg.Roots.HookLog
	if hookDir == "" {
		hookDir = hooklog.DefaultDir()
	}

	rows := []doctorRow{
		doctorClaudeCode(cfg, since, hookDir),
		doctorCodex(cfg, since, hookDir),
		doctorPi(cfg, since),
	}

	fmt.Fprintf(w, "%-12s %-16s %-25s %s\n", "HARNESS", "SESSIONS (30d)", "HOOKS", "LAST HOOK EVENT")
	for _, r := range rows {
		fmt.Fprintf(w, "%-12s %-16d %-25s %s\n", r.harness, r.sessions, r.hooks, r.last)
	}

	fmt.Fprintf(w, "\n%-8s %s\n", "gh", doctorGh())
	fmt.Fprintf(w, "%-8s %s\n", "config", doctorConfigPath(root))
	fmt.Fprintf(w, "%-8s %s\n", "llm", doctorLLM(cfg))
	return nil
}

func doctorClaudeCode(cfg *config.Config, since time.Time, hookDir string) doctorRow {
	root := cfg.Roots.ClaudeCode
	if root == "" {
		root = claudecode.DefaultRoot()
	}
	return doctorRow{
		harness:  "claude-code",
		sessions: doctorSessionFiles(root, since, claudecode.Discover),
		hooks:    doctorInstalled(claudecode.HooksInstalled()),
		last:     doctorLastHook(hookDir, model.HarnessClaudeCode),
	}
}

func doctorCodex(cfg *config.Config, since time.Time, hookDir string) doctorRow {
	root := cfg.Roots.Codex
	if root == "" {
		root = codex.DefaultRoot()
	}
	hooks := "not installed"
	if codex.HooksInstalled() {
		trusted, total := codex.HookTrustState()
		hooks = fmt.Sprintf("installed, %d/%d trusted", trusted, total)
	}
	return doctorRow{
		harness:  "codex",
		sessions: doctorSessionFiles(root, since, codex.Discover),
		hooks:    hooks,
		last:     doctorLastHook(hookDir, model.HarnessCodex),
	}
}

func doctorPi(cfg *config.Config, since time.Time) doctorRow {
	root := cfg.Roots.Pi
	if root == "" {
		root = pi.DefaultRoot()
	}
	return doctorRow{
		harness:  "pi",
		sessions: doctorSessionFiles(root, since, pi.Discover),
		hooks:    doctorInstalled(pi.HooksInstalled()),
		// Pi hook records live inside session files, not in the hook log.
		last: "-",
	}
}

func doctorInstalled(installed bool) string {
	if installed {
		return "installed"
	}
	return "not installed"
}

// doctorSessionFiles counts the transcripts a harness has touched in the
// window. Unreadable roots count as zero: doctor describes, it never fails.
func doctorSessionFiles(root string, since time.Time, discover func(string, time.Time) ([]string, error)) int {
	if root == "" {
		return 0
	}
	files, err := discover(root, since)
	if err != nil {
		return 0
	}
	return len(files)
}

// doctorLastHook returns the newest record time in the hook log for one
// harness, "never" when the log holds none.
func doctorLastHook(dir string, h model.Harness) string {
	recs, err := hooklog.Read(dir, h, time.Time{})
	if err != nil {
		return "never"
	}
	var newest time.Time
	for _, group := range recs {
		for _, rec := range group {
			if rec.TS.After(newest) {
				newest = rec.TS
			}
		}
	}
	if newest.IsZero() {
		return "never"
	}
	return newest.UTC().Format("2006-01-02 15:04") + " UTC"
}

// doctorGh reports gh's authentication state from `gh auth status`'s exit
// code: 0 authenticated, non-zero not authenticated, exec failure not found.
func doctorGh() string {
	err := doctorRun("gh", "auth", "status")
	if err == nil {
		return "authenticated"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "not authenticated"
	}
	return "not found"
}

// doctorConfigPath reports the repository's config file, or "defaults" when
// the repo root has none.
func doctorConfigPath(root string) string {
	if root == "" {
		return "defaults"
	}
	path := filepath.Join(root, config.FileName)
	if _, err := os.Stat(path); err != nil {
		return "defaults"
	}
	return engine.Home(path)
}

// doctorLLM reports the configured provider, its model, and whether the
// credential it needs is present on this machine.
func doctorLLM(cfg *config.Config) string {
	provider := cfg.LLM.Provider
	if provider == "" {
		provider = "none"
	}
	model := cfg.LLM.Model
	if model == "" {
		model = config.DefaultModel
	}
	switch provider {
	case "none":
		return fmt.Sprintf("none (model %s, LLM pass off)", model)
	case "anthropic":
		state := "ANTHROPIC_API_KEY not set"
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			state = "ANTHROPIC_API_KEY set"
		}
		return fmt.Sprintf("anthropic (model %s, %s)", model, state)
	case "claude-cli":
		state := "claude binary not found"
		if _, err := doctorLookPath("claude"); err == nil {
			state = "claude binary found"
		}
		return fmt.Sprintf("claude-cli (model %s, %s)", model, state)
	case "pi":
		state := "pi binary not found"
		if _, err := doctorLookPath("pi"); err == nil {
			state = "pi binary found"
		}
		return fmt.Sprintf("pi (model %s, %s)", model, state)
	case "http":
		// Only the variable's name is ever reported: doctor must not print a
		// credential.
		state := "no base_url"
		if cfg.LLM.BaseURL != "" {
			state = cfg.LLM.BaseURL
		}
		switch {
		case cfg.LLM.AuthTokenEnv == "":
			state += ", no token configured"
		case os.Getenv(cfg.LLM.AuthTokenEnv) == "":
			state += fmt.Sprintf(", %s not set", cfg.LLM.AuthTokenEnv)
		default:
			state += fmt.Sprintf(", %s set", cfg.LLM.AuthTokenEnv)
		}
		return fmt.Sprintf("http (model %s, %s)", model, state)
	default:
		return fmt.Sprintf("%s (model %s, unknown provider)", provider, model)
	}
}
