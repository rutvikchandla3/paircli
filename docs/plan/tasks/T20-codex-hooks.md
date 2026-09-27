# T20 — Codex hooks

**Model:** sonnet · **Wave:** C · **Depends on:** T08 · **Milestone:** M2

## Goal

Install paircli hooks into Codex CLI and record the same kinds of hook
records as Claude Code (T08), without bypassing Codex's hook trust review.

## Read first

- `docs/plan/tasks/T08-hooklog-claude-hooks.md` (mirror its structure and rules)
- `internal/claudecode/hooks.go` after T08, `internal/hooklog`
- Codex hooks docs: https://learn.chatgpt.com/docs/hooks (events and payloads)
- Run `codex --version` and, if available, inspect `~/.codex/hooks.json` shape on
  your machine (read-only; never modify the real file in tests).

## Files you own

- `internal/codex/hooks.go`, `internal/codex/hooks_test.go` (new)
- `internal/codex/codex.go` (remove the stub `InstallHooks`, `HooksInstalled`,
  `UninstallHooks`, `RunHookEvent` added earlier; keep `ScanPathB` for T24 to delete)

## Facts to rely on

- Hook config lives in `$CODEX_HOME/hooks.json` (default `~/.codex/hooks.json`),
  shape `{"hooks":{"<Event>":[{"matcher":"…","hooks":[{"type":"command","command":"…"}]}]}}`.
- Events: `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`,
  `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `Stop`,
  `Interrupt`, `SubagentStart`, `SubagentStop`. `PostToolUse` sees `Bash`,
  `apply_patch` and MCP tools, not hosted web search.
- Payload fields include `session_id`, `transcript_path`, `cwd`,
  `hook_event_name`, `turn_id`, `model`, `permission_mode`, `tool_name`,
  `tool_input`, `tool_use_id`, `tool_response`, `source`, `trigger`, `reason`.
- **Trust:** Codex skips new or changed hooks until the user reviews and
  trusts them. It stores approvals in `config.toml` as
  `[hooks.state."<hooks.json path>:<event_snake_case>:<group>:<index>"]` with
  `trusted_hash = "sha256:…"`. paircli must **never** write these entries.

## API

```go
func HooksPath() string                       // $CODEX_HOME/hooks.json or ~/.codex/hooks.json
func InstallHooks() error
func InstallMessage() string                  // shown by `paircli hook install codex`
func UninstallHooks() error
func HooksInstalled() bool
func HookTrustState() (trusted, total int)    // read-only scan of config.toml for our hook keys
func RunHookEvent(event string, stdin io.Reader) error
```

- Install events: `SessionStart`, `UserPromptSubmit`, `PostToolUse` (matcher `Bash`),
  `PermissionRequest`, `Interrupt`, `PreCompact`, `Stop`, `SessionEnd`.
  Command `<abs paircli> hook codex <Event>`. Same merge, backup
  (`hooks.json.paircli.bak`), idempotency and atomic-write rules as T08.
- `InstallMessage`: `Codex needs you to review and trust new hooks before they run. Start codex and approve the paircli hooks when prompted, then run 'paircli doctor'.`
- `HookTrustState`: parse `config.toml` (same dir as hooks.json) line by line
  for section headers `[hooks.state."<HooksPath()>:<event>:<g>:<i>"]` followed by
  a `trusted_hash` line; count those whose `(event, g, i)` points at one of our
  commands in the current hooks.json. `total` = number of our hooks.
- `RunHookEvent`: same record rules as the Claude Code table in T08, with these mappings:
  `SessionStart` (`Reason: source`), `UserPromptSubmit` (snapshot), `PostToolUse`
  (git commands only), `PermissionRequest` (ToolName, ToolUseID, Decision ask),
  `Interrupt` (`Reason: "user"`), `PreCompact` (`Reason: trigger`), `Stop`
  (snapshot), `SessionEnd` (`Reason: reason`). Harness `codex`. Print nothing;
  exit 0; errors to the hook error log.

## Tests

With `t.Setenv("CODEX_HOME", tmp)` and `PAIRCLI_EVENTS_DIR`:
- `TestInstall_Fresh`, `TestInstall_PreservesExisting` (another tool's hooks
  stay), `TestInstall_Idempotent`, `TestUninstall`, `TestHooksInstalled`.
- `TestInstall_NeverWritesTrust` — `config.toml` is byte-identical before and after install.
- `TestHookTrustState` — fixture config.toml with trusted keys for 3 of our 8 hooks → (3, 8).
- `TestRunHookEvent_*` — SessionStart, PostToolUse git commit vs other, Interrupt, Stop snapshot, garbage stdin (nil error, nothing on stdout).

## Acceptance

```sh
go test ./internal/codex/...
go build ./cmd/paircli && CODEX_HOME=$(mktemp -d) ./paircli hook install codex
```

Manual check for the orchestrator (not required to merge): on a machine with
Codex, install, trust the hooks in Codex, run one short session in a git repo,
and confirm records appear in `~/.paircli/events/codex/` with the rollout's
`session_meta.id` as `session_id`. If the ids differ, report it; T10's merge keys on it.
