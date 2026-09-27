# T25 — End-to-end golden scenario

**Model:** sonnet · **Wave:** D2 · **Depends on:** T24 · **Milestone:** M1

## Goal

One realistic, fully synthetic PR produced across all three harnesses, run
through `scan.Run`, with golden outputs. It is the regression test for the
whole product and the example in the docs.

## Read first

- `docs/plan/formats/*.md`, `docs/SIGNALS.md`, `internal/scan`
- The worked example in the "Session Signal Catalog" doc (PR #482, linked from `docs/SIGNALS.md`)

## Files you own

- `internal/e2e/e2e_test.go`, `internal/e2e/scenario.go` (builders), `internal/e2e/testdata/**`

## Scenario: PR #482 "Add retry with backoff to webhook sender" (acme/shop)

Build everything under `t.TempDir()` at test time (write transcripts with
timestamps relative to a fixed date, 2026-09-27):

- A repo dir with `.git/HEAD` (`ref: refs/heads/feat/retry`) and `.git/config`
  (`origin` = `git@github.com:acme/shop.git`). Transcripts use this dir as cwd.
- **Claude Code session A** (hooked; hook log records with SHAs of commits 1–2):
  prompt “Add retry with backoff to the webhook sender. Don't touch the queue.”;
  AskUserQuestion Redis vs Postgres → Postgres; ExitPlanMode approved (4 steps,
  incl. "add jitter"); edits to `src/webhooks/retry.ts` (11 edits, several
  edit→test cycles); `npm test` fail → edit `test/webhook.test.ts` loosening
  `toBe(5)` → `toBeGreaterThan(0)` → `npm test` pass at 14:02; one more edit to
  `retry.ts` at 14:07 (no test after); `npm i p-retry@6.2.1`;
  `npm i express-retry-webhook` failing with `E404`; interrupt while editing
  `migrations/0042_retry.sql`; `prisma migrate deploy`; `gh issue view 312`
  then an edit to `.github/workflows/release.yml`; `cat .env`; a TodoWrite
  leaving "add jitter" pending; compaction at 14:30 followed by an edit to
  `src/metrics.ts`; `permission-mode` bypassPermissions; a subagent writing
  `test/retry.test.ts`.
- **Codex session B** (reconstructed, `session_meta.git` = commit 2): edits
  `src/webhooks/sender.ts` adding an `Idempotency-Key` header after an
  Extension web.search; runs `npx tsc --noEmit` (pass).
- **Pi session C** (extension hook records present): a short branch that is
  abandoned (branch_summary) plus an edit to `src/webhooks/sender.ts`.
- **Commit 3** contains a hand-written change to `README.md` that no session made (→ unattributed).
- Fake `pr.Runner` returning PR view JSON (3 commits, body claiming "All tests
  pass. Adds tests for the retry path."), the combined diff, and per-commit patches consistent with the transcripts.
- `scan.Options{Now: 2026-09-27T16:00:00Z, Version: "test"}`, roots pointed at the temp dirs, `PAIRCLI_EVENTS_DIR` at the temp hook log.

## Assertions

Golden files (`-update` regenerates): `testdata/golden/signals.json`,
`report.md`, `comment.md`. Plus explicit checks that fail with readable messages:

| Signal | Expected |
|---|---|
| AUTH-2 | 3 sessions; 1 unattributed commit; state per ratio |
| AUTH-1 | agent lines > 0; `README.md` lines uncaptured |
| INT-2 | one decision mentioning Postgres |
| INT-3 | plan approved |
| VER-1 | npm test group with last status pass |
| VER-2 | alert; anchor in `src/webhooks/retry.ts` |
| VER-4 | alert; fail→edit→pass on `test/webhook.test.ts` |
| DEC-1 | alert; anchor in `migrations/0042_retry.sql` |
| DEC-2 | Pi branch finding |
| FRI-1 | `src/webhooks/retry.ts` hotspot |
| FRI-3 | compaction with `src/metrics.ts` edited after |
| EXP-1 | migration finding |
| EXP-2 | `p-retry` agent choice; `express-retry-webhook` does not exist |
| EXP-3 | chain `gh issue view 312` → `.github/workflows/release.yml` |
| EXP-4 | `.env` read |
| OVS-1 | bypass for the Claude Code session; Codex approvals |
| OVS-3 | subagent lines in `test/retry.test.ts` |
| CON-2 | open task "add jitter" |
| DEC-3, CON-1, CON-3 | absent or unknown (LLM off) |

Also: `comment.md` has ≤ 5 bullet lines and contains no prompt text; the raw
`.env` value planted in the Claude Code transcript output never appears in
any output file.

## Acceptance

```sh
go test ./internal/e2e/... -count=1
go test ./internal/e2e/... -run E2E -update && git diff --stat internal/e2e/testdata/golden   # only when intentionally updating
```
