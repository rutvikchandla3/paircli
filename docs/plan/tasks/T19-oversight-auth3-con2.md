# T19 — OVS-1, OVS-2, OVS-3, AUTH-3, CON-2

**Model:** haiku · **Wave:** C · **Depends on:** T02 T07 · **Milestone:** M1

## Read first

- `docs/plan/CONTRACTS.md` engine, testkit; `docs/plan/tasks/T12-intent.md` "Pattern for every detector"
- `docs/SIGNALS.md` rows OVS-1..3, AUTH-3, CON-2

## Files you own

- `internal/detect/oversight/ovs1.go`, `ovs2.go`, `ovs3.go`, `oversight_test.go`
- `internal/detect/authorship/auth3.go`, `auth3_test.go`
- `internal/detect/consistency/con2.go`, `con2_test.go`

**Tool actions** = main- or sub-agent events of kind `command` (not `ByUser`),
`file_edit`, `mcp_call`, `tool_call`, `lookup`, `subagent`.

## OVS-1 Autonomy envelope

Walk each session's events keeping the current `Mode` (start: unknown). For
each tool action decide its posture:

| Harness / mode | Posture |
|---|---|
| Pi (any) | `unapproved` (Pi has no permission layer) |
| Claude Code `bypass` | `unapproved` |
| Claude Code `accept_edits` | `unapproved` for `file_edit`, else `approval_possible` |
| Claude Code `auto` | `auto_approved` |
| Claude Code `ask`, `plan` | `approval_possible` |
| Codex `Approval == "never"` | `unapproved` |
| Codex `on-request`, `on-failure`, `untrusted` | `approval_possible` |
| no mode seen yet | `unknown` |

Also count `permission` events with `Decision == "ask"` as prompts shown.

- Finding per session, info:
  - Claude Code: `` `{ref}`: {pct unapproved} of {n} tool calls ran with no approval step (modes: {distinct raw modes}). ``
  - Codex: `` `{ref}`: approvals {approval}, sandbox {sandbox} for {pct} of {n} tool calls. `` using the most common pair.
  - Pi: `` `{ref}`: no permission layer; {n} tool calls. ``
- Summary: `{pct} of {n} tool calls ran with no approval step.` plus ` {k} permission prompts were shown.` when `k > 0`.
- State `info` always (calibration, not a verdict).
- Data `{"tool_calls","unapproved","auto_approved","approval_possible","unknown","prompts_shown","sessions":[{"ref","modes","sandbox","unapproved","total"}]}`.

## OVS-2 Human touchpoints

Human events (main agent): `prompt`, `question` with any answer, `permission`
with `By == "user"`, `command` with `ByUser`, `tool_rejected`, `interrupt`.

Per session, split the session at human events into stretches (session start
→ first human event, between human events, last human event → session end).
Longest stretch = most tool actions (ties: longer duration).
Lines written in it = attribution lines whose `Source.Session` is the
session and whose Source event lies inside the stretch.

- Finding per session, info: `` `{ref}`: longest unattended stretch {minutes} min, {k} tool calls, {Plural(lines,"PR line","PR lines")} written. `` anchors = `c.AnchorsFor(source events in the stretch)`.
- Summary: overall longest: `Longest unattended stretch: {minutes} min and {k} tool calls in `{ref}`; {lines} PR lines were written in it.`
- State `info`. Data `{"human_events": n, "longest": {"session","from","to","minutes","tool_calls","lines"}}`.

## OVS-3 Delegated work

- Subagents: `subagent` events (main) plus distinct `AgentID`s on events.
- Lines per agent id: attribution lines with that `AgentID`.
- Finding per subagent, info: `` Subagent `{Type or AgentID}` ({Model or "model unknown"}) wrote {Plural(lines,"PR line","PR lines")}{ in `file`…}. `` anchors for its lines.
- Summary: `{n} subagents; {k} PR lines were written by subagents.` / `clear`, `No subagents were used.`
- State `info` when any. Data `{"subagents":[{"id","type","model","lines"}]}`.

## AUTH-3 Model mix and disclosure trailer

- Count attribution lines labeled `agent` or `agent_then_human` by
  `(harness, model)`: harness = the part of `Source.Session` before `:`;
  model = `LineAttribution.Model`, else `"unknown"`.
- Trailer: one line per pair sorted by count desc: `Assisted-by: {harness}:{model}`.
- Summary: `Assisted-by: {h1}:{m1} ({pct1}), {h2}:{m2} ({pct2}) of agent lines.` trimmed to 160 chars (drop trailing pairs, add `, …`).
- No agent lines → `Unknown("No agent-written lines in this PR.")`. State `info`.
- Data `{"models":[{"harness","model","lines","share"}], "trailer": "Assisted-by: …\nAssisted-by: …", "efforts": {model: [efforts seen in model_change events]}}`.

## CON-2 Open loops (deterministic part)

- **Open tasks:** per session, replay `todo` events in order: `Replace` →
  the list becomes `Items`; otherwise upsert each item by `ID` (or by `Text`
  when `ID` is empty), updating `Status`/`Text` when set. At the end, items
  whose status is not `completed`, `done`, `cancelled`, `canceled` or
  `deleted` are open → `Task still open at session end: “{Clip(text,80)}”` severity alert.
- **TODO comments added:** added PR lines (non-`generated` files) matching
  `\b(TODO|FIXME|XXX|HACK)\b` → `` `{file}:{line}` adds a {word}: {Clip(trimmed,80)} `` severity info, anchored.
- Summary: `{t} tasks still open at session end; {n} TODO/FIXME comments added.` (omit zero parts);
  none → `clear`, `No open tasks and no TODO/FIXME comments added.`
- State `alert` if open tasks, else `info` if TODOs. Data `{"open_tasks": t, "todo_comments": n}`.

## Tests

- OVS-1: `TestOVS1_ClaudeModes` (bypass, accept_edits split, ask), `TestOVS1_Codex`, `TestOVS1_Pi`, `TestOVS1_UnknownBeforeFirstMode`, `TestOVS1_PromptsShown`.
- OVS-2: `TestOVS2_LongestStretch`, `TestOVS2_LinesInStretch`, `TestOVS2_UserRunIsHuman`.
- OVS-3: `TestOVS3_SubagentLines`, `TestOVS3_None`.
- AUTH-3: `TestAUTH3_TrailerOrder`, `TestAUTH3_SummaryTrim`, `TestAUTH3_NoAgentLines`.
- CON-2: `TestCON2_ReplaceThenIncremental`, `TestCON2_CompletedNotOpen`, `TestCON2_TodoComments`, `TestCON2_GeneratedIgnored`, `TestCON2_Clear`.

## Acceptance

```sh
go test ./internal/detect/oversight/... ./internal/detect/authorship/... ./internal/detect/consistency/...
```
