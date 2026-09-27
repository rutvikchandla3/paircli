# T06 — PR fetch + unified-diff parser

**Model:** sonnet · **Wave:** B · **Depends on:** T01 · **Milestone:** M1

## Goal

Fetch a PR's metadata, combined diff and (optionally) per-commit patches via
the `gh` CLI, and parse unified diffs into `model.DiffFile`.

## Read first

- `docs/plan/CONTRACTS.md` "internal/pr and internal/diff"
- `cmd/paircli/scan.go` (`resolveRepoAndPR`, `fetchPR` — legacy logic to port; do not edit the file)
- `internal/model/pr.go`

## Files you own

- `internal/diff/diff.go`, `internal/diff/diff_test.go`, `internal/diff/testdata/**`
- `internal/pr/pr.go`, `internal/pr/pr_test.go`, `internal/pr/testdata/**`

## `internal/diff`

`Parse(unified string) ([]model.DiffFile, error)` handles `git diff` /
`gh pr diff` output:
- File sections start at `diff --git a/<old> b/<new>`. Paths come from the
  `+++ b/…` / `--- a/…` lines when present (strip `a/` `b/`; `/dev/null` means
  added/deleted); fall back to the `diff --git` header. Quoted paths with
  spaces (`"a/my file.txt"`) are unquoted.
- Status: `new file mode` → added; `deleted file mode` → deleted;
  `rename from`/`rename to` → renamed with `OldPath`; otherwise modified.
- `Binary files … differ` or `GIT binary patch` → `Binary:true`, no hunks.
- Hunk header `@@ -a[,b] +c[,d] @@ section` (missing counts mean 1). Track
  old/new line numbers: context lines advance both, `-` old only, `+` new only.
  `\ No newline at end of file` is ignored.
- `Path` for deleted files is the old path.
- Malformed input returns the files parsed so far and a non-nil error only
  when nothing could be parsed from non-empty input.

## `internal/pr`

```go
type Runner interface{ Run(args ...string) ([]byte, error) }
type GH struct{}                         // exec.Command("gh", args...).Output(), stderr included in errors
func Fetch(r Runner, repo string, number int, withCommitPatches bool) (*model.PR, error)
func ResolveArg(cwd, arg string) (repo string, number int, err error)
```

`Fetch` calls, in order:
1. `gh pr view <n> -R <repo> --json number,url,title,body,headRefName,baseRefName,createdAt,commits`
   → fill PR fields; `commits[]` → `Commit{SHA: oid, Time: committedDate, Message: messageHeadline + "\n\n" + messageBody (trimmed)}`.
2. `gh pr diff <n> -R <repo>` → `diff.Parse` → `PR.Files`.
3. If `withCommitPatches`: for each commit (max 100; skip the rest with no
   error), `gh api repos/<repo>/commits/<sha>` → JSON `files[]` with
   `filename`, `previous_filename`, `status`, `patch`. Build each
   `DiffFile` by wrapping `patch` in a minimal header and running
   `diff.Parse`, or by parsing hunks directly; `status` values
   `added|removed|modified|renamed` map to model statuses (`removed` → deleted).
   Files without `patch` (binary or too large) get `Binary:true`.

`ResolveArg` ports the legacy `resolveRepoAndPR`: a PR URL
(`github.com/<o>/<r>/pull/<n>`) or a bare number resolved against the repo
in `cwd` through `gitinfo.Resolve`.

## Tests

`internal/diff`:
- `TestParse_Modified` — line numbers across two hunks, section text.
- `TestParse_AddedDeleted` — `/dev/null` handling; deleted file uses old path.
- `TestParse_Rename` — with and without content changes.
- `TestParse_Binary`
- `TestParse_NoNewlineMarker`
- `TestParse_QuotedPath`
- `TestParse_MissingCounts` — `@@ -3 +3 @@`.
- `TestParse_Empty` — empty input → nil, nil.

`internal/pr` (fake Runner that maps joined args → canned output in `testdata/`):
- `TestFetch_Basic` — fields, commits, files.
- `TestFetch_CommitPatches` — per-commit files and statuses.
- `TestFetch_CommitLimit` — 101 commits → 100 fetched.
- `TestFetch_GHError` — error wraps the command and stderr.
- `TestResolveArg_URL` and `TestResolveArg_Number` (temp git dir with a config file, as in `internal/gitinfo` tests).

## Acceptance

```sh
go test ./internal/diff/... ./internal/pr/...
```
