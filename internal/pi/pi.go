// Package pi is a stub for v1. See docs/ARCHITECTURE.md's "Pi" section for
// the open questions that block full implementation.
package pi

import (
	"github.com/rutvikchandla3/paircli/internal/session"
)

// TODO(pi): Path B needs the exact local session store path/format Pi's
// extension writes confirmed at implementation time — ARCHITECTURE.md notes
// this has "not yet [been] inspected". Returning no sessions rather than
// guessing at an unconfirmed format.
func ScanPathB(sessionStoreDir string) ([]session.Session, error) {
	return nil, nil
}
