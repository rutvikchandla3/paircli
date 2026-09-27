# Codex CLI rollout format → model events

Checked against Codex CLI 0.154.0 rollouts and older mid-2026 rollouts on a
real machine. Examples are **synthetic**. Codex has written two generations
of events; a single file can contain both. Parse both.

## Files

```
~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl
~/.codex/archived_sessions/rollout-<timestamp>-<uuid>.jsonl
```

Each line: `{"timestamp": RFC3339, "type": <top-level type>, "payload": {...}}`
(newer files also carry `"ordinal"`). Use the line's `timestamp` for the event.

## Session header

```json
{"timestamp":"2026-09-27T14:00:00.100Z","type":"session_meta","payload":{"id":"019f0000-aaaa-7000-8000-000000000001","timestamp":"2026-09-27T14:00:00.000Z","cwd":"/work/shop","originator":"codex_cli_rs","cli_version":"0.154.0","source":"cli","git":{"commit_hash":"4be1c0ffee4be1c0ffee4be1c0ffee4be1c0ffee","branch":"feat/retry","repository_url":"git@github.com:acme/shop.git"}}}
```

- `ID=payload.id`, `CWD=payload.cwd`, `HarnessVersion=payload.cli_version`.
- If `payload.git` exists: `StartSHA=commit_hash`, `Branch=branch`,
  `RepoRemote=gitinfo.OwnerRepoFromURL(repository_url)`, and emit
  `git_head{SHA:commit_hash, Branch, Trigger:"transcript_meta"}` at the header timestamp.
- If the payload has `forked_from_id`, `parent_thread_id` or `parent_id`, set `ParentID`.
- Ignore `base_instructions`, `context_window`, everything else.

## Per-turn settings

```json
{"timestamp":"2026-09-27T14:00:01.000Z","type":"turn_context","payload":{"turn_id":"t1","cwd":"/work/shop","approval_policy":"never","sandbox_policy":{"type":"danger-full-access"},"model":"gpt-5.6-luna","effort":"xhigh","collaboration_mode":{"mode":"default","settings":{"model":"gpt-5.6-luna","reasoning_effort":"xhigh"}}}}
```

- `mode_change{Approval:approval_policy, Sandbox:sandbox_policy.type,
  Permission:"plan" when collaboration_mode.mode=="plan" else ""}` when any
  of the three differs from the last emitted value.
- `model_change{Model:model, Effort:effort or collaboration_mode.settings.reasoning_effort}` on change.
- The event `thread_settings_applied` carries the same fields under
  `payload.thread_settings`; treat it the same way.

## Rules files

```json
{"timestamp":"2026-09-27T14:00:01.100Z","type":"world_state","payload":{"full":true,"state":{"agents_md":{"text":"# Project rules\n- Never edit generated/."}}}}
```

→ `instructions{Path:"AGENTS.md", Scope:"project", Content, Hash, FirstLine}`
once per distinct hash.

## Event families: prefer the newer form per family

Newer rollouts put most things in `type:"event_msg"` with
`payload.type:"item_completed"` and `payload.item.type` naming the item.
Older rollouts use dedicated `event_msg` payload types. **For each family
below, if the file contains at least one newer item of that family, ignore
the older events of that family in the same file.** This avoids double counting.

| Family | Newer (`item_completed` → `item.type`) | Older (`event_msg` → `payload.type`) |
|---|---|---|
| prompt | `UserMessage` | `user_message` |
| assistant | `AgentMessage` | `agent_message` |
| command | `CommandExecution` | `exec_command_end` |
| edit | `FileChange` | `patch_apply_end` |
| mcp | `McpToolCall` | `mcp_tool_call_end` |
| web | `Extension` with `kind:"web.search"` | `web_search_end` |
| subagent | `SubAgentActivity` | `sub_agent_activity` |
| compaction | `ContextCompaction` | `context_compacted` |

### prompt

```json
{"timestamp":"2026-09-27T14:00:02.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"UserMessage","id":"m1","content":[{"type":"text","text":"Add retry with backoff to the webhook sender."}]}}}
{"timestamp":"2026-07-09T19:47:40.516Z","type":"event_msg","payload":{"type":"user_message","message":"update local main\n","images":[]}}
```

→ `prompt{Text}` (join text parts; trim trailing newline). `Images` =
len(images)+len(local_images) when present. Ignore `response_item` messages
with `role:"user"` or `role:"developer"`: those are injected context, not prompts.

### assistant

`AgentMessage.content[].text` joined, or older `agent_message.message` →
`assistant_message`. Ignore `Reasoning`/`agent_reasoning`.

### command

```json
{"timestamp":"2026-09-27T14:02:00.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"CommandExecution","id":"exec-1","command":["/bin/zsh","-lc","npm test"],"cwd":"file:///work/shop","parsed_cmd":[{"type":"unknown","cmd":"npm test"}],"status":"completed","exit_code":1,"aggregated_output":"Tests: 2 failed, 60 passed","duration":{"secs":4,"nanos":500000000}}}}
```

- `Cmd`: if `command` is an array whose second-to-last element is `-lc` or
  `-c`, use the last element; otherwise join with spaces. Older events may
  store `command` as an array too.
- `CWD`: strip a leading `file://`.
- `exit_code`: a number in newer files, a **string** (`"0"`) in older ones. Parse both.
- `Status`: exit 0 → ok; non-zero → failed; `status:"interrupted"`/`"cancelled"` → interrupted; missing exit code → unknown.
- `Output` = `aggregated_output` (else `stdout`+`stderr`), truncated.
- `DurationMs` = secs*1000 + nanos/1e6.
- If every `parsed_cmd[]` entry has `type:"read"`, also emit one
  `file_read{Path:entry.path}` per entry (skip entries without `path`).

### edit

```json
{"timestamp":"2026-09-27T14:01:30.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"FileChange","id":"exec-2","status":"completed","changes":{"/work/shop/src/webhooks/retry.ts":{"type":"update","unified_diff":"@@ -38,2 +38,4 @@\n export async function sendWithRetry(evt) {\n+  const max = 5;\n+  for (let i = 1; i <= max; i++) {\n","move_path":null},"/work/shop/src/webhooks/types.ts":{"type":"add","content":"export type Attempt = number;\n"}}}}}
```

One `file_edit` per `changes` key, `Via:"apply_patch"`, `Event.ID` =
`<item id>/<n>` where n counts changes in sorted-path order:
- `add` → `Op=create`, `Added=SplitLines(content)`.
- `update` → `Op=update` (or `move` when `move_path` is set, with `MovePath`);
  parse `unified_diff` hunks (`@@ -a,b +c,d @@` headers, lines prefixed
  `' '`, `'+'`, `'-'`; `\ No newline at end of file` ignored) into `Hunks`,
  `Added`, `Removed`.
- `delete` → `Op=delete`, `Removed=SplitLines(content)`.
- `status` other than `completed`, or older `success:false` → `Failed=true`.

**Fallback:** if the file has no edit-family events at all, parse
`response_item` `custom_tool_call` with `name:"apply_patch"`: `input` is an
`*** Begin Patch` … `*** End Patch` block with `*** Add File: <path>`,
`*** Update File: <path>` (+ optional `*** Move to: <path>`), `*** Delete File: <path>`
sections; `+`/`-`/` ` lines inside `@@` chunks.

### mcp

Newer `McpToolCall{server, tool, arguments, readOnlyHint, result.isError}`;
older `mcp_tool_call_end{invocation{server, tool, arguments}, result}` where
`result` is `{"Ok":{…}}` or `{"Err":…}`. → `mcp_call`.

### web

Newer `Extension{kind:"web.search", query, action{type, queries[]|url}}`;
older `web_search_end{query, action}`. → `lookup{Kind:"search", Query}`; if
`action.type` is `open_page` or `fetch` with a `url`, `Kind:"fetch", URLs:[url]`.

### subagent

`SubAgentActivity{kind:"started", agent_thread_id, agent_path}` (or older
`sub_agent_activity`) → `subagent{AgentID:agent_thread_id, Type:agent_path}`.
Ignore kinds other than `started`. Enrich `Model` from the matching
`response_item` `function_call` `name:"spawn_agent"` whose `call_id` equals
the activity `id`/`event_id` (arguments JSON: `model`, `reasoning_effort`, `task_name`).

### compaction

`ContextCompaction` item, older `context_compacted`, and top-level
`type:"compacted"` (payload has `message`, the summary) describe the same
compaction. Emit one `compaction` per group of these within 10 seconds of
each other; `Summary` = `compacted.payload.message` when present.

## Other events (both generations)

| Source | Event |
|---|---|
| `event_msg` `turn_aborted` (`reason`) | `interrupt{Reason: "user" if reason=="interrupted" else reason}` |
| `event_msg` `thread_rolled_back` | `reset{Type:"rollback"}` |
| `item_completed` `Plan` (`text`) | `plan{Source:"plan_item", Text}` |
| `response_item` `function_call` `update_plan` (arguments `{"plan":[{"step","status"}]}`) | `todo{Replace:true, Items}` |
| `response_item` `function_call` `request_user_input` + its `function_call_output` (same `call_id`) | `question{Tool:"request_user_input"}`: items from arguments (`questions[]` with `question`/`options`, or a single `question`); `Answer` = output text. Skip when the output says the tool is unavailable. |
| `item_completed` `ImageView` (`path`) | `image{Ref:path}` |
| `event_msg` `task_complete` | nothing (Finalize marks final messages) |
| `response_item` `function_call` `exec_command`/`shell` | only when the file has no command-family events: `command{Cmd from arguments.cmd or arguments.command, Status:unknown}` |

Ignore: `token_count`, `token_usage_record`, `agent_reasoning`, `reasoning`,
`inter_agent_communication_metadata`, `tool_search_*`, `CollabAgentToolCall`,
`wait`/`write_stdin`/`list_agents`/`send_message` calls, unknown types.

## Pitfalls

- `exit_code` type differs by generation.
- Paths in `changes` are absolute; paths in `parsed_cmd` may be absolute or relative to `cwd`.
- The model for events comes from the latest `turn_context`; set `Event.Model` to it.
- Many older rollouts lack `session_meta.git`; then `StartSHA` stays empty.
