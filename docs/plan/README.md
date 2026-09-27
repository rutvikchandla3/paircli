# paircli v2 build plan

This directory is a complete, executable plan for building the v2 signal
pipeline described in [`../SIGNALS.md`](../SIGNALS.md). It is written so that
an orchestrator can hand each task to a Sonnet or Haiku subagent with no other
context.

| File | What it is |
|---|---|
| `README.md` (this file) | Scope, architecture, task graph, orchestration protocol, conventions, definition of done |
| [`CONTRACTS.md`](CONTRACTS.md) | Frozen types and every cross-package API. Read before any task. |
| [`_contracts/`](_contracts/) | Compiled Go source for `internal/model`, `internal/config`, `internal/redact`. T01 copies it. Go ignores `_`-prefixed dirs. |
| [`formats/`](formats/) | Field-level references for Claude Code, Codex and Pi transcripts (synthetic examples, real structure) |
| [`tasks/`](tasks/) | One self-contained spec per task (T01–T28) |

## Scope

**In scope:** the 30 signals in `SIGNALS.md`; transcript parsers for Claude
Code, Codex CLI and Pi; hook capture for all three; PR/commit correlation;
line attribution; `signals.json`, `report.md`, `comment.md`,
`authorship.json`; optional grounded LLM pass; Agent Trace export.

**Out of scope (deferred by product decision):**
- **Redaction.** Do not implement secret scrubbing of stored or posted text.
  The seam exists: `internal/redact.Text` is a no-op that every renderer and
  every `Evidence.Excerpt` must call. Compensating defaults until redaction
  lands: `comment.md` omits prompt text unless `comment.include_prompts` is
  true; the LLM pass is off unless configured; `.paircli/` stays gitignored.
  EXP-4 (secret *contact* detection) is in scope; it reports masked matches.
- Any UI beyond Markdown/JSON files and one PR comment.
- Harnesses other than Claude Code, Codex CLI and Pi.

## Architecture

```
paircli scan <pr>   →   scan.Run (internal/scan)                                  T24
  1  pr.Fetch                     → model.PR (+ per-commit patches)                 T06
  2  config.Load(repo root)       → *config.Config                                  T01
  3  link.Discover(cfg, window)   → []*model.Session                                T10
       claudecode|codex|pi .Discover/.ParseFile                                     T03 T04 T05
       hooklog.Read + hooklog.Merge  (+ pi.HookRecords)                             T08 T05
  4  link.Select                  → candidate sessions, RelPath/RepoRoot filled     T10
  5  attrib.Attribute             → *model.Attribution                              T11
     attrib.CommitSessions        → commit SHA → session refs
  6  link.Finalize                → linked sessions, []model.CommitLink             T10
  7  engine.NewContext + engine.Run → []model.Signal (27 deterministic detectors)   T02 T11–T19
  8  (--llm) judge.Run → ctx.Judgments; engine.RunIDs(DEC-3, CON-1, CON-3);
     enrich.Apply(ctx, signals)                                                     T23 T26
  9  render.BuildReport + render.Write → .paircli/pr-<n>/…                          T22
 10  (--post) upsert one PR comment via gh                                          T24

paircli hook <harness> <Event>   (invoked by Claude Code / Codex hooks)            T08 T20
Pi extension ~/.pi/agent/extensions/paircli.ts → pi.appendEntry("paircli", …)      T21
paircli hook install|uninstall <harness>, paircli doctor                           T08 T20 T21 T24
```

### Package map

| Package | Owner task | Purpose |
|---|---|---|
| `internal/model` | T01 | Frozen types (copied from `_contracts/`) |
| `internal/config` | T01 | `.paircli.json` + defaults |
| `internal/redact` | T01 | No-op redaction seam |
| `internal/engine` | T02 | Detector registry, `Context`, catalog metadata, support table |
| `internal/testkit` | T02 | Builders for sessions, PRs and contexts in tests |
| `internal/detect/<question>` | T11–T19, T26 | One file per signal, self-registering via `init()` |
| `internal/claudecode` | T03 (transcript), T08 (hooks) | Claude Code parser + hook install/handler |
| `internal/codex` | T04 (rollout), T20 (hooks) | Codex parser + hooks |
| `internal/pi` | T05 (session), T21 (extension) | Pi parser + extension installer |
| `internal/pr`, `internal/diff` | T06 | `gh` fetch + unified-diff parser |
| `internal/classify` | T07 | Command/path/check/secret classification |
| `internal/hooklog` | T08 | Hook record log: append, read, merge into sessions, snapshots |
| `internal/llm` | T09 | LLM providers (Anthropic API, `claude` CLI, fake) |
| `internal/link` | T10 | Discovery, selection, path normalization, commit linking |
| `internal/attrib` | T11 | Line attribution |
| `internal/render` | T22 | Report building and file output |
| `internal/judge` | T23 | Fact bundle, prompts, validation → `model.Judgments` |
| `internal/enrich` | T26 | Merge judgments into deterministic signals |
| `internal/agenttrace` | T27 | Agent Trace export |
| `internal/scan` | T24 (T26, T27 add small blocks) | The pipeline as a library; the CLI and e2e tests call `scan.Run` |
| `internal/e2e` | T25 (T26 adds the LLM variant) | Golden end-to-end scenario |
| `internal/gitinfo` | existing; T08 adds `head.go`, T10 adds `toplevel.go` | Git helpers |
| `cmd/paircli` | T02 (`detectors.go`), T08 (`hook.go`), T24 (rest), T27 (one flag) | CLI |

Legacy code (`internal/session`, `internal/output`, `internal/correlate`,
`internal/claudecode/pathb.go`, the Codex/Pi `ScanPathB` stubs) stays
compiling until **T24**, which removes it. No other task deletes legacy code.

## Milestones

| Milestone | Ships | Tasks |
|---|---|---|
| **M1** Deterministic report, no hooks needed | `paircli scan` with all derived/recorded signals from transcripts | T01–T07, T10–T19, T22, T24, T25 |
| **M2** Hooks | Exact SHA links, human-edit detection, permission and rule events | T08, T20, T21 |
| **M3** Grounded judgment + interop | `--llm`, inferred signals, Agent Trace | T09, T23, T26, T27 |
| Docs | README/ARCHITECTURE refresh | T28 |

Milestones are about what ships. Waves (below) are about what can run in
parallel; M2/M3 tasks start early where they have no dependencies.

## Task graph

| ID | Task | Model | Wave | Depends on |
|---|---|---|---|---|
| [T01](tasks/T01-contracts.md) | Install contracts + helper tests | haiku | A | — |
| [T02](tasks/T02-engine-testkit.md) | Engine, catalog, testkit, detector skeletons | sonnet | A | T01 |
| [T03](tasks/T03-claude-code-parser.md) | Claude Code transcript parser | sonnet | B | T01 |
| [T04](tasks/T04-codex-parser.md) | Codex rollout parser | sonnet | B | T01 |
| [T05](tasks/T05-pi-parser.md) | Pi session parser | sonnet | B | T01 |
| [T06](tasks/T06-pr-diff.md) | PR fetch + unified-diff parser | sonnet | B | T01 |
| [T07](tasks/T07-classify.md) | Classifiers | haiku | B | T01 |
| [T08](tasks/T08-hooklog-claude-hooks.md) | Hook log + Claude Code hooks v2 | sonnet | B | T01 |
| [T09](tasks/T09-llm-providers.md) | LLM providers | sonnet | B | T01 |
| [T10](tasks/T10-link.md) | Discovery, selection, linking | sonnet | C | T02 T03 T04 T05 T06 T08 |
| [T11](tasks/T11-attribution.md) | Attribution + AUTH-1, AUTH-2 | sonnet | C | T02 T06 T07 |
| [T12](tasks/T12-intent.md) | INT-1..4 | haiku | C | T02 T07 |
| [T13](tasks/T13-ver-1-2.md) | VER-1, VER-2 | sonnet | C | T02 T07 |
| [T14](tasks/T14-ver-3-4-5.md) | VER-3, VER-4, VER-5 | sonnet | C (after T13) | T02 T07 T13 |
| [T15](tasks/T15-ver6-fri4.md) | VER-6, FRI-4 | haiku | C | T02 T07 |
| [T16](tasks/T16-decisions.md) | DEC-1, DEC-2 | sonnet | C | T02 T07 |
| [T17](tasks/T17-friction.md) | FRI-1, FRI-2, FRI-3 | haiku | C | T02 T07 |
| [T18](tasks/T18-exposure.md) | EXP-1..4 | sonnet | C | T02 T07 |
| [T19](tasks/T19-oversight-auth3-con2.md) | OVS-1..3, AUTH-3, CON-2 | haiku | C | T02 T07 |
| [T20](tasks/T20-codex-hooks.md) | Codex hooks | sonnet | C | T08 |
| [T21](tasks/T21-pi-extension.md) | Pi extension + installer | sonnet | C | T05 T08 |
| [T22](tasks/T22-render.md) | Report building + renderers | sonnet | C | T02 |
| [T23](tasks/T23-judge.md) | Grounded LLM judge | sonnet | C | T02 T09 |
| [T24](tasks/T24-scan-integration.md) | Scan integration, doctor, `--post`, legacy removal | sonnet | D | all of A–C |
| [T25](tasks/T25-e2e-golden.md) | End-to-end golden scenario | sonnet | D2 | T24 |
| [T26](tasks/T26-inferred-enrich.md) | Inferred detectors, enrichment, `--llm` | sonnet | D2 | T23 T24 |
| [T27](tasks/T27-agent-trace.md) | Agent Trace export | sonnet | E | T11 T22 T24 T26 |
| [T28](tasks/T28-docs.md) | Docs refresh | haiku | E | T24–T27 |

Waves: **A** (sequential: T01 then T02) → **B** (7 in parallel) → **C** (13 in
parallel; T14 starts when T13 is merged) → **D** (T24) → **D2** (T25, T26 in
parallel) → **E** (T27 then T28).

## Orchestration protocol

For the orchestrator (a Claude session with the Agent tool):

1. Create an integration branch `v2` from `main`. All task branches merge into it.
2. For each wave, spawn one subagent per task with `isolation: "worktree"`,
   `model` from the table, and the prompt template below. Run a wave's tasks
   in parallel; never start a task before its dependencies are merged into `v2`.
3. When a subagent reports done, in its worktree:
   - check `git diff --stat v2...HEAD` touches only the task's **Files you own**;
   - run `gofmt -l .` (must print nothing), `go vet ./...`, `go test ./...`;
   - skim the tests against the task's **Tests** list.
   Then merge into `v2` (`git merge --no-ff`), rerun `go build ./... && go test ./...`
   on `v2`. If anything fails, send the failure back to the same subagent
   (SendMessage) rather than fixing it yourself.
4. Haiku tasks get a review pass before merge: a Sonnet subagent reads the
   task spec and the diff and lists deviations. Fix via the original subagent.
5. **Contract gaps.** Subagents may not edit `internal/model`, `internal/config`,
   `internal/redact`, `internal/engine` (after T02), or anything in
   `docs/plan/`. If a task cannot be done within the contracts, the subagent
   stops and reports `CONTRACT GAP: <what> — <why> — <proposed change>`.
   The orchestrator decides, makes the change on `v2` (updating
   `_contracts/` and `CONTRACTS.md` to match), and resumes the task.
6. After each wave: `go test ./...` on `v2` and a one-paragraph status note.
7. Nothing is pushed or opened as a PR without the user's go-ahead.

### Subagent prompt template

```
You are implementing task {ID} of paircli, a Go CLI in this repository.

Read, in this order, before writing code:
  1. docs/plan/README.md  — sections "Conventions" and "Definition of done"
  2. docs/plan/CONTRACTS.md
  3. docs/plan/tasks/{ID}-{slug}.md  — your task; follow it exactly
  4. any files listed under "Read first" in your task

Rules:
  - Only create or modify the files listed under "Files you own".
  - Do not edit internal/model, internal/config, internal/redact, internal/engine
    (unless you are T01/T02), or docs/plan/. If the contracts block you, stop
    and reply "CONTRACT GAP: ..." with the exact change you need.
  - Test fixtures must be synthetic. Never copy anything from ~/.claude, ~/.codex,
    ~/.pi or ~/.paircli into the repository. This repository is public.
  - No network access in tests. No new Go module dependencies.
  - Commit your work on this worktree's branch with message "{ID}: <title>".

When done, reply with: files changed, `go test ./...` output (last 20 lines),
anything you could not do, and any deviation from the spec with the reason.
```

## Starting the build

Paste this into a Claude Code session opened in this repository (an Opus or
Sonnet session works as orchestrator):

```
Execute the paircli v2 build plan in docs/plan/README.md.
Follow its "Orchestration protocol" exactly: integration branch v2, one
worktree-isolated subagent per task using the model in the task table and
the subagent prompt template, wave by wave, verify and merge each task before
starting dependents, Sonnet review for Haiku tasks, and stop on any CONTRACT GAP
to ask me. Start with wave A (T01, then T02). Report after each wave.
Do not push or open PRs.
```

## Conventions

- **Go 1.22, standard library only.** No new module dependencies. The Pi
  extension (T21) is TypeScript using only Node built-ins and a type-only
  import from the Pi package.
- **Errors:** return wrapped errors (`fmt.Errorf("codex: parse %s: %w", path, err)`).
  Parsers skip malformed lines and keep going; they return an error only when
  the file cannot be read at all.
- **Hooks never break the agent.** Hook handlers exit 0, print nothing to
  stdout, and log failures to `~/.paircli/hook-errors.log`.
- **Determinism.** Output must be byte-identical for identical input: sort
  maps before iterating, never print wall-clock time except `GeneratedAt`
  (injected, so tests can pin it).
- **Time:** parse all timestamps to `time.Time` in UTC.
- **Paths:** store what the transcript recorded in `Path`; `internal/link` fills
  `RelPath` (forward slashes, no leading `./`).
- **Signal text:** `Summary` and `Finding.Summary` are plain sentences, at
  most 160 characters, no emoji, no Markdown except backticks around paths
  and commands. Use the formats given in each task. Times use `engine.Clock`.
- **Prompt text stays out of summaries.** Only INT-1 may put human prompt
  text in `Signal.Summary`; other signals may quote prompts only inside
  `Finding.Summary` or `Evidence.Excerpt`. `comment.md` uses signal summaries
  only and skips INT-1 unless `comment.include_prompts` is true.
- **Home paths:** show paths outside the repo with `engine.Home` so reports never contain `/Users/<name>/`.
- **Evidence:** every finding that comes from a session event includes at
  least one `Evidence` built with `engine.Context.Evidence`, which clips the
  excerpt and passes it through `redact.Text`.
- **Tests:** table-driven where it helps; use `internal/testkit` builders for
  detector tests; name tests after the behavior (`TestVER2_EditAfterLastPass`).
  Golden files live in `testdata/golden/`; each package with golden files defines
  `var update = flag.Bool("update", false, "rewrite golden files")` and is updated with
  `go test ./internal/<pkg>/... -update` (never `./...`, which fails in packages without the flag).
- **Unique names in shared packages.** Several tasks write into the same Go
  package (`internal/detect/*`, `internal/claudecode`, `internal/codex`,
  `internal/pi`, `internal/scan`). Every unexported identifier and every test
  helper you add there must start with your signal ID or task scope in
  lowercase (`ver4Sequences`, `exp2ParsePackageJSON`, `ccHookPayload`,
  `rolloutState`), so parallel branches merge without "redeclared" errors.
- **Comments:** doc comment on every exported identifier; otherwise comment
  only non-obvious logic. Match the existing code's plain style.

## Definition of done (every task)

- Only owned files changed; legacy code untouched (except T24).
- `gofmt -l .` prints nothing; `go vet ./...` and `go test ./...` pass.
- Every item in the task's **Tests** section exists and passes.
- Every **Acceptance** command in the task runs as described.
- No TODOs except `TODO(Txx)` pointing at a later task in this plan.
- The final reply lists files changed, test output, and deviations.
