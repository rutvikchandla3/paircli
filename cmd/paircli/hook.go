package main

import (
	"fmt"
	"io"
	"os"

	"github.com/rutvikchandla3/paircli/internal/claudecode"
	"github.com/rutvikchandla3/paircli/internal/codex"
	"github.com/rutvikchandla3/paircli/internal/pi"
)

// harnessHooks is the per-harness hook integration `paircli hook` dispatches
// to. Every harness package (claudecode, codex, pi) implements this same
// shape so the CLI never needs to change when a new harness's hooks land.
type harnessHooks struct {
	Install        func() error
	Uninstall      func() error
	Installed      func() bool
	Run            func(event string, stdin io.Reader) error
	InstallMessage func() string // optional: extra line printed after install
}

// harnessTable maps each supported harness name to its hook integration.
var harnessTable = map[string]harnessHooks{
	"claude-code": {
		Install:   claudecode.InstallHooks,
		Uninstall: claudecode.UninstallHooks,
		Installed: claudecode.HooksInstalled,
		Run:       claudecode.RunHookEvent,
	},
	"codex": {
		Install:        codex.InstallHooks,
		Uninstall:      codex.UninstallHooks,
		Installed:      codex.HooksInstalled,
		Run:            codex.RunHookEvent,
		InstallMessage: codex.InstallMessage,
	},
	"pi": {
		Install:        pi.InstallHooks,
		Uninstall:      pi.UninstallHooks,
		Installed:      pi.HooksInstalled,
		Run:            pi.RunHookEvent,
		InstallMessage: pi.InstallMessage,
	},
}

// runHook dispatches:
//
//	paircli hook install <harness>
//	paircli hook uninstall <harness>
//	paircli hook <harness> <Event>   (invoked BY the installed hook)
func runHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: paircli hook install <harness> | paircli hook uninstall <harness> | paircli hook <harness> <event>")
	}

	switch args[0] {
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("usage: paircli hook install <harness>")
		}
		return installHarness(args[1])
	case "uninstall":
		if len(args) < 2 {
			return fmt.Errorf("usage: paircli hook uninstall <harness>")
		}
		return uninstallHarness(args[1])
	}

	harness := args[0]
	if len(args) < 2 {
		return fmt.Errorf("usage: paircli hook %s <event>", harness)
	}
	event := args[1]

	h, ok := harnessTable[harness]
	if !ok {
		return fmt.Errorf("unknown harness %q", harness)
	}
	// Run implementations must never return a non-nil error for a bad event:
	// they log failures themselves and return nil so the calling agent is
	// never broken by a failing hook.
	return h.Run(event, os.Stdin)
}

func installHarness(harness string) error {
	h, ok := harnessTable[harness]
	if !ok {
		return fmt.Errorf("unknown harness %q (want claude-code | codex | pi)", harness)
	}
	if err := h.Install(); err != nil {
		return err
	}
	fmt.Printf("paircli: installed %s hooks\n", harness)
	if h.InstallMessage != nil {
		fmt.Println(h.InstallMessage())
	}
	return nil
}

func uninstallHarness(harness string) error {
	h, ok := harnessTable[harness]
	if !ok {
		return fmt.Errorf("unknown harness %q (want claude-code | codex | pi)", harness)
	}
	if err := h.Uninstall(); err != nil {
		return err
	}
	fmt.Printf("paircli: uninstalled %s hooks\n", harness)
	return nil
}
