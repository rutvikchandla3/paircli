# T17 — FRI-1 Churn hotspots, FRI-2 Loops and unresolved errors, FRI-3 Context resets

**Model:** haiku · **Wave:** C · **Depends on:** T02 T07 · **Milestone:** M1

## Read first

- `docs/plan/CONTRACTS.md` engine, testkit, classify; `docs/plan/tasks/T12-intent.md` "Pattern for every detector"
- `docs/SIGNALS.md` rows FRI-1..3

## Files you own

- `internal/detect/friction/fri1.go`, `fri2.go`, `fri3.go`, `friction_test.go`

A command is a **check** when `classify.CheckClass(classify.Command(cmd, c.Config.ExtraChecks))` returns true.

## FRI-1 Churn hotspots

- For each PR file: `edits` = non-failed `file_edit` events with that `RelPath`;
  `cycles` = number of those edits that are followed, in the same session and
  before the next edit to the same file, by a check command.
- `median` = median of `edits` over PR files with ≥1 edit (use the lower middle for even counts; minimum 1).
- Hotspot: `edits >= 5` and `edits >= 3 × median`.
- Findings for hotspots, sorted by edits desc, max 5, severity info:
  `` `{file}`: {edits} edits, {Plural(cycles,"edit-then-check cycle","edit-then-check cycles")}. `` anchor `{File}`.
- Summary: `` Most rework: `{top}` ({edits} edits). Other files: {max other} edits or fewer. `` ;
  no hotspot → `clear`, `No file stood out: at most {max} edits per file.`
- State `info` when hotspots exist. Data `{"files":[{"path","edits","cycles"}]}` for every PR file with edits, sorted by edits desc.

## FRI-2 Loops and unresolved errors

Normalize a command with `classify.ShortCmd`. Per session:
- **Loop:** a run of ≥3 failing executions of the same normalized command with
  no successful execution of it in between →
  `` `{cmd}` failed {n} times in a row ({Clock first}–{Clock last}). `` severity alert.
- **Unresolved check:** a failing check command whose normalized form never
  succeeds later in the timeline (any session) →
  `` `{cmd}` was still failing at the end ({Clock}). `` severity alert. Report each command once.
- **Timeouts:** `Status == timeout` → `` `{cmd}` timed out at {Clock}. `` info.
- **Harness failures:** `failure` events → `Turn cut short by {Type} at {Clock}.` info.

Summary: `{l} repeated failures, {u} checks still failing at the end, {t} timeouts.` (omit zero parts; keep at least one);
none → `clear`, `No repeated failures, unresolved check failures or timeouts.`
State `alert` if loops or unresolved > 0, else `info` if timeouts/failures, else `clear`.
Data `{"loops","unresolved","timeouts","failures"}`.

## FRI-3 Context resets

- Resets: `compaction` events and `reset` events (all types), per session.
- For each reset: `after` = non-failed edits in the same session with TS after the reset;
  anchors = `c.AnchorsFor(after…)`.
- Finding per reset, info: compaction → `Context compacted ({Trigger or "unknown trigger"}) at {Clock}; {k} PR files edited afterwards.`;
  reset → `Session {Type} at {Clock}; {k} PR files edited afterwards.` where
  `k` = distinct files among the anchors. Evidence on the reset event.
- Summary: `{n} context resets ({kinds}); {lines} PR lines were written after a reset.`
  (lines = number of attribution lines whose Source is in any `after` set);
  none → `clear`, `No compactions, clears, resumes or forks.`
- State `info` when any. Data `{"resets": n, "lines_after": lines, "by_type": {...}}`.

## Tests

- FRI-1: `TestFRI1_Hotspot`, `TestFRI1_Cycles`, `TestFRI1_NoHotspot`, `TestFRI1_FailedEditsIgnored`.
- FRI-2: `TestFRI2_LoopOfThree`, `TestFRI2_LoopBrokenBySuccess`, `TestFRI2_UnresolvedCheck`, `TestFRI2_ResolvedInLaterSession`, `TestFRI2_NonCheckFailureNotUnresolved` (a failing `grep` is ignored), `TestFRI2_TimeoutAndFailure`.
- FRI-3: `TestFRI3_CompactionThenEdits`, `TestFRI3_ResetTypes`, `TestFRI3_None`.

## Acceptance

```sh
go test ./internal/detect/friction/... -run 'FRI1|FRI2|FRI3'
```
