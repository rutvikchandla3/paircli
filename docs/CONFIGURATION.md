# Configuration

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
| `llm.provider` | `none` / `anthropic` / `claude-cli` / `pi` / `http` | `none` |
| `llm.model` | model name | `claude-opus-5-5` |
| `llm.max_input_chars` | int | `200000` |
| `llm.base_url` | URL the `http` provider posts to | none (provider off) |
| `llm.headers` | extra request headers for `http` | none |
| `llm.auth_token_env` | env var holding the `http` credential | none |
| `comment.include_prompts` | bool | `false` |
| `comment.max_lines` | int | `5` |

### LLM providers

The grounded pass is off unless `llm.provider` (or `--llm`) names one.

| Provider | Talks to | Configured by |
|---|---|---|
| `anthropic` | the Anthropic Messages API | `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` |
| `claude-cli` | your local `claude` binary, non-interactive | `PAIRCLI_CLAUDE_BIN` or `$PATH` |
| `pi` | your local `pi` binary, non-interactive | `PAIRCLI_PI_BIN` or `$PATH` |
| `http` | any gateway speaking the Anthropic Messages shape | `llm.base_url`, `llm.headers`, `llm.auth_token_env` |

`pi` runs with `--no-session --no-tools`, so a judge run never lands in
`~/.pi/agent/sessions` — the store paircli reads as session data — and never
touches the repository. It passes `--model` only when one is configured, since
the shared default names a Claude model pi need not serve.

`http` posts `{llm.base_url}/v1/messages` and sends the credential named by
`llm.auth_token_env` as `Authorization: Bearer <token>`. Naming the variable
rather than storing the token keeps it out of `.paircli.json`, and out of the
`snapshot.json` that mirrors the config. `llm.headers` is applied last, so a
gateway that wants its token in another header can be told to. Run
`paircli doctor` to check the endpoint and whether that variable is set.

The `scan` flags `--window-before`, `--window-after`, `--llm` and `--model`
override the matching config values for one run. `paircli judge` takes its
config from the snapshot it replays; its `--llm` and `--model` flags override
it for that run only. Run `paircli --help` for the full flag list (including
`--no-hooks`, `--no-commit-patches`, `--no-agent-trace`, `--snapshot`,
`--out`, `--json` and `judge --force`).

## Privacy details

**What is read.** Local harness transcripts (`~/.claude/projects`,
`~/.codex`, `~/.pi/agent/sessions` — overridable via `roots` in
`.paircli.json`), paircli's own hook records, and the PR itself via `gh`
(metadata, diff, per-commit patches). Nothing else. No telemetry.

**What is written where.**

- `.paircli/pr-<n>/<head-commit>/` inside the repo — the scan artifacts (gitignored).
- `~/.paircli/events/<harness>/<date>.jsonl` — hook event records.
- Hook install edits the harness's own config: `~/.claude/settings.json`,
  `$CODEX_HOME/hooks.json` (never `config.toml`), and
  `~/.pi/agent/extensions/paircli.ts`.
- With `--post`, the rendered `comment.md` is posted to the PR via `gh`.
- With `--llm`, the grounded judge sends a fact bundle — prompts, diffs and
  summaries — to the provider you configured. It is off unless you opt in:
  `anthropic` goes to Anthropic, `claude-cli` and `pi` go wherever those tools
  are pointed, and `http` goes to the `llm.base_url` gateway you named.
  `paircli judge` sends the same bundle for the snapshot you hand it, and reads
  nothing else, so a replay elsewhere sends exactly what a scan with `--llm`
  would have.

**Redaction is not implemented yet.** The seam exists (`internal/redact.Text`)
but is a no-op, so stored and posted text can contain secrets that appeared
in the sessions. EXP-4 reports secret-shaped matches masked, and
`comment.md` never includes prompt text unless you set `comment.include_prompts`
(or pass `--include-prompts`) — until real redaction lands, those are the
compensating defaults.
