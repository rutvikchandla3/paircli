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

`paircli login` uses `https://pair.sh` by default. Set
`PAIRCLI_SERVER_URL` or pass `--server` to log in against a private or local
Pair backend; credentials are scoped to the server that issued them.

## Use

```sh
paircli hook install      # optional: link sessions to commits by exact SHA
paircli scan 482          # scan a PR by number or URL
paircli scan 482 --post   # also post the summary as a PR comment
paircli doctor            # check what paircli can see on this machine
paircli setup             # update paircli, install hooks, log in (idempotent)
paircli status            # login, hooks, setup, recent publications (refreshes pending ones)
paircli publish 482       # scan, redact and publish a PR's evidence to Pair
paircli publish 482 --dry-run   # build the bundle and show the plan, upload nothing
```

With the hooks installed, a successful `gh pr create` or a `git push` on a
branch with an open PR starts `paircli publish` in the background. See
[docs/SETUP.md](docs/SETUP.md).

Output goes to `~/.paircli/scans/<owner>__<repo>/pr-482/<head-commit>/`
(`--out` overrides it) — never into the repository, so a `git add .` cannot
commit it. Start with `report.md`; `comment.md` is the PR comment and
`signals.json` is the full machine-readable result.

## Signals

Thirty signals across eight questions: intent, authorship, verification,
decisions, friction, exposure, oversight and consistency. See
[docs/SIGNALS.md](docs/SIGNALS.md).

Three signals need an LLM pass (`--llm`); everything else is deterministic.

## Privacy

No telemetry. paircli reads local session transcripts and the PR via `gh`.
Nothing is sent to an LLM unless you opt in with `--llm`. `scan` keeps its
output on your machine. `publish` is the only command that uploads: a bundle
of the scan output plus the new part of the raw session transcripts, with
secrets (tokens, keys, passwords, your own secret-looking environment
variables) redacted first. Redaction is pattern based and best effort.
What is uploaded and what is redacted:
[docs/CONFIGURATION.md](docs/CONFIGURATION.md).

## More

- [Setup, status and hooks](docs/SETUP.md)
- [Configuration](docs/CONFIGURATION.md) (`.paircli.json`, LLM providers)
- [Releasing and updates](docs/RELEASING.md)
- [Architecture](docs/ARCHITECTURE.md)

## Development

```sh
go test ./...
```
