# T26 — Inferred detectors, enrichment, `--llm`

**Model:** sonnet · **Wave:** D2 · **Depends on:** T23 T24 · **Milestone:** M3

## Goal

Turn `model.Judgments` into the three inferred signals (DEC-3, CON-1, CON-3),
merge the other inferred items into their deterministic signals, and run the
judge from `scan.Run` when an LLM provider is configured.

## Read first

- `docs/plan/CONTRACTS.md` "internal/enrich", "internal/judge", engine
- `internal/judge`, `internal/scan/scan.go` (the `// T26` block)
- `docs/SIGNALS.md` rows with an M3 part

## Files you own

- `internal/detect/decisions/dec3.go`, `internal/detect/consistency/con1.go`, `con3.go` + tests
- `internal/enrich/*.go` + tests
- `internal/scan/scan.go` (only the `// T26` block)
- `internal/e2e/e2e_llm_test.go` + `internal/e2e/testdata/golden_llm/**`

## Citation helpers (`internal/enrich/cite.go`, exported for the detectors)

```go
// Evidence turns "ev:<ref>/<event>" cites into Evidence (via c.Session and the event ID); other cites are skipped.
func Evidence(c *engine.Context, cites []string) []model.Evidence
// Anchors turns "file:<path>:<a>-<b>" cites and explicit file/lines fields into Anchors.
func Anchors(file, lines string, cites []string) []model.Anchor
```

## Inferred detectors

All three: `c.Judgments == nil` → `engine.Unknown(id, "LLM pass is off. Run with --llm to fill this in.")`.

- **DEC-3 Decision trail:** finding per `Decisions` item, info:
  `{choice} — {reason} ({by})`, evidence from cites. Summary
  `{n} decisions shaped this diff; first: {Clip(choice, 80)}.`; none → `clear`, `No decisions were identified.`
- **CON-1 Claim check:** finding per claim: contradicted → alert
  `“{Clip(claim,80)}” is contradicted: {reason}`; supported → info
  `“{claim}” is supported.`; no_evidence → info `“{claim}”: no evidence either way.`
  Summary `{c} claims contradicted, {s} supported, {u} without evidence.`;
  State alert if any contradicted; no claims → `clear`, `No checkable claims found.`
- **CON-3 Scope match:** untraced items → alert findings
  `` `{file}` {lines} does not trace to the ask, plan or a decision. `` with anchors;
  Summary `{t} of {n} hunks trace to the ask or plan; {u} do not.`; all traced → `clear`.

## `enrich.Apply(c, signals) []model.Signal`

Returns a copy; `c.Judgments == nil` → signals unchanged. Added findings have
`Provenance: inferred`. Update the signal's State to `alert` where noted, and
append ` (+{k} inferred)` to the Summary only if it stays ≤160 characters.

| Signal | From | Change |
|---|---|---|
| INT-1 | `AskSummary` | prepend finding `Ask (condensed): {Clip(ask,120)}`; `Data["ask_summary"]` |
| INT-3 | `PlanDrift` | finding per item (`missing_step` → `Planned step with no matching change: …`; `unplanned_change` → `` Change not in the plan: `{file}` {lines} ``); State alert if any |
| INT-4 | `RuleViolations` | alert finding `` `{file}` {lines} may break the rule “{Clip(rule,80)}”. ``; State alert |
| DEC-1 | `Corrections` | match by `Event` to candidate findings: `is_correction:false` → severity info and `Data["confirmed"]=false`; true → `Data["confirmed"]=true`, append ` ({about})` to the finding summary. Recompute State/Summary with only confirmed-or-unreviewed candidates |
| DEC-2 | `Abandoned` | add each summary to the finding whose first evidence matches, as `Data["summary"]`; unmatched → new info finding |
| FRI-3 | `LostConstraints` | alert finding `Constraint missing after compaction: “{Clip(c,100)}”`; State alert |
| CON-2 | `Caveats` | info finding `Agent noted: “{Clip(text,120)}”` |

Record `Judgments.Errors` in each touched signal's `Data["llm_errors"]` when non-empty.

## Wiring in `scan.Run` (`// T26` block)

```go
if o.LLM != nil {
	j, err := judge.Run(ctx, o.LLM, ec, sigs, cfg)
	if err != nil { /* keep deterministic output; add a note to the report footer via Data on AUTH-2 */ }
	ec.Judgments = j
	inferred := engine.RunIDs(ec, "DEC-3", "CON-1", "CON-3")
	sigs = replaceByID(sigs, inferred)
	sigs = enrich.Apply(ec, sigs)
}
```

## Tests

- Detectors: `TestDEC3_*`, `TestCON1_Verdicts`, `TestCON3_Untraced`, each with a nil-Judgments unknown case.
- Enrich: one test per table row; `TestApply_NilJudgments`; `TestApply_SummaryLengthKept`.
- `TestE2E_WithLLM`: T25's scenario with `llm.Fake` replies from `testdata/golden_llm/replies/*.json`, golden outputs in `testdata/golden_llm/`.

## Acceptance

```sh
go test ./internal/detect/... ./internal/enrich/... ./internal/e2e/...
```
