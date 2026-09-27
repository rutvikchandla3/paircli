# Pi session format → model events

Checked against Pi 0.87.1 sessions on a real machine and Pi's own
`docs/session-format.md` (installed at
`$(npm root -g)/@earendil-works/pi-coding-agent/docs/session-format.md`).
Examples are **synthetic**.

## Files

```
~/.pi/agent/sessions/--<cwd with / replaced by ->--/<timestamp>_<uuid>.jsonl
```

Some tools nest further directories (for example `…/<run>/run-1/session.jsonl`);
discover recursively. Session versions 1–3 exist; Pi migrates on load, but
files on disk may still be older versions.

## Header (first line, no id/parentId)

```json
{"type":"session","version":3,"id":"8f0c2c7e-0000-4000-8000-000000000003","timestamp":"2026-09-27T14:00:00.000Z","cwd":"/work/shop","parentSession":"/home/u/.pi/agent/sessions/--work-shop--/2026-09-26T10-00-00-000Z_1111.jsonl"}
```

`ID=id`, `CWD=cwd`. `parentSession` (optional, set by fork/clone) → `ParentID`
= the uuid part of that file name (text after the last `_`, without `.jsonl`).
Pi records no git data: `StartSHA`, `Branch`, `RepoRemote` stay empty unless
the paircli extension wrote hook records.

## Tree and active branch

Every other entry has `id`, `parentId`, `timestamp` (ISO). Entries form a
tree. The **leaf** is the last entry in file order that has an `id`. The
**active path** is leaf → parentId → … → root. Mark every event from an entry
not on the active path with `OffBranch:true`. Keep those events: file edits
made on an abandoned branch still happened on disk. Version 1 files have no
ids: treat file order as the active path.

## Entries

### `message`

`message.role` decides:

**user**
```json
{"type":"message","id":"a1b2c3d4","parentId":"00000001","timestamp":"2026-09-27T14:00:05.000Z","message":{"role":"user","content":[{"type":"text","text":"Add retry with backoff to the webhook sender."}],"timestamp":1790517605000}}
```
→ `prompt{Text}`; content may be a string or an array of `text`/`image`
blocks (`Images` = image count).

**assistant**
```json
{"type":"message","id":"b2c3d4e5","parentId":"a1b2c3d4","timestamp":"2026-09-27T14:00:09.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"…"},{"type":"text","text":"Adding the retry loop."},{"type":"toolCall","id":"call_1","name":"edit","arguments":{"path":"src/webhooks/retry.ts","edits":[{"oldText":"  return post(url, body);","newText":"  return sendWithRetry(url, body);"}]}}],"provider":"anthropic","model":"claude-opus-5-5","usage":{"input":900,"output":80,"cacheRead":20000,"cacheWrite":0,"totalTokens":20980,"cost":{"total":0.0412}},"stopReason":"toolUse"}}
```
- `text` blocks → `assistant_message` (usage incl. `CostUSD=usage.cost.total` on the first).
- `toolCall` blocks → pending by `id` with `name`, `arguments`, model.
- `stopReason:"aborted"` → `interrupt{Reason:"aborted"}`.
- `stopReason:"error"` → `failure{Type:"api_error", Message:errorMessage}`.
- `stopReason:"length"` → `failure{Type:"length"}`.
- Emit `model_change` when `model` differs from the current one.

**toolResult** (completes the pending call with the same `toolCallId`)
```json
{"type":"message","id":"c3d4e5f6","parentId":"b2c3d4e5","timestamp":"2026-09-27T14:00:10.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"edit","content":[{"type":"text","text":"Successfully replaced 1 block(s) in src/webhooks/retry.ts."}],"details":{"diff":"  40   const max = 5;\n- 41   return post(url, body);\n+ 41   return sendWithRetry(url, body);\n  42 }"},"isError":false}}
```

| toolName | Event | Mapping |
|---|---|---|
| `bash` | `command` | `Cmd=arguments.command`; `Status = failed if isError else ok`; `ExitCode`: parse a trailing `exit code (\d+)` / `exited with code (\d+)` from the output if present, else 0 on success and nil on error; `Output` = text content, truncated. |
| `edit` | `file_edit` | `Path=arguments.path`, `Op=update`, `Via="edit"`. From `details.diff`: lines matching `^\+\s*\d+\s` are added, `^-\s*\d+\s` removed; strip that prefix (sign, spaces, line number, one space). If `details.diff` is absent: `Added`/`Removed` from `arguments.edits[].newText/oldText` (or top-level `newText/oldText`), line-diffed as in the Claude Code Edit fallback. `isError` → `Failed=true`. |
| `write` | `file_edit` | `Path=arguments.path`, `Op=create`, `Via="write"`, `Added=SplitLines(arguments.content)`. |
| `read` | `file_read` | `Path=arguments.path`. |
| `web_search` | `lookup` | `Kind="search"`, `Query` = `arguments.query` or `arguments.queries` joined with " | ". |
| `fetch_content` | `lookup` | `Kind="fetch"`, `URLs` = `arguments.urls` or `[arguments.url]`. |
| `subagent` | `subagent` | `Type` = `arguments.agent` or the first `arguments.tasks[].agent`; `Prompt=Clip(task,500)`; `Status` = "error" if isError. Ignore calls whose `arguments.action` is `list`/`status`. |
| name contains `mcp` | `mcp_call` | `Server` = text before the first `_` after `mcp`, best effort; `Tool` = the rest. |
| other | `tool_call` | |

Paths in Pi arguments are often **relative to the session cwd**; keep them
as recorded. `internal/link` resolves them.

**bashExecution** (human ran `!cmd`)
```json
{"type":"message","id":"d4e5f6a7","parentId":"c3d4e5f6","timestamp":"2026-09-27T14:05:00.000Z","message":{"role":"bashExecution","command":"git status","output":"On branch feat/retry","exitCode":0,"cancelled":false,"truncated":false}}
```
→ `command{ByUser:true, Cmd, ExitCode, Status: interrupted if cancelled, failed if exitCode≠0, else ok}`.

**system** → for every `<project_instructions path="…">` in
`message.sections.project_context`, emit `instructions{Path, Scope:"project"}`
(content between the tags, clipped). Other roles (`custom`, `hookMessage`) → ignore.

### Other entry types

| type | Event |
|---|---|
| `model_change` (`provider`, `modelId`) | `model_change{Model:modelId, Provider}` |
| `thinking_level_change` (`thinkingLevel`) | `model_change{Model:<current>, Effort:thinkingLevel}` |
| `compaction` (`summary`, `fromHook`) | `compaction{Trigger: "extension" if fromHook else "auto", Summary}` |
| `branch_summary` (`summary`, `fromId`) | `reset{Type:"branch_switch", Summary}` |
| `session_info` (`name`) | sets `Session.Title` (last one wins) |
| `custom` with `customType:"paircli"` | not an event: returned by `HookRecords` (see below) |
| `custom_message` with `customType:"answers"` | `question{Tool:"answers", Items:[{Question:"", Answer:content}]}` |
| `usage`, `label`, `context_edit`, other `custom`/`custom_message` | ignore |

## Hook records written by the paircli extension

The T21 extension calls `pi.appendEntry("paircli", record)` where `record`
is a `model.HookRecord` JSON object with `harness:"pi"` and
`session_id` = header id:

```json
{"type":"custom","id":"e5f6a7b8","parentId":"d4e5f6a7","timestamp":"2026-09-27T14:06:00.000Z","customType":"paircli","data":{"v":1,"harness":"pi","event":"tool_execution_end","session_id":"8f0c2c7e-0000-4000-8000-000000000003","ts":"2026-09-27T14:06:00.000Z","cwd":"/work/shop","head_sha":"9c1e0d2f9c1e0d2f9c1e0d2f9c1e0d2f9c1e0d2f","branch":"feat/retry","trigger":"commit","command":"git commit -m \"retry\""}}
```

`pi.HookRecords(path)` returns the `data` objects decoded as
`model.HookRecord` (fill `SessionID` from the header when empty). `ParseFile`
does not turn them into events; `internal/link` passes them to `hooklog.Merge`.
