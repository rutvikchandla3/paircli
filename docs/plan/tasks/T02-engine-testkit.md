# T02 — Engine, catalog, testkit, detector skeletons

**Model:** sonnet · **Wave:** A · **Depends on:** T01 · **Milestone:** M1

## Goal

Build the detector engine every signal plugs into, the test builders every
detector test uses, and empty packages so later tasks never edit shared files.

## Read first

- `docs/plan/CONTRACTS.md` sections "internal/engine" and "internal/testkit"
- `docs/SIGNALS.md` (catalog and harness support tables)
- `internal/model/*.go`

## Files you own

- `internal/engine/*.go`
- `internal/testkit/*.go`
- `internal/detect/{intent,authorship,verification,decisions,friction,exposure,oversight,consistency}/doc.go`
- `cmd/paircli/detectors.go`

## 1. `internal/engine/catalog.go`

Use exactly this content (it compiles against `internal/model`):

```go
package engine

import "github.com/rutvikchandla3/paircli/internal/model"

// Meta is the fixed catalog metadata for one signal (docs/SIGNALS.md).
type Meta struct {
	ID         string
	Question   model.ReviewQuestion
	Title      string
	Priority   model.Priority
	Provenance model.Provenance
}

var catalog = []Meta{
	{"INT-1", model.QIntent, "Ask ledger", model.P0, model.Derived},
	{"INT-2", model.QIntent, "Human-answered decisions", model.P0, model.Recorded},
	{"INT-3", model.QIntent, "Approved plan and plan drift", model.P0, model.Recorded},
	{"INT-4", model.QIntent, "Rules in effect", model.P1, model.Recorded},
	{"AUTH-1", model.QAuthorship, "Line-level authorship", model.P0, model.Derived},
	{"AUTH-2", model.QAuthorship, "Session-commit map and coverage", model.P0, model.Derived},
	{"AUTH-3", model.QAuthorship, "Model mix and disclosure trailer", model.P1, model.Recorded},
	{"VER-1", model.QVerification, "Verification log", model.P0, model.Recorded},
	{"VER-2", model.QVerification, "Verification freshness", model.P0, model.Derived},
	{"VER-3", model.QVerification, "Check-to-change coverage", model.P1, model.Derived},
	{"VER-4", model.QVerification, "Test integrity", model.P0, model.Derived},
	{"VER-5", model.QVerification, "Quality-gate bypasses", model.P0, model.Derived},
	{"VER-6", model.QVerification, "Runtime and visual evidence", model.P1, model.Recorded},
	{"DEC-1", model.QDecisions, "Human corrections", model.P0, model.Recorded},
	{"DEC-2", model.QDecisions, "Abandoned approaches", model.P1, model.Derived},
	{"DEC-3", model.QDecisions, "Decision trail", model.P1, model.Inferred},
	{"FRI-1", model.QFriction, "Churn hotspots", model.P1, model.Derived},
	{"FRI-2", model.QFriction, "Loops and unresolved errors", model.P1, model.Derived},
	{"FRI-3", model.QFriction, "Context resets", model.P1, model.Recorded},
	{"FRI-4", model.QFriction, "Knowledge lookups", model.P2, model.Recorded},
	{"EXP-1", model.QExposure, "Side-effect ledger", model.P0, model.Derived},
	{"EXP-2", model.QExposure, "Dependency provenance", model.P0, model.Derived},
	{"EXP-3", model.QExposure, "Untrusted-input chain", model.P0, model.Derived},
	{"EXP-4", model.QExposure, "Secret contact", model.P1, model.Derived},
	{"OVS-1", model.QOversight, "Autonomy envelope", model.P0, model.Recorded},
	{"OVS-2", model.QOversight, "Human touchpoints", model.P1, model.Derived},
	{"OVS-3", model.QOversight, "Delegated work", model.P2, model.Recorded},
	{"CON-1", model.QConsistency, "Claim check", model.P0, model.Inferred},
	{"CON-2", model.QConsistency, "Open loops", model.P1, model.Recorded},
	{"CON-3", model.QConsistency, "Scope match", model.P1, model.Inferred},
}

// support[id][harness] = {transcript-only level, hooked level}; see docs/SIGNALS.md "Harness support".
type level [2]string

const (
	full    = "full"
	partial = "partial"
	none    = "none"
)

var (
	ff = level{full, full}
	pf = level{partial, full}
	pp = level{partial, partial}
	np = level{none, partial}
)

func row(cc, cx, pi level) map[model.Harness]level {
	return map[model.Harness]level{model.HarnessClaudeCode: cc, model.HarnessCodex: cx, model.HarnessPi: pi}
}

var support = map[string]map[model.Harness]level{
	"INT-1": row(ff, ff, ff), "INT-2": row(ff, ff, pf), "INT-3": row(ff, pp, np), "INT-4": row(pf, ff, pf),
	"AUTH-1": row(ff, pf, pf), "AUTH-2": row(pf, pf, pf), "AUTH-3": row(ff, ff, ff),
	"VER-1": row(ff, ff, pf), "VER-2": row(ff, ff, pf), "VER-3": row(pp, pp, pp),
	"VER-4": row(ff, ff, ff), "VER-5": row(ff, ff, ff), "VER-6": row(ff, pp, pp),
	"DEC-1": row(ff, ff, pf), "DEC-2": row(pf, pp, ff), "DEC-3": row(ff, ff, pp),
	"FRI-1": row(ff, ff, ff), "FRI-2": row(ff, ff, ff), "FRI-3": row(ff, ff, ff), "FRI-4": row(ff, ff, ff),
	"EXP-1": row(ff, ff, ff), "EXP-2": row(ff, ff, ff), "EXP-3": row(ff, ff, ff), "EXP-4": row(ff, ff, ff),
	"OVS-1": row(ff, ff, pf), "OVS-2": row(ff, ff, ff), "OVS-3": row(ff, pf, pp),
	"CON-1": row(ff, ff, pp), "CON-2": row(ff, ff, pp), "CON-3": row(ff, ff, pp),
}

// Catalog returns the 30 signals in catalog order.
func Catalog() []Meta { return append([]Meta(nil), catalog...) }
```


## 2. `internal/engine/engine.go`

Implement the API in CONTRACTS.md:

- `NewContext`: copies the slices it receives into the Context (don't keep the
  caller's backing arrays), builds `Timeline` from every event of every
  session, sorted by `(TS, Session.Ref(), Seq)`. `Attribution` nil → empty
  `&model.Attribution{}`. `cfg` nil → `config.Default()`.
- `Ref(it)` → `model.EventRef{Session: it.S.Ref(), Event: it.E.ID}`.
- `Evidence(it, excerpt)` → `model.Evidence{Session, Event, TS, Excerpt: redact.Text(model.Clip(oneLine(excerpt), 200))}`
  where `oneLine` replaces newlines and tabs with spaces and collapses runs of spaces.
- `AnchorsFor(items...)` → `Attribution.AnchorsFor` over their refs.
- `Of(kinds...)` → timeline items whose `E.Kind` is in kinds, preserving order.
- `IsPRFile(rel)` → `PR.File(rel) != nil`.
- `HasSessions()` → `len(Sessions) > 0`.
- `Session(ref)` → the linked session with that `Ref()`, or nil.
- `SessionLinks` is an exported field; `NewContext` leaves it nil and the scan pipeline sets it.
- Helpers in `internal/engine/format.go`: `Clock(t)` → `t.UTC().Format("15:04") + " UTC"`;
  `Plural(n, one, many)` → `"1 edit"`, `"3 edits"`; `Pct(part, whole)` → rounded percent
  string, `"0%"` when whole is 0; `Home(path)` → replaces `os.UserHomeDir()` prefix with `~`.
- Registry: package-level slice + map. `Register` panics on a duplicate ID or
  an ID not in the catalog. `Detectors()` returns them in catalog order.
- `Run(c)`: for each registered detector in catalog order call `Detect` inside a
  `recover`; a panic becomes `Unknown(id, "Detector failed: <panic value>")`
  with `Data["error"]`. Force `sig.ID = d.ID()` and copy catalog
  metadata over whatever the detector set for Question/Title/Priority/Provenance.
  Fill `Support`: for each harness present in `c.Sessions`, use
  `support[id][h][1]` if any session of that harness has `Capture == hooked`,
  else `[0]`. Catalog IDs without a registered detector are **omitted**.
- `RunIDs(c, ids...)` → same, restricted to those IDs, in catalog order.
- `NewSignal(id)` → Signal with catalog metadata and `State: model.StateClear`; panics for unknown IDs.
- `Unknown(id, reason)`, `NoSessions(id)` as documented.

## 3. `internal/testkit`

Implement the builders in CONTRACTS.md. Details:
- Defaults: `Harness` as given, `ID` as given, `CWD` and `RepoRoot` `/repo`,
  `RepoRemote` `acme/shop`, `Branch` `feat/test`, `Capture` reconstructed,
  `SourcePath` `/fixtures/<id>.jsonl`, model `test-model`.
- Clock starts at `2026-09-27T14:00:00Z`; every event advances it one minute
  **after** being stamped. `At("14:30")` sets the clock for the next event.
- `Edit(rel, added...)`: `file_edit` with `Path "/repo/"+rel`, `RelPath rel`,
  `Op update`, `Via "edit"`, `Added` as given, one hunk whose lines are the
  added lines prefixed `+`. `Replace(rel, removed, added)` adds `-` lines too.
  `Create` sets `Op create`, `Via "write"`.
- `Read(rel)`: `file_read` with Path/RelPath.
- `Run(cmd, exit, output)`: `Status ok` if exit==0, `failed` if >0, `unknown`
  with nil `ExitCode` if exit<0. `UserRun` sets `ByUser`.
- `Add(e)`: fills zero `ID` (`e<n>`), `TS` (clock), `Origin` (transcript), `Model`
  (current model for message/command/edit/tool events), `AgentID` (current agent).
- `Build()` calls `Finalize()` and returns the session.
- `PR(repo, n)`: `URL https://github.com/<repo>/pull/<n>`, `Title "Test PR"`,
  `HeadRef feat/test`, `BaseRef main`. `Add(path, start, lines...)` appends a
  hunk of added lines numbered from `start` (creating the file with status
  `modified` if new). `Remove` appends deleted lines. `Commit(sha, hhmm)` adds
  a commit at that time on 2026-09-27 with no files.
- `ExactAttribution`: for every added PR line (in file order): trivial if
  `model.IsTrivialLine`; else the **latest** non-failed `file_edit` (by
  timeline order) whose `RelPath` equals the file and whose `Added` contains
  the line after `NormalizeLine` → `agent` with `Source`, `Model`, `AgentID`;
  else an `external_edit` on that file containing it → `human_in_session`;
  else `uncaptured`. Fill `Counts`, `Total`, `Explained`.
- `Ctx(pr, sessions...)` → `engine.NewContext(pr, sessions, ExactAttribution(pr, sessions), nil, config.Default())`.
- `Find(sigs, id)` → pointer to the matching signal or nil.

## 4. Skeleton packages

Create `internal/detect/<q>/doc.go` for each of the 8 questions with only:

```go
// Package intent holds the detectors for the "What was actually asked for?" question (INT-*).
package intent
```

(adjust package name and text per question). Create `cmd/paircli/detectors.go`:

```go
package main

// Detector packages register themselves in init(). Keep this list complete.
import (
	_ "github.com/rutvikchandla3/paircli/internal/detect/authorship"
	_ "github.com/rutvikchandla3/paircli/internal/detect/consistency"
	_ "github.com/rutvikchandla3/paircli/internal/detect/decisions"
	_ "github.com/rutvikchandla3/paircli/internal/detect/exposure"
	_ "github.com/rutvikchandla3/paircli/internal/detect/friction"
	_ "github.com/rutvikchandla3/paircli/internal/detect/intent"
	_ "github.com/rutvikchandla3/paircli/internal/detect/oversight"
	_ "github.com/rutvikchandla3/paircli/internal/detect/verification"
)
```

## Tests

`internal/engine/engine_test.go`
- `TestCatalog_30UniqueIDs` — 30 entries, unique IDs, every ID has a support row.
- `TestNewContext_TimelineOrder` — two sessions with interleaved timestamps produce a merged, ordered timeline; ties broken by session ref then Seq.
- `TestEvidence_ClipsAndFlattens` — newlines removed, 250-rune excerpt clipped to 200 + "…".
- `TestRun_RecoversPanic` — a test detector that panics yields `State unknown` and `Data["error"]`.
- `TestRun_ForcesMetadata` — a detector returning a wrong Title/Priority is corrected.
- `TestRun_Support` — Claude Code session hooked, Codex reconstructed → `AUTH-2` support `{"claude-code":"full","codex":"partial"}`.
- `TestRegister_DuplicatePanics` and `TestRegister_UnknownPanics` (use a fresh registry via an unexported reset helper in `export_test.go`).
- `TestRunIDs_Subset`.
- `TestFormatHelpers` — Clock converts a +05:30 time to UTC; Plural singular/plural; Pct(1,3)=="33%", Pct(0,0)=="0%"; Home.

`internal/testkit/testkit_test.go`
- `TestSessionBuilder_ClockAndTurns` — `At`, default +1 minute, turns via Finalize.
- `TestExactAttribution` — agent line, human (external edit) line, uncaptured line, trivial line, failed edit ignored, later edit wins for Source.
- `TestPRBuilder_LineNumbers` — `Add("a.go", 10, "x", "y")` gives NewNo 10 and 11.

## Acceptance

```sh
go build ./... && go vet ./... && go test ./internal/engine/... ./internal/testkit/...
```
