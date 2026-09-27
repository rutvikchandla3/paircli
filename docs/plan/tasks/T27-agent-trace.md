# T27 — Agent Trace export

**Model:** sonnet · **Wave:** E · **Depends on:** T11 T22 T24 T26 · **Milestone:** M3

## Goal

Write AUTH-1 attribution as Agent Trace records (`agent-trace.json`) so other
tools can consume paircli's line attribution without a custom format.

## Read first

- The Agent Trace specification: https://agent-trace.dev/ . Fetch it and
  follow its current version exactly (field names, required fields,
  contributor types, range format, version string). If the site is
  unreachable, stop and report; do not guess the schema.
- `internal/model/attribution.go`, `docs/plan/CONTRACTS.md` "internal/agenttrace"

## Files you own

- `internal/agenttrace/*.go` + tests + `testdata/golden/`
- `internal/scan/scan.go` (add one call after `render.Write`, plus an `Options.NoAgentTrace` field)
- `cmd/paircli/scan.go` (add `--no-agent-trace`)

## Behavior

`Write(dir, pr, attr, sessions)` writes `dir/agent-trace.json`:
- One trace record per PR file with attributed lines (or whatever grouping the spec requires).
- Contributor mapping: `agent` → AI, `agent_then_human` → mixed,
  `human_in_session` → human, `uncaptured` → unknown, `trivial` → omitted.
  Use the spec's exact enum values.
- Ranges: contiguous runs of lines with the same label and source session.
- Conversation/context references: the session ref, harness, model, and the
  transcript path through `engine.Home`. Never embed transcript content.
- Include the PR head commit SHA / repo where the spec has a place for VCS info.
- Deterministic output (sorted, no wall-clock except where the spec requires a timestamp; use `GeneratedAt`).

## Tests

- `TestWrite_Golden` — testkit attribution with all labels → golden file.
- `TestWrite_ValidatesRequiredFields` — every required field in the spec is present (encode the list you read from the spec in the test).
- `TestWrite_NoContentLeak` — output contains no line text from the PR.

## Acceptance

```sh
go test ./internal/agenttrace/... ./internal/scan/...
```
