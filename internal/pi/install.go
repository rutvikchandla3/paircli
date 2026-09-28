package pi

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed extension/paircli.ts
var extensionSource []byte

// extensionMarker is the required first line of paircli's Pi extension. It is
// how InstallHooks, UninstallHooks and HooksInstalled recognize a file paircli
// owns, and it guards against clobbering an unrelated paircli.ts.
const extensionMarker = "// paircli-extension v1"

// piDirEnv overrides the default ~/.pi/agent directory, used by tests so they
// never touch a real Pi install.
const piDirEnv = "PAIRCLI_PI_DIR"

// DefaultDir returns $PAIRCLI_PI_DIR if set, else ~/.pi/agent.
func DefaultDir() string {
	if d := os.Getenv(piDirEnv); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// ExtensionPath returns the path paircli installs its Pi extension to:
// DefaultDir()/extensions/paircli.ts.
func ExtensionPath() string {
	return filepath.Join(DefaultDir(), "extensions", "paircli.ts")
}

// InstallHooks writes the embedded Pi extension, creating the extensions
// directory as needed. It overwrites an older paircli extension, but refuses
// to overwrite a paircli.ts whose first line is not extensionMarker.
func InstallHooks() error {
	path := ExtensionPath()

	if existing, err := os.ReadFile(path); err == nil {
		if !hasMarker(existing) {
			return fmt.Errorf("pi: refusing to overwrite %s: not a paircli extension (first line is not %q)", path, extensionMarker)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, extensionSource, 0o644)
}

// InstallMessage is the extra line `paircli hook install pi` prints after
// InstallHooks succeeds, telling the user how to load the extension.
func InstallMessage() string {
	return "Restart pi (or run /reload) to load the paircli extension."
}

// UninstallHooks removes our Pi extension, but only if it is actually ours
// (its first line is extensionMarker). A missing file is not an error.
func UninstallHooks() error {
	path := ExtensionPath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !hasMarker(data) {
		return nil
	}
	return os.Remove(path)
}

// HooksInstalled reports whether our Pi extension is installed: the file
// exists and starts with extensionMarker.
func HooksInstalled() bool {
	data, err := os.ReadFile(ExtensionPath())
	if err != nil {
		return false
	}
	return hasMarker(data)
}

// RunHookEvent is a no-op for Pi: the extension writes hook records into the
// session file itself, so there is nothing for `paircli hook pi <Event>` to do.
// The cmd/paircli hook table requires the function to exist regardless.
func RunHookEvent(event string, stdin io.Reader) error { return nil }

// hasMarker reports whether data's first line is extensionMarker.
func hasMarker(data []byte) bool {
	first := data
	if i := strings.IndexByte(string(data), '\n'); i >= 0 {
		first = data[:i]
	}
	return strings.TrimRight(string(first), "\r") == extensionMarker
}
