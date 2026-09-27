# T23 — Grounded LLM judge

**Model:** sonnet · **Wave:** C · **Depends on:** T02 T09 · **Milestone:** M3

## Goal

Build a compact fact bundle from the engine context and the deterministic
signals, run four small LLM jobs over it, and return validated
`model.Judgments` in which every item cites bundle ids. Anything uncited is dropped.

## Read first

- `docs/plan/CONTRACTS.md` "internal/judge", "internal/llm", `model.Judgments`
- `docs/SIGNALS.md` (inferred parts: INT-1 condense, INT-3 drift, INT-4
  violations, DEC-1 classification, DEC-2 summaries, DEC-3, FRI-3 lost
  constraints, CON-1, CON-2 caveats, CON-3)

## Files you own

- `internal/judge/bundle.go`, `jobs.go`, `prompts.go`, `validate.go`, `judge.go` + tests + `testdata/`

## Fact bundle (`bundle.go`)

```go
type Bundle struct {
	PR            BundlePR         `json:"pr"`
	Asks          []BundleText     `json:"asks"`
	Decisions     []BundleQA       `json:"decisions"`
	Plans         []BundlePlan     `json:"plans"`
	FinalMessages []BundleText     `json:"final_messages"`
	Corrections   []BundleText     `json:"corrections"`
	Abandoned     []BundleText     `json:"abandoned"`
	Compactions   []BundleText     `json:"compactions"`
	Rules         []BundleText     `json:"rules"`
	Signals       []BundleSignal   `json:"signals"`
	Diff          []BundleDiffFile `json:"diff"`
}
```

Every item has an `id`:
- events: `ev:<session ref>/<event id>` (asks, decisions, plans, final
  messages, correction candidates, compactions, rules);
- signals: `sig:<ID>`;
- hunks: `file:<path>:<newStart>-<newEnd>`.

Contents and limits: PR title, body (`Clip` 4000), files `{path,status,added,removed}`;
asks = main-agent prompts (Clip 1000, with `steering`); decisions = question
QAs; plans (Clip 4000, `approved`); final messages = `Message.Final` events
(Clip 2000); corrections = DEC-1 findings of kind `correction_prompt` (use
their evidence event ids) and interrupts; abandoned = DEC-2 findings (id =
first evidence event); compactions with summaries (Clip 3000); rules =
instruction contents (Clip 3000); signals = every non-unknown signal
`{id, state, summary, findings: first 10 finding summaries}`; diff = up to 30
PR files, each hunk `{id, header, added: first 40 added lines}`.

`BuildBundle(ec, signals, maxChars)` trims until `json.Marshal` fits
`maxChars`, in this order: hunk lines to 10 each → rule contents to 500 →
compaction summaries to 1000 → asks to 300 → diff files to 15 → final
messages to 500. Also returns the set of valid ids.

## Jobs (`jobs.go`, `prompts.go`)

One `llm.Request` per job: `System` = the shared system prompt, `Prompt` =
job instructions + `\n\nFACT BUNDLE:\n` + bundle JSON, `MaxTokens` 4096,
`Model` = `cfg.LLM.Model`. Jobs run sequentially in this order.

Shared system prompt (use verbatim):

```
You review evidence about how a pull request was produced with AI coding agents.
Use only the facts in the JSON fact bundle. Every item you output must cite one
or more ids that appear in the bundle ("id" fields). If the bundle does not
support a statement, leave it out. Do not judge the author's skill, speed or
intent. Reply with a single JSON object that matches the requested schema and
nothing else.
```

In the table, `\|` is an escaped `|` (meaning "or").

| Job | Instructions (summarized; write them out in `prompts.go`) | Output schema |
|---|---|---|
| `claims` | Extract up to 10 checkable claims from `pr.body` and `final_messages` (tests pass, tests added, no behavior change, fixed X, removed Y). For each, decide `supported`, `contradicted` or `no_evidence` using `signals` and `diff`. | `{"claims":[{"claim","source":"pr_body"\|"final_message","verdict","reason","cites":[]}]}` |
| `story` | Condense `asks` into the original ask, refinements and final scope. List up to 8 decisions that shaped the diff (human answers, approved plan, stated reasons). For each `abandoned` item write a one-sentence summary. For each `corrections` item say whether it is really a correction and what it corrected. | `{"ask_summary":{"ask","refinements":[],"final_scope","cites":[]},"decisions":[{"choice","reason","by":"human"\|"agent","cites":[]}],"abandoned":[{"events":[ids],"summary","cites":[]}],"corrections":[{"event":id,"is_correction":bool,"about","cites":[]}]}` |
| `scope` | For each diff hunk, say whether it traces to an ask, plan step or decision. List plan steps with no matching hunk and hunks no plan step covers. List hunks that appear to break a rule in `rules`. | `{"scope":[{"file","lines","traced","trace_to","cites":[]}],"plan_drift":[{"kind":"missing_step"\|"unplanned_change","text","file","lines","cites":[]}],"rule_violations":[{"rule","file","lines","cites":[]}]}` |
| `caveats` | List limitations or assumptions the agent stated in `final_messages`. List constraints from early `asks` that are missing from later `compactions` summaries. | `{"caveats":[{"text","cites":[]}],"lost_constraints":[{"constraint","cites":[]}]}` |

## Validation (`validate.go`)

- Strip a surrounding Markdown code fence; `json.Unmarshal` into the job's
  struct. Invalid → one retry with the prompt `Your previous reply was not
  valid JSON. Reply with only the JSON object.` appended; still invalid →
  append `"<job>: invalid JSON"` to `Judgments.Errors` and continue.
- Drop any item whose `cites` is empty or contains an id not in the bundle.
- `ev:` ids in `events`/`event` fields must be valid event ids → convert to
  `model.EventRef{Session, Event}` (split on the last `/`).
- Enums checked (`verdict`, `source`, `by`, `kind`); invalid → drop the item.
- Clip: claims and decisions text 160 chars, reasons 300, summaries 200.
- `file` values must be PR files; `lines` must look like `N` or `N-M`.

`Run(ctx, p, ec, signals, cfg)`: `p == nil` → `nil, nil`. Returns
`Judgments{Model: cfg.LLM.Model, …}`; a provider error on a job is recorded in
`Errors` and the other jobs still run; returns an error only if every job failed.

## Tests (with `llm.Fake`)

- `TestBuildBundle_IDsAndContent` (testkit context + signals).
- `TestBuildBundle_Budget` — small `maxChars` trims in the documented order and still fits.
- `TestJob_Claims_Valid`, `TestJob_DropsUncited`, `TestJob_DropsUnknownCite`, `TestJob_BadEnumDropped`.
- `TestJob_FencedJSON`, `TestJob_RetryOnInvalidJSON`, `TestJob_InvalidTwiceRecordsError`.
- `TestRun_ProviderErrorContinues`, `TestRun_NilProvider`.
- `TestPrompts_ContainSystemRules` — the system prompt text matches the spec exactly.

## Acceptance

```sh
go test ./internal/judge/...
```
