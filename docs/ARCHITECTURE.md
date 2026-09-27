# Architecture

## Two capture paths

**Path A — hooks installed.** paircli ships a hook/plugin per harness that
runs *during* the session and writes a normalized event to a local event log
(`~/.paircli/events/<harness>/<date>.jsonl`), including a `git rev-parse HEAD`
snapshot taken at session-end and after every commit-producing shell command.
This is the only way to get exact SHA linkage for harnesses whose own
transcripts don't record it (confirmed true for Claude Code — see below).

**Path B — nothing installed, reconstruct from local transcripts.** paircli
reads each harness's own on-disk session files directly and normalizes them
into the same schema Path A produces, just with lower-confidence fields where
the harness never recorded them. This is the fallback, always available,
requires no prior setup, and is what `paircli scan <pr>` runs by default if it
finds no hook event log.

Both paths converge on the same normalized `Session` schema before
correlation runs, so the correlator never needs to know which path produced a
given session record.

## Why not depend on agent-beacon

We cloned and read agent-beacon's source directly (not just its docs) before
deciding this. Findings that shaped the design:

- Its `git.go` only ever extracts **branch + remote**, parsed straight from
  `.git/HEAD` and `.git/config` (no shellout, worktree-aware via `commondir`).
  It **never captures a commit SHA** anywhere in the codebase.
- Its Claude Code, Codex, and Pi integrations are all **poll-and-parse-local-
  files**, not live push hooks feeding structured events — despite the docs
  table implying otherwise for some rows.
- Claude Code's own transcript format (`~/.claude/projects/*/*.jsonl`) has no
  commit SHA field at all. Beacon's own code comment says as much.
- Codex's rollout file has a raw `Git map[string]interface{}` blob in
  `session_meta` that Beacon reads but never surfaces a commit hash from —
  worth checking directly (see Codex section) since the field may contain one.
- **Beacon has no PR-correlation algorithm at all.** Its only "which commit is
  this" logic (`ci/session.go`) reads `GITHUB_SHA` from CI env vars during a
  live Actions run — it never matches a *historical* local session to a PR
  after the fact. This is the actual novel part of paircli; nothing to import.

Conclusion: their local-file parsing code (`git.go`'s HEAD/config parsing
approach, and the general poll-local-files pattern for Claude Code sessions)
is reusable *technique*, not a dependency. The correlation engine, the SHA
hook, and the structured signal output are ours to build from scratch.

## PR ↔ session correlation algorithm

Input: a PR (via `gh pr view <n> --json commits,files,createdAt,headRefName`).

1. **Exact SHA match.** For every commit SHA in the PR, look for a session
   record (from either capture path) that recorded that exact SHA (via our
   hook's `git rev-parse HEAD` snapshot, or a harness transcript that happens
   to embed one — confirm Codex's `Git` blob). Match confidence: `exact`.
2. **Heuristic fallback**, for commits/sessions with no SHA available
   (this is the *only* path for Claude Code today): score each candidate
   session against each unmatched commit by:
   - repo identity match (remote URL or local path résolved to same repo) —
     required, not just scored;
   - branch name match against the commit's branch/PR head ref — strong
     positive signal, not required (agents/humans rebase, rename branches);
   - time-window overlap: session `[start,end]` intersects
     `[commit.timestamp - N, commit.timestamp + N]`, N configurable
     (default 2h to absorb longer edit/test loops before a commit lands).
   Sessions above a score threshold are attached with confidence `inferred`;
   below it, left unmatched.
3. Anything left over becomes `unattributed_commits` in the output — always
   surfaced, never silently dropped.

SHA-first-then-heuristic-fallback, as scoped with the user; there was no
existing algorithm to adopt from agent-beacon for this step.

## Per-harness capture notes (v1 scope: Claude Code, Codex CLI, Pi)

### Claude Code
- Path A (hooks): register `SessionStart`, `PostToolUse`, `SessionEnd` hooks
  in `settings.json` that shell out to `paircli hook claude-code <event>`,
  capturing `git rev-parse HEAD` at session end and after any tool call whose
  command looks like `git commit`.
- Path B (reconstruct): parse `~/.claude/projects/*/*.jsonl` directly —
  fields available per Beacon's confirmed schema: `sessionId`, `cwd`,
  `gitBranch`, `timestamp`, `version`, message/tool content. No SHA field
  exists in these files, so Path B for Claude Code is **always** heuristic-
  only unless our hook also ran during that session.

### Codex CLI
- Path A (hooks): Codex's hook surface needs a live capability check against
  the installed Codex version (`codex --help`, and its config docs) before
  wiring — do not assume parity with Claude Code's hook system.
- Path B (reconstruct): parse Codex's rollout JSONL
  (`session_meta`/`turn_context`/`response_item`/`event_msg` entries per
  Beacon's schema). **Action item before implementation**: inspect a real
  Codex session file's `session_meta.git` map directly — Beacon reads it but
  discards it, so it's unconfirmed whether it contains a commit SHA. If it
  does, Codex may get `exact` confidence for free even in Path B.

### Pi
- Path A (extension): Pi is extension + poll per its own model; hook into
  its session lifecycle events (`session_start`/`session_shutdown` style
  envelope) the same way Beacon's `pi_event` command does, adding our own
  `git rev-parse HEAD` capture at each envelope.
- Path B (reconstruct): parse whatever local session store the Pi extension
  writes (confirm exact path/format at implementation time — not yet
  inspected). No git correlation confirmed available without Path A.

## Output: the structured signal folder

```
.paircli/pr-<number>/
  report.md              # human-readable summary, the primary artifact
  signals.json            # full structured signal set, see SIGNALS.md
  sessions/
    <harness>-<session_id>.json   # normalized session record
  raw/                     # optional: pointers (not copies) to source
                            # transcript paths on disk, for drill-in
```

`signals.json` is the machine-readable contract other tooling (a PR-review
UI, a bot comment, CI check) can build on; `report.md` is what a human reads
first. Every session record and every top-level signal carries the
`confidence` field defined in SIGNALS.md.

## CLI shape (v1)

```
paircli scan <pr-number-or-url>   # run both capture paths as needed, correlate, write output folder
paircli hook install <harness>    # install Path-A hooks for one harness
paircli hook <harness> <event>    # internal: invoked BY the installed hook
paircli doctor                    # report which harnesses have hooks installed / are reconstructable on this machine
```
