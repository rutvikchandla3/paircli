package deviceauth

import (
	"os/exec"
	"runtime"
)

// OpenBrowser opens rawURL in the user's browser. It is a package-level
// variable so callers (and tests) can replace it; login treats any error as a
// warning, never a failure.
var OpenBrowser = func(rawURL string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
	default:
		return exec.Command("xdg-open", rawURL).Start()
	}
}
