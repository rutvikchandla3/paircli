package engine

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Clock formats t as a 24h UTC clock time, e.g. "14:02 UTC".
func Clock(t time.Time) string {
	return t.UTC().Format("15:04") + " UTC"
}

// Plural formats n with one when n == 1, otherwise many, e.g. Plural(1, "edit", "edits") == "1 edit".
func Plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Pct formats part/whole as a rounded percentage string, e.g. "45%".
// It returns "0%" when whole is 0.
func Pct(part, whole int) string {
	if whole == 0 {
		return "0%"
	}
	p := float64(part) / float64(whole) * 100
	return fmt.Sprintf("%.0f%%", p)
}

// Home replaces the user's home directory prefix in path with "~", so
// reports never contain "/Users/<name>/". Paths outside the home directory,
// or when it cannot be resolved, are returned unchanged.
func Home(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	sep := string(os.PathSeparator)
	if strings.HasPrefix(path, home+sep) {
		return "~" + path[len(home):]
	}
	return path
}
