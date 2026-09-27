# Signal catalog (v2)

paircli turns AI coding sessions (Claude Code, Codex CLI, Pi) into review
signals for a PR. This file is the product source of truth: what each signal
means, where its data comes from, and how much to trust it. The algorithms
live in the task specs under [`plan/tasks/`](plan/tasks/); the data types
live in [`plan/CONTRACTS.md`](plan/CONTRACTS.md).

Long-form rationale, research and UI mocks: the "Session Signal Catalog" doc
(https://claude.ai/artifact/CuEY6SMP18P2TVbeNqcCxJ).

## Ground rules

- **In scope:** harness transcripts on disk, paircli hook/extension records,
  and the PR's own commits and diff (the join key). Tickets, CI, incident
  history and code graphs are out of scope.
- **Provenance on everything.** `recorded` = the harness wrote it down.
  `derived` = deterministic code over recorded events. `inferred` = grounded
  LLM judgment; every inferred item cites the events it used, and uncited
  items are dropped.
- **Absence is not evidence.** Every report shows capture coverage (AUTH-2)
  first, so "no tests ran" is never confused with "we didn't see the session
  where tests ran".
- **Signals describe a change, never a person.** See "Won't build" below.

## Catalog

State values: `alert` (look at this), `info` (context), `clear` (checked,
nothing found), `unknown` (no data). Milestones: M1 = deterministic report
from transcripts, M2 = hooks, M3 = LLM pass.

| ID | Question | Signal | Pri | Provenance | Milestone | One-line definition |
|---|---|---|---|---|---|---|
| INT-1 | intent | Ask ledger | P0 | derived | M1 (condense: M3) | Every human prompt in order, including mid-turn steering and slash-command arguments |
| INT-2 | intent | Human-answered decisions | P0 | recorded | M1 | Questions the agent asked and the option a human picked |
| INT-3 | intent | Approved plan and plan drift | P0 | recorded | M1 (drift: M3) | Plan text a human approved; drift = planned steps with no change, changes never planned |
| INT-4 | intent | Rules in effect | P1 | recorded | M2 (violations: M3) | Instruction files (CLAUDE.md, AGENTS.md, rules) loaded in the sessions |
| AUTH-1 | authorship | Line-level authorship | P0 | derived | M1 (human edits via hooks: M2) | Each added PR line labeled agent / agent-then-human / human-in-session / uncaptured |
| AUTH-2 | authorship | Session-commit map and coverage | P0 | derived | M1 | Which sessions produced which commits, and what share of lines they explain |
| AUTH-3 | authorship | Model mix and disclosure trailer | P1 | recorded | M1 | Models behind the agent lines, plus a ready `Assisted-by:` trailer |
| VER-1 | verification | Verification log | P0 | recorded | M1 | Test, lint, typecheck and build commands that ran, with outcome |
| VER-2 | verification | Verification freshness | P0 | derived | M1 | PR lines edited after the last passing check; code never checked |
| VER-3 | verification | Check-to-change coverage | P1 | derived | M1 | Changed source files no check plausibly exercised (heuristic, labeled "likely") |
| VER-4 | verification | Test integrity | P0 | derived | M1 | Tests edited after failing, assertions weakened, skips added, snapshots regenerated, tests deleted |
| VER-5 | verification | Quality-gate bypasses | P0 | derived | M1 | `--no-verify`, hook-skip env vars, new lint/type suppressions, CI or coverage config edits |
| VER-6 | verification | Runtime and visual evidence | P1 | recorded | M1 | Screenshots, dev servers, local requests, editor errors still open at the end |
| DEC-1 | decisions | Human corrections | P0 | recorded | M1 (classification: M3) | Interrupts, rejected tool calls, denials and correction prompts, mapped to the lines they touched |
| DEC-2 | decisions | Abandoned approaches | P1 | derived | M1 (summaries: M3) | Code written then reverted, rollbacks, abandoned Pi branches |
| DEC-3 | decisions | Decision trail | P1 | inferred | M3 | The choices that shaped the diff, each with reason and citation |
| FRI-1 | friction | Churn hotspots | P1 | derived | M1 | Files with far more edits and edit-to-check cycles than the rest |
| FRI-2 | friction | Loops and unresolved errors | P1 | derived | M1 | Same failing command 3+ times; errors never followed by success |
| FRI-3 | friction | Context resets | P1 | recorded | M1 (lost constraints: M3) | Compactions, clears, resumes, forks; PR lines written after a reset |
| FRI-4 | friction | Knowledge lookups | P2 | recorded | M1 | Web searches and fetches, mapped to the hunks that followed |
| EXP-1 | exposure | Side-effect ledger | P0 | derived | M1 | Commands with effects outside the diff (migrations, pushes, deletes, infra, publishes, writes outside the repo) |
| EXP-2 | exposure | Dependency provenance | P0 | derived | M1 | Packages the agent added, whether a human asked for them, failed installs of names that don't exist |
| EXP-3 | exposure | Untrusted-input chain | P0 | derived | M1 | External content read, then sensitive paths edited in the same session |
| EXP-4 | exposure | Secret contact | P1 | derived | M1 | Secret files read; secret-shaped strings seen in prompts or output (reported masked) |
| OVS-1 | oversight | Autonomy envelope | P0 | recorded | M1 | Permission/approval/sandbox mode per turn; share of tool calls with no approval step |
| OVS-2 | oversight | Human touchpoints | P1 | derived | M1 | Human inputs on the timeline; longest unattended stretch and the lines written in it |
| OVS-3 | oversight | Delegated work | P2 | recorded | M1 | Subagents, their models, and the lines they wrote |
| CON-1 | consistency | Claim check | P0 | inferred | M3 | Claims in the PR body and final messages checked against recorded facts |
| CON-2 | consistency | Open loops | P1 | recorded | M1 (stated caveats: M3) | Todo items still open, TODO/FIXME added, caveats the agent stated |
| CON-3 | consistency | Scope match | P1 | inferred | M3 | Hunks that trace to the ask, plan or a decision, and hunks that trace to nothing |

## Harness support

Each cell is *transcript only → with paircli hooks/extension*:
`full`, `partial`, or `none`. The engine attaches the row for each harness
present in a report as `Signal.support`.

| ID | Claude Code | Codex | Pi |
|---|---|---|---|
| INT-1 | full → full | full → full | full → full |
| INT-2 | full → full | full → full | partial → full |
| INT-3 | full → full | partial → partial | none → partial |
| INT-4 | partial → full | full → full | partial → full |
| AUTH-1 | full → full | partial → full | partial → full |
| AUTH-2 | partial → full | partial → full | partial → full |
| AUTH-3 | full → full | full → full | full → full |
| VER-1, VER-2 | full → full | full → full | partial → full |
| VER-3 | partial → partial | partial → partial | partial → partial |
| VER-4, VER-5 | full → full | full → full | full → full |
| VER-6 | full → full | partial → partial | partial → partial |
| DEC-1 | full → full | full → full | partial → full |
| DEC-2 | partial → full | partial → partial | full → full |
| DEC-3, CON-1, CON-3 | full → full | full → full | partial → partial |
| FRI-1..4 | full → full | full → full | full → full |
| EXP-1..4 | full → full | full → full | full → full |
| OVS-1 | full → full | full → full | partial → full |
| OVS-2 | full → full | full → full | full → full |
| OVS-3 | full → full | partial → full | partial → partial |
| CON-2 | full → full | full → full | partial → partial |

## Won't build

Easy to compute, deliberately excluded:

- Developer productivity or AI-usage leaderboards from sessions.
- Prompt-quality grades of people (coaching belongs in an author-only view).
- Idle, away or typing time (Claude Code's `away_summary` and `turn_duration` are ignored).
- A single composite PR risk score.
- Token spend in the reviewer view (kept in `sessions/*.json` only).
- The raw transcript as the primary UI (it stays one click deep).

## Deferred

- **Redaction** of secrets and private text in stored or posted output. Until
  it lands: `internal/redact.Text` is a no-op seam every renderer calls,
  `comment.md` never includes prompt text unless `comment.include_prompts`
  is set, and the LLM pass is off by default.
