package main

import (
	"strings"
	"testing"
	"time"
)

// TestParseScanArgs covers flags after the positional PR argument and the
// windows/LLM overrides the CLI applies to the repo config.
func TestParseScanArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want scanArgs
	}{
		{
			name: "number with flags after it",
			args: []string{"42", "--post", "--json", "--out", "/tmp/out",
				"--include-prompts", "--no-hooks", "--no-commit-patches",
				"--llm", "anthropic", "--model", "claude-opus-5-5",
				"--window-before", "24h", "--window-after", "90m"},
			want: scanArgs{
				PRArg:           "42",
				Out:             "/tmp/out",
				Post:            true,
				IncludePrompts:  true,
				LLM:             "anthropic",
				Model:           "claude-opus-5-5",
				NoHooks:         true,
				NoCommitPatches: true,
				WindowBefore:    24 * time.Hour,
				WindowAfter:     90 * time.Minute,
				JSON:            true,
			},
		},
		{
			name: "bare URL, defaults",
			args: []string{"https://github.com/acme/shop/pull/9"},
			want: scanArgs{PRArg: "https://github.com/acme/shop/pull/9"},
		},
		{
			name: "single-dash long flags",
			args: []string{"7", "-post", "-llm", "none"},
			want: scanArgs{PRArg: "7", Post: true, LLM: "none"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseScanArgs(c.args)
			if err != nil {
				t.Fatalf("parseScanArgs(%v): %v", c.args, err)
			}
			if got != c.want {
				t.Errorf("parseScanArgs(%v) = %+v, want %+v", c.args, got, c.want)
			}
		})
	}
}

// TestParseScanArgs_Errors covers the usage errors.
func TestParseScanArgs_Errors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--post"},                   // flag before the positional argument
		{"7", "--not-a-flag"},        // unknown flag
		{"7", "--window-before", ""}, // unparsable duration
		{"7", "extra"},               // trailing positional
	} {
		if _, err := parseScanArgs(args); err == nil {
			t.Errorf("parseScanArgs(%v): expected an error", args)
		}
	}
}

// TestParseScanArgs_UnknownFlagNamesTheFlag keeps the flag error readable:
// runScan returns it verbatim, and the flag package's own message names it.
func TestParseScanArgs_UnknownFlagNamesTheFlag(t *testing.T) {
	_, err := parseScanArgs([]string{"7", "--nope"})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want it to name the unknown flag", err)
	}
}
