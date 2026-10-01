// Command paircli aggregates PR-review process signals across Claude Code,
// Codex CLI, and Pi sessions, correlates them to a PR's commits, and writes
// a structured output folder. See docs/SIGNALS.md and docs/ARCHITECTURE.md.
package main

import (
	"fmt"
	"os"
)

// version is the CLI version, written into every report.
var version = "0.2.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "scan":
		err = runScan(os.Args[2:])
	case "judge":
		err = runJudge(os.Args[2:])
	case "hook":
		err = runHook(os.Args[2:])
	case "doctor":
		err = runDoctor(os.Args[2:])
	case "version":
		fmt.Printf("paircli %s\n", version)
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "paircli: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "paircli: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `paircli — PR process-signal aggregator

Usage:
  paircli scan <pr-number-or-url> [flags]   discover linked agent sessions, run the
                                            signal pipeline, write the output folder
  paircli judge <snapshot.json> [flags]     run the grounded LLM pass over a scan's
                                            snapshot and rewrite its report files
  paircli hook install [harness]            install hooks; with no harness, every one
                                            detected on this machine (claude-code | codex | pi)
  paircli hook uninstall [harness]          remove hooks; with no harness, all installed ones
  paircli hook <harness> <event>            internal: invoked BY the installed hook
  paircli doctor                            report capture state per harness, gh, config and llm
  paircli version                           print the version

scan flags:
  --out DIR               output folder (default <repo>/.paircli/pr-<n>)
  --post                  create or update the PR comment
  --include-prompts       let comment.md carry prompt text
  --llm PROVIDER          none | anthropic | claude-cli | pi | http (default: config)
  --model MODEL           provider model (default: config)
  --no-hooks              ignore hook-log records
  --no-commit-patches     skip per-commit patches
  --no-agent-trace        skip agent-trace.json
  --snapshot              also write snapshot.json, a full replay record
  --window-before DUR     session search window before the first commit (default 48h)
  --window-after DUR      session search window after the last commit (default 2h)
  --json                  print signals.json to stdout

judge flags:
  --out DIR               output folder (default: the snapshot's own folder)
  --llm PROVIDER          none | anthropic | claude-cli | pi | http (default: the snapshot's config)
  --model MODEL           provider model (default: the snapshot's config)
  --no-agent-trace        skip agent-trace.json
  --force                 replay even if another paircli version wrote the snapshot
  --json                  print signals.json to stdout

See docs/SIGNALS.md and docs/ARCHITECTURE.md for the full design.
`)
}
