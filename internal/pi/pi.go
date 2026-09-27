// Package pi is a stub for v1. See docs/ARCHITECTURE.md's "Pi" section for
// the open questions that block full implementation.
package pi

import (
	"errors"
	"io"

	"github.com/rutvikchandla3/paircli/internal/session"
)

// TODO(pi): Path B needs the exact local session store path/format Pi's
// extension writes confirmed at implementation time — ARCHITECTURE.md notes
// this has "not yet [been] inspected". Returning no sessions rather than
// guessing at an unconfirmed format.
func ScanPathB(sessionStoreDir string) ([]session.Session, error) {
	return nil, nil
}

// TODO(pi): Path A should hook Pi's session lifecycle envelope
// (session_start/session_shutdown per ARCHITECTURE.md, mirroring Beacon's
// pi_event command) and add our own `git rev-parse HEAD` capture there.
func InstallHooks() error {
	println("pi: hook install not yet implemented, tracked in docs/ARCHITECTURE.md")
	return nil
}

// HooksInstalled always reports false until InstallHooks is implemented.
func HooksInstalled() bool { return false }

// UninstallHooks is a stub until T21 implements the Pi extension.
func UninstallHooks() error {
	return errors.New("pi hooks: not implemented yet (T21)")
}

// RunHookEvent is a stub until T21 implements the Pi extension.
func RunHookEvent(event string, stdin io.Reader) error { return nil }
