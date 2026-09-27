# T08 — Hook log + Claude Code hooks v2

**Model:** sonnet · **Wave:** B · **Depends on:** T01 · **Milestone:** M2

## Goal

A shared hook log (write, read, merge into sessions, working-tree snapshots)
and a rewritten Claude Code hook integration that records exact commit SHAs,
turn-boundary snapshots, permission decisions, loaded rule files, cwd changes
and compactions. Also restructure `paircli hook` so Codex (T20) and Pi (T21)
plug in without editing the CLI.

## Read first

- `docs/plan/CONTRACTS.md` "internal/hooklog", "internal/gitinfo additions", `model.HookRecord`
- `internal/claudecode/hooks.go`, `cmd/paircli/hook.go`, `internal/gitinfo/sha.go` (legacy, you replace them)
- Claude Code hooks reference: https://code.claude.com/docs/en/hooks (payload fields per event)

## Files you own

- `internal/hooklog/*.go` (new) + tests + `testdata/`
- `internal/gitinfo/head.go` (new) + test
- `internal/claudecode/hooks.go` (rewrite) + `hooks_test.go`
- `cmd/paircli/hook.go` (rewrite)
- `internal/codex/codex.go`, `internal/pi/pi.go`: **only** add the stub
  functions listed below; do not change existing code there.

## 1. `internal/gitinfo/head.go`

`HeadAndBranch(dir)`: run `git -C dir rev-parse HEAD` and
`git -C dir rev-parse --abbrev-ref HEAD` with a 2-second context timeout each.
Branch `"HEAD"` (detached) → `""`. Errors are returned; callers ignore them.

## 2. `internal/hooklog`

- `DefaultDir()`: `$PAIRCLI_EVENTS_DIR` if set, else `~/.paircli/events`.
- `Append(dir, rec)`: set `rec.V = model.HookRecordVersion` if 0; file
  `dir/<harness>/<rec.TS UTC as 2006-01-02>.jsonl`; `MkdirAll 0o755`; open
  `O_APPEND|O_CREATE|O_WRONLY 0o644`; write the JSON line plus `\n` in a single
  `Write` call.
- `Read(dir, h, since)`: list `dir/<h>/*.jsonl`, skip files whose date is before
  `since`'s UTC date, decode each line (skip malformed), keep `TS >= since`
  (zero since = all), group by `SessionID` (skip empty), sort each group by `TS`
  then `Event`.
- `Snapshot(repoDir)`: find the top level (`git -C repoDir rev-parse --show-toplevel`),
  then `git -C <top> status --porcelain=v1 -z --untracked-files=all` (2 s
  timeout). For each entry with status `M`, `A`, `?`, `R` (use the new path),
  `C`, or `U`, skipping deletions: skip files over 1 MB or containing a NUL byte
  in the first 8 KB; read the file, `model.SplitLines`, hash each line with
  `model.LineHash`. Cap at 200 files (sorted by path). Return
  `[]model.FileLines` with repo-relative `/` paths, sorted by path.
- `Merge(s, recs)` converts records to events with `Origin: hook`, IDs
  `hook-1`, `hook-2`, … in record order, then calls `s.Finalize()`. Set
  `s.Capture = hooked` when at least one record produced an event. Rules
  (one record may produce several events):

| Record | Events |
|---|---|
| any with `HeadSHA != ""` | `git_head{SHA, Branch, Trigger}` |
| any with `Snapshot != nil` | `snapshot{Trigger, Files}` |
| `SessionStart` with `Reason` in `resume`, `clear`, `compact`, `fork` | `reset{Type: Reason}` unless the session already has a transcript `reset` of that type within 10 s |
| `PermissionRequest` | `permission{Tool: ToolName, Decision: "ask", By: "user"}` |
| `PermissionDenied` | `permission{Tool, Decision: "deny", By: "auto"}` |
| `InstructionsLoaded` | `instructions{Path, Reason}` unless a transcript `instructions` event has the same `Path` |
| `CwdChanged` | `cwd_change{From: CwdFrom, To: CWD}` |
| `PreCompact` / `PostCompact` | if a transcript `compaction` exists within ±10 min: set its `Trigger` when empty; else add `compaction{Trigger: Reason}` (only for `PreCompact`) |
| `Interrupt` (Codex) | `interrupt{Reason: "user"}` unless a transcript interrupt exists within 5 s |
| `SessionEnd` / `session_shutdown` (Pi) | `session_end{Reason}` |
| records with only `Error` | nothing |

Snapshot/git_head events keep the record's `TS`. `SessionStart` for a session
with no events sets nothing else.

## 3. Claude Code hooks (`internal/claudecode/hooks.go`)

```go
func SettingsPath() (string, error)       // ~/.claude/settings.json
func InstallHooks() error
func UninstallHooks() error
func HooksInstalled() bool
func RunHookEvent(event string, stdin io.Reader) error
```

**Events and matchers installed** (Claude Code settings shape
`{"hooks":{"<Event>":[{"matcher":"…","hooks":[{"type":"command","command":"…","timeout":10}]}]}}`;
omit `matcher` where none is listed):

| Event | Matcher |
|---|---|
| `SessionStart` | — |
| `UserPromptSubmit` | — |
| `PostToolUse` | `Bash` |
| `PermissionRequest` | — |
| `PermissionDenied` | — |
| `InstructionsLoaded` | — |
| `CwdChanged` | — |
| `PreCompact` | — |
| `Stop` | — |
| `SessionEnd` | — |

Command: `<absolute path of the running paircli binary> hook claude-code <Event>`
(`os.Executable` + `filepath.EvalSymlinks`; quote with `'…'` when the path has spaces).

- `InstallHooks`: read settings (missing → `{}`; invalid JSON → error, change
  nothing). On the first install copy the original to `settings.json.paircli.bak`
  (never overwrite an existing backup). For each event, remove any existing
  hook whose command contains ` hook claude-code ` (older installs,
  other paths), then append ours. Preserve all other keys and hooks. Write
  with 2-space indent to a temp file in the same dir and rename.
- `UninstallHooks`: remove our hooks; drop matcher groups left with no hooks and events left empty.
- `HooksInstalled`: true when every event above has a hook whose command
  contains ` hook claude-code <Event>`.
- `RunHookEvent(event, stdin)`: accept legacy args `session-start`,
  `post-tool-use`, `session-end` (map to the new names). Decode stdin into a
  struct with `session_id, cwd, hook_event_name, source, tool_name,
  tool_input (json.RawMessage), tool_use_id, file_path, load_reason,
  previous_cwd, compact_reason, trigger, end_reason, reason`. Build a
  `HookRecord{Harness: claude-code, Event, SessionID, TS: time.Now().UTC(), CWD}` and
  (in the table, `\|` is an escaped `|`):

| Event | Extra fields |
|---|---|
| `SessionStart` | HeadSHA, Branch, `Trigger:"session_start"`, `Reason: source` |
| `UserPromptSubmit` | HeadSHA, Branch, `Trigger:"prompt"`, `Snapshot` — **never** store the prompt text |
| `PostToolUse` | only when `tool_input.command` matches `\bgit\s+(commit\|merge\|rebase\|cherry-pick\|am\|revert\|reset\|checkout\|switch\|pull\|stash)\b`: HeadSHA, Branch, `Trigger:"commit"`, `Command` (first 300 chars), ToolName, ToolUseID. Otherwise write nothing. |
| `PermissionRequest` / `PermissionDenied` | ToolName, ToolUseID |
| `InstructionsLoaded` | `Path: file_path`, `Reason: load_reason` |
| `CwdChanged` | `CwdFrom: previous_cwd` |
| `PreCompact` | `Reason`: first non-empty of `compact_reason`, `trigger` |
| `Stop` | HeadSHA, Branch, `Trigger:"stop"`, `Snapshot` |
| `SessionEnd` | HeadSHA, Branch, `Trigger:"session_end"`, `Reason`: first of `end_reason`, `reason` |

  Git failures go into `rec.Error`; still append the record. **Print nothing
  to stdout.** Any error (bad stdin, unwritable log) is appended to
  `~/.paircli/hook-errors.log` with a timestamp, and the function returns nil.
  Budget: under 300 ms typical; only call git for events that need it.

## 4. `cmd/paircli/hook.go`

```
paircli hook install <harness>
paircli hook uninstall <harness>
paircli hook <harness> <Event>
```

A table maps `claude-code`, `codex`, `pi` to `{Install, Uninstall func() error;
Installed func() bool; Run func(string, io.Reader) error}`. The `<harness>
<Event>` path must never exit non-zero: log errors as above and return nil.
`install` prints one line saying what was written (plus any extra message the
harness package returns via an optional `InstallMessage() string`).

Add stubs so this compiles (T20/T21 replace them):
- `internal/codex/codex.go`: `func UninstallHooks() error { return errors.New("codex hooks: not implemented yet (T20)") }`,
  `func RunHookEvent(event string, stdin io.Reader) error { return nil }`.
- `internal/pi/pi.go`: same two functions (message mentions T21).

## Tests

- `hooklog`: `TestAppendRead_RoundTrip` (two harnesses, two days, since filter,
  malformed line skipped, grouping and order); `TestMerge_Conversions` (one
  record of each kind → expected events and IDs); `TestMerge_Dedupe`
  (compaction within 10 min updates Trigger; interrupt within 5 s skipped;
  instructions with same path skipped); `TestMerge_SetsCaptureAndFinalizes`;
  `TestSnapshot` (temp git repo: modified, added, untracked, deleted, binary,
  >1 MB files; skip the test if `git` is not on PATH).
- `claudecode`: with `t.Setenv("HOME", tmp)`: `TestInstall_Fresh`,
  `TestInstall_PreservesOtherHooksAndKeys`, `TestInstall_Idempotent` (twice →
  one hook per event), `TestInstall_ReplacesLegacyCommand`, `TestInstall_Backup`,
  `TestInstall_InvalidJSON`, `TestUninstall`, `TestHooksInstalled`.
  `TestRunHookEvent_*` with `PAIRCLI_EVENTS_DIR` set to a temp dir and a temp
  git repo as `cwd`: SessionStart (sha + reason), UserPromptSubmit (snapshot,
  no prompt text in the file), PostToolUse git commit (trigger commit) vs
  `ls` (no record), PermissionDenied, InstructionsLoaded, SessionEnd with
  `reason` fallback, legacy arg `session-end`, garbage stdin (returns nil,
  error logged, stdout empty — capture `os.Stdout` with a pipe).
- `gitinfo`: `TestHeadAndBranch` (temp repo; detached HEAD → empty branch).

## Acceptance

```sh
go test ./internal/hooklog/... ./internal/claudecode/... ./internal/gitinfo/...
go build ./cmd/paircli && HOME=$(mktemp -d) ./paircli hook install claude-code && cat "$HOME/.claude/settings.json"
```
