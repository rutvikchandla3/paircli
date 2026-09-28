package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/pr"
	"github.com/rutvikchandla3/paircli/internal/scan"
)

// scanArgs is a parsed `paircli scan` invocation.
type scanArgs struct {
	PRArg           string
	Out             string
	Post            bool
	IncludePrompts  bool
	LLM             string
	Model           string
	NoHooks         bool
	NoCommitPatches bool
	NoAgentTrace    bool
	WindowBefore    time.Duration
	WindowAfter     time.Duration
	JSON            bool
}

// parseScanArgs parses `scan <pr-number-or-url> [flags]`: the positional PR
// argument comes first and the flags after it, so the flag package never has
// to decide what an interspersed positional means.
func parseScanArgs(args []string) (scanArgs, error) {
	var a scanArgs
	if len(args) < 1 {
		return a, fmt.Errorf("usage: paircli scan <pr-number-or-url> [flags]")
	}
	if strings.HasPrefix(args[0], "-") {
		return a, fmt.Errorf("usage: paircli scan <pr-number-or-url> [flags] (the PR argument comes first)")
	}
	a.PRArg = args[0]

	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // runScan reports parse errors itself
	fs.StringVar(&a.Out, "out", "", "output folder")
	fs.BoolVar(&a.Post, "post", false, "create or update the PR comment")
	fs.BoolVar(&a.IncludePrompts, "include-prompts", false, "let comment.md carry prompt text")
	fs.StringVar(&a.LLM, "llm", "", "LLM provider: none | anthropic | claude-cli")
	fs.StringVar(&a.Model, "model", "", "LLM model")
	fs.BoolVar(&a.NoHooks, "no-hooks", false, "ignore hook-log records")
	fs.BoolVar(&a.NoCommitPatches, "no-commit-patches", false, "skip per-commit patches")
	fs.BoolVar(&a.NoAgentTrace, "no-agent-trace", false, "skip agent-trace.json")
	fs.DurationVar(&a.WindowBefore, "window-before", 0, "session search window before the first commit")
	fs.DurationVar(&a.WindowAfter, "window-after", 0, "session search window after the last commit")
	fs.BoolVar(&a.JSON, "json", false, "print signals.json to stdout")
	if err := fs.Parse(args[1:]); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return a, nil
}

// runScan implements `paircli scan`: it resolves the PR and repo, applies the
// flag overrides to the repo configuration, and runs the pipeline.
func runScan(args []string) error {
	a, err := parseScanArgs(args)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, number, err := pr.ResolveArg(cwd, a.PRArg)
	if err != nil {
		return err
	}

	cfg, err := config.Load(scan.RepoRoot(cwd, repo))
	if err != nil {
		return err
	}
	if a.WindowBefore != 0 {
		cfg.WindowBefore = config.Duration{Duration: a.WindowBefore}
	}
	if a.WindowAfter != 0 {
		cfg.WindowAfter = config.Duration{Duration: a.WindowAfter}
	}
	if a.LLM != "" {
		cfg.LLM.Provider = a.LLM
	}
	if a.Model != "" {
		cfg.LLM.Model = a.Model
	}
	provider, err := llm.New(cfg.LLM)
	if err != nil {
		return err
	}

	res, err := scan.Run(context.Background(), scan.Options{
		Repo:            repo,
		Number:          number,
		CWD:             cwd,
		OutDir:          a.Out,
		Runner:          pr.GH{},
		Config:          cfg,
		NoHooks:         a.NoHooks,
		NoCommitPatches: a.NoCommitPatches,
		NoAgentTrace:    a.NoAgentTrace,
		Post:            a.Post,
		IncludePrompts:  a.IncludePrompts,
		LLM:             provider,
		Version:         version,
	})
	if err != nil {
		return err
	}

	cov := res.Report.Coverage
	summary := fmt.Sprintf("paircli: wrote %s — %d sessions, %s of changed lines explained, %d alerts",
		res.OutDir, cov.Sessions, engine.Pct(cov.LinesExplained, cov.LinesTotal), res.Alerts)

	if !a.JSON {
		fmt.Println(summary)
		return nil
	}
	// Keep stdout valid JSON for `--json | jq`.
	fmt.Fprintln(os.Stderr, summary)
	out, err := json.MarshalIndent(res.Report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
