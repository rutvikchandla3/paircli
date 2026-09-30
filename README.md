# paircli

paircli turns local AI coding sessions — Claude Code, Codex CLI and Pi — into
review signals for a pull request: what was asked for, who wrote which lines,
whether it was verified, where the agent struggled, what it touched outside
the diff, and how closely a human was watching. It reads only what is already
on your disk, correlates sessions to the PR's commits, and writes a report a
reviewer can read in seconds. It does not review the code itself, does not
produce a verdict, and does not score people — signals describe a change,
never a person.

## Install

Build from source (the repo is not tagged yet, so `go install …@latest` will
work only once a release is published):

```sh
git clone https://github.com/rutvikchandla3/paircli
cd paircli
go build ./cmd/paircli
./paircli --help
```

Once tagged:

```sh
go install github.com/rutvikchandla3/paircli/cmd/paircli@latest
```

## Quick start

`paircli` needs [`gh`](https://cli.github.com/) to fetch the PR. Then:

```sh
# Install hooks so future sessions are linked to commits by exact SHA
# instead of being reconstructed from transcripts alone.
paircli hook install claude-code
paircli hook install codex
paircli hook install pi

# Scan a PR by number or URL and write the report into .paircli/pr-482/.
paircli scan 482
paircli scan https://github.com/owner/repo/pull/482

# Also post or update the summary comment on the PR.
paircli scan 482 --post

# Check what paircli can see on this machine: sessions per harness,
# hook install state, gh auth, config, LLM provider.
paircli doctor
```

**Codex trust note.** Codex runs no hook until you have reviewed and trusted
it inside Codex itself. After `paircli hook install codex`, start Codex and
approve the paircli hooks when prompted, then run `paircli doctor` — it shows
how many of the installed hooks Codex has marked trusted (`installed, N/M
trusted`). paircli never edits Codex's trust state (`config.toml`).

Hooks are optional: without them paircli reconstructs sessions from the
harnesses' own transcript files, with weaker commit linkage.

## What you get

`paircli scan 482` writes (deterministically — identical input produces
byte-identical files):

| File | What it is |
|---|---|
| `.paircli/pr-482/report.md` | The human-readable report — read this first. Coverage and alerts on top, then one section per review question. |
| `.paircli/pr-482/comment.md` | The PR comment body: a one-line coverage summary plus the top alerts. `--post` upserts it (the comment carrying the `<!-- paircli -->` marker is updated in place). |
| `.paircli/pr-482/signals.json` | The full structured report — the machine-readable contract. |
| `.paircli/pr-482/authorship.json` | Per-file, per-line authorship attribution. |
| `.paircli/pr-482/agent-trace.json` | The same attribution as a vendor-neutral [Agent Trace](https://agent-trace.dev/) record (skip with `--no-agent-trace`). |
| `.paircli/pr-482/snapshot.json` | The full replay record: PR, linked sessions, attribution, commit links and the resolved config. A snapshot rebuilds the deterministic report exactly, so the LLM pass can be run later or elsewhere. Written with `--snapshot`. |
| `.paircli/pr-482/sessions/<harness>-<id>.json` | One normalized session record per linked session. |

Short excerpts from the golden end-to-end scenario (synthetic data):

`comment.md`:

> **paircli** · 3 sessions (1 claude-code, 1 codex, 1 pi) · 90% of changed lines explained · capture: partial
>
> - ▲ **Verification freshness:** 8 PR hunks were edited after the last passing test run (14:02 UTC). `VER-2`
> - ▲ **Test integrity:** Test integrity issues: 3 test edits between fail and pass. `VER-4`

`report.md`:

> **Coverage:** 3 sessions (1 claude-code, 1 codex, 1 pi) · capture: partial · 90% of changed lines explained · 1 unattributed commit
>
> **VER-2 Verification freshness:** 8 PR hunks were edited after the last passing test run (14:02 UTC).
> - `src/webhooks/retry.ts` lines 12 changed at 14:07 UTC, after the last passing run.

## Signals

Thirty signals across eight review questions. Details, provenance rules and
per-harness support live in [`docs/SIGNALS.md`](docs/SIGNALS.md).

| ID | Question | Signal |
|---|---|---|
| INT-1 | intent | Ask ledger |
| INT-2 | intent | Human-answered decisions |
| INT-3 | intent | Approved plan and plan drift |
| INT-4 | intent | Rules in effect |
| AUTH-1 | authorship | Line-level authorship |
| AUTH-2 | authorship | Session-commit map and coverage |
| AUTH-3 | authorship | Model mix and disclosure trailer |
| VER-1 | verification | Verification log |
| VER-2 | verification | Verification freshness |
| VER-3 | verification | Check-to-change coverage |
| VER-4 | verification | Test integrity |
| VER-5 | verification | Quality-gate bypasses |
| VER-6 | verification | Runtime and visual evidence |
| DEC-1 | decisions | Human corrections |
| DEC-2 | decisions | Abandoned approaches |
| DEC-3 | decisions | Decision trail *(needs `--llm`)* |
| FRI-1 | friction | Churn hotspots |
| FRI-2 | friction | Loops and unresolved errors |
| FRI-3 | friction | Context resets |
| FRI-4 | friction | Knowledge lookups |
| EXP-1 | exposure | Side-effect ledger |
| EXP-2 | exposure | Dependency provenance |
| EXP-3 | exposure | Untrusted-input chain |
| EXP-4 | exposure | Secret contact |
| OVS-1 | oversight | Autonomy envelope |
| OVS-2 | oversight | Human touchpoints |
| OVS-3 | oversight | Delegated work |
| CON-1 | consistency | Claim check *(needs `--llm`)* |
| CON-2 | consistency | Open loops |
| CON-3 | consistency | Scope match *(needs `--llm`)* |

Every signal renders as `alert` (look at this), `info` (context), `clear`
(checked, nothing found) or `unknown` (no data). DEC-3, CON-1 and CON-3 are
the LLM-judged signals: without `--llm` they show as "Not available".

## Privacy

**What is read.** Local harness transcripts (`~/.claude/projects`,
`~/.codex`, `~/.pi/agent/sessions` — overridable via `roots` in
`.paircli.json`), paircli's own hook records, and the PR itself via `gh`
(metadata, diff, per-commit patches). Nothing else. No telemetry.

**What is written where.**

- `.paircli/pr-<n>/` inside the repo — the report files (gitignored).
- `~/.paircli/events/<harness>/<date>.jsonl` — hook event records.
- Hook install edits the harness's own config: `~/.claude/settings.json`,
  `$CODEX_HOME/hooks.json` (never `config.toml`), and
  `~/.pi/agent/extensions/paircli.ts`.
- With `--post`, the rendered `comment.md` is posted to the PR via `gh`.
- With `--llm`, the grounded judge sends a fact bundle — prompts, diffs and
  summaries — to the provider you configured (`anthropic` or `claude-cli`).
  It is off unless you opt in.

**Redaction is not implemented yet.** The seam exists (`internal/redact.Text`)
but is a no-op, so stored and posted text can contain secrets that appeared
in the sessions. EXP-4 reports secret-shaped matches masked, and
`comment.md` never includes prompt text unless you set `comment.include_prompts`
(or pass `--include-prompts`) — until real redaction lands, those are the
compensating defaults.

## Configuration

`.paircli.json` at the repository root; a missing file means defaults. List
fields are appended to the defaults; non-zero scalar fields replace them.

| Key | Type | Default |
|---|---|---|
| `sensitive_paths` | globs | CI configs, Dockerfiles, deploy/infra/terraform, auth/security/migrations dirs, manifest files, `.env*` |
| `generated_paths` | globs | `*.pb.go`, `*_generated.*`, `*.min.js`, vendor/dist dirs, lockfiles, `*.snap` |
| `test_path_patterns` | RE2 regexes on repo-relative paths | none |
| `extra_checks` | `[{"class","regex"}]`, class `test`/`lint`/`typecheck`/`build` | none |
| `window_before` | Go duration string | `"48h"` before the first PR commit |
| `window_after` | Go duration string | `"2h"` after the last PR commit |
| `roots.claude_code` | path | `~/.claude/projects` |
| `roots.codex` | path | `~/.codex` |
| `roots.pi` | path | `~/.pi/agent/sessions` |
| `roots.hook_log` | path | `~/.paircli/events` |
| `llm.provider` | `none` / `anthropic` / `claude-cli` | `none` |
| `llm.model` | model name | `claude-opus-5-5` |
| `llm.max_input_chars` | int | `200000` |
| `comment.include_prompts` | bool | `false` |
| `comment.max_lines` | int | `5` |

The `scan` flags `--window-before`, `--window-after`, `--llm` and `--model`
override the matching config values for one run. Run `paircli --help` for the
full flag list (including `--no-hooks`, `--no-commit-patches`,
`--no-agent-trace`, `--snapshot`, `--out`, `--json`).

## Development

```sh
go test ./...
```

Golden files live in `testdata/golden/`; a package with golden files defines
an `-update` flag and is updated with:

```sh
go test ./internal/e2e/... -update   # or whichever package owns the golden files
```

The design docs — the full v2 build plan, frozen contracts and per-task
specs — live under [`docs/plan/`](docs/plan/); the signal catalog is
[`docs/SIGNALS.md`](docs/SIGNALS.md) and the pipeline is described in
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).