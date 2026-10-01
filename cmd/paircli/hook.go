package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	// Detect reports whether this harness looks present on this machine: the
	// directory paircli writes its hook config into exists. `paircli hook
	// install` with no harness installs only for detected harnesses, so it
	// never writes into a tool that is not here.
	Detect func() bool
}

// harnessTable maps each supported harness name to its hook integration.
var harnessTable = map[string]harnessHooks{
	"claude-code": {
		Install:   claudecode.InstallHooks,
		Uninstall: claudecode.UninstallHooks,
		Installed: claudecode.HooksInstalled,
		Run:       claudecode.RunHookEvent,
		Detect:    func() bool { return dirOfSettings(claudecode.SettingsPath) },
	},
	"codex": {
		Install:        codex.InstallHooks,
		Uninstall:      codex.UninstallHooks,
		Installed:      codex.HooksInstalled,
		Run:            codex.RunHookEvent,
		InstallMessage: codex.InstallMessage,
		Detect:         func() bool { return dirOf(codex.HooksPath()) },
	},
	"pi": {
		Install:        pi.InstallHooks,
		Uninstall:      pi.UninstallHooks,
		Installed:      pi.HooksInstalled,
		Run:            pi.RunHookEvent,
		InstallMessage: pi.InstallMessage,
		Detect:         func() bool { return dirExists(pi.DefaultDir()) },
	},
}

// harnessOrder is the order `paircli hook install|uninstall` walks when no
// harness is named, so the output is stable across runs.
var harnessOrder = []string{"claude-code", "codex", "pi"}

// dirOfSettings reports whether the directory holding pathFunc()'s file
// exists. It is the common case for harnesses whose config file is the thing
// paircli writes (Claude Code's settings.json).
func dirOfSettings(pathFunc func() (string, error)) bool {
	path, err := pathFunc()
	if err != nil {
		return false
	}
	return dirOf(path)
}

// dirOf reports whether the directory containing path exists. An empty path
// (unresolvable home directory) is never a match, so a harness is never
// "detected" just because filepath.Dir turned "" into ".".
func dirOf(path string) bool {
	if path == "" {
		return false
	}
	return dirExists(filepath.Dir(path))
}

// dirExists reports whether path names an existing directory.
func dirExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// runHook dispatches:
//
//	paircli hook install [harness]      (no harness: every detected one)
//	paircli hook uninstall [harness]    (no harness: every detected one)
//	paircli hook <harness> <Event>      (invoked BY the installed hook)
func runHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: paircli hook install [harness] | paircli hook uninstall [harness] | paircli hook <harness> <event>")
	}

	switch args[0] {
	case "install":
		if len(args) < 2 {
			return installAllHarnesses(os.Stdout)
		}
		return installHarness(os.Stdout, args[1])
	case "uninstall":
		if len(args) < 2 {
			return uninstallAllHarnesses(os.Stdout)
		}
		return uninstallHarness(os.Stdout, args[1])
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

// installAllHarnesses installs hooks for every harness detected on this
// machine, so setting up needs one command rather than one per harness. A
// harness that fails does not stop the others: every failure is reported and
// the command exits non-zero if any of them failed.
func installAllHarnesses(w io.Writer) error {
	var (
		detected int
		failed   []string
	)
	for _, name := range harnessOrder {
		if !harnessTable[name].Detect() {
			fmt.Fprintf(w, "paircli: skipped %s (not detected)\n", name)
			continue
		}
		detected++
		if err := installHarness(w, name); err != nil {
			fmt.Fprintf(w, "paircli: %s: %v\n", name, err)
			failed = append(failed, name)
		}
	}

	if detected == 0 {
		return fmt.Errorf("no harnesses detected (looked for ~/.claude, ~/.codex or $CODEX_HOME, ~/.pi/agent); install one explicitly with: paircli hook install <harness>")
	}
	if len(failed) > 0 {
		return fmt.Errorf("hook install failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// uninstallAllHarnesses removes paircli's hooks wherever they are installed.
// Unlike install it keys off Installed(), not Detect(): a detected harness
// with no paircli hooks has nothing to remove and is simply skipped.
func uninstallAllHarnesses(w io.Writer) error {
	var (
		removed int
		failed  []string
	)
	for _, name := range harnessOrder {
		if !harnessTable[name].Installed() {
			fmt.Fprintf(w, "paircli: skipped %s (not installed)\n", name)
			continue
		}
		if err := uninstallHarness(w, name); err != nil {
			fmt.Fprintf(w, "paircli: %s: %v\n", name, err)
			failed = append(failed, name)
			continue
		}
		removed++
	}

	if removed == 0 {
		fmt.Fprintln(w, "paircli: no paircli hooks installed")
	}
	if len(failed) > 0 {
		return fmt.Errorf("hook uninstall failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// installHarness installs one named harness. An explicitly named harness is
// always installed, even if Detect() says it is absent: the user asked for it
// by name, so paircli creates the config rather than second-guessing them.
func installHarness(w io.Writer, harness string) error {
	h, ok := harnessTable[harness]
	if !ok {
		return fmt.Errorf("unknown harness %q (want claude-code | codex | pi)", harness)
	}
	// Install rewrites the absolute paircli binary path, so a re-run after
	// moving or reinstalling paircli is meaningful, not a no-op.
	was := h.Installed()
	if err := h.Install(); err != nil {
		return err
	}
	if was {
		fmt.Fprintf(w, "paircli: refreshed %s hooks\n", harness)
	} else {
		fmt.Fprintf(w, "paircli: installed %s hooks\n", harness)
	}
	if h.InstallMessage != nil {
		fmt.Fprintln(w, h.InstallMessage())
	}
	return nil
}

func uninstallHarness(w io.Writer, harness string) error {
	h, ok := harnessTable[harness]
	if !ok {
		return fmt.Errorf("unknown harness %q (want claude-code | codex | pi)", harness)
	}
	if err := h.Uninstall(); err != nil {
		return err
	}
	fmt.Fprintf(w, "paircli: uninstalled %s hooks\n", harness)
	return nil
}
