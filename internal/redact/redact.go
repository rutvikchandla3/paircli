// Package redact is the single seam for scrubbing sensitive text before it is
// written to report files or posted to GitHub.
//
// Redaction is deferred (docs/plan/README.md, "Out of scope"). Text is a
// no-op today. Every renderer and every Evidence excerpt must still call it,
// so turning redaction on later is a change to this file only.
package redact

// Text returns s with sensitive content removed. Currently a no-op.
func Text(s string) string { return s }
