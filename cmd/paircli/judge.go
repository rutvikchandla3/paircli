package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/scan"
	"github.com/rutvikchandla3/paircli/internal/snapshot"
)

// judgeArgs is a parsed `paircli judge` invocation.
type judgeArgs struct {
	Path         string
	Out          string
	LLM          string
	Model        string
	NoAgentTrace bool
	Force        bool
	JSON         bool
}

// parseJudgeArgs parses `judge <snapshot.json> [flags]`. As in parseScanArgs the
// positional argument comes first and the flags after it, so the flag package
// never has to decide what an interspersed positional means.
func parseJudgeArgs(args []string) (judgeArgs, error) {
	var a judgeArgs
	if len(args) < 1 {
		return a, fmt.Errorf("usage: paircli judge <snapshot.json> [flags]")
	}
	if strings.HasPrefix(args[0], "-") {
		return a, fmt.Errorf("usage: paircli judge <snapshot.json> [flags] (the snapshot path comes first)")
	}
	a.Path = args[0]

	fs := flag.NewFlagSet("judge", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // runJudge reports parse errors itself
	fs.StringVar(&a.Out, "out", "", "output folder")
	fs.StringVar(&a.LLM, "llm", "", "LLM provider: none | anthropic | claude-cli | pi | http")
	fs.StringVar(&a.Model, "model", "", "LLM model")
	fs.BoolVar(&a.NoAgentTrace, "no-agent-trace", false, "skip agent-trace.json")
	fs.BoolVar(&a.Force, "force", false, "replay a snapshot another paircli wrote")
	fs.BoolVar(&a.JSON, "json", false, "print signals.json to stdout")
	if err := fs.Parse(args[1:]); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return a, nil
}

// runJudge implements `paircli judge`: it replays a snapshot's signal pipeline
// with the grounded LLM pass on, and rewrites the report files from the result.
//
// The pass does not have to happen during the scan, and this is what makes that
// useful: the snapshot is a complete input, so the judging can run later, on
// another machine, or behind a gateway, without the PR or the local session
// stores being read again.
func runJudge(args []string) error {
	a, err := parseJudgeArgs(args)
	if err != nil {
		return err
	}
	snap, err := snapshot.Read(a.Path)
	if err != nil {
		return err
	}
	outDir := judgeOutDir(a.Out, a.Path)

	// The snapshot carries the config the scan ran with, which is what the
	// replay must use. --llm and --model override it for this run, on the
	// snapshot itself rather than a copy: the provider is built from that
	// config, the judge's requests take their model from it, and the judgments
	// record the model they were produced with.
	cfg := snap.Config
	if cfg == nil {
		cfg = config.Default()
	}
	if a.LLM != "" {
		cfg.LLM.Provider = a.LLM
	}
	if a.Model != "" {
		cfg.LLM.Model = a.Model
	}
	snap.Config = cfg

	provider, err := llm.New(cfg.LLM)
	if err != nil {
		return err
	}
	if provider == nil {
		// A deterministic replay is a replay, not a judging: refuse rather than
		// rewrite the report with the same signals the scan already reported.
		return fmt.Errorf("judge: no LLM provider configured (the snapshot's config says %q): "+
			"pass --llm anthropic, claude-cli, pi or http", cfg.LLM.Provider)
	}

	res, err := scan.Replay(context.Background(), scan.ReplayOptions{
		Snapshot:     snap,
		OutDir:       outDir,
		LLM:          provider,
		NoAgentTrace: a.NoAgentTrace,
		Force:        a.Force,
		Version:      version,
	})
	if err != nil {
		return err
	}
	// A pass that failed leaves the deterministic report in place and is not an
	// error — but for this command the pass is the whole point, so say so rather
	// than leave a report that looks like it was never judged.
	if len(res.LLMErrors) > 0 {
		fmt.Fprintf(os.Stderr, "paircli: the judge pass was incomplete: %s\n",
			strings.Join(res.LLMErrors, "; "))
	}
	return printResult(res, a.JSON)
}

// judgeOutDir is the folder a judging writes into: --out when it is given, and
// otherwise the folder the snapshot itself is in, which is where the scan that
// wrote it put the report the judging replaces.
func judgeOutDir(out, snapshotPath string) string {
	if out != "" {
		return out
	}
	return filepath.Dir(snapshotPath)
}
