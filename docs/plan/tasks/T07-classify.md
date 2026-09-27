# T07 — Classifiers (commands, paths, checks, secrets)

**Model:** haiku · **Wave:** B · **Depends on:** T01 · **Milestone:** M1

## Goal

One package that every detector uses to classify shell commands, file paths,
check results, secret-shaped strings and suppression markers. Everything is
table-driven so it is easy to extend.

## Read first

- `docs/plan/CONTRACTS.md` "internal/classify" (exact API)
- `internal/model/event.go` (`Command`), `internal/config/config.go`

## Files you own

- `internal/classify/command.go`, `shell.go`, `packages.go`, `check.go`, `path.go`, `glob.go`, `secrets.go`, `markers.go`
- matching `_test.go` files

## Shell helpers (`shell.go`)

- `Unwrap(cmd)`: if cmd matches `^(/bin/|/usr/bin/)?(ba|z)?sh\s+-l?c\s+(['"])(.*)\3\s*$` (dot matches newline), return the inner text with `'\''` → `'`. Repeat until no wrapper. Otherwise return cmd trimmed.
- `Segments(cmd)`: split `Unwrap(cmd)` on `&&`, `||`, `;`, `|`, and newlines that are outside single quotes, double quotes and `$( … )`, and not backslash-escaped. Trim each; drop empty ones.
- `Tokens(segment)`: shell-like split on unquoted whitespace; remove surrounding quotes; keep `$VAR` literally.
- `stripPrefix(tokens)` (unexported): drop leading `NAME=value` assignments and the words `sudo`, `time`, `command`, `exec`, `nohup`, `env` (plus its assignments). Return the remaining tokens **and** the assignments (VER-5 needs them).
- `ShortCmd(cmd)`: `Unwrap`, collapse whitespace runs to one space, `model.Clip(…, 80)`.

## `Command(cmd, extra)` (`command.go`)

**Note:** in every table in this file, `\|` is a Markdown-escaped `|`; in the
Go regex it is plain regex alternation.

For each segment, strip prefixes, rebuild a normalized string `s` from the
remaining tokens joined by single spaces, and test the table below (Go RE2,
anchored with `^` unless shown otherwise). A segment may get several classes.
Also apply `extra` (`config.CheckPattern{Class, Regex}`) to `s`. Return the
union over all segments, sorted and deduplicated.

| Class | Patterns on `s` |
|---|---|
| `test` | `(npm\|pnpm\|yarn\|bun) (run )?test\b`, `(npm\|pnpm\|yarn) t\b`, `(npx \|pnpm exec \|yarn \|bunx )?(jest\|vitest\|mocha\|ava\|playwright test\|cypress run)\b`, `(python3? -m \|uv run \|poetry run )?pytest\b`, `py\.test\b`, `python3? -m unittest\b`, `go test\b`, `cargo (test\|nextest)\b`, `(bundle exec )?(rspec\|rake test)\b`, `(mvn\|\./mvnw)\b.*\btest\b`, `(gradle\|\./gradlew)\b.*\btest\b`, `dotnet test\b`, `node --test\b`, `deno test\b`, `make (test\|check)\b`, `tox\b`, `phpunit\b`, `swift test\b`, `mix test\b` |
| `typecheck` | `(npx \|pnpm exec \|yarn \|bunx )?tsc\b`, `(npm\|pnpm\|yarn\|bun) (run )?(typecheck\|type-check\|check-types\|tsc)\b`, `(uv run \|poetry run \|python3? -m )?mypy\b`, `pyright\b`, `go vet\b`, `cargo check\b` |
| `lint` | `(npx \|pnpm exec \|yarn \|bunx )?(eslint\|oxlint\|stylelint\|biome (lint\|check))\b`, `(npx )?prettier (--check\|-c)\b`, `(npm\|pnpm\|yarn\|bun) (run )?lint\b`, `(uv run \|poetry run )?(ruff( check)?\|flake8\|pylint)\b`, `black --check\b`, `golangci-lint\b`, `staticcheck\b`, `cargo clippy\b`, `(bundle exec )?rubocop\b`, `shellcheck\b`, `gofmt -l\b` |
| `build` | `(npm\|pnpm\|yarn\|bun) (run )?build\b`, `go build\b`, `cargo build\b`, `make( \|$)` (only when not already `test`), `(mvn\|\./mvnw)\b.*\b(package\|install\|compile)\b`, `(gradle\|\./gradlew)\b.*\b(build\|assemble)\b`, `dotnet build\b`, `(npx )?(vite\|next\|webpack\|rollup\|esbuild\|nuxt\|astro) build\b` |
| `format` | `(npx )?prettier (--write\|-w)\b`, `gofmt -w\b`, `go fmt\b`, `black\b` (not `--check`), `ruff format\b`, `cargo fmt\b` (not `--check`), `(npm\|pnpm\|yarn\|bun) (run )?format\b` |
| `install` | `npm (i\|install\|add\|ci)\b`, `pnpm (add\|i\|install)\b`, `yarn add\b`, `yarn( install)?$`, `bun (add\|i\|install)\b`, `pip3? install\b`, `uv (add\|pip install)\b`, `poetry add\b`, `go get\b`, `go install \S+@`, `cargo (add\|install)\b`, `gem install\b`, `bundle (add\|install)\b`, `brew install\b`, `apt(-get)? install\b` |
| `git_commit` | `git commit\b` |
| `git_push` | `git push\b` |
| `git_force_push` | `git push\b` with a token `-f`, `--force`, `--force-with-lease…`, or a refspec starting with `+` |
| `git_reset_hard` | `git reset\b.*--hard\b` |
| `git_discard` | `git checkout (--\|\.$\|\. )`, `git checkout -- `, `git restore\b` (unless `--staged` only), `git clean\b.*-[a-zA-Z]*f`, `git stash( (push\|save))?( \|$)` (not `list`/`show`/`pop`/`apply`/`drop`), `git switch\b.*--discard-changes` |
| `git_history` | `git (rebase\|cherry-pick\|merge\|revert\|am)\b`, `git commit\b.*--amend\b` |
| `no_verify` | `git (commit\|push\|merge\|rebase)\b` with a token `--no-verify`, or (commit only) a short-flag cluster token matching `^-[a-zA-Z]*n[a-zA-Z]*$` that is not the value following `-m`, `-F`, `--message`, `-C`, `-c` |
| `hook_skip_env` | an assignment `HUSKY=0`, `HUSKY_SKIP_HOOKS=1`, `SKIP=…`, `LEFTHOOK=0`, `OVERCOMMIT_DISABLE=1`, `PRE_COMMIT_ALLOW_NO_CONFIG=1`; or `git -c core.hooksPath=\S+` |
| `rm_rf` | `rm\b` with flags containing `r`/`R` (e.g. `-rf`, `-fr`, `-Rf`, `-r -f`, `--recursive`) |
| `migration` | `prisma (migrate (deploy\|dev\|reset)\|db push)\b`, `knex migrate\b`, `sequelize db:migrate\b`, `typeorm migration:run\b`, `alembic upgrade\b`, `flask db upgrade\b`, `python3? manage\.py migrate\b`, `(bin/)?rails db:(migrate\|reset\|drop)\b`, `rake db:migrate\b`, `goose\b.*\bup\b`, `migrate\b.*\bup\b`, `atlas migrate apply\b`, `dbmate (up\|migrate)\b`, `drizzle-kit (push\|migrate)\b`, `supabase db (push\|reset)\b`, `(psql\|mysql)\b.*(-f\|--file)\b`, `(psql\|mysql)\b.*\b(INSERT\|UPDATE\|DELETE\|DROP\|ALTER\|CREATE\|TRUNCATE)\b` (case-insensitive) |
| `local_http` | `(curl\|wget\|http\|https\|xh)\b` whose URL host is `localhost`, `127.0.0.1`, `0.0.0.0` or `[::1]` (checked **before** network classes; a local request is never `network_*`) |
| `network_write` | `curl\b` with `-X`/`--request` `POST\|PUT\|PATCH\|DELETE`, or `-d`, `--data…`, `-F`, `--form`, `-T`, `--upload-file`; `wget\b.*(--post-data\|--post-file\|--method=(POST\|PUT\|PATCH\|DELETE))`; `(http\|https\|xh) (POST\|PUT\|PATCH\|DELETE)\b`; `scp\b`; `rsync\b` with an argument containing `:`; `ssh\b` |
| `network_read` | other `curl\b`, `wget\b`, `(http\|https\|xh)\b`; `git (clone\|fetch\|pull)\b` |
| `container` | `(docker\|podman)\b`, `docker-compose\b` |
| `infra` | `(kubectl\|helm\|terraform\|tofu\|pulumi\|ansible\|ansible-playbook\|cdk\|serverless\|sls)\b` |
| `cloud` | `(aws\|gcloud\|gsutil\|az\|doctl\|fly\|flyctl\|vercel\|netlify\|heroku\|wrangler\|firebase\|supabase)\b` |
| `publish` | `(npm\|pnpm) publish\b`, `yarn (npm )?publish\b`, `cargo publish\b`, `twine upload\b`, `poetry publish\b`, `gem push\b`, `docker push\b`, `gh release create\b`, `goreleaser( release)?\b` |
| `gh_write` | `gh (pr (create\|merge\|close\|edit\|comment\|review\|ready\|reopen)\|issue (create\|close\|edit\|comment\|reopen)\|release (create\|delete\|edit\|upload)\|repo (create\|delete\|edit\|rename\|archive)\|workflow run\|run (rerun\|cancel)\|secret set\|variable set\|label create)\b`; `gh api\b` with `-X`/`--method` other than GET, or any `-f`/`-F`/`--field`/`--raw-field` |
| `gh_read` | any other `gh\b` command |
| `dev_server` | `(npm\|pnpm\|yarn\|bun) (run )?(dev\|start\|serve\|preview)\b`, `(npx )?(vite\|next dev\|nuxt dev\|astro dev)\b` (not `build`), `(bin/)?rails (s\|server)\b`, `python3? manage\.py runserver\b`, `flask run\b`, `uvicorn\b`, `gunicorn\b`, `php artisan serve\b`, `hugo server\b`, `python3? -m http\.server\b` |
| `snapshot_update` | a `test` segment with a token `-u`, `--updateSnapshot`, `--update-snapshots`, `--snapshot-update`, or assignment `UPDATE_SNAPSHOTS=1` |
| `read_file` | `(cat\|head\|tail\|less\|more\|bat\|nl)\b` with a non-flag argument; `sed -n\b` |

`CheckClass(classes)` returns the first of `test`, `typecheck`, `lint`,
`build` present.

## `packages.go`

`Packages(cmd)`: for `install` segments, return non-flag arguments after the
subcommand as packages, skipping values of flags that take one (`-r`, `-c`,
`--registry`, `--index-url`, `-e`, `--prefix`, `--save-prefix`). Parse
versions: npm `name@1.2.3`, `@scope/name@^1`; pip `name==1.2`, `name>=1.2`,
`name[extra]==1.2`; go `module@v1.2.3`; cargo `name@1.2`. Manager = first
token (`npm`, `pnpm`, `yarn`, `bun`, `pip`, `uv`, `poetry`, `go`, `cargo`,
`gem`, `bundle`, `brew`, `apt`). Bare `npm install` / `pip install -r x` → no packages.

`ReadTargets(cmd)`: non-flag args of `read_file` segments (for `sed -n`,
skip the first non-flag arg, the script). `RmTargets(cmd)`: non-flag args of `rm_rf` segments.

## `check.go`

`Check(c)`:
1. Status from the command: `ExitCode==0` or (`ExitCode==nil` and `Status==ok`) → pass;
   `ExitCode>0` or `Status==failed` or `Status==timeout` → fail; otherwise unknown.
2. Counts from `c.Output`, using the **last** match of each pattern; unset counts are `-1`:
   - `(\d+) passed`, `(\d+) failed`, `(\d+) (skipped|ignored)` (jest, vitest, pytest, cargo)
   - `(\d+) passing`, `(\d+) failing`, `(\d+) pending` (mocha) — only if the first set found nothing
   - `# pass (\d+)`, `# fail (\d+)`, `# skipped (\d+)` (node --test)
   - `(\d+) examples?, (\d+) failures?(?:, (\d+) pending)?` (rspec): passed = examples − failures − pending
   - go test: `Failed` = count of lines starting `--- FAIL:`; `Passed` = count of `--- PASS:` (leave −1 when zero and no `-v`)
3. If status is unknown and counts were found: fail when `Failed > 0`, else pass.

## `path.go` and `glob.go`

`Path(rel, cfg)` returns every class that applies:

| Class | Rule on the repo-relative path (`/` separators) |
|---|---|
| `test` | regexes `(^\|/)(test\|tests\|__tests__\|spec\|specs\|e2e)/`, `\.(test\|spec)\.[cm]?[jt]sx?$`, `_test\.go$`, `(^\|/)test_[^/]*\.py$`, `_test\.py$`, `_spec\.rb$`, `Tests?\.(java\|cs\|kt)$`, plus `cfg.TestPathPatterns` |
| `ci_config` | `^\.github/workflows/`, `^\.gitlab-ci\.yml$`, `^\.circleci/`, `^Jenkinsfile$`, `^azure-pipelines\.yml$`, `^\.buildkite/`, `^bitbucket-pipelines\.yml$`, `^\.travis\.yml$` |
| `manifest` | basename in `package.json go.mod requirements.txt pyproject.toml setup.py setup.cfg Pipfile Cargo.toml Gemfile pom.xml build.gradle build.gradle.kts composer.json mix.exs deno.json`, or matches `requirements[-_.].*\.txt`, `\.csproj$` |
| `lockfile` | basename in `package-lock.json pnpm-lock.yaml yarn.lock bun.lock bun.lockb go.sum Cargo.lock poetry.lock Pipfile.lock Gemfile.lock composer.lock uv.lock` |
| `secret_file` | basename matches `^\.env(\..+)?$` except `.env.example`, `.env.sample`, `.env.template`, `.env.dist`; or `\.(pem\|key\|p12\|pfx\|keystore\|jks)$`; `^id_(rsa\|ed25519\|ecdsa\|dsa)`; `^\.(npmrc\|pypirc\|netrc)$`; `^credentials(\.json)?$`; `kubeconfig`; `service-account.*\.json$`; or the path contains `.aws/credentials` or `.ssh/` |
| `generated` | any `cfg.GeneratedPaths` glob matches |
| `sensitive` | any `cfg.SensitivePaths` glob matches |
| `snapshot` | contains `__snapshots__/` or ends `.snap` |
| `docs` | ends `.md`, `.mdx`, `.rst`, `.adoc`, or starts `docs/` |

`Glob(pattern, path)`: `**` matches zero or more whole segments; `*` any
run within a segment; `?` one char; `[...]` a class (use `path.Match` per
segment). A pattern starting with `**/` also matches at the root
(`**/package.json` matches `package.json`). Patterns ending `/**` match
everything below that directory.

## `secrets.go`

`Secrets(text)` runs these patterns in order; a span matched by an earlier
pattern is not reported again. Deduplicate by `(Kind, Masked)`.
`Masked` = first 4 characters + `…` + `(<n> chars)`.

| Kind | Pattern |
|---|---|
| `private_key` | `-----BEGIN ((RSA\|EC\|DSA\|OPENSSH\|PGP) )?PRIVATE KEY( BLOCK)?-----` |
| `aws_access_key` | `\b(AKIA\|ASIA)[0-9A-Z]{16}\b` |
| `github_token` | `\bgh[pousr]_[A-Za-z0-9]{36,}\b`, `\bgithub_pat_[A-Za-z0-9_]{40,}\b` |
| `anthropic_key` | `\bsk-ant-[A-Za-z0-9_-]{20,}` |
| `openai_key` | `\bsk-(proj-)?[A-Za-z0-9_-]{20,}` |
| `stripe_key` | `\b(sk\|rk)_live_[A-Za-z0-9]{16,}\b` |
| `slack_token` | `\bxox[baprs]-[A-Za-z0-9-]{10,}` |
| `google_api_key` | `\bAIza[0-9A-Za-z_-]{35}\b` |
| `jwt` | `\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}` |
| `db_url_password` | `\b(postgres\|postgresql\|mysql\|mongodb(\+srv)?\|redis\|amqp)://[^:\s/]+:[^@\s]+@` |
| `generic_secret` | `(?i)\b(api[_-]?key\|secret\|token\|password\|passwd)\b\s*[:=]\s*['"]?[A-Za-z0-9/+_\-]{16,}` |

## `markers.go`

- `Suppression(line)` → kind for the first of: `eslint-disable`, `biome-ignore`,
  `@ts-ignore`, `@ts-expect-error`, `@ts-nocheck`, `# type: ignore`, `# noqa`,
  `pylint: disable`, `nolint`, `rubocop:disable`, `@SuppressWarnings`,
  `#[allow(`, `istanbul ignore`, `c8 ignore`, `pragma: no cover`, `NOSONAR`,
  `#pragma warning disable`. Kind is the matched text.
- `SkipMarker(line)` → `"only"` for `\b(it|test|describe|context)\.only\(`,
  `\b(fit|fdescribe)\(`; `"skip"` for `\b(it|test|describe|context)\.skip\(`,
  `\b(xit|xtest|xdescribe)\(`, `@pytest\.mark\.skip(if)?\b`, `pytest\.skip\(`,
  `unittest\.skip`, `\bt\.Skip(Now|f)?\(`, `@(Ignore|Disabled)\b`, `#\[ignore\]`,
  `test\.fixme\(`; `"xfail"` for `@pytest\.mark\.xfail\b`; `"todo"` for `\b(it|test)\.todo\(`.
- `AssertionLike(line)` → matches `\b(expect|assert\w*|should|require\.\w+|t\.(Error|Errorf|Fatal|Fatalf|Fail|FailNow)|Assert\.\w+|XCTAssert\w*)\b`.

## Tests

One table-driven test per file with at least these rows:

- `Command`: `npm test` → test; `bash -lc 'cd app && pnpm vitest run'` → test;
  `npx tsc --noEmit` → typecheck; `ruff check .` → lint; `make` → build;
  `make test` → test only; `git commit -nm "fix"` → git_commit+no_verify;
  `git commit -m "-n is fine"` → git_commit only;
  `HUSKY=0 git commit -m x` → git_commit+hook_skip_env;
  `git push --force-with-lease origin HEAD` → git_push+git_force_push;
  `git push origin +main` → force; `git reset --hard HEAD~1`;
  `git checkout -- src/a.ts` → git_discard; `git stash list` → nothing;
  `rm -rf dist node_modules` → rm_rf; `prisma migrate deploy` → migration;
  `curl -X POST https://api.example.com/x` → network_write;
  `curl http://localhost:3000/health` → local_http only;
  `curl -s https://example.com` → network_read;
  `gh pr view 12` → gh_read; `gh api repos/o/r/issues -f title=x` → gh_write;
  `npm publish` → publish; `kubectl apply -f k.yaml` → infra;
  `jest -u` → test+snapshot_update; `npm run dev` → dev_server;
  `cat src/a.ts | head -20` → read_file; `pip install requests==2.32.0` → install;
  `echo hi` → nothing; extra pattern `{Class:"test", Regex:"^just test"}` → test.
- `Packages`: `npm i p-retry@6.2.1 -D` → {npm,p-retry,6.2.1};
  `pnpm add @scope/pkg@^1 other` → two packages; `pip install -r req.txt flask` → flask only;
  `go get github.com/x/y@v1.2.3`; `npm install` → none.
- `Check`: jest output "Tests: 2 failed, 60 passed, 62 total" exit 1 → fail 60/2;
  pytest "=== 1 failed, 5 passed, 2 skipped in 0.3s ===" → counts;
  mocha "62 passing\n2 failing"; cargo "test result: ok. 10 passed; 0 failed; 1 ignored";
  node "# pass 3\n# fail 0"; rspec "12 examples, 1 failure, 2 pending" → passed 9;
  go "--- FAIL: TestX" ×2 exit 1; unknown status with "3 passed" → pass.
- `Path`: every class with one positive and one negative example, including
  `.env.example` not secret and `**/package.json` matching root `package.json` as sensitive.
- `Glob`: `**/x.go` vs `x.go` and `a/b/x.go`; `deploy/**` vs `deploy/a/b`; `*.tf` vs `a/b.tf` (false: `*` does not cross `/`).
- `Secrets`: one hit per kind; masked value never contains more than 4 source characters; an `sk-ant-…` key reported once as `anthropic_key`, not also `openai_key`.
- `Suppression`, `SkipMarker`, `AssertionLike`: positive and negative rows.
- `Unwrap`/`Segments`/`Tokens`: quotes, escaped `'\''`, `&&` inside quotes not split, `$(a && b)` not split.

## Acceptance

```sh
go test ./internal/classify/... -count=1
```
