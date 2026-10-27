package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/selfupdate"
	"github.com/rutvikchandla3/paircli/internal/state"
)

// Setup states, as written to setup.json and shown by `paircli status`.
const (
	setupIdle    = "idle" // no run in progress; the last run (if any) succeeded
	setupRunning = "running"
	setupFailed  = "failed"
)

// setupStatus is setup.json: the outcome of the latest `paircli setup` run.
// Every run overwrites it, so a failure is visible with its real error until
// the next run replaces it; it never blocks a retry.
type setupStatus struct {
	SchemaVersion int               `json:"schema_version"`
	State         string            `json:"state"`
	Error         *string           `json:"error"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Log           string            `json:"log"`
	Steps         map[string]string `json:"steps,omitempty"`
}

func setupStatusPath() string { return filepath.Join(state.Dir(), "setup.json") }

// readSetupStatus loads setup.json. A missing or unreadable file is idle.
func readSetupStatus() setupStatus {
	var s setupStatus
	data, err := os.ReadFile(setupStatusPath())
	if err != nil || json.Unmarshal(data, &s) != nil || s.State == "" {
		return setupStatus{SchemaVersion: state.SchemaVersion, State: setupIdle}
	}
	return s
}

func writeSetupStatus(s setupStatus) error {
	s.SchemaVersion = state.SchemaVersion
	s.UpdatedAt = time.Now().UTC()
	return state.WriteJSONAtomic(setupStatusPath(), s)
}

// The steps of a setup run, replaceable so tests need no network, no real
// hook config and no browser.
var (
	setupSelfUpdate   = selfUpdateQuietly
	setupInstallHooks = installHooksFor
	setupEnsureLogin  = ensureLogin
)

// setupArgs is a parsed `paircli setup` invocation.
type setupArgs struct {
	Harness       string
	PluginVersion string
	NoLogin       bool
	JSON          bool
}

func parseSetupArgs(args []string) (setupArgs, error) {
	var a setupArgs
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // setupRun reports parse errors itself
	fs.StringVar(&a.Harness, "harness", "", "claude-code | codex | pi (default: every detected harness)")
	fs.StringVar(&a.PluginVersion, "plugin-version", "", "Pair plugin version (used by the plugin integration)")
	fs.BoolVar(&a.NoLogin, "no-login", false, "skip the device login")
	fs.BoolVar(&a.JSON, "json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if a.Harness != "" {
		if _, ok := harnessTable[a.Harness]; !ok {
			return a, fmt.Errorf("unknown harness %q (want claude-code | codex | pi)", a.Harness)
		}
	}
	return a, nil
}

func runSetup(args []string) error { return setupRun(args, os.Stdout, os.Stderr) }

// setupRun makes this machine ready for `paircli publish`: it updates the CLI,
// installs the agent hooks and logs in. It is safe to run any number of times
// and to start from a hook: a second run while one is in progress exits at
// once. It can take minutes, because the login waits for the user.
//
// stderr (and the run's log file) carry progress, including the login code
// and URL; stdout carries only the one-line result, or the JSON with --json.
func setupRun(args []string, stdout, stderr io.Writer) error {
	a, err := parseSetupArgs(args)
	if err != nil {
		return err
	}
	lock, err := state.TryLock("setup")
	if errors.Is(err, state.ErrLocked) {
		return printSetupResult(stdout, a.JSON, "setup already running", readSetupRunning())
	}
	if err != nil {
		return err
	}
	defer lock.Release()

	logPath, logFile := openSetupLog()
	if logFile != nil {
		defer logFile.Close()
		stderr = io.MultiWriter(stderr, logFile)
	}
	status := setupStatus{State: setupRunning, Log: logPath, Steps: map[string]string{}}
	if err := writeSetupStatus(status); err != nil {
		fmt.Fprintf(stderr, "paircli: could not write setup status: %v\n", err)
	}

	var failures []string
	step := func(name string, run func() (string, error), tolerate bool) {
		fmt.Fprintf(stderr, "paircli: setup: %s\n", name)
		outcome, err := run()
		switch {
		case err == nil:
			status.Steps[name] = outcome
		case tolerate:
			status.Steps[name] = "skipped: " + err.Error()
			fmt.Fprintf(stderr, "paircli: setup: %s skipped: %v\n", name, err)
		default:
			status.Steps[name] = "failed: " + err.Error()
			failures = append(failures, name+": "+err.Error())
			fmt.Fprintf(stderr, "paircli: setup: %s failed: %v\n", name, err)
		}
	}

	step("update", func() (string, error) { return setupSelfUpdate(context.Background(), a.PluginVersion) }, true)
	step("hooks", func() (string, error) { return setupInstallHooks(stderr, a.Harness) }, false)
	if a.NoLogin {
		status.Steps["login"] = "skipped: --no-login"
	} else {
		step("login", func() (string, error) {
			if _, err := setupEnsureLogin(resolveServer(""), stderr, stderr); err != nil {
				return "", err
			}
			return "logged in", nil
		}, false)
	}

	status.State = setupIdle
	summary := "setup complete"
	if len(failures) > 0 {
		msg := strings.Join(failures, "; ")
		status.State, status.Error = setupFailed, &msg
		summary = "setup failed: " + msg
	}
	if err := writeSetupStatus(status); err != nil {
		fmt.Fprintf(stderr, "paircli: could not write setup status: %v\n", err)
	}
	if err := printSetupResult(stdout, a.JSON, summary, status); err != nil {
		return err
	}
	if len(failures) > 0 {
		return errors.New("setup failed")
	}
	return nil
}

// readSetupRunning is the status reported to a run that lost the lock race.
func readSetupRunning() setupStatus {
	s := readSetupStatus()
	s.State = setupRunning
	return s
}

func printSetupResult(w io.Writer, jsonOut bool, summary string, s setupStatus) error {
	if jsonOut {
		s.SchemaVersion = state.SchemaVersion
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	_, err := fmt.Fprintf(w, "paircli: %s\n", summary)
	return err
}

// openSetupLog creates this run's log file. It returns no file when the log
// directory is unusable: a missing log must not stop setup.
func openSetupLog() (string, *os.File) {
	dir := state.LogDir()
	if dir == "" || os.MkdirAll(dir, 0o700) != nil {
		return "", nil
	}
	path := filepath.Join(dir, "setup-"+time.Now().Format("20060102-150405")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", nil
	}
	return path, f
}

// selfUpdateQuietly installs a newer paircli when there is one. Its failures
// (offline, Homebrew-managed, plugin too old) never fail setup, so the caller
// tolerates its error.
func selfUpdateQuietly(ctx context.Context, pluginVersion string) (string, error) {
	result, err := selfupdate.Run(ctx, selfupdate.Options{
		CurrentVersion: version,
		ManifestURL:    os.Getenv("PAIRCLI_UPDATE_MANIFEST_URL"),
		PluginVersion:  pluginVersion,
		AllowInsecure:  os.Getenv("PAIRCLI_ALLOW_INSECURE_DOWNLOAD") == "1",
	})
	switch {
	case err != nil:
		return "", err
	case result.SkippedReason != "":
		return "skipped: " + result.SkippedReason, nil
	case result.Updated:
		return fmt.Sprintf("updated %s to %s", result.CurrentVersion, result.LatestVersion), nil
	}
	return "current (" + result.CurrentVersion + ")", nil
}

// installHooksFor installs the named harness's hooks, or every detected
// harness's when none is named.
func installHooksFor(w io.Writer, harness string) (string, error) {
	if harness == "" {
		return "installed for detected harnesses", installAllHarnesses(w)
	}
	return "installed for " + harness, installHarness(w, harness)
}
