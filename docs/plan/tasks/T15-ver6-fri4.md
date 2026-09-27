# T15 — VER-6 Runtime and visual evidence, FRI-4 Knowledge lookups

**Model:** haiku · **Wave:** C · **Depends on:** T02 T07 · **Milestone:** M1

## Read first

- `docs/plan/CONTRACTS.md` engine, testkit, classify; `docs/plan/tasks/T12-intent.md` "Pattern for every detector"
- `docs/SIGNALS.md` rows VER-6, FRI-4

## Files you own

- `internal/detect/verification/ver6.go`, `ver6_test.go`
- `internal/detect/friction/fri4.go`, `fri4_test.go`

## VER-6 Runtime and visual evidence

Findings, in timeline order:
- `image` events → `` Screenshot captured at {Clock} via `{Tool}`. `` (omit ` via …` when Tool is empty). info. Data `{"ref": Image.Ref}`.
- commands with class `dev_server` → `` Dev server started: `{ShortCmd}` at {Clock}. `` info.
- commands with class `local_http` → `` Local request: `{ShortCmd}` ({status}) at {Clock}. `` info.
- Open editor errors: for each PR file, the **latest** `diagnostics` event with
  that `RelPath`; if `Errors > 0` → `` `{file}` still had {Plural(Errors,"error","errors")} at {Clock}: {Clip(first message, 80)} ``
  severity `alert`, anchor `{File}`.

Summary: alerts first — `{n} PR files still had editor errors at the end of the session.`;
else `{i} screenshots, {d} dev server runs, {l} local requests captured.`;
else `No screenshots, dev servers or local requests were captured.`
State: `alert` if open errors; else `info`.
Data: `{"images": i, "dev_servers": d, "local_requests": l, "open_errors": [{"file","errors"}]}`.

## FRI-4 Knowledge lookups

- Lookups: `lookup` events; `mcp_call` events whose `Server` matches
  `(?i)(context7|docs|deepwiki|devdocs|mdn)`.
- For each lookup, "followed by" edits = `file_edit` events in the **same
  session** after it, within the next 10 events (by `Seq`) or 15 minutes,
  whichever ends first, whose `RelPath` is a PR file.
- Finding per lookup, info: search → `` Searched “{Clip(Query,80)}” at {Clock} ``;
  fetch → `` Fetched {host of first URL}{path clipped to 40} at {Clock} ``;
  docs MCP → `` Looked up docs via `{Server}` at {Clock} ``. When followed by
  edits append ``; edits followed in `{first file}` `` and set anchors = `c.AnchorsFor(those edits)`.
- Summary: `{n} web or docs lookups; {m} were followed by edits to PR files.` or
  State `clear`, `No web or docs lookups.`
- State `info` when any. Data `{"lookups": n, "followed_by_edits": m}`.

## Tests

- `TestVER6_Mixed` (image, dev server, local request), `TestVER6_OpenErrorsLatestOnly` (an earlier error later fixed → no alert), `TestVER6_None`.
- `TestFRI4_SearchFollowedByEdit` (anchors), `TestFRI4_WindowLimit` (edit 20 minutes later not linked), `TestFRI4_DocsMCP`, `TestFRI4_None`.

## Acceptance

```sh
go test ./internal/detect/verification/... -run VER6 && go test ./internal/detect/friction/... -run FRI4
```
