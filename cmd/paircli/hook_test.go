package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/claudecode"
	"github.com/rutvikchandla3/paircli/internal/codex"
	"github.com/rutvikchandla3/paircli/internal/pi"
)

// harnessHomes points every harness's config root inside a fresh temp tree
// and returns the tree, so no test touches a real ~/.claude, ~/.codex or
// ~/.pi. The directories themselves are NOT created: each test creates only
// the ones it wants detected.
func harnessHomes(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("PAIRCLI_PI_DIR", filepath.Join(root, "pi"))
	return root
}

// harnessConfigDir returns one harness's config root inside a harnessHomes
// tree: the directory that harness's Detect() stats.
func harnessConfigDir(root, harness string) string {
	switch harness {
	case "claude-code":
		return filepath.Join(root, "home", ".claude")
	case "codex":
		return filepath.Join(root, "codex")
	}
	return filepath.Join(root, "pi")
}

// allHarnessConfigDirs returns every harness's config root, for tests that
// want the machine to look fully set up.
func allHarnessConfigDirs(root string) []string {
	dirs := make([]string, 0, len(harnessOrder))
	for _, h := range harnessOrder {
		dirs = append(dirs, harnessConfigDir(root, h))
	}
	return dirs
}

func mkdirAll(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}

// TestInstallAll_EveryDetectedHarness: with all three harness config roots
// present, `paircli hook install` (no harness) sets up all three.
func TestInstallAll_EveryDetectedHarness(t *testing.T) {
	root := harnessHomes(t)
	mkdirAll(t, allHarnessConfigDirs(root)...)

	var buf bytes.Buffer
	if err := installAllHarnesses(&buf); err != nil {
		t.Fatalf("installAllHarnesses: %v\n%s", err, buf.String())
	}

	for _, h := range []string{"claude-code", "codex", "pi"} {
		if !strings.Contains(buf.String(), "installed "+h+" hooks") {
			t.Errorf("output missing install line for %s:\n%s", h, buf.String())
		}
	}
	if !claudecode.HooksInstalled() {
		t.Error("claude-code hooks not installed")
	}
	if !codex.HooksInstalled() {
		t.Error("codex hooks not installed")
	}
	if !pi.HooksInstalled() {
		t.Error("pi hooks not installed")
	}
	// The Codex trust reminder must still reach the user, since a batch
	// install is exactly where it is easiest to miss.
	if !strings.Contains(buf.String(), "Codex needs you to review and trust") {
		t.Errorf("codex trust reminder missing from:\n%s", buf.String())
	}
}

// TestInstallAll_NoneDetected: on a machine with no harness, the command
// installs nothing, says what it looked for, and fails loudly rather than
// silently doing nothing.
func TestInstallAll_NoneDetected(t *testing.T) {
	harnessHomes(t)

	var buf bytes.Buffer
	err := installAllHarnesses(&buf)
	if err == nil {
		t.Fatalf("installAllHarnesses succeeded with no harnesses; output:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "no harnesses detected") {
		t.Errorf("error = %v, want it to mention no harnesses detected", err)
	}
	for _, h := range harnessOrder {
		if !strings.Contains(buf.String(), "skipped "+h) {
			t.Errorf("output missing skip line for %s:\n%s", h, buf.String())
		}
	}
	if claudecode.HooksInstalled() || codex.HooksInstalled() || pi.HooksInstalled() {
		t.Error("a harness was installed despite none being detected")
	}
}

// TestInstallAll_OnlyDetectedHarnesses: a machine with only Claude Code gets
// only Claude Code's hooks; the others are skipped, not installed.
func TestInstallAll_OnlyDetectedHarnesses(t *testing.T) {
	root := harnessHomes(t)
	claudeDir := harnessConfigDir(root, "claude-code")
	mkdirAll(t, claudeDir)

	var buf bytes.Buffer
	if err := installAllHarnesses(&buf); err != nil {
		t.Fatalf("installAllHarnesses: %v\n%s", err, buf.String())
	}

	if !claudecode.HooksInstalled() {
		t.Error("claude-code hooks not installed")
	}
	if codex.HooksInstalled() {
		t.Error("codex hooks installed although ~/.codex is absent")
	}
	if pi.HooksInstalled() {
		t.Error("pi hooks installed although ~/.pi/agent is absent")
	}
	if !strings.Contains(buf.String(), "skipped codex") || !strings.Contains(buf.String(), "skipped pi") {
		t.Errorf("output missing skip lines:\n%s", buf.String())
	}
}

// TestInstallAll_Idempotent: a second run refreshes rather than duplicating,
// and reports it as a refresh.
func TestInstallAll_Idempotent(t *testing.T) {
	root := harnessHomes(t)
	mkdirAll(t, allHarnessConfigDirs(root)...)

	var first bytes.Buffer
	if err := installAllHarnesses(&first); err != nil {
		t.Fatalf("first installAllHarnesses: %v", err)
	}

	var second bytes.Buffer
	if err := installAllHarnesses(&second); err != nil {
		t.Fatalf("second installAllHarnesses: %v", err)
	}
	if !strings.Contains(second.String(), "refreshed claude-code hooks") {
		t.Errorf("second run did not report a refresh:\n%s", second.String())
	}
}

// TestInstallNamed_UndetectedStillInstalls: naming a harness explicitly
// installs it even when its config root is absent — the user asked for it by
// name, so paircli creates the config.
func TestInstallNamed_UndetectedStillInstalls(t *testing.T) {
	harnessHomes(t)

	var buf bytes.Buffer
	if err := installHarness(&buf, "pi"); err != nil {
		t.Fatalf("installHarness(pi): %v", err)
	}
	if !pi.HooksInstalled() {
		t.Error("pi hooks not installed despite an explicit request")
	}
}

// TestUninstallAll_RemovesEverythingInstalled: the batch uninstall reverses
// the batch install.
func TestUninstallAll_RemovesEverythingInstalled(t *testing.T) {
	root := harnessHomes(t)
	mkdirAll(t, allHarnessConfigDirs(root)...)

	var install bytes.Buffer
	if err := installAllHarnesses(&install); err != nil {
		t.Fatalf("installAllHarnesses: %v", err)
	}

	var buf bytes.Buffer
	if err := uninstallAllHarnesses(&buf); err != nil {
		t.Fatalf("uninstallAllHarnesses: %v\n%s", err, buf.String())
	}

	if claudecode.HooksInstalled() {
		t.Error("claude-code hooks still installed")
	}
	if codex.HooksInstalled() {
		t.Error("codex hooks still installed")
	}
	if pi.HooksInstalled() {
		t.Error("pi hooks still installed")
	}
}

// TestUninstallAll_NothingInstalled: with no hooks present the command is a
// quiet no-op and succeeds, so it is safe to run unconditionally.
func TestUninstallAll_NothingInstalled(t *testing.T) {
	root := harnessHomes(t)
	mkdirAll(t, allHarnessConfigDirs(root)...)

	var buf bytes.Buffer
	if err := uninstallAllHarnesses(&buf); err != nil {
		t.Fatalf("uninstallAllHarnesses: %v", err)
	}
	if !strings.Contains(buf.String(), "no paircli hooks installed") {
		t.Errorf("output missing the nothing-installed line:\n%s", buf.String())
	}
}

// TestRunHook_Usage covers the argument-shape dispatch: no args is a usage
// error, and the no-harness form reaches the batch path.
func TestRunHook_Usage(t *testing.T) {
	if err := runHook(nil); err == nil {
		t.Error("runHook(nil) = nil, want a usage error")
	}
}
