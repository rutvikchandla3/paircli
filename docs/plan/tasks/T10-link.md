# T10 — Discovery, selection, linking

**Model:** sonnet · **Wave:** C · **Depends on:** T02 T03 T04 T05 T06 T08 · **Milestone:** M1

## Goal

Find the sessions that belong to a PR: read every harness's transcripts in the
time window, merge hook records, normalize paths, keep candidates for this
repo, and after attribution decide which sessions are linked and how each PR
commit ties to sessions.

## Read first

- `docs/plan/CONTRACTS.md` "internal/link", "internal/gitinfo additions", "Harness parsers", "internal/hooklog"
- `internal/correlate/correlate.go` and its test (legacy algorithm; port the time-overlap logic, do not edit)
- `internal/gitinfo/gitinfo.go`

## Files you own

- `internal/link/*.go` + tests + `testdata/`
- `internal/gitinfo/toplevel.go` + test

## gitinfo additions (`toplevel.go`)

- `TopLevel(dir)`: walk up from `dir` to the first directory containing a
  `.git` entry (file or dir); return that directory. Error if none.
- `OwnerRepoFromURL(url)`: exported wrapper around the existing unexported `ownerRepoFromURL`.

## `WindowFor(pr, cfg)`

`From` = earliest commit time − `cfg.WindowBefore`; `To` = latest commit
time + `cfg.WindowAfter`. No commits → `From = pr.CreatedAt − WindowBefore`,
`To = time.Now()`.

## `Discover(cfg, w, withHooks)`

1. For each harness, root = `cfg.Roots.<harness>` or the package's
   `DefaultRoot()`. `files := Discover(root, w.From)`; `ParseFile` each
   (errors: skip the file, continue).
2. Keep sessions overlapping the window: `End >= w.From && Start <= w.To`.
3. Merge sessions with the same `Harness` and `ID` (resumed Claude Code
   sessions can span files): concatenate events, drop events with a duplicate
   `ID` (keep the first), keep the earliest `SourcePath`, call `Finalize()`.
4. If `withHooks`: hook dir = `cfg.Roots.HookLog` or `hooklog.DefaultDir()`.
   For Claude Code and Codex: `hooklog.Read(dir, h, w.From.Add(-24*time.Hour))`
   and `hooklog.Merge(session, recs[session.ID])`. For Pi:
   `pi.HookRecords(session.SourcePath)` then `hooklog.Merge`.
5. Return sessions sorted by `Start`, then `Ref()`.

Discovery must not read files older than the window (the `since` filter does
this); on a machine with thousands of transcripts it should finish in seconds.

## `Select(pr, sessions, w)`

For each session (mutates in place):

1. **Repo root.** If `CWD` exists on disk: `RepoRoot = TopLevel(CWD)`; if
   `RepoRemote` is empty, `RepoRemote = gitinfo.Resolve(CWD).OwnerRepo`.
   If `CWD` no longer exists (deleted worktree), leave `RepoRoot` empty and
   keep any `RepoRemote` the parser set (Codex).
2. **RelPath** for every `Edit`, `Read`, `External`, `Instructions`,
   `Diagnostics` payload (and `Snapshot` paths are already relative):
   - `abs := Path`; strip a `file://` prefix; if not absolute, join with `CWD`; clean it.
   - `RepoRoot != ""` and `abs` is inside it → `RelPath = filepath.ToSlash(rel)`.
   - `RepoRoot == ""` → the longest PR file path `p` with `strings.HasSuffix(abs, "/"+p)` → `RelPath = p`; none → `""`.
   - otherwise `""` (outside the repo).
   - Codex `MovePath`: leave as recorded.
3. **Candidate** when the session overlaps the window **and** either
   `strings.EqualFold(RepoRemote, pr.Repo)`, or `RepoRemote == ""` and at least
   one non-failed edit has a `RelPath` that is a PR file. Others are dropped.

Return candidates (sorted by `Start`) and the dropped count.

## `Finalize(pr, cands, attr, commitSessions, droppedEarlier)`

PR commit set `C` = SHAs of `pr.Commits`. For each candidate session `s`,
pick the first method that applies:

1. `sha_exact`: a `git_head` event with `Trigger` in `commit`, `prompt`,
   `stop`, `session_end` and `SHA ∈ C`.
2. `sha_ancestor`: `s.StartSHA ∈ C`, or a `git_head` with `Trigger`
   `session_start`/`transcript_meta` and `SHA ∈ C`.
3. `content`: `attr` has at least one line with `Source.Session == s.Ref()`.
4. `heuristic`: `s.Branch == pr.HeadRef` (non-empty) **and** `[s.Start, s.End]`
   overlaps `[commit.Time − 2h, commit.Time + 2h]` for some PR commit.
5. none → dropped (add to `Dropped`).

Commit links, one per PR commit, in PR order:
1. sessions with a `git_head` (`commit`/`prompt`/`stop`/`session_end`) whose SHA
   equals the commit → `Method: sha_exact`, `Confidence: exact`;
2. else `commitSessions[sha]` (only linked sessions) → `content`, `inferred`;
3. else linked sessions matching the heuristic rule for this commit → `heuristic`, `inferred`;
4. else `none`, `unknown`, and the SHA goes to `Unattributed`.

`Sessions` in the result are the linked ones sorted by `Start`;
`SessionLinks` maps each linked ref to its method.

## Tests

Use `testkit` builders plus small on-disk fixtures in `testdata/` (synthetic
transcripts for each harness copied from the parser tasks' formats; a fake
hook log dir; a temp git repo created in the test for `TopLevel`/`Resolve`).

- `TestWindowFor` — with commits and without.
- `TestDiscover_AllHarnesses` — one fixture per harness under temp roots set via `cfg.Roots`; window filtering by overlap; mtime filter honored (old file not parsed — use a fixture that would fail to parse to prove it's skipped).
- `TestDiscover_MergeDuplicateIDs`.
- `TestDiscover_HooksMerged` — Claude Code + Codex via hook log; Pi via custom entries; `Capture` becomes hooked.
- `TestSelect_RepoMatch` — matching remote kept; other repo dropped.
- `TestSelect_RelPath` — absolute inside root; relative (Pi) joined with CWD; outside repo → "".
- `TestSelect_DeletedWorktreeSuffixMatch` — CWD missing, edits mapped by suffix, kept as candidate.
- `TestFinalize_Methods` — one session per method; precedence when several apply; unlinked session dropped.
- `TestFinalize_CommitLinks` — exact, content, heuristic, unattributed.
- `TestTopLevel` and `TestOwnerRepoFromURL`.

## Acceptance

```sh
go test ./internal/link/... ./internal/gitinfo/...
```
