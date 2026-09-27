# T18 — Exposure: EXP-1 Side effects, EXP-2 Dependencies, EXP-3 Untrusted input, EXP-4 Secret contact

**Model:** sonnet · **Wave:** C · **Depends on:** T02 T07 · **Milestone:** M1

## Read first

- `docs/plan/CONTRACTS.md` engine, testkit, classify; `docs/plan/tasks/T12-intent.md` "Pattern for every detector"
- `docs/SIGNALS.md` rows EXP-1..4

## Files you own

- `internal/detect/exposure/exp1.go`, `exp2.go`, `exp3.go`, `exp4.go`, `manifests.go`, `exposure_test.go`

Commands include human-run ones; mark them with ` (run by the author)` in the finding.

## EXP-1 Side-effect ledger

Commands with any of these classes produce a finding (one per command, the
most severe class decides the wording):

| Class | Severity | Finding |
|---|---|---|
| `git_force_push` | alert | `` Force-pushed with `{cmd}` at {Clock}. `` |
| `git_reset_hard` | alert | `` Hard reset with `{cmd}` at {Clock}. `` |
| `migration` | alert | `` Ran a database migration: `{cmd}` at {Clock}. `` |
| `publish` | alert | `` Published a package or release: `{cmd}` at {Clock}. `` |
| `infra`, `cloud` | alert | `` Changed infrastructure or cloud resources: `{cmd}` at {Clock}. `` |
| `network_write` | alert | `` Sent data to an external host: `{cmd}` at {Clock}. `` |
| `gh_write` | alert | `` Changed GitHub state: `{cmd}` at {Clock}. `` |
| `rm_rf` | alert, or info when every target's base name is in `dist build out target node_modules .next .nuxt .cache coverage __pycache__ .pytest_cache tmp .turbo` | `` Deleted files with `{cmd}` at {Clock}. `` |
| `container` | info | `` Ran a container command: `{cmd}` at {Clock}. `` |
| `git_push` | info | `` Pushed with `{cmd}` at {Clock}. `` |

Also:
- non-failed `file_edit` with `RelPath == ""` in a session whose `RepoRoot != ""`
  → `` Wrote outside the repo: `{Home(Path)}` at {Clock}. `` alert, or info when
  the path is under `/tmp`, `/private/tmp`, `/var/folders` or `os.TempDir()`.
- `cwd_change` to a directory outside `RepoRoot` → `` Moved the working directory outside the repo to `{Home(To)}`. `` info.

Summary: `Effects outside the diff: {counts by label, e.g. "1 migration, 1 force push, 2 deletions"}.`
none → `clear`, `No commands with effects outside the diff.`
State `alert` if any alert finding, else `info`/`clear`. Data `{"by_class": {class: n}, "outside_repo_writes": n}`.

## EXP-2 Dependency provenance

**Installs** (commands with class `install`): for each `classify.Packages(cmd)`:
- if the command failed and its output matches
  `(?i)(E404|404 Not Found|not found in the npm registry|No matching distribution|Could not find a version|could not resolve|unknown revision|no such package|is not in the npm registry)`
  → finding alert `` Tried to install `{name}`: the registry says it does not exist. ``

**Added dependencies** from the PR diff (`manifests.go`), per manifest file:
- `package.json`: walk each hunk's lines (context and added), tracking whether
  the current object key is one of `dependencies`, `devDependencies`,
  `peerDependencies`, `optionalDependencies` (a line `"<key>": {` opens it, a
  line starting `}` closes it). Added lines `"<name>": "<version>"` inside
  such a block count. If the block cannot be determined from the hunk,
  count the line only when the version looks like a version
  (`^[\^~<>=]*\d|^workspace:|^npm:|^git|^file:|^latest$`).
- `go.mod`: added `require` lines or lines inside a `require (` block: `module vX` (`// indirect` → Data `indirect: true`).
- `requirements*.txt`: added non-comment lines `name[extras]` + optional specifier.
- `pyproject.toml`: added `"name<spec>",` array items, and `name = "…"` under `[tool.poetry.*dependencies]`.
- `Cargo.toml`: added `name = "…"` / `name = { … }` lines under `[dependencies]`, `[dev-dependencies]`, `[build-dependencies]`.
- `Gemfile`: added `gem "name"` lines.

For each added dependency:
- `requested` = some human `prompt` text contains the name (case-insensitive, word boundary).
- `installed_by` = the install command that named it, if any (evidence).
- Finding: requested → info `` Added `{name}{@version}` (mentioned by the author). ``;
  otherwise alert `` Added `{name}{@version}`: the agent chose it; no prompt mentions it. ``
  Anchor at the manifest line. Evidence: install command and/or the attributed source event.

Summary: `{n} dependencies added ({a} agent choice); {f} install attempts for packages that do not exist.` (omit zero parts);
none → `clear`, `No dependencies added or installed.`
State `alert` when any alert finding. Data `{"added":[{"file","name","version","requested","indirect"}], "failed_installs":[names]}`.

## EXP-3 Untrusted-input chain

External reads (per session):
- `lookup` events;
- `mcp_call` whose `Server` or `Tool` matches
  `(?i)(github|gitlab|linear|jira|slack|notion|confluence|web|fetch|browser|playwright|search|http|url|issue|drive|gmail|mail|discord)`;
- commands with class `network_read` or `gh_read`.

Sensitive edits: non-failed `file_edit` with a `RelPath` whose `classify.Path`
includes `sensitive` or `ci_config`.

For each sensitive edit, pair it with the latest external read **before** it in
the same session. One finding per (read, file) pair, alert:
`` {Clock read} read external content ({describe read}) → {Clock edit} edited `{file}`. ``
where describe read = `` `{ShortCmd}` `` or `search “{Clip(query,40)}”` or `` `{server}.{tool}` ``.
Anchors: `c.AnchorsFor(edit)`, fallback `{File}`. Evidence: read and edit.

Summary: `{n} sensitive files were edited after reading external content in the same session.`;
none → `clear`, `No sensitive files were edited after reading external content.`
Data `{"external_reads": r, "chains": n}`.

## EXP-4 Secret contact

Never put an unmasked secret anywhere: use `SecretHit.Masked`.
- Secret-file reads: `file_read` events and `classify.ReadTargets` of commands
  whose path (relative or base name) is classified `secret_file` →
  `` Read `{Home(path)}` at {Clock}. `` info (one finding per file with a count).
- Secrets in the PR diff: `classify.Secrets` over each added line (skip
  `generated` files) → `` Possible {Kind} in `{file}:{line}` ({Masked}). `` alert, anchor at the line.
- Secrets in prompts or command output: `classify.Secrets` over prompt text and
  `Command.Output` → `A {Kind} appeared in {a prompt|command output} at {Clock} ({Masked}).` alert.
  Evidence excerpt must be the masked value only.

Summary: `{d} possible secrets in the diff, {o} in prompts or output; {r} secret files read.` (omit zero parts);
none → `clear`, `No secret files read and no secret-shaped strings found.`
State `alert` if `d + o > 0`, `info` if only reads. Data `{"diff": d, "output": o, "reads": r, "kinds": [..]}`.

## Tests

- EXP-1: one test per class row (table-driven), `TestEXP1_CleanupRmIsInfo`, `TestEXP1_WriteOutsideRepo` (tmp path info vs home path alert, `~` in text), `TestEXP1_Clear`.
- EXP-2: `TestEXP2_NonexistentPackage`, `TestEXP2_PackageJSONBlockTracking` (a `scripts` entry is not a dependency), `TestEXP2_VersionHeuristicWithoutBlock`, `TestEXP2_GoModRequireBlock`, `TestEXP2_Requirements`, `TestEXP2_Requested`, `TestEXP2_Clear`.
- EXP-3: `TestEXP3_IssueThenWorkflowEdit`, `TestEXP3_ReadAfterEditNotLinked`, `TestEXP3_OtherSessionNotLinked`, `TestEXP3_Clear`.
- EXP-4: `TestEXP4_EnvReadViaCat`, `TestEXP4_SecretInDiffMasked` (assert the raw secret appears nowhere in the marshaled signal), `TestEXP4_SecretInOutput`, `TestEXP4_ExampleEnvNotSecret`, `TestEXP4_Clear`.

## Acceptance

```sh
go test ./internal/detect/exposure/...
```
