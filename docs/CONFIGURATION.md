# Configuration

`.paircli.json` at the repository root; a missing file means defaults. List
fields are appended to the defaults; non-zero scalar fields replace them. A key
whose default is on is a pointer internally, so "not mentioned" and `false` are
different things: omit it to keep the default.

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
| `publish.changed_file_contents` | bool | `true` |

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

- `~/.paircli/scans/<owner>__<repo>/pr-<n>/<head-commit>/` — the scan
  artifacts (0700). Off-repo on purpose, so a `git add .` in the checkout can
  never commit them; `--out` overrides the location. Output an earlier paircli
  wrote to `<repo>/.paircli/` is left where it is.
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

**What `paircli publish` uploads.** One zip per PR head, to Pair's S3 through a
presigned URL Pair issues for your login:

- `bundle.json`: the repo, PR number, head, the redaction summary (rules
  version and how many matches of each kind were removed) and whether raw
  sessions are included.
- `.paircli/pr-<n>/<head>/`: `report.md`, `comment.md`, `signals.json`,
  `authorship.json`, `snapshot.json`, `agent-trace.json` and `sessions/*.json`
  from the scan. `snapshot.json` and the session files carry the normalized
  session timelines: prompts, commands, tool results and file edits.
- `.paircli/pr-<n>/<head>/changed-files/`: the whole text of every changed file,
  as `manifest.json` plus one file per side. `new/` is the file at the PR head
  and `old/` the file at the merge base GitHub diffed against, so a reader can
  see a changed line inside the function around it. Redacted like everything
  else, capped at 1 MiB a side and 16 MiB overall, with the parts around a
  change kept and the rest dropped when a file is larger than that. Files that
  are not text — images, recordings, archives, compiled output — are never
  fetched at all, and every file that was left out is recorded in the manifest
  with a reason. Turn it off per repository with `publish.changed_file_contents`
  or per run with `publish --no-file-contents`; `scan` takes the same flag.
- `raw-sessions/`: the Claude Code (main, resumed and subagent files) and
  Codex transcript bytes that are new since the last publication of that PR,
  with a manifest of source offsets and SHA-256s. At most 80 MiB per
  publication; a range that does not fit is skipped with a note in the log,
  and a bundle that would pass 100 MiB leaves the raw sessions out.

`changed-files/manifest.json` is the list for its subtree, the way the list
above is for the scan folder: a file it does not name is never uploaded.

Files in the scan folder that are not on that list are never uploaded. The
upload happens only through `paircli publish` (run by hand or started by the
installed hooks); `--dry-run` shows the bundle without sending it. Publishing
also posts one comment with the pair.sh link on the PR (skip it with
`--no-comment`) and sends a desktop notification.

## Redaction

Everything `paircli publish` uploads, and every string the reports render,
goes through `internal/redact` (rules version `1`). A match is replaced by
`[REDACTED:<kind>]`; redacting twice changes nothing. The kinds:

| Kind | What it matches |
|---|---|
| `github_token` | `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_` and `github_pat_` tokens |
| `api_key` | `sk-` keys (at least 20 characters with a digit or capital, so slugs are left alone) |
| `aws_access_key` | `AKIA` / `ASIA` access key ids |
| `aws_secret` | the value of `aws_secret_access_key = ...` |
| `slack_token` | `xoxb-`, `xoxp-`, `xoxa-`, `xoxs-`, `xoxr-`, `xoxo-`, `xapp-` tokens |
| `jwt` | three-part `eyJ...` JSON web tokens |
| `bearer` | the credential in `Authorization: Bearer ...` and `Authorization: Basic ...` |
| `private_key` | whole `-----BEGIN ... PRIVATE KEY-----` blocks |
| `secret_assignment` | the value in `api_key`, `token`, `secret`, `password` or `passwd` assignments (`name = value`, `name: "value"`, JSON, URL query) |
| `env_secret` | the exact value of this process's environment variables whose names contain `KEY`, `TOKEN`, `SECRET`, `PASSWORD`, `PASSWD`, `AUTH`, `CREDENTIAL` or `COOKIE` |
| `npm_token` | the `_authToken=` values in `~/.npmrc` |

Source files are redacted as whole buffers rather than line by line, so a rule
can match across lines — which is what the `private_key` rule needs, since a PEM
block spans several. A replacement keeps the newlines it removed, so line N of
the redacted file is line N of the original and a diff's line numbers still
point at the right code. Raw transcripts keep the stricter line-by-line rule:
one match can never join two JSONL lines.

Generic assignments redact only values that look like secrets: at least 8
characters, not a path, URL, boolean, number, placeholder (`$VAR`, `<token>`,
`xxxx`, `YOUR_API_KEY`), plain word or code expression (`config.token`,
`getToken()`). Environment values count only when they are at least 12
characters and not a path, URL or boolean; `GIT_AUTHOR_*` is not mistaken for
`AUTH`. Raw transcripts are redacted line by line, so one match can never join
two JSONL lines.

Redaction is pattern based and best effort. It does not find secrets that
have no recognizable shape, and it does not remove names, code or prose.
`comment.md` never includes prompt text unless you set
`comment.include_prompts` (or pass `--include-prompts`), and EXP-4 reports
secret-shaped matches masked.
