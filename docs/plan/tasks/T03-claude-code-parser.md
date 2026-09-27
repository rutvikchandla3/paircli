# T03 — Claude Code transcript parser

**Model:** sonnet · **Wave:** B · **Depends on:** T01 · **Milestone:** M1

## Goal

Turn Claude Code transcripts (main + subagents) into `*model.Session` timelines.

## Read first

- `docs/plan/formats/claude-code.md` (the mapping spec; follow it exactly)
- `docs/plan/CONTRACTS.md` section 1 and "Harness parsers"
- `internal/claudecode/pathb.go` (legacy parser: reuse ideas, do not modify)

## Files you own

- `internal/claudecode/transcript.go`, `internal/claudecode/transcript_tools.go` (new)
- `internal/claudecode/transcript_test.go` (new)
- `internal/claudecode/testdata/**` (new, synthetic only)

Do not touch `pathb.go` or `hooks.go`.

## API

```go
func DefaultRoot() string                                  // ~/.claude/projects
func Discover(root string, since time.Time) ([]string, error)
func ParseFile(path string) ([]*model.Session, error)
```

- `Discover`: files matching `root/*/*.jsonl` with `ModTime() >= since`
  (zero since = all), sorted. Never returns files under `*/subagents/`.
  Missing root → `nil, nil`.
- `ParseFile`: read line by line with a reader that tolerates very long lines
  (64 MB). Skip lines that are not valid JSON. Group entries by `sessionId`
  (entries without one attach to the most recent sessionId seen; if none yet,
  skip). Dedupe by `uuid`. Apply the mapping in the format doc. Then load
  subagent transcripts from `<dir of path>/<sessionId>/subagents/agent-*.jsonl`
  and merge them as described there. Set `SourcePath=path`, `Capture=reconstructed`.
  Call `Finalize()` on each session. Return sessions sorted by `Start`.

Implementation notes:
- Decode each line into a struct with `json.RawMessage` for `message`,
  `toolUseResult`, `attachment`, `compactMetadata`; branch on type.
- Keep a per-session state struct: pending tool calls, current model/effort,
  last permission mode, last timestamp, open `ByUser` command waiting for output,
  per-kind counters for generated IDs.
- Emit events with `Origin: model.OriginTranscript`.
- Event IDs: tool events use the `tool_use` id (Codex-style ids are opaque);
  prompts/messages/interrupts use the entry `uuid` (suffix `/<n>` when one
  entry yields several events); attachments use `"<uuid>/<n>"`.

## Fixtures (write by hand, synthetic)

`testdata/projects/-work-shop/s1.jsonl` covering, in one plausible session:
typed prompt; slash command `/deep-research some args`; `/clear`; a
`<local-command-stdout>` line (ignored); `isMeta` line (ignored);
`permission-mode` changes `default`→`bypassPermissions` (one entry without a
timestamp); assistant text + Edit tool_use + tool_result with
`structuredPatch`; Write create; Bash success (object result); Bash failure
(`Exit code 2`); Bash interrupted; a rejected tool call; Read;
AskUserQuestion with answers; ExitPlanMode approved; TodoWrite; Agent tool
with `resolvedModel`; WebSearch; WebFetch; an `mcp__github__get_issue` call;
a tool_result containing an image block (tiny fake base64); attachments
`edited_text_file`, `diagnostics`, `instructions`, `nested_memory`,
`queued_command` (one human, one task-notification); `compact_boundary` then
an `isCompactSummary` user entry; `[Request interrupted by user]`;
`<bash-input>`/`<bash-stdout>` pair; an `ai-title` entry; a duplicated
`uuid` line; one malformed line; one unknown `type`.

`testdata/projects/-work-shop/s1/subagents/agent-abc123.jsonl` with one Edit and one Bash.

`testdata/projects/-work-shop/s2.jsonl`: a tiny second session (for Discover).

## Tests

- `TestDiscover` — finds s1 and s2, not the subagent file; `since` filters by mtime (use `os.Chtimes`).
- `TestParse_SessionFields` — ID, CWD, Branch, HarnessVersion, Title, Start/End, Capture.
- `TestParse_Prompts` — typed prompt, slash command (name + args), steering from `queued_command`, task-notification ignored, `isMeta` and local-command lines ignored.
- `TestParse_Reset` — `/clear` → reset.
- `TestParse_Edit` — hunks, Added, Removed from structuredPatch; Write create Added = content lines.
- `TestParse_EditFallback` — Edit without structuredPatch derives Added/Removed from old/new strings.
- `TestParse_Bash` — success exit 0/ok; failure exit 2/failed; interrupted; output truncated via `TruncateOutput` for a >8 KB output.
- `TestParse_Rejection` — rejected call yields only `tool_rejected`.
- `TestParse_QuestionPlanTodo` — AskUserQuestion answers; ExitPlanMode approved=true; TodoWrite items with Replace.
- `TestParse_Subagent` — `subagent` event with model; subagent file events appended with AgentID `abc123`; subagent prompts skipped.
- `TestParse_LookupsMCP` — WebSearch, WebFetch, MCP server/tool split.
- `TestParse_Image` — image event with Ref, no data stored.
- `TestParse_Attachments` — external_edit Lines stripped of number prefixes; diagnostics counts; instructions scope/hash/first line; nested_memory.
- `TestParse_Compaction` — trigger from boundary; summary attached from the following summary entry.
- `TestParse_Modes` — mode_change only on change; missing timestamp uses previous.
- `TestParse_ModelChange` — emitted when model or effort changes.
- `TestParse_UserBash` — ByUser command with output from `<bash-stdout>`.
- `TestParse_Dedupe` — duplicated uuid produces one event.
- `TestParse_Malformed` — malformed line and unknown type are skipped, no error.
- `TestParse_Final` — last assistant message before each prompt is Final (via Finalize).

## Acceptance

```sh
go test ./internal/claudecode/...
go vet ./internal/claudecode/...
```

Optional local smoke check (do not commit output): write a throwaway test
under `/tmp` or run `go run` against one real transcript in `~/.claude/projects`
and confirm it parses without panicking.
