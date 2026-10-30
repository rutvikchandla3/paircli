# Setup, status and hooks

## `paircli setup`

```
paircli setup [--harness claude-code|codex|pi] [--plugin-version X] [--no-login] [--json]
```

Makes a machine ready to publish. It runs three steps, and is safe to run as
often as you like:

1. **Update.** Installs a newer paircli release if there is one. A failure
   (offline, a Homebrew-managed install, a development build) is noted and
   skipped; it never fails setup. `--plugin-version` is the Pair plugin's
   version, which the release manifest may require to be recent enough.
2. **Hooks.** `paircli hook install <harness>`, or for every harness detected
   on this machine when `--harness` is not given.
3. **Login.** The device login, unless a usable credential for the server
   already exists or `--no-login` is given. The one-time code and URL are
   printed to stderr and written to the run's log, because the plugin starts
   setup in the background. The login can take minutes.

Only one setup runs at a time (`locks/setup.lock`); a second run prints that
setup is already running and exits 0. stdout carries one line (or the JSON
with `--json`); progress goes to stderr and to `~/.paircli/logs/setup-<time>.log`.
Exit status is 1 if a hook install or the login failed.

### `setup.json`

Every run rewrites `~/.paircli/state/setup.json`:

```json
{
  "schema_version": 1,
  "state": "idle",
  "error": null,
  "updated_at": "2026-10-27T09:30:00Z",
  "log": "/home/me/.paircli/logs/setup-20261027-093000.log",
  "steps": {"update": "current (0.3.0)", "hooks": "installed for claude-code", "login": "logged in"}
}
```

`state` is `running` while a run is in progress, `failed` when a step other
than the update failed (`error` then holds the real message of each failing
step), and `idle` otherwise. A failure is never sticky: the next run starts
over and replaces the record.

## `paircli status`

```
paircli status [--json] [--pr <github-pr-url>]
```

Reads local state and exits 0 whatever it finds, even when logged out or offline. `--json`:

```json
{
  "schema_version": 1,
  "cli_version": "0.3.0",
  "server_url": "https://pair.sh",
  "logged_in": true,
  "hooks": {"claude-code": true, "codex": false, "pi": false},
  "setup": {"state": "idle", "error": null, "log": "...", "updated_at": "..."},
  "publications": []
}
```

`logged_in` is whether a credential for `server_url` exists and has not
expired; no credential is ever printed. A `running` setup record that no live
process holds the lock for (the run was killed) is reported as `failed`.
`publications` are the records `paircli publish` keeps per PR, most recently
updated first; `--pr` keeps only one PR's.

A publication that is still `uploaded` or `processing` is refreshed first: for
up to five of them, `status` asks the server for the analysis state (3 second
limit each) and saves the answer into the record (`succeeded` and `failed` are
both `ready`, `queued` and `running` stay `processing`; a failed job's error
code is appended to the publication's log file and shown nowhere else). This
is how a publish run without `--wait` ends up `ready`. When you are not logged
in or the server cannot be reached, the stored record is shown unchanged.

The human output speaks only of what the publisher did, one line per PR:
`running (<step>)`, `uploaded` (the evidence is stored; the record's
`processing` also reads `uploaded`), `ready`, or `failed (<step>)` with the
error and log for a run that stopped before its upload was complete. It never
shows an analysis state or an analysis error code, and a record an earlier
version left as failed at the analysis step reads `ready`. `--json` is as
neutral: each publication has `schema_version`, `pr_url`, `owner`, `repo`,
`number`, `head_sha`, `state` (`running`, `uploaded`, `ready` or `failed`, read
as above), `step`, `error` (set only for a `failed` state), `publication_id`,
`log`, `review_url`, `started_at` and `updated_at`, and no field about the
analysis; a record's last step is `finalize`, never `analysis`. The same goes
for `paircli publish --json`, whose `state` is `uploaded` or `ready`. (The
stored record under `~/.paircli/state/` keeps the analysis job and state for
`status`' own refresh.)

## Hooks that publish

`paircli hook install` registers a `PostToolUse` hook on Bash commands for
Claude Code and Codex. Besides recording git state changes, it watches for:

- a successful `gh pr create`, and
- a successful `git push` of the current branch while it has an open PR
  (`gh pr view` in the command's directory, 3 second limit).

It then starts `paircli publish <pr-url> --repo-dir <dir> --wait` detached, in its
own session, with stdout and stderr in
`~/.paircli/logs/publish-<owner>__<repo>__<n>-<time>.log`, and prints one JSON
object so the agent and the user see where the review will appear:

```json
{"systemMessage":"Pair is publishing this PR's review in the background. Pair review: https://pair.sh/owner/repo/pull/12",
 "hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"Pair is publishing the review for this pull request in the background. Pair review: https://pair.sh/owner/repo/pull/12. Share this link with the user."}}
```

The directory is the command's own: `git -C <dir> push` and `cd <dir> && git
push` are followed. Pushes that do not update just the current branch
(`--all`, `--delete`, `-d`, `--mirror`, `--tags`, a `:ref` refspec), failed or
interrupted commands, a `gh pr create` that did not print exactly one GitHub
PR URL, and commands that only mention `git push` (`echo 'git push'`) do
nothing. Nothing is printed in those cases, and a failure to start the publish
is logged to `~/.paircli/hook-errors.log` and never reaches the agent.

Repeated pushes are safe: `paircli publish` takes a per-PR lock. A publish
started while another one is running for the PR leaves a marker
(`~/.paircli/state/locks/<owner>__<repo>__<n>.pending`) and exits; the running
one publishes again when it finishes (up to 3 extra times), so the newest head
is never skipped. The lock is held only until the upload is stored: the
detached `--wait` that follows runs without it, so a push while it waits
publishes at once. A newer publish that itself follows its review (`--wait`,
which the hooks pass) ends the older run's wait without a notification (the
older run notifies for nothing; the newer one does, once its review is ready).
A newer publish without `--wait` follows nothing and ends nothing: the older
run keeps following and sends its `Pair review ready` (the same review URL).
A hook-started publish sends two notifications:
`PR #<n> uploaded to Pair` after the upload and `Pair review ready` (the review
URL in the text) when the review is ready.

## Where things live

| Path | What |
| --- | --- |
| `~/.paircli/bin/paircli` | the installed binary |
| `~/.paircli/state/` | `setup.json`, `publications/`, `locks/`, journals |
| `~/.paircli/scans/<owner>__<repo>/pr-<n>/<head>/` | one scan's output folder (0700) |
| `~/.paircli/logs/` | `setup-*.log`, `publish-*.log` |
| `~/.paircli/hook-errors.log` | hook failures |
| `~/.paircli/auth.json` | the credential (0600) |
