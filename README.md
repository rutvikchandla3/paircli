# paircli

Turns local AI coding sessions (Claude Code, Codex CLI, Pi) into review signals
for a pull request: what was asked, who wrote which lines, whether it was
verified, where the agent struggled, and how closely a human was watching.

It reads only what is already on your disk and writes a report a reviewer can
read in seconds. It does not review code, give a verdict, or score people.

## Install

```sh
git clone https://github.com/rutvikchandla3/paircli
cd paircli
go build ./cmd/paircli
```

Needs [`gh`](https://cli.github.com/) to fetch the PR.

## Use

```sh
paircli hook install      # optional: link sessions to commits by exact SHA
paircli scan 482          # scan a PR by number or URL
paircli scan 482 --post   # also post the summary as a PR comment
paircli doctor            # check what paircli can see on this machine
paircli judge snapshot.json --llm anthropic   # re-run the LLM pass on a saved scan
paircli update            # install the latest release
```

Output goes to `.paircli/pr-482/<head-commit>/`. Start with `report.md`;
`comment.md` is the PR comment and `signals.json` is the full machine-readable
result.

## Commands

| Command | What it does |
| --- | --- |
| `paircli scan <pr-number-or-url> [flags]` | Discover linked agent sessions, run the signal pipeline, write the output folder |
| `paircli judge <snapshot.json> [flags]` | Run the grounded LLM pass over a scan's snapshot and rewrite its report files |
| `paircli hook install [harness]` | Install hooks; with no harness, every one detected on this machine |
| `paircli hook uninstall [harness]` | Remove hooks; with no harness, all installed ones |
| `paircli hook <harness> <event>` | Internal: invoked by the installed hook, not run by hand |
| `paircli login [flags]` | Log in to the pair backend with a device code |
| `paircli logout [flags]` | Remove the stored credential |
| `paircli doctor` | Report capture state per harness, `gh`, config and LLM |
| `paircli update [flags]` | Download and install the latest verified CLI release |
| `paircli version` | Print the version |
| `paircli help` (`-h`, `--help`) | Print usage |

`harness` is one of `claude-code`, `codex` or `pi`.

### `scan` flags

| Flag | Description |
| --- | --- |
| `--out DIR` | Output folder (default `<repo>/.paircli/pr-<n>/<head-commit>`) |
| `--post` | Create or update the PR comment |
| `--include-prompts` | Let `comment.md` carry prompt text |
| `--llm PROVIDER` | `none`, `anthropic`, `claude-cli`, `pi` or `http` (default: config) |
| `--model MODEL` | Provider model (default: config) |
| `--no-hooks` | Ignore hook-log records |
| `--no-commit-patches` | Skip per-commit patches |
| `--no-agent-trace` | Skip `agent-trace.json` |
| `--snapshot` | Also write `snapshot.json`, a full replay record |
| `--window-before DUR` | Session search window before the first commit (default `48h`) |
| `--window-after DUR` | Session search window after the last commit (default `2h`) |
| `--json` | Print `signals.json` to stdout |

### `judge` flags

| Flag | Description |
| --- | --- |
| `--out DIR` | Output folder (default: the snapshot's own folder) |
| `--llm PROVIDER` | `none`, `anthropic`, `claude-cli`, `pi` or `http` (default: the snapshot's config) |
| `--model MODEL` | Provider model (default: the snapshot's config) |
| `--no-agent-trace` | Skip `agent-trace.json` |
| `--force` | Replay even if another paircli version wrote the snapshot |
| `--json` | Print `signals.json` to stdout |

### `login` / `logout` flags

| Flag | Applies to | Description |
| --- | --- | --- |
| `--server URL` | `login` | Backend base URL (default `$PAIRCLI_SERVER_URL` or the built-in one) |
| `--no-browser` | `login` | Do not open the browser automatically |
| `--timeout DUR` | `login` | Overall time to wait for authorization (default `15m`) |
| `--json` | both | Print the device code payload (`login`) or the result (`logout`) as JSON |

### `update` flags

| Flag | Description |
| --- | --- |
| `--check` | Check for an update without installing it |
| `--quiet` | Suppress output when already current |
| `--json` | Print the update result as JSON |
| `--manifest-url URL` | Release manifest URL (default `$PAIRCLI_UPDATE_MANIFEST_URL` or GitHub Releases) |

## Signals

Thirty signals across eight questions: intent, authorship, verification,
decisions, friction, exposure, oversight and consistency. See
[docs/SIGNALS.md](docs/SIGNALS.md).

Three signals need an LLM pass (`--llm`); everything else is deterministic.

## Privacy

No telemetry. paircli reads local session transcripts and the PR via `gh`.
Nothing is sent to an LLM unless you opt in with `--llm`. Redaction is not
implemented yet, so stored text may contain secrets from your sessions.
Details: [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

## More

- [Configuration](docs/CONFIGURATION.md) (`.paircli.json`, LLM providers)
- [Releasing and updates](docs/RELEASING.md)
- [Architecture](docs/ARCHITECTURE.md)

## Development

```sh
go test ./...
```
