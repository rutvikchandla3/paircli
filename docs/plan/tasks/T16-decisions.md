# T16 — DEC-1 Human corrections, DEC-2 Abandoned approaches

**Model:** sonnet · **Wave:** C · **Depends on:** T02 T07 · **Milestone:** M1 (LLM enrichment: T26)

## Read first

- `docs/plan/CONTRACTS.md` engine, testkit, classify; `docs/plan/tasks/T12-intent.md` "Pattern for every detector"
- `docs/SIGNALS.md` rows DEC-1, DEC-2

## Files you own

- `internal/detect/decisions/dec1.go`, `dec2.go`, `decisions_test.go`

## DEC-1 Human corrections

**Correction events** (main agent only):
- `interrupt`;
- `tool_rejected`;
- `permission` with `Decision == "deny"` and `By != "auto"` (auto-mode denials are not human);
- **candidate correction prompts**: `prompt` events (steering or not) with
  text ≤ 400 chars whose lowercased, trimmed text starts with, or contains
  within its first 60 chars, any of: `no,` `no ` `nope` `don't` `dont ` `do not`
  `stop` `revert` `undo` `wrong` `that's not` `thats not` `not what` `instead`
  `why did you` `you broke` `roll back` `rollback` `put it back` `that broke`,
  **and** whose session had at least one `command` or `file_edit` in the
  previous turn (`Turn - 1`; for steering prompts, the current turn).

**What each correction touched** (same session):
- interrupt at turn `t`: non-failed edits with `Turn == t` and TS before the interrupt;
- correction prompt at turn `t`: edits with `Turn == t-1` (steering: `Turn == t`, before it);
- rejection / denial: nothing (the tool never ran).

Anchors = `c.AnchorsFor(those edits)`.

Findings (timeline order):
- interrupt: `` Interrupted at {Clock} while the agent was editing `{f1}`{, `f2`}. `` or `Interrupted at {Clock}.`
- rejection: `` Rejected a `{Tool}` call at {Clock}. ``
- denial: `` Denied a `{Tool}` permission request at {Clock}. ``
- correction prompt: `` Correction at {Clock}: “{Clip(text, 80)}” `` plus ` (changes to `{f1}`)` when anchors exist.
Severity `alert` when the finding has anchors, else `info`. Evidence on the
correction event (+ up to 2 touched edits). Data `{"kind": "interrupt"|"rejection"|"denial"|"correction_prompt", "candidate": true}` (`candidate` only on prompts).

Summary (no prompt text): `{n} human corrections ({i} interrupts, {r} rejections, {p} correction prompts); {k} touched PR lines.`
(omit zero parts inside the parentheses). None → `clear`, `No interrupts, rejections or corrections.`
State `alert` if `k > 0`, else `info` when `n > 0`.
Data `{"interrupts","rejections","denials","correction_prompts","touching_pr"}`.

## DEC-2 Abandoned approaches

Findings from four sources, timeline order, severity `info`:

1. **Reverted edits.** For each non-failed agent edit `E` with ≥2 non-trivial
   `Added` lines: `lost` = its non-trivial added lines (strict) that are
   absent from the PR's added lines for that file **and** appear in the
   `Removed` of a later edit to the same file in the same session. If
   `len(lost) >= 3` or `len(lost) >= 0.5 × added` → reverted. Merge
   consecutive reverted edits of the same session and file.
   `` Code written in `{file}` at {Clock} ({n} lines, turns {a}–{b}) was later removed. ``
   Evidence: first edit and the removing edit.
   Also: a file created (`Op create`) and later deleted (`Op delete`, or an
   `rm` whose `classify.RmTargets` includes it) in the same session →
   `` Created and later deleted `{file}`. ``
2. **Discards.** Commands with class `git_discard` or `git_reset_hard` that come
   after at least one edit in the session → `` Discarded changes with `{ShortCmd}` at {Clock}. ``
3. **Rollbacks.** `reset` with `Type == "rollback"` → `Rolled back the conversation at {Clock}.`
4. **Abandoned branches (Pi).** `reset` with `Type == "branch_switch"` →
   `Left a branch at {Clock}: {Clip(Summary, 120)}`; Data `{"off_branch_edits": n}` =
   edits with `OffBranch` in that session.

Summary: `{n} abandoned attempts ({counts by kind}).` or `clear`, `No reverted code, discards, rollbacks or abandoned branches.`
State `info` when any. Data `{"reverted_edits","created_then_deleted","discards","rollbacks","branches","off_branch_edits"}`.

## Tests

- DEC-1: `TestDEC1_InterruptMapsToTurnEdits`, `TestDEC1_CorrectionPromptPreviousTurn`,
  `TestDEC1_NotCorrectionWithoutPriorAction`, `TestDEC1_KeywordsBoundary`
  ("nothing to add" is not a correction; "No, use Postgres" is),
  `TestDEC1_RejectionAndDenial` (auto denial excluded), `TestDEC1_SummaryHasNoPromptText`, `TestDEC1_Clear`.
- DEC-2: `TestDEC2_RevertedEdit`, `TestDEC2_KeptEditNotReverted` (lines still in the PR),
  `TestDEC2_CreatedThenDeleted`, `TestDEC2_Discard`, `TestDEC2_Rollback`, `TestDEC2_PiBranch`, `TestDEC2_Clear`.

## Acceptance

```sh
go test ./internal/detect/decisions/...
```
