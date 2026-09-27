# T05 — Pi session parser

**Model:** sonnet · **Wave:** B · **Depends on:** T01 · **Milestone:** M1

## Goal

Turn Pi session files (a tree of entries) into `*model.Session` timelines,
flag abandoned branches, and expose paircli hook records stored by the Pi extension.

## Read first

- `docs/plan/formats/pi.md` (the mapping spec; follow it exactly)
- `docs/plan/CONTRACTS.md` section 1 and "Harness parsers"
- Optional: Pi's own `docs/session-format.md` (path in the format doc) if installed locally

## Files you own

- `internal/pi/session.go`, `internal/pi/session_tools.go`, `internal/pi/tree.go` (new)
- `internal/pi/session_test.go`, `internal/pi/tree_test.go` (new)
- `internal/pi/testdata/**` (new, synthetic only)

Do not touch `internal/pi/pi.go` (T21 owns it).

## API

```go
func DefaultRoot() string                                  // ~/.pi/agent/sessions
func Discover(root string, since time.Time) ([]string, error)
func ParseFile(path string) ([]*model.Session, error)      // 0 or 1 session
func HookRecords(path string) ([]model.HookRecord, error)
```

- `Discover`: every `*.jsonl` under root recursively, modtime filter, sorted.
- `ParseFile`:
  1. Read all lines; the first valid line with `type:"session"` is the header.
     No header → return nil, nil.
  2. Build the tree (`tree.go`): `activePath(entries) map[string]bool`.
     Leaf = last entry in file order with a non-empty `id`. Walk `parentId`
     to the root; stop on cycles or missing parents. Version 1 (no ids) → every entry active.
  3. Walk entries in file order and emit events per the format doc; set
     `OffBranch = !active[entry.id]` on every event from that entry.
  4. Pending tool calls are keyed by tool-call id; results complete them.
     Unresolved calls are emitted at the assistant entry's timestamp with
     unknown status.
  5. `Event.ID`: entry id; when one entry yields several events, `<id>/<n>`;
     tool events use the tool-call id.
  6. Set `Capture = reconstructed` (T10 upgrades it when hook records merge), call `Finalize()`.
- `HookRecords`: decode `data` of every `custom` entry with `customType:"paircli"`
  into `model.HookRecord`; fill empty `SessionID` from the header id and empty
  `Harness` with `pi`; parse `TS` from `data.ts`, falling back to the entry timestamp.

## Fixtures (synthetic)

- `testdata/--work-shop--/2026-09-27T14-00-00-000Z_<uuid-a>.jsonl` (v3):
  header with `parentSession`; system message with two `<project_instructions path=…>`;
  model_change; thinking_level_change; user prompt (string content); user
  prompt (array content + image); assistant with text + edit toolCall;
  toolResult edit with `details.diff`; toolResult edit **without** details
  (fallback from `edits[]`); write; read; bash success; bash error with
  "exited with code 3"; web_search; fetch_content; subagent (+ one `action:list` call ignored);
  an MCP-named tool; bashExecution (human `!git status`); assistant
  `stopReason:"aborted"`; assistant `stopReason:"error"` with errorMessage;
  compaction; session_info name; `custom_message` answers; two `custom`
  entries with `customType:"paircli"` (one commit record with head_sha, one
  stop record with a snapshot); a **branch**: after some entries, a
  `branch_summary` whose `parentId` is an earlier entry, followed by new
  entries — the entries between the branch point and the old leaf must be
  OffBranch, including one file edit.
- `testdata/--work-shop--/nested/run-1/session.jsonl`: minimal v3 file (Discover recursion).
- `testdata/--legacy--/v1.jsonl`: version-1 file without ids.

## Tests

- `TestDiscover_Recursive`
- `TestParse_Header` — ID, CWD, ParentID from `parentSession` file name, Title from session_info.
- `TestActivePath` — branch fixture: expected active set; cycle-safe.
- `TestParse_OffBranch` — edit on the abandoned branch has OffBranch=true; active edits false.
- `TestParse_EditDiff` — Added/Removed from `details.diff` with prefixes stripped.
- `TestParse_EditFallback` — from `edits[]`.
- `TestParse_WriteReadBash` — including exit code parsed from error output.
- `TestParse_UserBash` — ByUser command, exit code, cancelled → interrupted.
- `TestParse_Lookups` — search queries joined; fetch URLs.
- `TestParse_Subagent` — list action ignored.
- `TestParse_StopReasons` — aborted → interrupt; error → failure with message.
- `TestParse_ModelChanges` — provider/model; thinking level carries current model.
- `TestParse_Instructions` — two instruction events from the system message.
- `TestParse_Usage` — CostUSD on the first message of an entry.
- `TestParse_V1` — no ids, all events active, no panic.
- `TestHookRecords` — two records decoded, SessionID filled, TS parsed; `ParseFile` does not emit them as events.

## Acceptance

```sh
go test ./internal/pi/...
go vet ./internal/pi/...
```
