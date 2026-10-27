// Package notify shows best-effort desktop notifications: osascript on macOS,
// notify-send on Linux, and nothing anywhere else. A notification that cannot
// be shown is not an error; publishing never depends on it.
package notify

import (
	"context"
	"os/exec"
	"runtime"
	"time"
)

const timeout = 5 * time.Second

// macScript reads the title and message from argv, so neither can alter the
// script.
const macScript = "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv)\nend run"

// run starts a command; tests replace it.
var run = func(argv []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Run()
}

// Send shows a notification and reports whether it was shown.
func Send(title, message string) bool {
	argv := command(runtime.GOOS, exec.LookPath, title, message)
	if argv == nil {
		return false
	}
	return run(argv) == nil
}

// command is the argv that shows the notification on goos, or nil when the
// platform has no supported notifier.
func command(goos string, lookPath func(string) (string, error), title, message string) []string {
	switch goos {
	case "darwin":
		if bin, err := lookPath("osascript"); err == nil {
			return []string{bin, "-e", macScript, "--", title, message}
		}
	case "linux":
		if bin, err := lookPath("notify-send"); err == nil {
			return []string{bin, "--app-name", "Pair", title, message}
		}
	}
	return nil
}
