# T21 — Pi extension + installer

**Model:** sonnet · **Wave:** C · **Depends on:** T05 T08 · **Milestone:** M2

## Goal

A Pi extension that stores paircli hook records inside each Pi session file
(`pi.appendEntry("paircli", record)`), plus install/uninstall commands.

## Read first

- `docs/plan/formats/pi.md` "Hook records written by the paircli extension"
- `docs/plan/tasks/T08-hooklog-claude-hooks.md` (record rules to mirror)
- Pi extension docs and types, if Pi is installed:
  `$(npm root -g)/@earendil-works/pi-coding-agent/docs/extensions.md` and
  `…/dist/core/extensions/types.d.ts`
- `~/.pi/agent/extensions/*.ts` on your machine for examples of the extension shape (read-only)

## Files you own

- `internal/pi/extension/paircli.ts` (new, embedded)
- `internal/pi/install.go`, `internal/pi/install_test.go`, `internal/pi/extension_hash_test.go` (new)
- `internal/pi/pi.go` (remove the stub hook functions; keep `ScanPathB` for T24)

## Extension (`paircli.ts`)

First line exactly `// paircli-extension v1`. TypeScript, Node built-ins only,
type-only import: `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";`.
Default export `function (pi: ExtensionAPI)`. Everything inside try/catch;
the extension must never throw, block a tool, or print.

Handlers (event shapes from `types.d.ts`; in the table, `\|` is an escaped `|`):

| Pi event | Record |
|---|---|
| `session_start` (`reason`) | `event:"session_start"`, head, `trigger:"session_start"`, `reason` |
| `tool_call` (`toolCallId`, `toolName`, `input`) | remember `input.command` for `toolName === "bash"`, keyed by `toolCallId`; no record |
| `tool_execution_end` (`toolCallId`, `toolName`, `isError`) | if the remembered command matches `/\bgit\s+(commit\|merge\|rebase\|cherry-pick\|am\|revert\|reset\|checkout\|switch\|pull\|stash)\b/`: `event:"tool_execution_end"`, head, `trigger:"commit"`, `command` (first 300 chars), `tool_name:"bash"`, `tool_use_id` |
| `before_agent_start` | `event:"before_agent_start"`, head, `trigger:"prompt"`, `snapshot` |
| `agent_end` | `event:"agent_end"`, head, `trigger:"stop"`, `snapshot` |
| `session_shutdown` (`reason`) | `event:"session_shutdown"`, head, `trigger:"session_end"`, `reason` |

Every record: `{v:1, harness:"pi", event, session_id:"", ts: new Date().toISOString(), cwd: ctx.cwd, head_sha, branch, trigger, …}`.
Leave `session_id` empty: `pi.HookRecords` fills it from the session header.

Helpers:
- `head(cwd)`: `execFileSync("git", ["-C", cwd, "rev-parse", "HEAD"], {timeout: 2000})` and `--abbrev-ref HEAD`; `"HEAD"` → `""`; failures → `error` field.
- `snapshot(cwd)`: same algorithm as Go `hooklog.Snapshot` (top level, `git status --porcelain=v1 -z --untracked-files=all`, skip deletions, >1 MB, binary; cap 200 files).
- `lineHash(s)`: FNV-1a 64-bit over the UTF-8 bytes of `s` with trailing spaces, tabs and `\r` removed, as 16 lowercase hex chars (use `BigInt`: offset `0xcbf29ce484222325n`, prime `0x100000001b3n`, mask `0xffffffffffffffffn`). It **must** equal Go's `model.LineHash`. Test vectors:

| input | hash |
|---|---|
| `""` | `cbf29ce484222325` |
| `"const max = 5;"` | `60ff39090249e173` |
| `"  return sendWithRetry(url, body);  "` | `dfa2ccde93f1965a` |
| `"héllo wörld"` | `11824ab841812022` |
| `"\ttabbed\r"` | `0677650ad81e5a82` |

Export `lineHash` as a named export too, so the Go test can call it.

## Installer (`install.go`)

```go
//go:embed extension/paircli.ts
var extensionSource []byte

func ExtensionPath() string   // $PAIRCLI_PI_DIR/extensions/paircli.ts, else ~/.pi/agent/extensions/paircli.ts
func InstallHooks() error     // write the file (0o644), creating the dir; overwrite an older paircli version
func InstallMessage() string  // "Restart pi (or run /reload) to load the paircli extension."
func UninstallHooks() error   // remove the file if it starts with the marker line
func HooksInstalled() bool    // file exists and starts with "// paircli-extension v1"
func RunHookEvent(event string, stdin io.Reader) error // not used by Pi; return nil
```

Refuse to overwrite a `paircli.ts` that does not start with the marker (return an error naming the path).

## Tests

- `TestInstall_WritesMarkedFile`, `TestInstall_RefusesForeignFile`, `TestUninstall`, `TestHooksInstalled` (with `PAIRCLI_PI_DIR`).
- `TestExtension_Hygiene` — the embedded source has the marker first line, no non-type imports outside `node:`, and contains every handler name in the table.
- `TestExtension_LineHashMatchesGo` — if `node` is on PATH, run a small script
  that loads the extension with `node --experimental-strip-types` (Node ≥ 22.6;
  otherwise skip) and prints `lineHash` for the vectors; compare with
  `model.LineHash`. Skip when node is unavailable or too old.

## Acceptance

```sh
go test ./internal/pi/...
go build ./cmd/paircli && PAIRCLI_PI_DIR=$(mktemp -d) ./paircli hook install pi
```

Manual check for the orchestrator (not required to merge): install for real,
run one short Pi session in a git repo, confirm `custom` entries with
`customType:"paircli"` in the session file and that `pi.HookRecords` reads them.
