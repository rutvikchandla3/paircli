package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withPiDir points PAIRCLI_PI_DIR at a fresh temp dir and returns it, so no
// test ever touches a real ~/.pi/agent.
func withPiDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(piDirEnv, dir)
	return dir
}

func TestExtensionPath_DefaultDir(t *testing.T) {
	dir := withPiDir(t)

	got := ExtensionPath()
	want := filepath.Join(dir, "extensions", "paircli.ts")
	if got != want {
		t.Errorf("ExtensionPath() = %q, want %q", got, want)
	}
}

func TestInstall_WritesMarkedFile(t *testing.T) {
	dir := withPiDir(t)

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	path := filepath.Join(dir, "extensions", "paircli.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed extension: %v", err)
	}
	if string(data) != string(extensionSource) {
		t.Error("installed file does not match the embedded extension source")
	}
	if !strings.HasPrefix(string(data), extensionMarker) {
		t.Errorf("installed file does not start with %q", extensionMarker)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat installed extension: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("installed file mode = %o, want 644", perm)
	}
}

func TestInstall_RefusesForeignFile(t *testing.T) {
	dir := withPiDir(t)

	extDir := filepath.Join(dir, "extensions")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(extDir, "paircli.ts")
	foreign := []byte("// someone else's extension\nexport default function () {}\n")
	if err := os.WriteFile(path, foreign, 0o644); err != nil {
		t.Fatal(err)
	}

	err := InstallHooks()
	if err == nil {
		t.Fatal("InstallHooks() = nil, want error for a foreign paircli.ts")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path %q", err, path)
	}

	// The foreign file must be untouched.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read foreign file: %v", err)
	}
	if string(got) != string(foreign) {
		t.Error("InstallHooks modified the foreign paircli.ts")
	}

	// HooksInstalled must not claim an install, and Uninstall must not delete it.
	if HooksInstalled() {
		t.Error("HooksInstalled() = true for a foreign paircli.ts")
	}
	if err := UninstallHooks(); err != nil {
		t.Errorf("UninstallHooks() error = %v, want nil", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("UninstallHooks removed a foreign paircli.ts: %v", err)
	}
}

func TestInstall_OverwritesOlderPaircliVersion(t *testing.T) {
	dir := withPiDir(t)

	extDir := filepath.Join(dir, "extensions")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(extDir, "paircli.ts")
	if err := os.WriteFile(path, []byte(extensionMarker+"\n// stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(extensionSource) {
		t.Error("InstallHooks did not overwrite the older paircli extension")
	}
}

func TestInstall_Idempotent(t *testing.T) {
	withPiDir(t)

	if err := InstallHooks(); err != nil {
		t.Fatalf("first InstallHooks() error = %v", err)
	}
	if err := InstallHooks(); err != nil {
		t.Fatalf("second InstallHooks() error = %v", err)
	}
	if !HooksInstalled() {
		t.Error("HooksInstalled() = false after two installs")
	}
}

func TestUninstall(t *testing.T) {
	dir := withPiDir(t)

	// A missing file is not an error.
	if err := UninstallHooks(); err != nil {
		t.Errorf("UninstallHooks() with no install error = %v, want nil", err)
	}

	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}
	if err := UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}
	if HooksInstalled() {
		t.Error("HooksInstalled() = true after uninstall")
	}
	if _, err := os.Stat(filepath.Join(dir, "extensions", "paircli.ts")); !os.IsNotExist(err) {
		t.Errorf("extension still present after uninstall: %v", err)
	}

	// Uninstalling twice is fine.
	if err := UninstallHooks(); err != nil {
		t.Errorf("second UninstallHooks() error = %v, want nil", err)
	}
}

func TestHooksInstalled(t *testing.T) {
	withPiDir(t)

	if HooksInstalled() {
		t.Error("HooksInstalled() = true before install")
	}
	if err := InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}
	if !HooksInstalled() {
		t.Error("HooksInstalled() = false after install")
	}
}

func TestInstallMessage(t *testing.T) {
	if msg := InstallMessage(); !strings.Contains(msg, "pi") {
		t.Errorf("InstallMessage() = %q, want it to mention pi", msg)
	}
}

func TestRunHookEvent_Noop(t *testing.T) {
	if err := RunHookEvent("session_start", strings.NewReader("not json at all")); err != nil {
		t.Errorf("RunHookEvent() error = %v, want nil", err)
	}
}
