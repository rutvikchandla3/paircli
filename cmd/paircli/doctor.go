package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rutvikchandla3/paircli/internal/claudecode"
	"github.com/rutvikchandla3/paircli/internal/codex"
	"github.com/rutvikchandla3/paircli/internal/pi"
)

type harnessStatus struct {
	name          string
	hookInstalled bool
	pathBNote     string // human-readable Path-B availability
}

// runDoctor reports, per harness, whether Path-A hooks appear installed and
// whether Path-B reconstruction data is available on this machine.
func runDoctor(args []string) error {
	statuses := []harnessStatus{
		claudeCodeStatus(),
		codexStatus(),
		piStatus(),
	}

	fmt.Println("HARNESS      HOOKS (Path A)   RECONSTRUCTION (Path B)")
	for _, s := range statuses {
		installed := "not installed"
		if s.hookInstalled {
			installed = "installed"
		}
		fmt.Printf("%-12s %-16s %s\n", s.name, installed, s.pathBNote)
	}
	return nil
}

func claudeCodeStatus() harnessStatus {
	dir := claudecode.DefaultProjectsDir()
	note := dirAvailabilityNote(dir, "*.jsonl transcripts")
	return harnessStatus{
		name:          "claude-code",
		hookInstalled: claudecode.HooksInstalled(),
		pathBNote:     note,
	}
}

func codexStatus() harnessStatus {
	return harnessStatus{
		name:          "codex",
		hookInstalled: codex.HooksInstalled(),
		pathBNote:     "not yet implemented (docs/ARCHITECTURE.md)",
	}
}

func piStatus() harnessStatus {
	return harnessStatus{
		name:          "pi",
		hookInstalled: pi.HooksInstalled(),
		pathBNote:     "not yet implemented (docs/ARCHITECTURE.md)",
	}
}

// dirAvailabilityNote checks whether dir exists and has any files matching
// *.jsonl one level down (claude-code's ~/.claude/projects/*/*.jsonl layout).
func dirAvailabilityNote(dir, kind string) string {
	if dir == "" {
		return "unavailable (could not resolve home dir)"
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Sprintf("no data (%s not found)", dir)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	if len(matches) == 0 {
		return fmt.Sprintf("no %s found under %s", kind, dir)
	}
	return fmt.Sprintf("available (%d %s under %s)", len(matches), kind, dir)
}
