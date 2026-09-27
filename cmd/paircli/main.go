// Command paircli aggregates PR-review process signals across Claude Code,
// Codex CLI, and Pi sessions, correlates them to a PR's commits, and writes
// a structured output folder. See docs/SIGNALS.md and docs/ARCHITECTURE.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "scan":
		err = runScan(os.Args[2:])
	case "hook":
		err = runHook(os.Args[2:])
	case "doctor":
		err = runDoctor(os.Args[2:])
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
  paircli scan <pr-number-or-url>   run both capture paths as needed, correlate, write output folder
  paircli hook install <harness>    install Path-A hooks for one harness (claude-code | codex | pi)
  paircli hook <harness> <event>    internal: invoked BY the installed hook
  paircli doctor                    report which harnesses have hooks installed / are reconstructable

See docs/SIGNALS.md and docs/ARCHITECTURE.md for the full design.
`)
}
