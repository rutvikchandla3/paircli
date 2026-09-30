package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/snapshot"
)

// TestParseJudgeArgs covers flags after the positional snapshot path.
func TestParseJudgeArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want judgeArgs
	}{
		{
			name: "path with flags after it",
			args: []string{"/tmp/pr-482/snapshot.json", "--out", "/tmp/judged",
				"--llm", "claude-cli", "--model", "claude-opus-5-5",
				"--no-agent-trace", "--force", "--json"},
			want: judgeArgs{
				Path:         "/tmp/pr-482/snapshot.json",
				Out:          "/tmp/judged",
				LLM:          "claude-cli",
				Model:        "claude-opus-5-5",
				NoAgentTrace: true,
				Force:        true,
				JSON:         true,
			},
		},
		{
			name: "bare path, defaults",
			args: []string{"snapshot.json"},
			want: judgeArgs{Path: "snapshot.json"},
		},
		{
			name: "single-dash long flags",
			args: []string{"snap.json", "-llm", "anthropic", "-json"},
			want: judgeArgs{Path: "snap.json", LLM: "anthropic", JSON: true},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseJudgeArgs(c.args)
			if err != nil {
				t.Fatalf("parseJudgeArgs(%v): %v", c.args, err)
			}
			if got != c.want {
				t.Errorf("parseJudgeArgs(%v) = %+v, want %+v", c.args, got, c.want)
			}
		})
	}
}

// TestParseJudgeArgs_Errors covers the usage errors.
func TestParseJudgeArgs_Errors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--json"},           // flag before the positional argument
		{"s.json", "--nope"}, // unknown flag
		{"s.json", "extra"},  // trailing positional
	} {
		if _, err := parseJudgeArgs(args); err == nil {
			t.Errorf("parseJudgeArgs(%v): expected an error", args)
		}
	}
}

// TestJudgeOutDir pins the --out default: the snapshot's own folder, which is
// where the scan that wrote it put the report the judging replaces.
func TestJudgeOutDir(t *testing.T) {
	for _, c := range []struct{ out, path, want string }{
		{"/tmp/judged", "/tmp/pr-482/snapshot.json", "/tmp/judged"},
		{"", "/tmp/pr-482/snapshot.json", "/tmp/pr-482"},
		{"", "snapshot.json", "."},
	} {
		if got := judgeOutDir(c.out, c.path); got != c.want {
			t.Errorf("judgeOutDir(%q, %q) = %q, want %q", c.out, c.path, got, c.want)
		}
	}
}

// TestRunJudge_NoProvider checks the refusal: a snapshot with no provider
// configured replays deterministically, which is not judging, so the command
// says so instead of rewriting the report with the signals it already has.
func TestRunJudge_NoProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	snap := snapshot.Build(snapshot.Input{Tool: "paircli 0.0.0-test", Config: config.Default()})
	if err := snapshot.Write(dir, snap); err != nil {
		t.Fatalf("snapshot.Write: %v", err)
	}

	err := runJudge([]string{path})
	if err == nil {
		t.Fatal("runJudge without a provider: expected an error")
	}
	if !strings.Contains(err.Error(), "--llm") {
		t.Errorf("err = %v, want it to name the flag that fixes it", err)
	}
}
