# Claude Code transcript format → model events

Checked against Claude Code 2.1.283 transcripts on a real machine (Sep 2026).
All examples below are **synthetic**: real structure, invented content.
The format is undocumented and changes between releases, so parse
defensively: unknown `type` values and unknown fields are ignored, never errors.

## Files

```
~/.claude/projects/<cwd-slug>/<sessionId>.jsonl                 main transcript
~/.claude/projects/<cwd-slug>/<sessionId>/subagents/agent-<agentId>.jsonl
~/.claude/projects/<cwd-slug>/<sessionId>/subagents/agent-<agentId>.meta.json
```

`<cwd-slug>` is the session cwd with `/` and `.` replaced by `-`. One JSON
object per line. Lines can exceed 10 MB (base64 images); use a
`bufio.Reader` with `ReadBytes('\n')` or a Scanner with a 64 MB buffer.

## Common entry fields

Most entries carry: `type`, `uuid`, `parentUuid`, `sessionId`, `timestamp`
(RFC3339 with ms, UTC), `cwd`, `gitBranch`, `version`, `isSidechain`,
`userType`, `entrypoint`. Group entries by `sessionId`. **Deduplicate by
`uuid`**: a resumed session may repeat earlier entries.

Session fields: `ID` = sessionId; `CWD` = last non-empty `cwd`; `Branch` =
last non-empty `gitBranch`; `HarnessVersion` = last `version`; `Title` =
`aiTitle` of the last `type:"ai-title"` entry; `Capture` = reconstructed.

## Entry types and mapping

### `type: "user"`, `message.content` is a string

```json
{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-27T14:00:05.120Z","cwd":"/work/shop","gitBranch":"feat/retry","promptSource":"typed","turnOrigin":"human","origin":{"kind":"human"},"promptId":"p1","message":{"role":"user","content":"Add retry with backoff to the webhook sender. Don't touch the queue."}}
```

Decide in this order:

1. `isCompactSummary: true` → not a prompt. Store `content` as the summary of
   the nearest preceding `compaction` event in the same session (if none,
   emit a `compaction` event with `Trigger:""` and that summary).
2. Content contains `<command-name>` → slash command. Extract name from
   `<command-name>…</command-name>` (keep the leading `/`) and args from
   `<command-args>…</command-args>`.
   - `/clear` → `reset{Type:"clear"}`.
   - `/compact` → ignore (the `compact_boundary` entry records it).
   - Settings/info commands → ignore: `/model /effort /config /context /cost /help /status /resume /login /logout /exit /permissions /hooks /memory /doctor /ide /theme /usage /agents /mcp /plugin /fast /statusline /terminal-setup /vim /add-dir /release-notes /bug /feedback`.
   - Anything else → `prompt{SlashCommand:name, Text:args}`.
3. Content starts with `<local-command-stdout>`, `<local-command-caveat>`,
   `<local-command-stderr>`, `<task-notification>`, `<system-reminder>` → ignore.
4. Content contains `<bash-input>` → `command{ByUser:true, Cmd:<inner text>, Status:unknown}`.
   A following user entry with `<bash-stdout>`/`<bash-stderr>` sets that
   command's `Output` (stdout then stderr) and `Status:ok` (or `failed` when
   stderr is non-empty and stdout empty).
5. `isMeta: true` → ignore.
6. Human test: `promptSource == "typed"` or `origin.kind == "human"` or
   (`origin` absent and `promptSource` absent and content does not start with `<`).
   → `prompt{Text:content}`. Otherwise ignore.

### `type: "user"`, `message.content` is an array

Blocks:
- `{"type":"text","text":"[Request interrupted by user]"}` or text starting
  with `[Request interrupted by user` → `interrupt{Reason:"user"}`.
- Other `text` blocks when the entry passes the human test above → one
  `prompt` whose Text joins the text blocks with `\n`; count `image` blocks
  into `Prompt.Images`.
- `tool_result` blocks → complete the pending tool call with the same
  `tool_use_id` (see "Tool calls"). The entry-level `toolUseResult` field
  belongs to that tool result.

### `type: "assistant"`

```json
{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-27T14:00:09.000Z","requestId":"r1","effort":"high","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"I'll add the retry loop."},{"type":"tool_use","id":"toolu_01A","name":"Edit","input":{"file_path":"/work/shop/src/webhooks/retry.ts","old_string":"  return post(url, body);","new_string":"  return sendWithRetry(url, body);"}}],"usage":{"input_tokens":1200,"output_tokens":90,"cache_read_input_tokens":30000,"cache_creation_input_tokens":0}}}
```

- `text` blocks → one `assistant_message` per block (skip empty text).
  Attach `usage` to the first message of the entry.
- `thinking` / `redacted_thinking` blocks → ignore.
- `tool_use` blocks → remember `{id, name, input, ts, model}` as pending.
- `message.model` + entry `effort`: emit `model_change{Model, Effort}` when
  either differs from the last emitted value. Skip model `"<synthetic>"`.

### Tool calls (pending `tool_use` + its `tool_result`)

The event is emitted with the `tool_result`'s timestamp and the pending
call's `id` as `Event.ID`. A `tool_use` that never gets a result is emitted
at its own timestamp with `Status:unknown` / `ToolCall.IsError:false`.

**Rejection first:** if the result has `is_error: true` and its content
starts with `The user doesn't want to proceed with this tool use`, or
`toolUseResult == "User rejected tool use"`, emit
`tool_rejected{Tool:name, ToolUseID:id, Reason:"user"}` and nothing else.

| Tool name | Event | Mapping |
|---|---|---|
| `Bash` | `command` | `Cmd=input.command`; `Background=input.run_in_background`. Success: `toolUseResult` is an object with `stdout`, `stderr`, `interrupted`, `timedOutAfterMs` → `ExitCode=0`, `Status=ok`; `interrupted:true` → `Status=interrupted`; `timedOutAfterMs` present → `Status=timeout`. Failure: `is_error:true`, content like `"Exit code 1\n<output>"` and `toolUseResult` a string `"Error: Exit code 1…"` → parse `^Exit code (\d+)` into `ExitCode`, `Status=failed`. `Output` = stdout + "\n" + stderr (or the content text), truncated. If `toolUseResult.bashEditDiff` is an object with a `structuredPatch` array or `filePath`, also emit a `file_edit` with `Via:"bash_edit"` built the same way as Edit; otherwise ignore it. |
| `Edit`, `MultiEdit` | `file_edit` | `Path=input.file_path`, `Op=update`, `Via="edit"`. Hunks from `toolUseResult.structuredPatch[]` (`oldStart, oldLines, newStart, newLines, lines[]` where lines keep `' '`/`'+'`/`'-'` prefixes). `Added`/`Removed` = the `+`/`-` lines without prefix. If `structuredPatch` is missing, derive: `Added` = lines of `new_string` not present in `old_string`, `Removed` = the reverse (MultiEdit: over all `edits[]`). `is_error` → `Failed=true`. |
| `Write` | `file_edit` | `Path=input.file_path`, `Via="write"`. `toolUseResult.type=="create"` → `Op=create`, `Added=model.SplitLines(input.content)`. `"update"` → `Op=update`, hunks from `structuredPatch` as for Edit. |
| `NotebookEdit` | `file_edit` | `Path=input.notebook_path`, `Via="notebook"`, `Added=SplitLines(input.new_source)`. |
| `Read` | `file_read` | `Path=input.file_path`. |
| `AskUserQuestion` | `question` | `Tool="AskUserQuestion"`. Items from `toolUseResult.questions[]` (`question`, `header`, `options[].label`); `Answer` = `toolUseResult.answers[question]` (a string; join arrays with ", "). If `toolUseResult` is missing, parse the result text `"=…"` pairs; if that fails, leave `Answer` empty. |
| `ExitPlanMode` | `plan` | `Source="exit_plan_mode"`, `Text=input.plan`. `Approved=true` when the result is not an error and its text starts with `User has approved`; `false` when it is an error; nil otherwise. |
| `TodoWrite` | `todo` | `Replace=true`, Items from `input.todos[]` (`content`→Text, `status`). |
| `TaskCreate` | `todo` | `Replace=false`, one item: `ID` = result text's task id if parseable else "", `Text` = first non-empty of `input.subject`, `input.title`, `input.description`, `Status="pending"`. |
| `TaskUpdate` | `todo` | `Replace=false`, one item: `ID=input.taskId` (or `input.id`), `Status=input.status`, `Text=input.subject` if present. |
| `Agent`, `Task` | `subagent` | `AgentID=toolUseResult.agentId`, `Type=input.subagent_type`, `Model=toolUseResult.resolvedModel`, `Prompt=Clip(input.prompt,500)`, `Status=toolUseResult.status`. |
| `WebSearch` | `lookup` | `Kind="search"`, `Query=input.query`. |
| `WebFetch` | `lookup` | `Kind="fetch"`, `URLs=[input.url]`. |
| `mcp__<server>__<tool>` | `mcp_call` | split the name on `__`; `Args=Clip(json(input),500)`; `IsError=is_error`. |
| anything else | `tool_call` | `Name`, `Input=Clip(json(input),500)`, `IsError`. |

**Images:** for any tool result whose content array holds
`{"type":"image","source":{"media_type":…,"data":…}}` blocks, also emit one
`image` event per block: `MediaType`, `Bytes=len(data)*3/4`, `Tool=name`,
`Ref="<source path>#<tool_use_id>/<block index>"`. Never store `data`.

### `type: "permission-mode"`

```json
{"type":"permission-mode","permissionMode":"bypassPermissions","sessionId":"s1","timestamp":"2026-09-27T14:00:00.500Z"}
```

→ `mode_change` when the value differs from the last one. Normalize:
`default`→`ask`, `acceptEdits`→`accept_edits`, `plan`→`plan`, `auto`→`auto`,
`bypassPermissions`/`dontAsk`→`bypass`; keep the original in `RawPermission`.
Some entries of this type have no `timestamp`: use the timestamp of the
previous entry in the file.

### `type: "attachment"` (field `attachment.type`)

| attachment.type | Event | Mapping |
|---|---|---|
| `edited_text_file` | `external_edit` | `Path=attachment.filename`, `Source="cc_attachment"`. `snippet` holds numbered lines `"37\tconst x = 1;"`: strip the `^\s*\d+\t` prefix of each line into `Lines`. |
| `diagnostics` | `diagnostics` (one per file) | for each `files[]`: `Path=uri` (strip `file://`), count `diagnostics[].severity` `Error`/`Warning`, `Messages` = first 5 messages. |
| `instructions` | `instructions` (one per file) | for each `files[]`: `Path`, `Scope=lower(type)` (`User`→user, `Project`→project, `Local`→local, `AutoMem`→memory), `Content=Clip(content, 16384)`, `Hash=sha256 hex`, `FirstLine`. |
| `nested_memory` | `instructions` | `Path=attachment.path`, `Scope="nested"`, content from `attachment.content.content`. |
| `queued_command` | `prompt{Steering:true}` | only when `commandMode` is not `"task-notification"` and `prompt` does not start with `<`. |
| anything else | ignore | |

### `type: "system"` (field `subtype`)

```json
{"type":"system","subtype":"compact_boundary","content":"Conversation compacted","compactMetadata":{"trigger":"auto","preTokens":168000,"postTokens":24000},"uuid":"c1","sessionId":"s1","timestamp":"2026-09-27T15:10:00.000Z"}
```

- `compact_boundary` → `compaction{Trigger:compactMetadata.trigger}`.
- `api_error` or any subtype containing `error` → `failure{Type:subtype, Message:content}`.
- everything else (`turn_duration`, `stop_hook_summary`, `away_summary`, …) → ignore.

### Ignored types

`file-history-snapshot`, `file-history-delta`, `queue-operation`,
`last-prompt`, `mode`, `atis-latch`, `summary`, and any unknown type.

## Subagent transcripts

For each session, glob `<dir>/<sessionId>/subagents/agent-*.jsonl`. Parse each
with the same rules, then append its events to the parent session with
`AgentID` = the file's agent id (`agent-<id>.jsonl` → `<id>`). Skip subagent
`prompt` events (the parent's `subagent` event already holds the prompt).
If `agent-<id>.meta.json` has a `model`/`resolvedModel` field, use it as
the default `Model` for that subagent's events.

## Pitfalls

- `toolUseResult` is sometimes an object, sometimes a string. Decode into
  `json.RawMessage` and branch.
- `message.content` is a string for plain prompts and an array otherwise.
- Tool ids look like `toolu_…` or `call_…` depending on the provider; treat them as opaque.
- Entries of one session can be spread across files after a resume.
  `ParseFile` handles one file only. `internal/link` (T10) merges sessions
  that share `Harness` and `ID`, drops events with duplicate IDs, and calls
  `Finalize` again.
