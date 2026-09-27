package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_Missing(t *testing.T) {
	// Create a temporary directory with no config file
	tempdir := t.TempDir()

	cfg, err := Load(tempdir)
	if err != nil {
		t.Errorf("Load(empty dir) = error %v, want nil", err)
	}

	if cfg == nil {
		t.Fatalf("Load returned nil config")
	}

	// Should return default values
	defaultCfg := Default()
	if cfg.LLM.Provider != defaultCfg.LLM.Provider {
		t.Errorf("LLM.Provider = %q, want default %q", cfg.LLM.Provider, defaultCfg.LLM.Provider)
	}
	if cfg.LLM.Model != defaultCfg.LLM.Model {
		t.Errorf("LLM.Model = %q, want default %q", cfg.LLM.Model, defaultCfg.LLM.Model)
	}
	if cfg.Comment.MaxLines != defaultCfg.Comment.MaxLines {
		t.Errorf("Comment.MaxLines = %d, want default %d", cfg.Comment.MaxLines, defaultCfg.Comment.MaxLines)
	}
}

func TestLoad_Merge(t *testing.T) {
	// Create a temporary directory with a config file
	tempdir := t.TempDir()
	configPath := filepath.Join(tempdir, FileName)

	configContent := `{
		"sensitive_paths": ["secret/**"],
		"window_before": "24h",
		"llm": {
			"provider": "anthropic"
		}
	}`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(tempdir)
	if err != nil {
		t.Errorf("Load failed: %v", err)
	}

	if cfg == nil {
		t.Fatalf("Load returned nil config")
	}

	// Check that user paths were added to defaults
	hasSecret := false
	for _, p := range cfg.SensitivePaths {
		if p == "secret/**" {
			hasSecret = true
			break
		}
	}
	if !hasSecret {
		t.Errorf("SensitivePaths missing user-provided \"secret/**\"")
	}

	// Check that window_before was merged
	if cfg.WindowBefore.Duration != 24*time.Hour {
		t.Errorf("WindowBefore = %v, want 24h", cfg.WindowBefore.Duration)
	}

	// Check that provider was set
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("LLM.Provider = %q, want \"anthropic\"", cfg.LLM.Provider)
	}

	// Check that model is still the default
	if cfg.LLM.Model != DefaultModel {
		t.Errorf("LLM.Model = %q, want default %q", cfg.LLM.Model, DefaultModel)
	}
}

func TestLoad_BadDuration(t *testing.T) {
	tempdir := t.TempDir()
	configPath := filepath.Join(tempdir, FileName)

	configContent := `{
		"window_after": "soon"
	}`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(tempdir)
	if err == nil {
		t.Errorf("Load with invalid duration = nil error, want error")
	}
	if cfg != nil {
		t.Errorf("Load with invalid duration = %v, want nil", cfg)
	}
}

func TestLoad_BadJSON(t *testing.T) {
	tempdir := t.TempDir()
	configPath := filepath.Join(tempdir, FileName)

	configContent := `{ invalid json`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(tempdir)
	if err == nil {
		t.Errorf("Load with bad JSON = nil error, want error")
	}
	if cfg != nil {
		t.Errorf("Load with bad JSON = %v, want nil", cfg)
	}

	// Check that error message mentions .paircli.json
	if !strings.Contains(err.Error(), FileName) {
		t.Errorf("Error message %q doesn't mention %q", err.Error(), FileName)
	}
}
