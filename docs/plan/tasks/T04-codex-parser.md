# T04 — Codex rollout parser

**Model:** sonnet · **Wave:** B · **Depends on:** T01 · **Milestone:** M1

## Goal

Turn Codex CLI rollout files (both event generations) into `*model.Session` timelines.

## Read first

- `docs/plan/formats/codex.md` (the mapping spec; follow it exactly)
- `docs/plan/CONTRACTS.md` section 1 and "Harness parsers"
- `internal/gitinfo/gitinfo.go` (`ownerRepoFromURL` exists but is unexported; see below)

## Files you own

- `internal/codex/rollout.go`, `internal/codex/rollout_items.go`, `internal/codex/patch.go` (new)
- `internal/codex/rollout_test.go`, `internal/codex/patch_test.go` (new)
- `internal/codex/testdata/**` (new, synthetic only)

Do not touch `internal/codex/codex.go` (T20 owns it).

## API

```go
func DefaultRoot() string                                  // ~/.codex
func Discover(root string, since time.Time) ([]string, error)
func ParseFile(path string) ([]*model.Session, error)      // always 0 or 1 session
```

- `Discover`: `root/sessions/**/rollout-*.jsonl` and `root/archived_sessions/**/*.jsonl`,
  modtime filter, sorted. Missing dirs → nil.
- `ParseFile`: two passes over the file. Pass 1 records which families have
  newer `item_completed` items. Pass 2 emits events per the format doc.
  Missing `session_meta` → derive ID from the file name (the trailing UUID)
  and CWD from the first `turn_context`. Call `Finalize()`.
- `RepoRemote`: `gitinfo.OwnerRepoFromURL` is added by T10 in wave C. In this
  task, implement a private `ownerRepo(url string) string` in `rollout.go`
  with the same behavior (handles `git@github.com:o/r.git`,
  `https://github.com/o/r(.git)`, `ssh://git@github.com/o/r.git`); T24 may
  switch it to the gitinfo helper.

`patch.go` holds two reusable parsers used by this package only:
- `parseUnifiedHunks(diff string) (hunks []model.Hunk, added, removed []string)`
- `parseApplyPatch(input string) []fileChange` for the `*** Begin Patch` fallback.

## Fixtures (synthetic)

- `testdata/sessions/2026/09/27/rollout-2026-09-27T14-00-00-<uuid-new>.jsonl` —
  newer generation: session_meta with git, world_state agents_md,
  turn_context (approval never, danger-full-access, effort xhigh), a second
  turn_context switching to `on-request`/`workspace-write`, UserMessage,
  AgentMessage, CommandExecution success (argv array), CommandExecution failure
  (exit 1), read-only CommandExecution with parsed_cmd reads, FileChange with
  add + update + delete, McpToolCall, Extension web.search, SubAgentActivity
  started + matching spawn_agent function_call, ContextCompaction + top-level
  `compacted` within 2 s (one compaction expected), `turn_aborted`,
  `thread_rolled_back`, Plan item, update_plan call, request_user_input call +
  output, ImageView. Include one legacy `exec_command_end` for a different
  command: it must be **ignored** because the file has newer command items.
- `testdata/sessions/2026/07/09/rollout-2026-07-09T19-00-00-<uuid-old>.jsonl` —
  older generation only: session_meta without git, user_message,
  agent_message, exec_command_end (exit_code as string), patch_apply_end
  (success true and false), mcp_tool_call_end with `Ok` result, web_search_end,
  sub_agent_activity, context_compacted.
- `testdata/sessions/2026/06/08/rollout-…-<uuid-patch>.jsonl` — only
  `custom_tool_call apply_patch` edits (fallback path) and `exec_command`
  function_calls without end events (command fallback).
- `testdata/archived_sessions/rollout-…-<uuid-arch>.jsonl` — minimal file for Discover.

## Tests

- `TestDiscover` — finds sessions and archived_sessions; mtime filter.
- `TestParse_Meta` — ID, CWD, version, StartSHA, Branch, RepoRemote `acme/shop`, `git_head` event with trigger `transcript_meta`.
- `TestParse_NoMetaGit` — older file: StartSHA empty, no git_head.
- `TestParse_Modes` — two mode_change events; values match; plan mode maps Permission `plan`.
- `TestParse_ModelEffort` — model_change events and `Event.Model` on later commands.
- `TestParse_Instructions` — AGENTS.md instructions once per distinct hash.
- `TestParse_Commands` — argv unwrapping, exit code int and string, status, DurationMs, output truncation; read-only command also yields file_read events.
- `TestParse_FamilyPreference` — legacy exec_command_end ignored when newer items exist.
- `TestParse_FileChange` — add/update/delete ops, hunks from unified_diff, per-change IDs `<item>/<n>`; failed patch_apply_end → Failed.
- `TestParse_ApplyPatchFallback` — Add/Update/Delete/Move sections parsed.
- `TestParse_CommandFallback` — exec_command calls without end events → commands with Status unknown.
- `TestParse_MCPAndWeb` — both generations.
- `TestParse_Subagent` — AgentID/Type from activity, Model from spawn_agent.
- `TestParse_CompactionDedupe` — one compaction with the `compacted` summary.
- `TestParse_InterruptRollback` — interrupt reason `user`; reset rollback.
- `TestParse_PlanTodoQuestion` — plan item text; update_plan → todo Replace; request_user_input question with answer; "unavailable" output skipped.
- `TestParseUnifiedHunks` — multiple hunks, `\ No newline at end of file`, context lines.
- `TestOwnerRepo` — the three URL forms.

## Acceptance

```sh
go test ./internal/codex/...
go vet ./internal/codex/...
```

Optional local smoke check (do not commit output): parse one real rollout
from `~/.codex/sessions` in a throwaway program and confirm no panic.
