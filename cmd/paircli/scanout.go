package main

import (
	"fmt"
	"io"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/state"
)

// legacyScanNote is the one line `scan` and `publish` print when a repository
// still holds a scan folder an earlier paircli wrote there.
//
// Output written before scans moved to ~/.paircli/scans is left exactly where
// it is — it is the user's evidence, and removing a directory under their repo
// root is not paircli's decision to make — so the note exists only to say where
// it is and where new scans go.
//
// The candidate directories are tried in order and the first that holds a scan
// folder wins. Callers pass the working directory first, because RepoRoot
// returns "" for a checkout whose origin remote does not match the PR — exactly
// when a stale folder is most likely to be lying around — and the resolved
// repository root second, because a scan run from a subdirectory would
// otherwise miss a folder one level up.
//
// The returned string is empty when there is nothing to report.
func legacyScanNote(dirs ...string) string {
	for _, dir := range dirs {
		legacy := state.LegacyScanDir(dir)
		if legacy == "" {
			continue
		}
		where := engine.Home(state.ScanRoot())
		if where == "" {
			// No home directory to name. The scan itself will refuse to run
			// without one, so saying where the new output goes is not worth a
			// half-sentence.
			return fmt.Sprintf("%s holds scan output from an earlier paircli, which is left untouched",
				engine.Home(legacy))
		}
		return fmt.Sprintf("%s holds scan output from an earlier paircli; new scans write to %s and that folder is left untouched",
			engine.Home(legacy), where)
	}
	return ""
}

// printLegacyScanNote writes the note to w when one of dirs holds legacy scan
// output. It goes to stderr so a --json run's stdout stays the one JSON
// document.
func printLegacyScanNote(w io.Writer, dirs ...string) {
	if note := legacyScanNote(dirs...); note != "" {
		fmt.Fprintln(w, "paircli: "+note)
	}
}
