// Package codex is a stub for v1. See docs/ARCHITECTURE.md's "Codex CLI"
// section for the open questions that block full implementation.
package codex

import (
	"errors"
	"io"

	"github.com/rutvikchandla3/paircli/internal/session"
)

// TODO(codex): Path B needs a real Codex rollout JSONL sample to confirm
// whether session_meta.git contains a usable commit SHA (ARCHITECTURE.md
// flags this as unconfirmed — Beacon reads the field but discards it). Until
// that's inspected, we deliberately return no sessions rather than fake
// data or guess at the schema.
func ScanPathB(rolloutDir string) ([]session.Session, error) {
	return nil, nil
}

// TODO(codex): Path A needs a live capability check against the installed
// Codex version (`codex --help` / its config docs) before wiring hooks —
// ARCHITECTURE.md explicitly says not to assume parity with Claude Code's
// hook system.
func InstallHooks() error {
	println("codex: hook install not yet implemented, tracked in docs/ARCHITECTURE.md")
	return nil
}

// HooksInstalled always reports false until InstallHooks is implemented.
func HooksInstalled() bool { return false }

// UninstallHooks is a stub until T20 implements Codex hooks.
func UninstallHooks() error {
	return errors.New("codex hooks: not implemented yet (T20)")
}

// RunHookEvent is a stub until T20 implements Codex hooks.
func RunHookEvent(event string, stdin io.Reader) error { return nil }
