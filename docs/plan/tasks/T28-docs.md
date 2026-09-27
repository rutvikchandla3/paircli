# T28 — Docs refresh

**Model:** haiku · **Wave:** E · **Depends on:** T24 T25 T26 T27 · **Milestone:** Docs

## Goal

Make the user-facing docs match the shipped v2 behavior.

## Read first

- `docs/SIGNALS.md`, `docs/plan/README.md` "Architecture", `cmd/paircli/*.go` (flags as implemented)
- `internal/e2e/testdata/golden/report.md` and `comment.md` (real examples to quote)

## Files you own

- `README.md`, `docs/ARCHITECTURE.md`
- `docs/SIGNALS.md` — only fix links or statements that no longer match the code; do not change signal definitions

## README.md

Sections, in order:
1. One paragraph: what paircli does (signals from Claude Code, Codex and Pi sessions for PR review) and what it does not (it doesn't review code or score people).
2. Install (build from source; `go install …@latest` once tagged).
3. Quick start: `paircli hook install claude-code|codex|pi` (with the Codex trust note), `paircli scan 482`, `paircli scan 482 --post`, `paircli doctor`.
4. What you get: the output folder table, a short excerpt of the golden `comment.md` and `report.md`.
5. Signals: a compact table (ID, question, signal) linking to `docs/SIGNALS.md`.
6. Privacy: what is read (local transcripts), what is written where (`.paircli/`,
   `~/.paircli/events/`), that redaction is not implemented yet, that
   `--post` never includes prompt text unless `comment.include_prompts` is
   set, and that `--llm` sends a fact bundle (prompts, diffs, summaries) to the chosen provider.
7. Configuration: `.paircli.json` keys with defaults.
8. Development: `go test ./...`, the golden `-update` flag, pointer to `docs/plan/`.

## ARCHITECTURE.md

Rewrite for v2: the pipeline (discover → select → attribute → finalize links
→ detectors → optional judge → render), capture paths (transcripts vs hooks
vs Pi extension), per-harness notes (Codex records the starting SHA in
`session_meta.git`; Claude Code and Pi need hooks for SHAs), correlation and
attribution rules in brief, and where each piece lives. Remove statements the
v1 doc marked as unconfirmed that are now settled.

## Acceptance

- Every command and flag mentioned exists (`paircli --help`, `paircli scan --help`).
- Every relative link resolves (check with a quick script or by hand).
- No real user data, usernames or home paths in examples.
