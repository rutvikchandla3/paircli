package main

import (
	"fmt"
	"os"

	"github.com/rutvikchandla3/paircli/internal/claudecode"
	"github.com/rutvikchandla3/paircli/internal/codex"
	"github.com/rutvikchandla3/paircli/internal/pi"
)

// runHook dispatches both `paircli hook install <harness>` (Path-A setup)
// and `paircli hook <harness> <event>` (invoked BY the installed hook), per
// ARCHITECTURE.md's CLI shape.
func runHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: paircli hook install <harness> | paircli hook <harness> <event>")
	}

	if args[0] == "install" {
		if len(args) < 2 {
			return fmt.Errorf("usage: paircli hook install <harness>")
		}
		return installHook(args[1])
	}

	harness := args[0]
	if len(args) < 2 {
		return fmt.Errorf("usage: paircli hook %s <event>", harness)
	}
	event := args[1]

	switch harness {
	case "claude-code":
		return claudecode.RunHookEvent(event, os.Stdin)
	case "codex":
		return fmt.Errorf("codex: hook event handling not yet implemented, tracked in docs/ARCHITECTURE.md")
	case "pi":
		return fmt.Errorf("pi: hook event handling not yet implemented, tracked in docs/ARCHITECTURE.md")
	default:
		return fmt.Errorf("unknown harness %q", harness)
	}
}

func installHook(harness string) error {
	switch harness {
	case "claude-code":
		if err := claudecode.InstallHooks(); err != nil {
			return err
		}
		path, _ := claudecode.SettingsPath()
		fmt.Printf("paircli: installed Claude Code hooks in %s\n", path)
		return nil
	case "codex":
		return codex.InstallHooks()
	case "pi":
		return pi.InstallHooks()
	default:
		return fmt.Errorf("unknown harness %q (want claude-code | codex | pi)", harness)
	}
}
