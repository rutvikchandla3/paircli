# T12 — Intent detectors: INT-1, INT-2, INT-3, INT-4

**Model:** haiku · **Wave:** C · **Depends on:** T02 T07 · **Milestone:** M1

## Goal

Four detectors that show what was asked, what a human decided, the approved
plan, and which rule files were in effect.

## Read first

- `docs/plan/CONTRACTS.md` "internal/engine" (Detector rules) and "internal/testkit"
- `docs/plan/README.md` "Conventions" (signal text, prompt-text rule)
- `docs/SIGNALS.md` rows INT-1..4

## Files you own

- `internal/detect/intent/int1.go`, `int2.go`, `int3.go`, `int4.go`
- `internal/detect/intent/intent_test.go`

## Pattern for every detector

```go
package intent

import (
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

type int1 struct{}

func init() { engine.Register(int1{}) }

func (int1) ID() string { return "INT-1" }

func (d int1) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())
	// ...
	return sig
}
```

Only main-agent events count (`E.AgentID == ""`) unless stated.

## INT-1 Ask ledger

- Items: `c.Of(model.KindPrompt)` with `AgentID == ""`.
- Finding per prompt (timeline order), severity `info`:
  - text = prompt text with newlines collapsed; for slash commands `"<SlashCommand> <Text>"`.
  - Summary: `model.Clip(text, 150)`, prefixed `(mid-task) ` when `Steering`.
  - Evidence: `c.Evidence(it, text)`.
  - Data: `{"session": ref, "turn": n, "steering": bool, "slash_command": name}`.
- Summary: `{n} prompts across {s} sessions; first: “{Clip(first text, 80)}”.`
  and, when steering prompts exist, append ` {k} sent mid-task.` if it fits in 160 chars.
- No prompts → State `info`, Summary `No human prompts were recorded.`
- State `info`. Data: `{"prompts": n, "steering": k, "sessions": s}`.

## INT-2 Human-answered decisions

- Items: `question` events; each `QA` is one decision.
- Finding per QA, severity `info`: `Q: {Clip(question,100)} → {Clip(answer,80)}`;
  empty answer → `Q: {…} (no answer recorded)`. Evidence on the question event. Data `{"header", "options"}`.
- Summary: `{n} decisions made by a human, latest at {Clock}.` or State `clear`, `The agent asked no questions.`
- State `info` when any. Data: `{"count": n}`.

## INT-3 Approved plan (capture only; drift arrives with T26)

- Items: `plan` events.
- Finding per plan: `Plan approved at {Clock} ({lines} lines).` /
  `Plan rejected at {Clock}.` / `Plan proposed at {Clock} (approval not recorded).`
  where lines = number of non-empty lines of `Text` (or steps count, "steps").
  Severity info. Evidence excerpt = first line of the plan.
- Summary: latest approved plan: `Plan approved at {Clock} in {ref}; {k} plans proposed in total.`;
  no approved plan but plans exist: `{k} plans proposed; none recorded as approved.`;
  none: State `clear`, `No plan was recorded.`
- Data: `{"plans":[{"session","event","approved": true|false|null,"source","text": Clip(Text, 4000)}]}`.

## INT-4 Rules in effect

- Items: `instructions` events. Deduplicate by `(display path, Hash)` where
  display path = `RelPath` if set, else `engine.Home(Path)`.
- Finding per file: `` Loaded `{display}` ({scope}): {Clip(FirstLine, 80)} `` (omit
  the colon part when FirstLine is empty). Severity info. Evidence on the first load.
- Summary: `{n} instruction files in effect: {first 3 display paths joined by ", "}{", …" if more}.`
  None: State `info`, `No instruction files were recorded.`
- Data: `{"files":[{"path","scope","hash","sessions":[refs]}]}`.

## Tests (`intent_test.go`, testkit)

- `TestINT1_OrderSteeringSlash` — two sessions, a steering prompt, a slash command; summary and findings.
- `TestINT1_SubagentPromptsIgnored`.
- `TestINT1_NoSessions`.
- `TestINT2_Answered` and `TestINT2_Unanswered`, `TestINT2_None`.
- `TestINT3_ApprovedRejectedUnknown` — three plans; summary uses the approved one.
- `TestINT4_DedupeAndHome` — same file loaded twice → one finding; a home-dir path shown with `~`.
- Every test asserts `len(Summary) <= 160`.

## Acceptance

```sh
go test ./internal/detect/intent/...
```
