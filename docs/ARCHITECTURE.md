# Architecture (v2)

paircli reads AI coding sessions (Claude Code, Codex CLI, Pi), correlates
them with a PR's commits, and writes a signal report. The signal catalog and
its ground rules are the source of truth: see
[`SIGNALS.md`](SIGNALS.md). The frozen types and cross-package APIs are in
[`plan/CONTRACTS.md`](plan/CONTRACTS.md); this document describes how the
shipped code fits together.

## The pipeline

`paircli scan <pr>` calls `internal/scan.Run`, which runs the steps in order.
The CLI (`cmd/paircli`) and the end-to-end golden test (`internal/e2e`) both
go through this one entry point.

1. **Fetch** — `pr.Fetch` resolves the PR via `gh` (number or URL) into
   `model.PR`: metadata, combined diff, and per-commit patches (skippable
   with `--no-commit-patches`). The PR's head is GitHub's `headRefOid`
   (`PR.HeadSHA`), never the last entry of the commit list, which GitHub
   caps; the output folder is named after it.
2. **Load config** — `config.Load` reads `<repoRoot>/.paircli.json` and
   merges it over built-in defaults. Flags override windows and LLM settings.
3. **Discover** — `link.Discover` parses every harness's local data inside
   the session search window (`window_before` before the PR's first commit,
   `window_after` after its last commit; default 48h/2h): harness
   transcripts via the `claudecode`/`codex`/`pi` parsers, plus hook records
   via `hooklog.Read`/`hooklog.Merge` and `pi.HookRecords` (unless
   `--no-hooks`). Every session is normalized into the same
   `model.Session` schema, whichever capture path produced it.
4. **Select** — `link.Select` keeps the plausible candidates: sessions whose
   repo root and file paths overlap the PR and whose time range intersects
   the window. The rest are counted as dropped candidates.
5. **Attribute** — `attrib.Attribute` labels every added PR line with who
   wrote it, and `attrib.CommitSessions` maps commit SHAs to sessions.
6. **Finalize links** — `link.Finalize` decides which candidates are really
   linked to this PR and with what method (`sha_exact`, `sha_ancestor`,
   `content`, `heuristic`; see below), and lists commits no session explains.
7. **Re-attribute over linked sessions only**, then `engine.NewContext` and
   `engine.Run` execute the 27 deterministic detectors (see
   `internal/detect/`, one file per signal, self-registering via `init()`).
8. **Optional judge** — with a provider configured (`--llm` or `llm.provider`
   in config: `anthropic`, `http`, `claude-cli` or `pi`), `judge.Run` builds a
   fact bundle and asks the LLM for grounded judgments; the three inferred
   signals (DEC-3, CON-1, CON-3) are recomputed from them and
   `enrich.Apply` folds the judgments back into the deterministic signals.
   Without a provider those three signals render as "Not available — run
   with --llm".
   The pass is split so the prompts and the validation are separable:
   `judge.BuildPrompts` prepares the bundle and the four job requests,
   `Set.Apply` validates and merges replies. `judge.RunFull` drives both
   through a provider and also reports, per job and per item kind, what
   could not be used. An inferred signal whose job failed, or whose items
   were all dropped because none could be cited, reports `unknown` rather
   than `clear`: "we could not check" is not "we checked and found
   nothing". Failures are noted on AUTH-2's `Data["llm_errors"]`, whether
   the pass failed entirely or only in part.
9. **Render** — `render.BuildReport` and `render.Write` produce
   `.paircli/pr-<n>/<head-commit>/`: `report.md`, `comment.md`,
   `signals.json`, `authorship.json` and
   `sessions/<harness>-<id>.json`. Output is
   deterministic: identical input produces byte-identical files.
10. **Agent Trace** — `agenttrace.WriteWith` exports the same attribution as
    a vendor-neutral Agent Trace record (`agent-trace.json`; skip with
    `--no-agent-trace`).
11. **Post** — with `--post`, `scan` upserts one PR comment via `gh`: the
    comment whose body starts with `<!-- paircli -->` is updated in place,
    otherwise a new comment is posted.

The CLI never writes model-authored text of its own: with no provider the
report is fully deterministic. Turning a scan into a published review is
`paircli publish`, below.

## Publishing: `paircli publish`

`paircli publish <pr> [--repo-dir DIR] [--wait] [--timeout DUR] [--dry-run]
[--no-comment] [--json]` runs the scan above (snapshot on, no LLM) and sends
the result, plus the new part of the raw agent transcripts, to Pair. Pair's
worker does all LLM generation; the client only collects, redacts and
uploads. The steps, each written to the PR's publication record
(`~/.paircli/state/publications/<owner>__<repo>__<n>.json`, see
[SETUP.md](SETUP.md)) so `paircli status` can show where a run is:

1. **Single flight, with a pending rerun.** `state.TryLock` on the PR. A run
   that finds the lock taken writes `locks/<key>.pending` (PR URL, repo dir,
   request time) and exits 0. The run holding the lock, after finishing its
   steps and still holding the lock, takes the marker and runs the whole
   pipeline again, which resolves `headRefOid` afresh, so a push made while it
   was scanning is published too. It does this at most 3 extra times per
   process (a marker left by the last allowed run stays for the next publish),
   and skips a rerun when a successful run already published the current head
   (one `gh pr view` call). The publication record ends with the last run's
   result; the log file keeps every run. `--dry-run` neither leaves nor takes
   markers.
2. **Login.** `ensureLogin` (device login if there is no usable credential);
   a login that cannot be completed exits 2. The server is the credential's
   `server_url`, else `resolveServer`.
3. **Scan** (`step: scan`). `scan.Run` with `Snapshot` on and no LLM. Its
   result carries the PR head and the linked sessions.
4. **Capture** (`step: capture`). `internal/capture` stages the byte ranges of
   each linked session's transcript files that are new since the last
   successful publication: the Claude Code main file, files that continue a
   resumed session, and `<id>/subagents/agent-*.jsonl`; the Codex rollout.
   Ranges are cut at line boundaries and redacted before they are hashed.
   Nothing is committed yet.
5. **Package** (`step: package`). `internal/publish.BuildBundle` writes a
   deterministic zip (sorted entries, 1980-01-01 timestamps, mode 0644,
   at most 100 MiB, no symlinks):

   ```
   bundle.json                                {schema_version, created_by, created_at, repo, pull_number,
                                               head_sha, scan_dir, redaction{applied, rules_version, counts},
                                               raw_sessions}
   .paircli/pr-<n>/<head>/...                 report.md comment.md signals.json authorship.json snapshot.json
                                               agent-trace.json sessions/*.json (redacted again on the way in)
   .paircli/pr-<n>/<head>/.pair/review-policy.yml
                                              the repo's critical-path policy, if it has one (<= 256 KiB)
   raw-sessions/manifest.json                 absent when nothing new was captured
   raw-sessions/<harness>/<session>/<file>    redacted transcript ranges
   ```

   Only those scan files go in: anything else that happens to sit in the scan
   folder (leftovers of older tooling, model output) stays local. If raw
   sessions would push the zip over 100 MiB they are left out and the
   publication still succeeds.
6. **Upload** (`step: upload`, `finalize`). The revision journal
   (`revisions.json`, flock-protected) maps `{server, head, sha256, size}` to a
   revision: the same bundle always gets the same one, a new bundle gets
   `max(clock ns, latest + 1)` so revisions keep rising. Then the publication
   protocol: `POST upload-url` (the response must echo what was sent and the
   signed form must name exactly this bundle's key, content type and SHA-256),
   a multipart POST to S3 (HTTPS to an `*.s3*.amazonaws.com` host only; the
   Pair token is never sent there), and `POST finalize` with
   `{"accepts_async": true}` (a `202` carries the analysis job, a `200` is
   accepted too). A transient finalize failure (network, 429, 5xx) is retried
   three times with backoff without uploading again; 401, 403 and 409 are
   not retried. Responses are capped at 1 MiB, never followed across
   redirects, and error messages never repeat server text.
7. **After finalize.** The capture cursors are committed
   (`capture-cursors.json`): only now is the captured range counted as
   published, so a failed run sends the same bytes next time. Republishing
   the head that last advanced a cursor repeats that head's range instead of
   sending an empty capture. A cursor whose file shrank, was rotated or was
   rewritten (its prefix hash no longer matches) restarts from byte 0. Then
   the PR link comment (`prcomment`: only a comment the current `gh` user
   wrote that carries `<!-- pair-review-link -->` is ever edited) and a
   desktop notification (`notify`: `osascript` or `notify-send`, otherwise
   nothing). Comment and notification failures are logged, never fatal.
8. **`--wait`** polls `GET .../analysis` every 5 s growing to 15 s until the
   job succeeds or fails, or `--timeout` (default 20m) passes.

`--dry-run` builds the bundle and prints the plan, bundle path, redaction
counts and the server it would use; it never logs in, calls the network,
writes a publication record or moves a cursor. Exit codes: 0 ok, 1 failure,
2 not logged in. A run also writes its own log to
`~/.paircli/logs/publish-<owner>__<repo>__<n>.log` (the record's `log`).

## Replaying a scan: `paircli judge`

Step 8 needs nothing that steps 1–7 produce beyond the read-only
`engine.Context`, and steps 9–10 are pure functions of that context and the
signals. `snapshot.Build`/`snapshot.Write` (step 8's first half, under
`--snapshot`) freeze exactly that input, so the whole tail of the pipeline can
run again later, or on another machine, from the record alone.

`paircli judge <snapshot.json>` is that replay: `snapshot.Read` →
`Snapshot.Context()` → `engine.Run` → `judge.RunFull` → merge → render. It
reads no network, no repository and no harness session store, and it defaults
`--out` to the snapshot's own folder so the judged report lands where the scan
would have written it. `scan.Replay` is the library entry point.

Two properties make it a replay rather than a re-implementation:

- **The merge is one function.** `scan.Merge` applies a pass to a signal set
  — note the failure on AUTH-2, recompute the inferred signals, mark an
  unusable one unknown, fold the rest in through `enrich` — and both the live
  pipeline and the replay call it, so their orderings cannot drift apart.
- **The deterministic signals are recomputed, never stored.** They are a pure
  function of the snapshot, so replaying one with no provider reproduces the
  scan's whole output folder byte for byte (the e2e test asserts exactly
  that). The record stays the single input; a snapshot is not a cache of its
  own report.

A snapshot another paircli version wrote is refused unless `--force`: the
signals depend on the detector code in the running binary, which the record
pins but cannot reproduce.

## Three capture paths

**Path A — hooks installed (Claude Code, Codex).** `paircli hook install
[harness]` registers hook commands in the harness's own config
(`~/.claude/settings.json` for Claude Code, `$CODEX_HOME/hooks.json` for
Codex). Naming no harness installs for every harness detected on the machine
— detected meaning the directory paircli would write into exists, so it never
creates config for a tool that is not installed. Naming one installs it
regardless, since the user asked for it by name. During a session the harness
invokes `paircli hook <harness> <Event>`, which appends a `model.HookRecord`
to `~/.paircli/events/<harness>/<date>.jsonl`: git HEAD snapshots (at session
start/end, prompts, and after state-changing git commands), working-tree
line-hash snapshots, permission decisions, rule/instruction files. Handlers
exit 0 — hooks never break the agent; failures go to the log. The one thing
a hook may print is the object described in [docs/SETUP.md](SETUP.md) after a
`gh pr create` or a branch `git push`, which also starts `paircli publish` in
the background. Hook records are merged into the parsed sessions by `hooklog.Merge`,
which upgrades capture to `hooked` and fills in exact SHAs.

**Path B — Pi extension.** Pi has no external hook process; instead
`paircli hook install pi` writes a TypeScript extension to
`~/.pi/agent/extensions/paircli.ts`. It observes the same moments (session
start/end, prompts, state-changing git commands) and appends a `paircli`
entry into the Pi session file itself via `pi.appendEntry`; the Pi parser
reads these back as hook records. Same effect as Path A, different plumbing.

**Path C — transcripts only, nothing installed.** Every harness writes its
own session files to disk, and the parsers read them directly. This is
always available with no prior setup but yields weaker linkage: what the
harness never recorded (SHAs, approval steps) stays unknown, and capture is
reported as `reconstructed` or `partial`. A session that also has hook
records is marked `hooked`.

All three paths converge on `model.Session` before correlation, so the rest
of the pipeline does not care where a session came from.

## Per-harness notes

**Claude Code.** Transcripts live in `~/.claude/projects/*/*.jsonl` and
record `sessionId`, `cwd`, `gitBranch`, timestamps, messages and tool calls
— but no commit SHA. Without hooks, linkage is `content`/`heuristic` at
best; hooks (`SessionStart`, `UserPromptSubmit`, `PostToolUse` on Bash,
`PreCompact`, `Stop`, `SessionEnd`) supply exact SHAs, permission modes,
interrupts and line-hash snapshots.

**Codex CLI.** Rollout files record the starting commit of the session in
`session_meta.git.commit_hash` — so a transcript-only scan can link a Codex
session to a commit it *started from* (`sha_ancestor`), but not to commits
made during it. Hooks (`SessionStart`, `UserPromptSubmit`, `PostToolUse` on
Bash, `PreCompact`, `Stop`, `SessionEnd`) go into `$CODEX_HOME/hooks.json`.
paircli never writes Codex's hook-trust approvals (`config.toml`): Codex
withholds new hooks until the user reviews and trusts them inside Codex,
which is why `paircli hook install codex` prints a reminder and `paircli
doctor` reports `installed, N/M trusted`.

**Pi.** Session files live under `~/.pi/agent/sessions`; the format includes
agent tool calls, branches and compactions. SHAs and permission/approval
data need the paircli extension (Path B above); with it, Pi sessions link
`sha_exact` and its partial support rows in
[`SIGNALS.md`](SIGNALS.md#harness-support) upgrade to full.

## Correlation and attribution, in brief

**Correlation** (`internal/link`). Discovery scans the window around the
PR's commit times. Select requires repo identity (root or origin remote)
and path overlap, then a time-range intersection with the window. Finalize
links a session to the PR by, in order:

- `sha_exact` — a hook or transcript recorded a PR commit SHA at a moment
  that places the session there (commit/prompt/stop/session_end triggers);
- `sha_ancestor` — the session was recorded starting on top of a PR commit;
- `content` — the session's edits produced lines that are in the PR;
- `heuristic` — repo + branch + time-window overlap only (±2h around a
  commit), confidence downgraded.

Commits with no session are surfaced as unattributed, never dropped, and
every report shows capture coverage (AUTH-2) first — "no tests ran" is never
confused with "we didn't see the session where tests ran".

**Attribution** (`internal/attrib`). Every added PR line is matched, in
priority order, against a timeline of all linked sessions' events: exact
agent edit → exact external edit → hook-snapshot-observed human line →
loosely reformatted agent edit → fuzzy (Levenshtein) match. Labels:
`agent`, `agent_then_human` (an agent line changed afterwards),
`human_in_session`, `uncaptured`, and `trivial` (blank/punctuation or
generated files, excluded from coverage ratios).

## Where each piece lives

| Package | Purpose |
|---|---|
| `internal/model` | Frozen types: sessions, events, PRs, signals, attribution, reports |
| `internal/config` | `.paircli.json` + defaults |
| `internal/redact` | Secret redaction: `Text` for rendered strings, a counting `Redactor` for files and raw transcripts (rules version `1`, see CONFIGURATION.md) |
| `internal/capture` | Two-phase incremental capture of raw Claude Code / Codex transcript ranges, with cursors |
| `internal/publish` | Bundle builder (deterministic zip), revision journal, publication API client (reserve, S3, finalize, analysis) |
| `internal/prcomment`, `internal/notify` | The Pair link PR comment (owned by the `gh` user) and desktop notifications |
| `internal/state` | Publication records, locks and journals under `~/.paircli/state` |
| `internal/scan` | The pipeline as a library (`Run`), and its replay from a snapshot (`Replay`) |
| `internal/link` | Discovery, selection, commit linking |
| `internal/attrib` | Line attribution, commit↔session map |
| `internal/pr`, `internal/diff` | `gh` fetch and unified-diff parsing |
| `internal/engine` | Detector registry, `Context`, signal catalog metadata, harness support table |
| `internal/detect/<question>` | One detector file per signal, self-registering |
| `internal/judge` | Fact bundle, prompts, response validation → `model.Judgments` |
| `internal/enrich` | Folds judgments into deterministic signals |
| `internal/llm` | Providers: Anthropic Messages (`anthropic`, and the configurable `http` gateway), `claude` and `pi` CLIs |
| `internal/claudecode`, `internal/codex`, `internal/pi` | Per-harness transcript parsers + hook install/handlers (Pi: extension installer) |
| `internal/hooklog` | Hook record log: append, read, merge into sessions |
| `internal/classify` | Command, path, check and secret-shape classification |
| `internal/render` | `report.md`, `comment.md`, `signals.json`, `authorship.json`, session files |
| `internal/agenttrace` | Agent Trace export |
| `internal/snapshot` | Replay record: serialize a scan, rebuild its `engine.Context` |
| `internal/gitinfo` | Git helpers (repo root, HEAD, remotes) |
| `internal/testkit` | Session/PR/context builders for tests |
| `internal/e2e` | Golden end-to-end scenario (`testdata/golden/`) |
| `cmd/paircli` | CLI: `scan`, `judge`, `publish`, `setup`, `status`, `hook`, `login`, `doctor`, `update`, `version` |

## Output folder

```
.paircli/pr-<number>/<head-commit>/
  report.md                 # human-readable report — read this first
  comment.md                # PR comment body (--post upserts it)
  signals.json              # full structured report, the machine contract
  authorship.json           # per-file, per-line attribution
  agent-trace.json          # vendor-neutral Agent Trace record
  snapshot.json             # full replay record (--snapshot)
  sessions/<harness>-<id>.json   # one normalized session record per linked session
```

`.paircli/` is gitignored. Session paths outside the repo are rendered
through `engine.Home` so reports never contain absolute home paths.
