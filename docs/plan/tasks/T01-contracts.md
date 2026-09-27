# T01 — Install contracts + helper tests

**Model:** haiku · **Wave:** A · **Depends on:** — · **Milestone:** M1

## Goal

Put the frozen contract packages in place and prove their helpers work.

## Read first

- `docs/plan/CONTRACTS.md` section 1
- `docs/plan/_contracts/**` (the exact source to install)

## Files you own

- `internal/model/*.go` (new)
- `internal/config/*.go` (new)
- `internal/redact/*.go` (new)
- `.gitignore` (append only)

## Steps

1. Copy every file under `docs/plan/_contracts/internal/` to the same path
   under `internal/`, byte for byte:
   ```sh
   cp -R docs/plan/_contracts/internal/model internal/model
   cp -R docs/plan/_contracts/internal/config internal/config
   cp -R docs/plan/_contracts/internal/redact internal/redact
   ```
   Do not edit these files. If one does not compile, stop and report a CONTRACT GAP.
2. Append `/.paircli.local.json` to `.gitignore` (reserved for local overrides).
3. Write the tests below.

## Tests

`internal/model/text_test.go`
- `TestTruncateOutput_Short` — a 100-byte string is returned unchanged.
- `TestTruncateOutput_Long` — a 10,000-byte string keeps the first and last 4000 bytes and contains `[2000 bytes truncated]`.
- `TestTruncateOutput_UTF8` — cutting through a multi-byte rune yields valid UTF-8 (`utf8.ValidString`).
- `TestClip` — `Clip("héllo", 3) == "hél…"`, `Clip("hi", 3) == "hi"`.
- `TestLooseLine` — `LooseLine("  const a = 'x';") == LooseLine("const a = \"x\"")`.
- `TestIsTrivialLine` — true for `""`, `"  }"`, `"});"`; false for `"return x;"`.
- `TestLineHash` — 16 hex chars; `LineHash("a  ") == LineHash("a")`; differs for `"a"` vs `"b"`.
- `TestSplitLines` — `""`→nil, `"a\n"`→`["a"]`, `"a\r\nb"`→`["a","b"]`, `"a\n\nb\n"`→`["a","","b"]`.

`internal/model/attribution_test.go`
- `TestAnchorsFor_Ranges` — lines 3,4,5,9 of `a.go` from ref R, line 10 from another ref → `[{a.go 3-5} {a.go 9}]`.
- `TestAnchorsFor_Empty` — nil attribution and empty refs return nil.
- `TestLinesFor` — returns ranges only for the requested labels.
- `TestRatio` — Total 0 → 0; Total 10, Explained 4 → 0.4.

`internal/model/session_test.go`
- `TestFinalize_SortsAndSequences` — out-of-order events are sorted by TS; equal TS keep input order; Seq = index.
- `TestFinalize_Turns` — prompt, message, steering prompt, prompt → turns 1,1,1,2; events before the first prompt have turn 0; a subagent prompt (AgentID set) does not advance the turn.
- `TestFinalize_FinalMessage` — two messages before a prompt: only the second is Final; the last message of the session is Final; a subagent message is never Final; a pre-set Final on an earlier message is cleared.
- `TestFinalize_StartEnd` — Start/End equal first/last event TS.

`internal/model/pr_test.go`
- `TestAddedLines` / `TestRemovedLines` — ordering across two hunks.
- `TestPRFile` — found and not found.

`internal/config/config_test.go`
- `TestLoad_Missing` — no file → `Default()` values.
- `TestLoad_Merge` — file with `sensitive_paths:["secret/**"]`, `window_before:"24h"`, `llm:{"provider":"anthropic"}` → defaults plus `secret/**`, 24h, provider anthropic, model still `claude-opus-5-5`.
- `TestLoad_BadDuration` — `"window_after":"soon"` → error.
- `TestLoad_BadJSON` — error mentions `.paircli.json`.

`internal/redact/redact_test.go`
- `TestTextNoop` — returns input unchanged.

## Acceptance

```sh
diff -r docs/plan/_contracts/internal/model internal/model | grep -v _test.go   # no differences except test files
go test ./internal/model/... ./internal/config/... ./internal/redact/...
```
