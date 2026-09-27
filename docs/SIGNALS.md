# Signal taxonomy

paircli's job: given a PR, produce a structured record of the *development
process* behind it — not just the diff. This is the signal set we capture,
grouped by what question each answers for a reviewer.

## 1. Provenance signals (who/what produced this code)

- `harnesses_used`: distinct set of agent harnesses involved (Claude Code,
  Codex CLI, Pi, human-direct-edit).
- `sessions`: list of session records (one per harness invocation), each with
  harness name, session id, start/end timestamps, model(s) used.
- `authorship_mix`: rough attribution of changed lines/files to
  human-typed vs agent-generated vs agent-edited-then-human-modified, where
  derivable.
- `capture_completeness`: `hooked` (our capture was installed and ran live) vs
  `reconstructed` (recovered post-hoc from local transcripts) vs `partial` vs
  `none` — this is itself a signal reviewers should see, since reconstructed
  data has lower confidence.

## 2. Process signals (how the work happened)

- `session_count` and `session_count_per_harness` — one harness with 6 short
  sessions reads differently than one long session.
- `wall_clock_span`: first session start → last session end for this PR.
  Distinct from CI/PR-open-to-merge time.
- `iteration_count`: number of user prompts/turns per session — a rough proxy
  for how much back-and-forth was needed.
- `correction_signals`: turns where the user's next prompt reads as a
  correction to the agent's prior action (heuristic: negative-sentiment
  short follow-up after a tool-heavy turn, e.g. "no, don't do that", "revert
  that", explicit `git checkout`/`git reset` tool calls issued by the human
  mid-session). Surfaces where the agent went down a wrong path.
- `plan_revisions`: count of distinct plans/approaches proposed within a
  session (via plan-mode entries, or repeated large rewrites of the same
  files).
- `test_run_count` / `test_failure_then_pass_count`: how many times tests
  were run, and whether failures were iterated on and resolved before the PR
  was opened, vs. never run locally at all.
- `interrupted_or_abandoned_sessions`: sessions that end mid-task (no
  matching commit, or the harness reports the run was cancelled).

## 3. Scope/diff-linkage signals

- `commits`: PR's commit list, each annotated with which session (if any)
  produced it, and by which method (`sha_exact` | `heuristic`).
- `files_touched_by_session`: cross-reference of PR file list against which
  session's tool calls wrote to each file — flags files changed outside any
  captured session (possible manual edit, or an uncaptured harness).
- `unattributed_commits`: PR commits with no session match at all — the
  strongest "we have no signal here" flag.

## 4. Tool/action signals

- `tool_call_summary`: counts by tool type per session (file edits, shell
  commands run, tests run, web/doc lookups, subagent/delegate calls).
- `shell_commands_run`: notable commands (build, test, lint, migration,
  deploy-adjacent) — not the full raw list, which is noise; filtered to
  categories a reviewer would care about.
- `external_context_pulled`: whether the session read docs, fetched URLs, or
  used MCP tools to pull in outside context (relevant for "did the agent
  actually understand the API it's using" style review questions).

## 5. Cost/efficiency signals

- `token_usage`: input/output/cached tokens per session, aggregated per PR.
- `model(s)`: which model(s) ran each session (relevant since capability
  differs materially by model).
- `approval_friction`: count of tool-call approvals requested vs. denied, and
  count of destructive-action confirmations — signals how much the human was
  actively supervising vs. running on autopilot.

## Non-goals for v1

- No sentiment/quality scoring of the agent's code itself — that's what the
  actual PR review is for. Signals describe *process*, not verdict.
- No attempt to capture full transcript replay in the primary output — the
  structured signal folder links to raw session sources so a reviewer *can*
  drill in, but the top-level output must stay skimmable.
- No signal requires a specific harness's proprietary format to be readable
  by a human — everything normalizes into one schema (see ARCHITECTURE.md).

## Confidence tagging

Every signal in the output carries a `confidence` of `exact` (hook-captured,
SHA-linked), `inferred` (heuristic-matched, e.g. time+path correlation), or
`unknown` (no data available for this harness/session). Reviewers should be
able to tell at a glance which parts of the report to trust fully.
