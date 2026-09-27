# paircli

paircli aggregates PR-review "process signals" — which AI coding harnesses
were used, how many sessions, how much back-and-forth, corrections, token
usage, and more — across Claude Code, Codex CLI, and Pi sessions, then
correlates those sessions to a given PR's commits and writes a structured
output folder a reviewer can read in seconds. The goal is to surface *how* a
PR was produced (process), not to re-review the diff itself (verdict).

Correlation runs SHA-exact match first: paircli's own installed hooks
capture `git rev-parse HEAD` at session-end, giving exact linkage between a
session and the commit it produced. Where that isn't available — either
because no hook was installed, or because a harness's own transcript format
never records a commit SHA at all (true for Claude Code, confirmed against
its transcript schema) — paircli falls back to a repo+branch+time-window
heuristic and flags the result as `inferred` rather than `exact`. Commits
that match nothing at all are always surfaced as `unattributed_commits`,
never silently dropped.

See [`docs/SIGNALS.md`](docs/SIGNALS.md) for the full signal taxonomy and
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the two capture paths, the
correlation algorithm, and per-harness capture notes. v1 fully implements
Claude Code (both capture paths); Codex CLI and Pi are stubbed pending open
questions documented in ARCHITECTURE.md.

## Install

The repo isn't tagged yet, so `go install` against a released version isn't
available. Build from source instead:

```sh
git clone https://github.com/rutvikchandla3/paircli
cd paircli
go build ./cmd/paircli
./paircli --help
```

Once a release is tagged:

```sh
go install github.com/rutvikchandla3/paircli/cmd/paircli@latest
```

## Usage

```sh
# Scan a PR: gathers sessions from all harnesses, correlates them against
# the PR's commits, and writes .paircli/pr-<number>/ in the current repo.
paircli scan 123
paircli scan https://github.com/owner/repo/pull/123

# Install Path-A hooks for a harness, so future sessions get exact SHA
# linkage instead of relying on heuristic reconstruction.
paircli hook install claude-code

# Internal: invoked BY the installed hook itself, not run directly by users.
paircli hook claude-code session-end

# Report which harnesses have hooks installed and which have local session
# data available for heuristic reconstruction.
paircli doctor
```

`paircli scan` writes:

```
.paircli/pr-<number>/
  report.md              # human-readable summary — read this first
  signals.json            # full structured signal set, see docs/SIGNALS.md
  sessions/<harness>-<session_id>.json
```
