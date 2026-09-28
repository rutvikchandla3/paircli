// Package codex parses Codex CLI rollout files (see rollout.go, T04) and
// manages paircli's Codex hook integration (see hooks.go, T20).
package codex

import (
	"github.com/rutvikchandla3/paircli/internal/session"
)

// TODO(codex): Path B needs a real Codex rollout JSONL sample to confirm
// whether session_meta.git contains a usable commit SHA (ARCHITECTURE.md
// flags this as unconfirmed — Beacon reads the field but discards it). Until
// that's inspected, we deliberately return no sessions rather than fake
// data or guess at the schema.
//
// TODO(T24): superseded by rollout.go's ParseFile; a later task deletes this.
func ScanPathB(rolloutDir string) ([]session.Session, error) {
	return nil, nil
}
