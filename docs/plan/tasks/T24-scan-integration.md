# T24 — Scan integration, doctor, `--post`, legacy removal

**Model:** sonnet · **Wave:** D · **Depends on:** every task in waves A–C · **Milestone:** M1

## Goal

Wire the whole pipeline into an importable `internal/scan` package and the
CLI, add `--post` and a v2 `doctor`, and delete the v1 code.

## Read first

- `docs/plan/README.md` "Architecture"; `docs/plan/CONTRACTS.md` (all APIs, including "internal/scan")
- Current `cmd/paircli/*.go`

## Files you own

- `internal/scan/*.go` + tests (new)
- `cmd/paircli/main.go`, `scan.go`, `doctor.go` (rewrite); `hook.go` untouched except imports if needed
- Deletions: `internal/session/`, `internal/output/`, `internal/correlate/`,
  `internal/claudecode/pathb.go`, `ScanPathB` in `internal/codex/codex.go` and
  `internal/pi/pi.go` (delete those files if nothing else remains)
- `internal/codex/rollout.go`: replace the private `ownerRepo` with `gitinfo.OwnerRepoFromURL` (only this change)

## `internal/scan`

```go
type Options struct {
	Repo            string    // "owner/repo"
	Number          int
	CWD             string    // where scan was invoked
	OutDir          string    // default <repo root or CWD>/.paircli/pr-<n>
	Runner          pr.Runner // gh runner; pr.GH{} in the CLI
	Config          *config.Config // nil → config.Load(repo root)
	NoHooks         bool
	NoCommitPatches bool
	Post            bool
	IncludePrompts  bool
	LLM             llm.Provider // nil = LLM pass off (T26 uses it)
	Now             time.Time    // zero → time.Now()
	Version         string
}

type Result struct {
	Report *model.Report
	OutDir string
	Alerts int
}

func Run(ctx context.Context, o Options) (*Result, error)
```

`Run` steps:
1. `p := pr.Fetch(o.Runner, o.Repo, o.Number, !o.NoCommitPatches)`.
2. Repo root: `gitinfo.TopLevel(o.CWD)` when that repo's `OwnerRepo` equals
   `o.Repo` (case-insensitive), else "". Config: `o.Config` or `config.Load(root)`.
   `Comment.IncludePrompts` is true if either config or `o.IncludePrompts` says so.
3. `w := link.WindowFor(p, cfg)`; `all := link.Discover(cfg, w, !o.NoHooks)`.
4. `cands, dropped := link.Select(p, all, w)`.
5. `attr := attrib.Attribute(p, cands, cfg)`; `cs := attrib.CommitSessions(p, cands)`.
6. `res := link.Finalize(p, cands, attr, cs, dropped)`.
7. `attr = attrib.Attribute(p, res.Sessions, cfg)` (only linked sessions).
8. `ec := engine.NewContext(p, res.Sessions, attr, res.Links, cfg)`; `ec.SessionLinks = res.SessionLinks`.
9. `sigs := engine.Run(ec)`.
10. `// T26: LLM pass` — leave a clearly marked no-op block where T26 adds the judge.
11. `rep := render.BuildReport(...)`; `render.Write(render.Options{OutDir, IncludePrompts, MaxCommentLines: cfg.Comment.MaxLines}, rep, attr, res.Sessions)`.
12. If `o.Post`: `postComment` (below).
13. `Alerts` = signals with State alert.

`postComment(r pr.Runner, repo string, n int, bodyPath string)`:
list comments with `gh api repos/<repo>/issues/<n>/comments --paginate`
(JSON array, `id`, `body`); if one body starts with `<!-- paircli -->`, update it with
`gh api -X PATCH repos/<repo>/issues/comments/<id> -F body=@<bodyPath>`;
otherwise `gh pr comment <n> -R <repo> --body-file <bodyPath>`.

## CLI

```
paircli scan <pr-number-or-url> [--out DIR] [--post] [--include-prompts]
                                [--llm none|anthropic|claude-cli] [--model M]
                                [--no-hooks] [--no-commit-patches]
                                [--window-before 48h] [--window-after 2h] [--json]
paircli hook install|uninstall <harness>
paircli hook <harness> <Event>
paircli doctor
paircli version
```

- Use the standard `flag` package with a per-subcommand `FlagSet`; flags may
  appear after the positional PR argument (parse positional first, then the rest).
- `--llm`/`--model` override `cfg.LLM`; build the provider with `llm.New`;
  `none` → nil.
- `--window-*` override config durations.
- Output: one line `paircli: wrote <dir> — {n} sessions, {pct} of changed lines explained, {a} alerts`;
  `--json` also prints `signals.json` to stdout.
- `var version = "0.2.0-dev"` in `main.go`; `paircli version` prints it.

## `doctor`

Aligned text, one row per harness:

```
HARNESS      SESSIONS (30d)   HOOKS                     LAST HOOK EVENT
claude-code  142              installed                 2026-09-27 14:07 UTC
codex        37               installed, 3/8 trusted    never
pi           12               not installed             -
```

Then: `gh`: `authenticated` / `not authenticated` (`gh auth status` exit code) /
`not found`; `config`: path of `.paircli.json` or `defaults`; `llm`: provider,
model, and whether the needed key/binary is present. Sessions (30d) =
`len(Discover(root, now-30d))`. Last hook event = newest record time in the
hook log for that harness (Pi: `-`, its records live in session files).

## Tests

- `internal/scan`: `TestRun_Minimal` — fake runner (PR JSON + diff), temp
  roots with one synthetic Claude Code transcript whose CWD is a temp dir with
  `.git/config` for `acme/shop`; asserts output files exist, AUTH-2 present,
  `Alerts` count. (The full golden scenario is T25.)
- `TestPostComment_CreateAndUpdate` — fake runner sees `gh pr comment` when no
  marker comment exists and `gh api -X PATCH` when one does.
- `cmd/paircli`: `TestParseScanArgs` (flags after positional, overrides), `TestDoctor_Output` with temp HOME (no panics, rows present).
- `go vet ./...` clean after deletions; no remaining imports of deleted packages.

## Acceptance

```sh
go build ./... && go vet ./... && go test ./...
go build -o /tmp/paircli ./cmd/paircli && /tmp/paircli version && /tmp/paircli doctor
```

Manual check for the orchestrator: run `/tmp/paircli scan <real PR number>`
in a repo where you have recent agent sessions, read `report.md`, and note
anything wrong. Never commit the output.
