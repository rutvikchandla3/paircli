// Package config loads paircli's per-repo configuration from <repo>/.paircli.json
// and merges it over built-in defaults.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileName is the per-repo config file, at the repository root.
const FileName = ".paircli.json"

// DefaultModel is the default model for the grounded LLM pass.
const DefaultModel = "claude-opus-5-5"

// Config is paircli's configuration.
type Config struct {
	SensitivePaths   []string       `json:"sensitive_paths"`    // globs (see classify.Glob); appended to defaults
	GeneratedPaths   []string       `json:"generated_paths"`    // globs; appended to defaults
	TestPathPatterns []string       `json:"test_path_patterns"` // extra RE2 regexes on repo-relative paths; appended
	ExtraChecks      []CheckPattern `json:"extra_checks"`       // extra check-command patterns; appended
	WindowBefore     Duration       `json:"window_before"`      // session search window before the first PR commit
	WindowAfter      Duration       `json:"window_after"`       // and after the last PR commit
	Roots            Roots          `json:"roots"`
	LLM              LLM            `json:"llm"`
	Comment          Comment        `json:"comment"`
}

// CheckPattern classifies extra commands as checks. Class is one of
// "test", "lint", "typecheck", "build".
type CheckPattern struct {
	Class string `json:"class"`
	Regex string `json:"regex"`
}

// Roots overrides where session data is read from. Empty means the harness default.
type Roots struct {
	ClaudeCode string `json:"claude_code"` // default ~/.claude/projects
	Codex      string `json:"codex"`       // default ~/.codex (sessions/ and archived_sessions/ under it)
	Pi         string `json:"pi"`          // default ~/.pi/agent/sessions
	HookLog    string `json:"hook_log"`    // default ~/.paircli/events
}

// LLM configures the optional grounded LLM pass.
type LLM struct {
	Provider      string `json:"provider"`        // "none" (default) | "anthropic" | "claude-cli"
	Model         string `json:"model"`           // default DefaultModel
	MaxInputChars int    `json:"max_input_chars"` // fact-bundle budget; default 200000
}

// Comment configures comment.md / --post.
type Comment struct {
	IncludePrompts bool `json:"include_prompts"` // default false: never put prompt text in a PR comment
	MaxLines       int  `json:"max_lines"`       // default 5
}

// Duration is a time.Duration that unmarshals from strings like "48h".
type Duration struct{ time.Duration }

// UnmarshalJSON parses a Go duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"48h\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// MarshalJSON writes the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.Duration.String()) }

// Default returns the built-in configuration.
func Default() *Config {
	return &Config{
		SensitivePaths: []string{
			".github/workflows/**", ".gitlab-ci.yml", ".circleci/**", "Jenkinsfile",
			"**/Dockerfile", "**/docker-compose*.yml", "deploy/**", "infra/**", "terraform/**", "**/*.tf",
			"**/auth/**", "**/security/**", "**/migrations/**",
			"**/package.json", "go.mod", "**/requirements*.txt", "**/pyproject.toml", "**/Cargo.toml", "**/Gemfile",
			"**/.env*", "**/.npmrc",
		},
		GeneratedPaths: []string{
			"**/*.pb.go", "**/*_generated.*", "**/*.gen.*", "**/generated/**", "**/__generated__/**",
			"**/*.min.js", "**/vendor/**", "**/dist/**",
			"**/package-lock.json", "**/pnpm-lock.yaml", "**/yarn.lock", "**/go.sum", "**/Cargo.lock", "**/poetry.lock", "**/*.snap",
		},
		WindowBefore: Duration{48 * time.Hour},
		WindowAfter:  Duration{2 * time.Hour},
		LLM:          LLM{Provider: "none", Model: DefaultModel, MaxInputChars: 200000},
		Comment:      Comment{IncludePrompts: false, MaxLines: 5},
	}
}

// Load reads <repoRoot>/.paircli.json if present and merges it over Default():
// list fields are appended to the defaults, non-zero scalar fields replace them.
// A missing file is not an error.
func Load(repoRoot string) (*Config, error) {
	cfg := Default()
	if repoRoot == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	var user Config
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	cfg.SensitivePaths = append(cfg.SensitivePaths, user.SensitivePaths...)
	cfg.GeneratedPaths = append(cfg.GeneratedPaths, user.GeneratedPaths...)
	cfg.TestPathPatterns = append(cfg.TestPathPatterns, user.TestPathPatterns...)
	cfg.ExtraChecks = append(cfg.ExtraChecks, user.ExtraChecks...)
	if user.WindowBefore.Duration != 0 {
		cfg.WindowBefore = user.WindowBefore
	}
	if user.WindowAfter.Duration != 0 {
		cfg.WindowAfter = user.WindowAfter
	}
	if user.Roots.ClaudeCode != "" {
		cfg.Roots.ClaudeCode = user.Roots.ClaudeCode
	}
	if user.Roots.Codex != "" {
		cfg.Roots.Codex = user.Roots.Codex
	}
	if user.Roots.Pi != "" {
		cfg.Roots.Pi = user.Roots.Pi
	}
	if user.Roots.HookLog != "" {
		cfg.Roots.HookLog = user.Roots.HookLog
	}
	if user.LLM.Provider != "" {
		cfg.LLM.Provider = user.LLM.Provider
	}
	if user.LLM.Model != "" {
		cfg.LLM.Model = user.LLM.Model
	}
	if user.LLM.MaxInputChars != 0 {
		cfg.LLM.MaxInputChars = user.LLM.MaxInputChars
	}
	cfg.Comment.IncludePrompts = user.Comment.IncludePrompts
	if user.Comment.MaxLines != 0 {
		cfg.Comment.MaxLines = user.Comment.MaxLines
	}
	return cfg, nil
}
