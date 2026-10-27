# Ship the changed files' whole contents in the scan bundle

Status: **plan, round 2, not approved**. No code is written from this document
yet.

Two PRs:

| # | Branch | What |
|---|---|---|
| 1 | `rutvikchandla3/scan-output-in-home` | Move the scan output folder out of the customer's repo into `~/.paircli/scans/` |
| 2 | `rutvikchandla3/ship-changed-file-contents` | Ship the changed files' whole contents, with the binary/media and cropping rules below (rebased on PR 1) |

PR 1 lands first because PR 2 puts whole source files into that folder, and
today nothing stops a `git add .` in a customer repo from committing it.

## Goal

`paircli publish` sends one zip per PR head. Today the only view of the PR's
code in that zip is `snapshot.json`'s `pr.files[].hunks` — the ~3 context lines
GitHub sends around each change (`internal/pr/pr.go` `fetchFiles`,
`internal/model/pr.go` `DiffFile`).

ai-pipeline is adding a *reading guide* stage. It cuts each changed file into
segments (hunk-based, with large hunks and new files cut at function
boundaries) and asks a model to label each segment `review` / `skim` / `skip`.
Its prompt (`evals/reading-guide/prompt.md` line 8) promises that segment
context "may be widened to the whole enclosing function". With hunks only,
neither the cutting nor the widening is possible.

This plan adds the full text of every changed file to the bundle — or, for a
file too large to send whole, the windows of it that the changes touch.

## What the other side does with it (verified)

Read out of `/Users/rutvik/rcode/ai-pipeline` (read-only from here):

- `ingest/bundle.py` `load_bundle` requires `bundle.json` `schema_version == 1`,
  a valid `repo` / `pull_number` / `head_sha`, an exact `scan_dir`, and
  `signals.json` + `authorship.json` under it. **Unknown extra keys and unknown
  extra files are not rejected.**
- `ingest/assemble.py` `ingest_local` does `shutil.copytree(bundle.scan_dir,
  scan_dir)` — **everything under the scan dir is carried into the service's own
  scan input**, so a new subtree under `.paircli/pr-<n>/<head>/` needs no ingest
  change at all.
- `ingest/archive.py` caps a bundle at 100 MiB of source, 1 GB expanded and
  **10,000 members**, and rejects duplicate entry names **case-insensitively**
  (`member.filename.lower()`). There is no entry-name-length rule.
- `generation/snapshot.py` reads `snapshot.json` with a plain `json.load`. It
  never checks the snapshot's `schema_version` and never checks its `hash`.
- `read_json` caps `bundle.json` and `raw-sessions/manifest.json` at 8 MiB.
- Nothing in `src/` consumes changed-file contents yet, so the on-disk contract
  below is ours to define.

---

## PR 1 — the scan folder moves out of the customer's repo

Today `internal/scan/scan.go:106-112` writes to
`<repo root or CWD>/.paircli/pr-<n>/<head>/`, and `docs/CONFIGURATION.md:66`
calls that folder "gitignored". No code adds it to `.gitignore` or
`.git/info/exclude` — `.gitignore` is only in paircli's own repo — so a
`git add .` in a customer repo commits the scan output today, and would commit
whole copies of their source files after PR 2.

**Move the default to `~/.paircli/scans/<owner>__<repo>/pr-<n>/<head>/`.**

- `<owner>__<repo>` follows the naming `state.Key` already uses
  (`state.go:88-91`, lowercased). A new `state.ScanDir(owner, repo, number,
  head)` beside `state.Dir()` and `state.LogDir()`, resolved through
  `os.UserHomeDir()` the same way (`state.go:69-86`), so pointing `HOME` at a
  temp dir is enough to redirect it in tests — the pattern `state`,
  `hooklog` and `deviceauth` all already use.
- **Only the local location moves.** The path inside the zip stays
  `.paircli/pr-<n>/<head>/`, and so does `bundle.json.scan_dir`: ai-pipeline's
  `ingest/bundle.py` requires `scan_dir == ".paircli/pr-<n>/<head>"` exactly.
  This costs nothing, because `internal/publish/bundle.go` already builds the
  zip prefix independently of where it read the files from — `scanRoot` and
  `ScanDir` in `bundleJSON` are both `path.Join(".paircli", ...)`
  (`bundle.go:112`, `:175`) while the input path is `o.ScanDir`. The two were
  already decoupled; this is the first time it matters.
- `--out` keeps working unchanged: `scan.Options.OutDir` is still honoured
  verbatim when set, and it is the only thing that writes outside the default.

### Every writer and reader of that folder

| Site | What it does today | What it needs |
|---|---|---|
| `internal/scan/scan.go:106-119` `Run` | Builds the default outDir from the repo root (or CWD) and `MkdirAll`s it | Use `state.ScanDir(...)`. The `base := root; if base == "" { base = o.CWD }` fallback disappears — a home path needs no repo root, which also removes the odd case of scan output landing in CWD when the checkout's remote does not match the PR |
| `internal/scan/replay.go:22-25`, `:60-62` | Requires an explicit `OutDir`; never defaults one | **Nothing.** `judge` supplies it |
| `cmd/paircli/judge.go:126-135` `judgeOutDir` | Defaults to the directory the snapshot is in | **Nothing.** It is derived from the path the user passed, so it follows the snapshot wherever it lives |
| `cmd/paircli/publish.go:391,424,660` | Logs `res.OutDir`, passes it as `BundleOptions.ScanDir`, prints it in the dry run | **Nothing.** It reads the folder `scan.Run` returned. Worth a glance that the log/dry-run line now shows a home path, and that `engine.Home`-style shortening is not applied to it (reports shorten session paths, not this one) |
| `internal/render/render.go:23`, `internal/render/*` | Writes report files into `opts.OutDir`, **and writes `opts.OutDir` into the footer of `comment.md`** (`render.go:468-469`: ``Full report: `<outDir>/report.md` ``) | **Real change, and the one that bites.** That footer becomes an absolute home path — `/Users/<name>/.paircli/scans/acme__shop/pr-482/<head>/report.md`. `comment.md` is in `bundle.go`'s `scanFiles`, so the publisher's username and home layout would go into every bundle; worse, `paircli scan --post` posts `comment.md` verbatim to GitHub (`scan.go:211` → `postComment` → `gh pr comment --body-file`), putting a local home path in a public PR comment. **Decided: drop the path from the footer entirely**, so it holds no local path in any form: `<sub>Signals describe the change, not the author.</sub>`. The three goldens that carry it are regenerated |
| `internal/publish/bundle.go:112,175` | Zip prefix and `bundle.json.scan_dir` | **Nothing** — deliberately unchanged (above) |
| `cmd/paircli/output.go:18` | Prints `res.OutDir` in the run summary | **Nothing.** Assertion in `cmd/paircli/scan_test.go` may pin the old wording |
| `cmd/paircli/status.go` | Reads `state.Publication` records only | **Nothing** — it never touches the scan folder |
| `internal/doctor`, hook handlers (`claudecode/hooks.go`, `codex/hooks.go`, `pi`) | Do not read the scan folder; hooks only write `~/.paircli/hook-errors.log` and start `paircli publish` | **Nothing** |
| `cmd/paircli/main.go:87` | `--out DIR  output folder (default <repo>/.paircli/pr-<n>/<head-commit>)` | New default text |
| `internal/scan/scan.go:45`, `internal/render/render.go:23`, `docs/plan/CONTRACTS.md:317,373,401`, `docs/plan/tasks/T24-scan-integration.md:31` | Doc comments and the contract table | New default text |
| `docs/ARCHITECTURE.md:58,113,306-317` | The output-folder layout block and the zip layout | The layout block is now a `~/.paircli` path; the zip block is unchanged |
| `docs/CONFIGURATION.md:66` | "`.paircli/pr-<n>/<head-commit>/` inside the repo — the scan artifacts (gitignored)" | Rewrite; the "gitignored" claim is the bug |
| `docs/SETUP.md:117-123` | "Where things live" table | New row: `~/.paircli/scans/<owner>__<repo>/pr-<n>/<head>/` |
| `internal/e2e/*` | Set `OutDir` explicitly (`w.outDir`), so they are default-independent | **Nothing**, except the new `HOME`-based default deserves one e2e that does not pass `--out` and asserts the folder lands under a temp `HOME` |

### What happens to `.paircli/` folders already in customer repos

**Recommendation: never delete them, and say so once.** A stale
`<repo>/.paircli/pr-*/` is the user's data — reports they may have linked to,
snapshots a `paircli judge` is pointed at — and silently removing a directory
under someone's repo root is exactly the kind of surprise paircli avoids
elsewhere (it never runs `git fetch`, never edits `config.toml`).

- `scan` and `publish`, when they find a legacy `.paircli/pr-*/` and are about
  to write somewhere else, log one line: the new location, the old one, and that
  the old one is untouched.
- `paircli doctor` gains a check that reports the same thing without needing a
  scan to be run.
- No deletion command is added. If one is ever wanted it should be explicit
  (`paircli doctor --clean-legacy`), not implicit.

**Scoping, so the hint actually fires.** Both checks must key off `o.CWD`, not
off the resolved repo root, and this is the whole reason the hint is worth
having. `scan.RepoRoot` returns `""` unless the checkout's origin remote matches
owner/repo (`scan.go:260-279`) — the "someone else's repo" case §7 already flags
for the config opt-out, and exactly when a user has a stale `.paircli/` lying
around from a previous clone. `doctorReport` has the same limitation: it uses
`scan.RepoRoot(cwd, "")` (`cmd/paircli/doctor.go:46-49`), so it can only report
a legacy folder in the directory it happens to be run from. CWD is always
available; use it for the legacy check in both places.

**File modes.** `scan.Run` does `os.MkdirAll(outDir, 0o755)` (`scan.go:117`),
which today creates directories inside the customer's repo and would, after the
move, create `~/.paircli` and `~/.paircli/scans` at 0755 as well — while
`state.Dir()` and its `WriteJSONAtomic` create `~/.paircli/state` at 0700 with
0600 files. Moving whole (best-effort) redacted source files under a
group-and-world-readable tree is a change in exposure worth making deliberately,
so PR 1 creates the parent chain — `~/.paircli`, `~/.paircli/scans`, the
`<owner>__<repo>` and `pr-<n>` levels — at **0700**. The artifact files stay at
their current modes: they now sit under a 0700 parent, which is the same
protection `~/.paircli/state` gives its 0600 files, and the zip's entry modes
are fixed at 0644 by `writeZip` regardless of what is on disk.

---

## PR 2 — the changed files' contents

### 1. Redaction of whole files

Two constraints pull against each other, and the naive answer gets one wrong.

**`redact.Bytes` cannot be used as-is.** It splits the input on `\n` and calls
`Text` per line (`internal/redact/redact.go:325-341`), which is right for a
JSONL transcript — the doc says so, and `TestBytesKeepsLinesAndJSON`
(`redact_test.go:158-176`) pins it. But the `private_key` rule is written with
`[\s\S]*?` (`redact.go:77`) precisely so it can span lines, so through `Bytes`
it is dead: a `-----BEGIN OPENSSH PRIVATE KEY-----` block in an added key file
would ship verbatim. A gap that is harmless in a transcript is not harmless in
a whole source tree.

**But `Text` on the whole file would break the contract.** `Text` is free to
replace a multi-line match with a one-line marker, and the entire value of this
artifact is that line N of the content is line N of the diff — the service
segments by the hunk's `old_start` / `new_start` and widens by line range. A
redaction that collapses four lines into one silently shifts every line number
after it.

**Recommendation: add a third entry point to `internal/redact` that runs the
rules over the whole buffer while preserving line structure.**

```go
// FileBytes returns b with secrets removed, running the rules over the whole
// buffer rather than line by line, so a rule may match across lines — which the
// private_key rule needs. Line structure is preserved: a match is replaced by
// the marker plus the newlines it swallowed, so line N of the result is line N
// of the input, whatever the rules did. internal/redact is the single seam for
// scrubbing secrets, so this belongs here and not in the caller.
func (r *Redactor) FileBytes(b []byte) []byte
```

- The replacement for **every** match in this pass is
  `marker(kind) + strings.Repeat("\n", newlines in the replaced span)`, so the
  newline count is preserved unconditionally and needs no per-rule flag. The
  marker lands where the match began and the lines it covered are left empty.
- **What is preserved is the newline count, and the line count only follows
  from it except at one edge**: a match that runs to the end of a file with no
  trailing newline re-emits its newlines, so the result ends with one where the
  input did not and counting the last line the usual way gives one fewer. Every
  line after the match keeps its number either way, so a reader indexing by
  line is unaffected; the file's own line count is taken from the input, not
  from the redacted text.
- **"The replaced span" is the group, not the whole match.** `apply`
  (`redact.go:341-372`) replaces `s[start:end]` for `m[2*ru.group:2*ru.group+2]`
  and copies `s[last:start]` — the match's prefix — through untouched. Several
  rules use `group: 1` with a prefix that can itself hold a newline: `sep` is
  `\\?["']?\s*(?::=|=>|[:=])\s*\\?["']?` (`redact.go:54`) and `\s*` matches
  `\n`, so `secret_assignment` can match `password =\n<value>` where the match
  holds a newline the group does not. Counting the match's newlines would *add*
  a line and trip the `line_shift` assertion below. Count `s[start:end]`.
- The literals pass is the other place a newline can move. `Text` applies
  `r.literals` with a plain `strings.ReplaceAll` (`redact.go:313-318`), not
  through `apply`; the value of an environment variable can hold a real newline
  (the JSON-escaped variant cannot — it holds a backslash and an `n`). That pass
  needs the same re-insertion.
- `Text` and `Bytes` are untouched, byte for byte. No existing golden changes,
  no transcript behaviour changes, and no rule changes what it redacts — so
  `RulesVersion` stays `"1"`.
- `filecontent.Write` asserts the invariant anyway — if the redacted bytes do
  not have the input's line count, the file is skipped with reason
  `line_shift` — because a violation here is a silent correctness bug on the
  other side, and a recorded skip is not.

Cropping (§5) happens **after** redaction, never before: a private key straddling
a window edge is then caught, because the whole file was redacted first and
cropping only drops lines.

### 2. Redaction happens at write time, in `filecontent`, not at zip time

**Recommendation: `filecontent.Write` redacts with `FileBytes` before anything
touches disk, and hashes the redacted bytes.**

The first draft had `filecontent` write raw bytes and let
`bundle.readScanFiles` redact on the way into the zip. That is wrong three ways:
unredacted copies of every changed file would sit in the scan folder; the
manifest's `sha256` would describe bytes that exist nowhere (the zip holds
redacted bytes, the scan dir raw ones); and `e2eAssertNoSecret`
(`internal/e2e/e2e_test.go:307-330`) walks the whole scan dir and exempts only
`sessions/`, so a planted secret in a changed file is a hard test failure.

Redacting in the writer is also the established pattern: `capture.Stage`
redacts before hashing (`internal/capture/capture.go`), and
`assemble._copy_raw_sessions` re-verifies those hashes on the other side.

`bundle.readScanFiles` still runs `redactor.Bytes(data)` over the content files
like every other scan file — now an idempotent no-op (`redact`'s package doc
promises idempotency, and `isPlaceholder` rejects a marker containing
"redacted") — so the bundle's guarantees stay enforced at the boundary rather
than assumed.

Counts: the content pass uses the process-wide `redact.Default()`, which
`cmd/paircli/publish.go:375-427` already resets before the scan and reads after
it, so the content's matches land in `bundle.json.redaction.counts` with no new
plumbing. The manifest records the delta this pass contributed, for debugging.

### 3. Where the content lives — separate files + a manifest, not `snapshot.json`

**Recommendation: a new subtree in the scan folder, `changed-files/`, with
`changed-files/manifest.json` describing it.**

Rejected: carrying the text inside `snapshot.json` as a field on each
`pr.files[]` entry.

| | `changed-files/` + manifest | inside `snapshot.json` |
|---|---|---|
| `snapshot.Hash` | untouched **by the content** (it is not in the record) | changes: the digest ties a set of judgments to one exact input, so widening it to include file text makes every re-cut of a file invalidate unrelated judgments |
| `snapshot.Version` | untouched (`"1"`) | widening the record invites a bump, and `snapshot.Read` hard-rejects a different version (`snapshot.go:142-145`) — every already-written snapshot stops replaying |
| size | its own budget, own zip entries | one giant JSON document; `paircli judge`'s replay parses megabytes it does not need |
| skips | a per-file, per-side reason next to the file | a parallel structure bolted onto a diff record |
| precedent | mirrors `raw-sessions/` + `raw-sessions/manifest.json` | none |

One honest correction to the "untouched" claim: the hash is *not* preserved by
this design overall, because §7 adds a config field and `Snapshot.Config` is a
field of the hashed record (`snapshot.go:60`, `:103-112`). Every newly written
snapshot gets a different hash. That is harmless — nothing compares hashes
across runs or versions (grep for `Hash` finds only `internal/snapshot`, and
ai-pipeline's `generation/snapshot.py` never reads `hash`) — but it is the
actual invariant.

`snapshot.json` is not among the e2e goldens (`internal/e2e/e2e_test.go:21`
lists only `signals.json`, `report.md`, `comment.md`), so nothing golden changes
either way.

The manifest is also the only thing `bundle.go` reads: it lists exactly which
files go in, so a stray file dropped into `changed-files/` is never uploaded,
keeping `bundle.go`'s standing rule — "the client never uploads text a model
wrote" (`TestBuildBundleOnlyIncludesScanOutput`).

### 4. Which base — the merge base, and only the merge base

**Recommendation: old-side content is taken at the merge base of the PR's head
and its base branch. If it cannot be resolved, old-side content is omitted for
every file with reason `no_base`, never falling back to the base branch tip.**

GitHub renders `gh pr diff` as `merge_base..head`, so the hunks' `old_start` /
`old_no` values (`diff.go:78-98`, `model/pr.go:49-55`) are indices into the file
*at the merge base*. The base branch tip is not that commit: it carries every
commit merged into the base branch since the PR forked, so a segment's
`old a-b` range would silently point at different code. A wrong answer the
service cannot detect is worse than a missing one it can.

Resolving it, in order:

1. `gh api repos/{repo}/compare/{base_ref}...{head_sha}` → `merge_base_commit.sha`.
   Authoritative and one call.
2. Local `git merge-base <head_sha> origin/<base_ref>` in the checkout, when the
   checkout is this repo (`scan.RepoRoot` returns a root only when the origin
   remote matches) and the objects are present.
3. Otherwise no base: `base_sha` is `""` and every old side is skipped
   `no_base`.

**The API goes first.** A local `merge-base` against a stale local branch — or a
local `main` not fetched since the head was rebased — yields a commit GitHub did
not diff against, and the failure is silent: old-side line numbers just disagree
with the hunks. The remote-tracking ref is used when the local path does run,
never a local branch of the same name.

`base_ref` (from `gh pr view`'s `baseRefName`, `pr.go:104`), `base_sha`,
`base_kind: "merge_base"` and `base_source: "api" | "git"` are all recorded.

### 5. What gets downloaded, and what is skipped before downloading

Decide from the path and the metadata first; download only what survives. In
this order:

| # | Check | Skip reason |
|---|---|---|
| 1 | Extension deny list, case-insensitive (§5.1) | `media` |
| 2 | `DiffFile.Binary`, **only when the file list came from `gh pr diff`** (§5.2) | `binary` |
| 3 | Reported `size` over `maxDownloadBytes` (20 MiB) | `too_large` |
| 4 | *download*, redact the whole file with `FileBytes` | — |
| 5 | NUL byte in the first 8 KiB, or not valid UTF-8 | `binary` |
| 6 | Fit: whole if it fits; otherwise crop to windows (§6) | `too_large` / `budget` |

The extension check runs before the `Binary` flag, which is a deliberate swap of
the two steps as originally specified. Both are pre-download decisions, so
nothing is lost, and it makes the reason for a given file independent of which
GitHub endpoint supplied the file list — otherwise the same `.png` is `media`
when the diff endpoint answered and `binary` when the files-API fallback did.
Flip it back if the flag-first reading was the intent; it is a one-line change.

#### 5.1 The extension deny list

A path whose extension is on this list is never fetched, whatever the diff says
about it. Case-insensitive, matched on the final `.`-suffix of the path.

| Group | Extensions |
|---|---|
| images | `png jpg jpeg gif webp bmp ico tiff tif heic heif avif psd ai eps` |
| audio, video | `mp3 wav ogg oga flac m4a aac opus mp4 m4v mov avi mkv webm wmv flv mpg mpeg 3gp` |
| fonts | `woff woff2 ttf otf eot` |
| archives, packages | `zip tar gz tgz bz2 xz zst 7z rar jar war apk deb rpm dmg iso` |
| documents | `pdf doc docx xls xlsx ppt pptx odt ods odp rtf pages key numbers` |
| compiled code | `exe dll so dylib a o obj lib class pyc pyo wasm bin` |
| databases, model weights | `sqlite sqlite3 db db3 mdb accdb realm onnx pt pth h5 safetensors gguf ckpt npy npz parquet` |

Additions beyond the list as first given: `tif heif avif ai eps oga aac opus
m4v wmv flv mpg mpeg 3gp zst apk deb rpm dmg iso odt ods odp rtf pages key
numbers obj pyo lib bin sqlite3 db3 mdb accdb realm onnx pt pth h5 safetensors
gguf ckpt npy npz parquet`. Borderline entries deliberately **left out**: `svg`
(text, and it can carry script and hidden text the reading guide flags — the
decision above), `pem`, `key` (already taken, but as `pages`/`key` above the
Apple formats only; a bare `.key`/`.pem` is text and must still be redacted, not
skipped), `csv`, `tsv`, `json`, `yaml`, `plist`, `ipynb`, `lock` — all text.

**Skip reason: `media`, not `binary`.** It is a policy decision made from the
path alone before any bytes exist, and keeping it separate from "we looked at
the bytes and they were binary" is the distinction that matters when someone is
debugging why a file has no content. If the consumer would rather have one
reason for both, `binary` is the smaller vocabulary and the change is a
constant.

#### 5.2 `DiffFile.Binary`, and why it needs to know the source

In the normal path `internal/diff/diff.go:71-74` sets `Binary` only for a
literal `Binary files … differ` / `GIT binary patch` header
(`diff.go:267-276`), i.e. genuinely binary — trustworthy. In the API fallback
`commitFileToDiffFile` (`internal/pr/pr.go:212-238`) sets `Binary` for **any**
file whose patch GitHub omitted (`pr.go:223-226`), a genuinely huge *text* file
included — not trustworthy, and such a file is exactly the one whose hunks give
the service nothing to segment, so it is worth serving.

The two are indistinguishable from the `DiffFile` alone, so **`internal/pr` must
record which path produced `PR.Files`**: a new optional
`model.PR.FilesSource string` (`"diff"` or `"api"`, `omitempty`), set in
`fetchFiles` / `fetchFilesFromAPI`. Additive, ignored by ai-pipeline, and it
makes the snapshot self-describing about where its diff came from. `Binary` is
trusted only when `FilesSource == "diff"`; otherwise the file goes to the
extension list and the byte check.

### 6. Sides over the size caps are cropped, not dropped

A side too large to send whole is cut down to the parts the change touches,
rather than skipped. The reading guide needs the enclosing function of a
changed line, not the rest of the file.

**Window maths.** Per side, per file:

- Seed each window from that side's changed lines: `NewNo` of the `LineAdd`
  lines for the new side, `OldNo` of the `LineDel` lines for the old side
  (`model/pr.go:64-70` — `OldNo` is 0 on an added line, `NewNo` 0 on a deleted
  one, so each side's own numbering falls out).
- A hunk that contributes no changed line to this side — a pure deletion's new
  side, a pure addition's old side — seeds from that hunk's own start number on
  this side (`NewStart` / `OldStart`, `diff.go:78-98` parses `-a,0` and `+c,0`
  correctly), clamped to ≥ 1. Without this, a modified file whose only hunks are
  deletions would yield an empty new side.
- Add `windowMargin` = 300 lines either way, clamp to `[1, full_lines]`, and
  merge overlapping or touching windows.
- Redact the **whole** file first (§1), then keep those lines. Cropping is a
  line-range selection over already-redacted, line-count-preserved bytes, so a
  secret spanning a window edge is still caught and every kept line keeps its
  original number.
- A cropped side's bytes are its kept lines, each terminated by `\n`, windows
  concatenated in ascending order. A full side's bytes are the file verbatim,
  trailing newline or not.
- **A side with no hunks at all has nothing to seed from**, so it is written
  whole when it fits `maxFileBytes` and skipped `too_large` (`limit:
  "file_bytes"`) when it does not — never `partial` with an empty `ranges`.
  This is not exotic: `commitFileToDiffFile` (`pr.go:223-226`) returns
  `Hunks: nil` for every file whose patch GitHub omitted, which is exactly the
  set §5.2 goes out of its way to fetch, and a 100 %-similarity rename or a
  mode-only change arrives with no `@@` at all (`diff.go:28-75`, `:246-247`).
  Serving the whole file is the only option: there is no anchor to crop around,
  and a crop of "everything" is the whole file anyway.

**Order of processing, because it decides the artifact.** Files are visited in
`pr.files` order and, within a file, `new` before `old`. The running total is
charged in that order, so which sides end up `partial` and which are `budget`
is fixed by it. Two runs on the same PR and head produce the same bytes; a
different order would not, and `partial` / `ranges` are contract fields.

**When it applies.** A side is written whole when it fits `maxFileBytes`;
otherwise cropped. When the 16 MiB total is exhausted, the remaining sides are
**cropped** too — even ones that would have fitted whole, which is right here:
cropping keeps every changed line and drops the parts around it. A side is
skipped `budget` only when even its windows do not fit what is left of the
total, and `too_large` when its windows exceed `maxFileBytes` or any kept line
exceeds `maxLineBytes` (8 KiB; a minified bundle or a one-line JSON blob is the
case it exists for — a hand-written source line is a few hundred bytes at most).
The per-line test is over the kept lines, not the whole file.

**The download ceiling is 20 MiB** (`maxDownloadBytes`), read from the `size`
the contents API reports, or `git cat-file -s <spec>` locally, and applied on
both paths so the artifact does not depend on where it was built. It exists
because cropping needs the whole file anyway: the contents API returns an empty
body above 1 MiB, so a cropped side needs `git/blobs/{sha}` (good to 100 MB) or
local `git cat-file`, and 20 MiB is well past anything worth segmenting.

**Known limit, stated up front:** cropping rescues large **modified or
renamed** files, and nothing else. On the `gh pr diff` path an added or deleted
file arrives as one hunk spanning the whole file, so every line is a changed
line, the single window covers everything, the crop equals the whole file, and
it is still over `maxFileBytes` → `too_large`. On the files-API path an
oversized added file is the file whose patch GitHub omitted, so it has no hunks
and falls to the no-hunks rule above → also `too_large`, for a different reason.
So an added or deleted file over the cap is never sent, on either path — and
big added files are disproportionately likely to take the API path, because
`gh pr diff` refuses a combined diff over 20,000 lines (`pr.go:154-183`).

### 7. Caps, sources and the opt-out

| Limit | Value | Constant |
|---|---|---|
| Written per file, per side | 1 MiB | `maxFileBytes = 1 << 20` |
| Downloaded per file, per side | 20 MiB | `maxDownloadBytes = 20 << 20` |
| Written, whole artifact | 16 MiB | `maxTotalBytes = 16 << 20` |
| Written sides, whole artifact | 6,000 | `maxSides = 6000` |
| One kept line | 8 KiB | `maxLineBytes = 8 << 10` |
| Window margin | 300 lines | `windowMargin = 300` |

All constants, not config — decided. The side cap exists because the service
rejects the **entire** bundle over 10,000 archive members (`archive.py:15-18`,
counted across every member): a GitHub-max 3,000-file PR doubles to ~6,000
content members on top of the scan files and up to 2,000 raw-session files
(`assemble.py:19`). A byte budget alone never bounds this.

**Bytes come from local git first, then the GitHub API.** `git cat-file blob
<sha>:<path>` when the object is present locally; `gh api` for anything the
checkout cannot serve (the PR may be someone else's, and paircli never runs
`git fetch`). **Never the working tree** — agreed: it may hold uncommitted
edits, and the published diff is always against committed SHAs.

API shape, per side: `gh api repos/{repo}/contents/{path}?ref={sha}`
(path segments URL-escaped) gives `size`, `sha` and base64 `content`; if `size`
exceeds the download ceiling, skip without fetching; if `encoding != "base64"`
or `content == ""` while `size > 0` (the API returns an empty body above 1 MiB),
fetch `gh api repos/{repo}/git/blobs/{sha}` and base64-decode. A 404 →
`missing`; any other error → `error`, with the message recorded and redacted.
The extra `gh api` calls are fine — they run on the user's machine under the
user's own `gh` login.

**A fetch failure never fails the publication.** It becomes a skip reason and a
log line, the way `capture.Stage` already degrades
(`cmd/paircli/publish.go:404`). This is also what keeps the e2e fakes usable:
the `internal/e2e` fake runner errors on any argument list it was not taught
(`scenario.go:184-191`).

**Do not bump `bundle.json`'s `schema_version`** (it stays `1`) and **do not add
keys to `bundle.json`**. `load_bundle` accepts `1` and ignores what it does not
know; `ingest_local`'s `copytree` carries the new subtree. A bump would be
actively harmful: an older service refuses any bundle whose `schema_version` is
not `1`. `snapshot.json` is not touched. A bundle with no `changed-files/`
remains valid and is exactly what a pre-change paircli produces. Content
redaction counts merge into `bundle.json.redaction.counts` through the existing
`redact.Merge(o.ScanCounts, fileCounts)` path.

**Privacy: on by default, opt out with `publish.changed_file_contents`
(`*bool`, nil = on) plus `paircli publish --no-file-contents`.** This uploads
far more of the customer's source than hunks do, so it is said plainly in
`docs/CONFIGURATION.md`'s privacy section. It is not a new category of exposure
— publish already uploads the full diff, the linked session timelines and up to
80 MiB of raw agent transcripts per PR, all more sensitive than source. The
config key is required, not optional: the installed hooks start
`paircli publish <pr-url> --repo-dir <dir>` detached with fixed arguments
(`docs/SETUP.md:91`), so a per-repo decision cannot be a flag the user types.

Two things to get right, both caught in review:

- **A default-`true` `bool` does not survive `config.Load`.** `Load`
  (`internal/config/config.go:139-183`) merges only non-zero scalars; the one
  unconditional assignment, `cfg.Comment.IncludePrompts = ...` (line 180), works
  only because its default is `false`. A `bool` defaulting to `true` assigned
  that way is forced to `false` by every `.paircli.json` that does not mention
  the key, silently turning the feature off for every existing repo. It must be
  `*bool` with a deliberate branch in `Load`, and `docs/CONFIGURATION.md:4`'s
  "non-zero scalar fields replace them" sentence needs amending to describe it.
- **The flag and the config must not fight.** `--no-file-contents` is an
  explicit *off* on both `scan` and `publish`, never an opt-in that can
  contradict a config saying on. Resolution is: flag given → off; else the
  config's `*bool`; else on. A `*bool` flag or an explicit tri-state would work
  too, but an off-only switch is the smaller surface and the one the hooks can
  live with, since they pass no flags at all and take the config.
- **A stale `changed-files/` must not defeat the opt-out.** The scan folder is
  keyed by head only, so a subtree written by an earlier run with the option on
  or by a publish made before the key was flipped is still there. `scan.Run`
  removes the subtree when the option is off, **and** `bundle.BuildBundle` gates
  on a new `BundleOptions.ChangedFiles bool` — the gate is what enforces it, the
  remove is hygiene.

One hole worth stating rather than fixing: `config.Load` is reached through
`scan.RepoRoot(CWD, repo)`, which returns `""` unless the checkout's origin
remote matches owner/repo (`scan.go:260-279`), so in someone else's repo the
config opt-out is silently ignored. `--no-file-contents` is the escape there.

---

## The on-disk contract

Relative to the scan folder, whose local location PR 1 moves to
`~/.paircli/scans/<owner>__<repo>/pr-<n>/<head>/` while its path **inside the
zip** stays `.paircli/pr-<n>/<head>/` — which is also `bundle.json.scan_dir`.

```
.paircli/pr-<n>/<head>/changed-files/manifest.json
.paircli/pr-<n>/<head>/changed-files/new/<repo path>
.paircli/pr-<n>/<head>/changed-files/old/<repo path>
```

- `new/` holds the file at `head_sha`; `old/` holds it at `base_sha`.
- A content file is either the file's bytes with secrets redacted by
  `redact.FileBytes` and nothing else changed, or — for a `partial` side — the
  redacted file's window lines, each terminated by `\n`, windows in ascending
  order.
- `manifest.files[]` has **one entry per `snapshot.json` `pr.files[]` entry, in
  the same order**, joined by `path`, so a consumer can pair the two without
  re-deriving anything.
- The zip carries these at the same paths, plus `bundle.json` at the root,
  unchanged.

Example manifest:

```json
{
  "schema_version": 1,
  "tool": "paircli 0.4.0",
  "created_at": "2026-10-27T12:00:00Z",
  "repo": "acme/shop",
  "pull_number": 482,
  "head_sha": "3f1c9a2e4b5d6f708192a3b4c5d6e7f8091a2b3c",
  "base_sha": "9d8c7b6a5f4e3d2c1b0a9f8e7d6c5b4a3f2e1d0c",
  "base_ref": "main",
  "base_kind": "merge_base",
  "base_source": "api",
  "redaction": {
    "rules_version": "1",
    "counts": { "secret_assignment": 2, "github_token": 1 }
  },
  "limits": {
    "max_file_bytes": 1048576, "max_download_bytes": 20971520, "max_total_bytes": 16777216,
    "max_sides": 6000, "max_line_bytes": 8192, "window_margin": 300
  },
  "totals": { "files": 7, "sides_written": 6, "sides_skipped": 6, "sides_partial": 1, "bytes": 100855 },
  "files": [
    {
      "path": "internal/publish/bundle.go",
      "status": "modified",
      "sides": {
        "new": { "file": "new/internal/publish/bundle.go", "sha256": "…", "bytes": 12345, "lines": 300,
                 "partial": false, "ranges": [], "full_lines": 300, "full_bytes": 12345, "source": "git" },
        "old": { "file": "old/internal/publish/bundle.go", "sha256": "…", "bytes": 12000, "lines": 296,
                 "partial": false, "ranges": [], "full_lines": 296, "full_bytes": 12000, "source": "git" }
      }
    },
    {
      "path": "assets/logo.png",
      "status": "added",
      "sides": {},
      "skipped": { "new": { "reason": "media" } }
    },
    {
      "path": "src/legacy.ts",
      "status": "deleted",
      "sides": {
        "old": { "file": "old/src/legacy.ts", "sha256": "…", "bytes": 900, "lines": 30,
                 "partial": false, "ranges": [], "full_lines": 30, "full_bytes": 900, "source": "api" }
      }
    },
    {
      "path": "web/app.min.js",
      "status": "modified",
      "sides": {
        "new": { "file": "new/web/app.min.js", "sha256": "…", "bytes": 74210, "lines": 602,
                 "partial": true, "ranges": [[1800, 2100], [4400, 4700]], "limit": "file_bytes",
                 "full_lines": 9000, "full_bytes": 3000000, "source": "api" }
      },
      "skipped": { "old": { "reason": "too_large", "limit": "line_bytes", "bytes": 3000000 } }
    },
    {
      "path": "src/renamed.ts",
      "old_path": "src/old-name.ts",
      "status": "renamed",
      "sides": {
        "new": { "file": "new/src/renamed.ts", "sha256": "…", "bytes": 700, "lines": 24,
                 "partial": false, "ranges": [], "full_lines": 24, "full_bytes": 700, "source": "git" },
        "old": { "file": "old/src/old-name.ts", "sha256": "…", "bytes": 700, "lines": 24,
                 "partial": false, "ranges": [], "full_lines": 24, "full_bytes": 700, "source": "git" }
      }
    },
    {
      "path": "media/clip.mp4",
      "status": "modified",
      "sides": {},
      "skipped": { "new": { "reason": "media" }, "old": { "reason": "no_base" } }
    },
    {
      "path": "src/flaky.db",
      "status": "modified",
      "sides": {},
      "skipped": {
        "new": { "reason": "error", "error": "gh api repos/acme/shop/contents/src/flaky.db: HTTP 502" },
        "old": { "reason": "no_base" }
      }
    }
  ]
}
```

Field notes:

- Every side object has the **same shape**, full or cropped: `file`, `sha256`,
  `bytes`, `lines`, `partial`, `ranges`, `full_lines`, `full_bytes`, `source`,
  and `limit` (omitted when nothing limited it). No `omitempty` on the rest — a
  consumer never has to branch on a missing key. (`partial` is therefore
  explicit rather than omitted-when-false.)
- `bytes` / `lines` describe **what was written**; `full_lines` / `full_bytes`
  describe the whole redacted file, which is what the hunk line numbers index.
  For a full side they are equal.
- `lines` is defined the same way for both: the number of `\n` in the bytes
  written, plus one when they are non-empty and do not end in `\n`. `full_lines`
  is that count over the whole redacted file, so an empty file is `0` on both —
  and `full_lines == 0` is the one case where §6's clamp has nothing to clamp,
  because a file with no lines has no hunks either and takes the no-hunks rule.
  A file whose last line has no trailing `\n` counts that line, which is what
  keeps `full_lines` equal to the diff's own line numbering.
- `limit` says **which** limit produced a `partial` or a skipped side, because
  `too_large` alone cannot. §5 step 3 skips on `download_bytes` (over 20 MiB,
  before a byte is fetched), §6 skips on `file_bytes` (the windows are over
  1 MiB), `line_bytes` (a kept line is over 8 KiB) or `budget` (they do not fit
  what is left of the total), and a `partial` side names `file_bytes` or
  `total_bytes` as the reason it was cropped. Without it, the example's
  `.min.js` old side at 2.9 MB cannot be told from a 30 MB one, and a cropped
  side cannot say whether the per-file cap or the running total cropped it.
- `ranges` is `[]` for a full side and a sorted, 1-based, inclusive list of
  `[start, end]` pairs **in the original file's line numbers** for a cropped
  one. The consumer's invariant: walking `ranges` in order and concatenating
  `end - start + 1` lines each reconstructs exactly the written lines, so
  written line *k* maps back to its original number. That is what the reading
  guide needs to line the content up with a hunk, and it is worth a test on our
  side and a sentence in the contract.
- `path` / `old_path` / `status` are copied from the snapshot's `DiffFile`.
  `model/pr.go:40` and `diff.go:55-61` confirm `path` **is the old path for a
  deletion**; `OldPath` is set only for a rename (`diff.go:31-47`).
- Which sides `status` calls for: `added` → new; `modified` / `renamed` → new
  and old; `deleted` → old. `sides` holds the ones written; `skipped` holds a
  reason for every side that `status` calls for but that is not in `sides`, so
  **`sides` ∪ `skipped` is always exactly the expected side set**. An optional
  `bytes` (known size of a dropped blob) and `error` (redacted, truncated to
  500 bytes) accompany the reason.
- `sha256` is over the file **as written** — after redaction and cropping —
  matching how `raw-sessions/manifest.json` describes its own redacted files and
  how `assemble._copy_raw_sessions` re-verifies them.
- `source` is `git` or `api` for the bytes; `base_source` is the same for the
  base commit. Debugging aids, not something to branch on.
- `totals.sides_partial` counts cropped sides.
- Two-space indent, one trailing newline, like every other artifact here.

Skip reasons, exactly: `media`, `binary`, `too_large`, `budget`, `missing`,
`error`, `no_base`, `line_shift`, `collision`, `unsafe_path`.

**Path safety**, against the rules `archive.py:48-53` `safe_relative` actually
enforces (`\`, a leading `/`, `//`, `.`/`..` parts, drive letters), plus the
case-insensitive duplicate-member check:

- A repo path that fails those rules, or **any path component over 255 bytes**,
  is skipped `unsafe_path`. Decided: no digest-fallback `entry` field. The 255
  figure is a filesystem limit on the service's side (its `extract_zip` writes
  to a real filesystem), not an archive rule — it has none.
- Not hypothetical otherwise: `safe_relative` rejects any backslash, and a Linux
  repo can hold such a path, which `diff.go:100-112` `unquoteGitPath`
  deliberately unescapes before it reaches us.
- Two paths differing only by case are legal in a Linux repo and would make the
  service reject the **whole bundle** as a duplicate. Entry names are compared
  case-insensitively before writing and the later path is skipped `collision`.
  The consumer already defends itself here — `generation/snapshot.py`'s
  `captured_files` dedupes paths "without repeats" — and an exact duplicate
  `path` in `pr.files` emits two manifest entries, the second wholly
  `collision`.
- **`copied` stays a documented limitation** (decided). `apiStatusMap`
  (`pr.go:75-80`) does not know it, so `commitFileToDiffFile` files it as
  `modified` with `old_path` unset (`pr.go:214-221`) and the old side is looked
  up at the *new* path at `base_sha` → `missing`. Same for `changed` /
  `unchanged` (mode-only), harmlessly.

---

## Files to change

### PR 1 — `rutvikchandla3/scan-output-in-home`

- `internal/state/state.go` — `ScanDir(owner, repo string, number int, head string) string`.
- `internal/scan/scan.go` — the default outDir; drop the CWD fallback; 0700
  parent chain; legacy-folder hint off `o.CWD`.
- `internal/render/render.go` — the `comment.md` footer through `engine.Home`
  (the leak), plus the doc comment.
- `cmd/paircli/doctor.go` — the legacy-folder check, keyed off `cwd` rather than
  the resolved repo root.
- `cmd/paircli/main.go` — `--out` help text.
- `cmd/paircli/scan.go` — legacy-folder hint on the scan path.
- `cmd/paircli/publish.go` — legacy-folder hint.
- `internal/scan/scan_test.go`, `cmd/paircli/scan_test.go`,
  `internal/render/render_test.go`, plus one new e2e that runs without `--out`
  and asserts the folder lands under a temp `HOME`.
- `docs/ARCHITECTURE.md:58,306-317`, `docs/CONFIGURATION.md:66`,
  `docs/SETUP.md:117-123`, `docs/plan/CONTRACTS.md:317,373,401`,
  `docs/plan/tasks/T24-scan-integration.md:31`.

### PR 2 — `rutvikchandla3/ship-changed-file-contents`

New:

- `internal/filecontent/filecontent.go` — `Limits`, `Options`, the `Manifest` /
  `ManifestFile` / `Side` types, `Write(dir, *Manifest)`.
- `internal/filecontent/collect.go` — base-SHA resolution, the git and `gh api`
  readers, the skip and window decisions, `Collect(...) *Manifest`.
- `internal/filecontent/window.go` — the window maths, separate because it is
  the piece with the most arithmetic and the most tests.
- `internal/filecontent/filecontent_test.go`, `collect_test.go`, `window_test.go`.
- `internal/e2e/e2e_filecontents_test.go`.

Changed:

- `internal/redact/redact.go` + `redact_test.go` — `Redactor.FileBytes`.
- `internal/model/pr.go` — `PR.FilesSource` (`omitempty`).
- `internal/pr/pr.go` — set it in `fetchFiles` / `fetchFilesFromAPI`.
- `internal/scan/scan.go` — `Options.ChangedFileContents bool`; after the
  snapshot write, `filecontent.Collect` + `Write` into `outDir`; remove a stale
  `changed-files/` when the option is off.
- `internal/publish/bundle.go` — `BundleOptions.ChangedFiles bool`; read the
  manifest and exactly the files it names. **A file the manifest names but that
  is missing is an error, not a skip** — unlike the optional scan files at
  `bundle.go:128-130`, skipping it breaks the "`sides` describe the zip exactly"
  invariant. Leave the raw-sessions fallback and sorted entries alone.
- `cmd/paircli/publish.go` — load the config for the opt-out. Note this file has
  **no config object and no repo root today** (it relies on `scan.Run` calling
  `config.Load` itself, `scan.go:96-104`), so the new `config.Load` must use the
  same root, `scan.RepoRoot(o.CWD, o.Repo)`. Add `--no-file-contents`. Extend
  `bundleSummary` / `printPlan` with the content counts.
- `cmd/paircli/scan.go` — `--no-file-contents`, the same off-switch as publish
  (see §7). **Not** an opt-in `--file-contents`: `cmd/paircli/scan.go:109-136`
  already passes `Config: cfg` into `scan.Options`, so a `bool` flag defaulting
  to `false` would silently turn the feature off for `scan` in every repo whose
  config says on — the same default-`true`-bool failure mode §7 goes to
  trouble to fix in `config.Load`.
- `internal/config/config.go` — `Publish` section, `changed_file_contents *bool`
  (nil = on), merged deliberately in `Load`.
- Test call sites that build `scan.Options` directly: `internal/e2e/e2e_test.go:31-40`,
  `internal/e2e/e2e_snapshot_test.go:24-33`, `internal/scan/scan_test.go:415`,
  `cmd/paircli/publish_test.go`.
- `internal/e2e/scenario.go` — canned `gh api .../compare/...` **and**
  `.../contents/...` responses. The e2e repo's `.git` is hand-written with no
  objects (`scenario.go:112-118`), so both local-git paths always fail there and
  every base comes from the API — which is the primary path anyway. Plant a
  secret in a changed file so `e2eAssertNoSecret` proves redaction.
- `docs/CONFIGURATION.md` — config table row (and the preamble's "non-zero
  scalar fields replace them" sentence, **line 4**), the redaction section
  (whole-file redaction), the upload list (line 99's "Files in the scan folder
  that are not on that list are never uploaded" needs the new row to stay true)
  and the privacy section.
- `docs/ARCHITECTURE.md` — the zip layout (lines 108-120) and line 149's claim
  that `--dry-run` "never … calls the network", already false today (`scan.Run`
  → `pr.Fetch` → `gh pr view` / `gh pr diff`) and now up to 2N more `gh api`
  calls. Restate as "no Pair server, no login, no publication record, no cursor
  movement".
- `docs/plan/CONTRACTS.md` — the new artifact in the output-files table (lines
  399-411; note `snapshot.json` is already missing from it).

## Tests

### PR 1

- `state.ScanDir` is under a temp `HOME`; `scan.Run` with no `OutDir` writes
  there and nowhere in the repo; `--out` still wins; `bundle.json.scan_dir` and
  the zip prefix are still `.paircli/pr-<n>/<head>` after the move (the
  regression that would silently break ai-pipeline's `load_bundle`).
- **`comment.md`'s footer contains no absolute home path** after the move, and
  the whole rendered comment does not contain the temp `HOME` — the same shape
  as the existing secret-confinement walk, because a PR comment is public.
- The scan tree is created 0700 under a temp `HOME`.
- The legacy-folder hint fires from a CWD whose origin remote does *not* match
  the PR's repo.

### PR 2

`internal/redact`:

- `FileBytes` redacts a multi-line PEM block that `Bytes` provably does not (the
  same input through both, side by side), and preserves the line count for that
  and for every rule in the golden table.
- `Text` and `Bytes` are unchanged: every existing golden passes untouched.
- Idempotency: `FileBytes(FileBytes(x)) == FileBytes(x)`.
- A match that swallows a newline still leaves the line count alone.

`internal/filecontent/window.go`:

- Seed, margin, clamp, and merge of overlapping **and** touching windows.
- Old numbering vs new numbering for the same hunk, including a hunk that
  contributes changed lines to only one side.
- A pure-deletion hunk seeds the new side from `NewStart`; a pure-addition hunk
  seeds the old side from `OldStart`; `-a,0` / `+c,0` clamp to ≥ 1.
- An added or a deleted file crops to a **single window spanning the file** —
  a unit test of the function, since at the `filecontent` level such a side
  never ends up `partial`: the crop equals the whole file, does not fit, and is
  `too_large`.
- A side with no hunks returns no windows at all, which the caller must read as
  "write it whole or skip it", never as `partial` with `ranges: []`.
- **The consumer invariant**: walking `ranges` and slicing the written bytes
  reproduces exactly the written lines, and written line *k* maps back to the
  original number.

`internal/filecontent`:

- Side selection per status; the old side is read at `old_path` for a rename.
- Old side uses `base_sha`, new side `head_sha` (asserted on the arguments the
  fake runner / fake git sees).
- Base resolution: the API answer wins when both are available; local
  `merge-base` against `origin/<base_ref>` when the API fails; no base → every
  old side `no_base` and `base_sha` empty.
- `git` preferred over `api` for bytes; `api` used for exactly the sides git
  could not serve.
- The deny list: a `.png` and a `.zip` skip `media` **with no fetch attempted**
  (asserted on the fake's call list), case-insensitively (`.PNG`).
- `DiffFile.Binary` is trusted when `FilesSource == "diff"` and ignored when
  `"api"`, where a large *text* file with an omitted patch is still served.
- The byte backstop still catches a NUL byte and invalid UTF-8 in a file that
  passed the extension check.
- Download ceiling: a `size` over 20 MiB skips `too_large` without a content
  fetch; a `size` between 1 MiB and 20 MiB goes through the blobs API.
- Cropping: a side over `maxFileBytes` is written `partial` with the right
  ranges and `limit: "file_bytes"`; a kept line over `maxLineBytes` makes the
  side `too_large` with `limit: "line_bytes"`; a cropped side whose windows
  still exceed `maxFileBytes` is `too_large`.
- A file with **no hunks** over `maxFileBytes` is `too_large`, not `partial`
  with an empty `ranges`; the same file under the cap is written whole.
- Budget: the total is exhausted by whole sides, then the remainder is cropped
  with `limit: "total_bytes"`, then `budget` only when windows do not fit —
  deterministically, and **in the stated order** (`pr.files` order, `new`
  before `old`): the same input twice gives byte-identical output, and a
  reversed file order gives visibly different `partial` sides.
- `limit` is present on every limit-driven skip and every `partial` side, and
  absent everywhere else.
- Redaction before cropping across a window edge: a PEM block straddling the
  boundary is `[REDACTED:private_key]` and the line count still matches the
  original.
- A secret in a changed file is `[REDACTED:…]` **in the file on disk**, its
  `sha256` matches the bytes on disk, and its count appears in the pass delta.
- `sides` ∪ `skipped` == the expected side set, over a table of all four
  statuses crossed with each skip reason.
- `unsafe_path` for a backslash path and for a 256-byte component.
- `collision` for `Foo.go` / `foo.go`.
- The manifest is deterministic: same input, same bytes.

`internal/publish`:

- Content files and the manifest land at
  `.paircli/pr-42/<head>/changed-files/...`, redacted, entry list sorted.
- A file in `changed-files/` the manifest does not name is not uploaded.
- A file the manifest names but that is absent is an error.
- A scan folder with no `changed-files/` produces exactly today's entry list —
  the older-consumer case, extending `TestBuildBundleLayoutAndBundleJSON`.
- A malformed manifest is an error rather than a silently empty subtree.
- The raw-sessions fallback still drops only raw sessions, keeping content.

`cmd/paircli`:

- `--dry-run --json` reports the content file / partial / skip / byte counts.
- `publish.changed_file_contents: false`, an unmentioned key, and
  `--no-file-contents` each behave correctly — **the unmentioned key must leave
  the feature on** (the `*bool` regression).
- A stale `changed-files/` left by an earlier run is not uploaded when the
  option is off.

`internal/e2e`:

- The full scenario writes the manifest, the content lands in the bundle, and
  `e2eAssertNoSecret` still passes with a secret planted in a changed file.
- Report, signals and the three goldens are unchanged — content must not reach
  the detectors.

## Open questions

All of these were answered before the build; they are kept with the answers so
the reasoning is on the record.

1. **Two steps in §5 are reordered.** The extension deny list runs before the
   `DiffFile.Binary` check, so a file's skip reason does not depend on which
   GitHub endpoint produced the file list. Both are pre-download, so nothing is
   lost. **Decided: keep the deny list first.**
2. **`media` vs `binary`** for the deny list. **Decided: a separate `media`.**
3. **Budget cropping could shrink the margin to fit.** **Decided: no. 300 lines
   fixed**; a side whose windows do not fit the remaining total is `budget`.
4. **`ranges` granularity.** **Decided: `[start, end]` pairs.**
5. **`comment.md`'s footer.** **Decided: drop the path from the footer entirely,
   in any form** — not even a home-shortened one, so the comment carries no
   local path at all. Goldens and docs updated to match.
6. **Oversized no-hunk files.** **Decided: they stay `too_large`**, with no
   heuristic anchor.
7. **`gh api` cost on a large PR.** **Accepted** — it runs under the user's own
   `gh` login.
8. **`copied` files.** **Decided: stays a documented limitation**, with the old
   side recorded `missing`.
9. **0700 directories, 0644 files.** **Accepted.**

## Review, round 1: what checking the plan against the code changed

A reviewer subagent went over the first draft against both repos, with code
pointers and no conclusions. Every claim was re-verified before being folded in.

- **Whole-file redaction (§1, new).** The draft said `redact.Bytes`'
  line-by-line behaviour was "what we want". It is not: the `private_key` rule
  spans lines by design, so through `Bytes` it is dead and a PEM block in a
  changed file would ship verbatim — behaviour `TestBytesKeepsLinesAndJSON` pins
  deliberately. Added `Redactor.FileBytes`.
- **Redact where the bytes are written (§2, new).** The draft had the scan
  folder hold raw bytes and the zip redact them, which would leave unredacted
  copies of the customer's files in the repo, make the manifest's `sha256`
  describe nothing, and fail `e2eAssertNoSecret`.
- **The hash claim (§3, corrected).** The draft claimed this layout leaves
  `snapshot.Hash` untouched while the alternative changes it. Both change it,
  because `Config` is inside the hashed record.
- **Config default (§7, corrected).** A `bool` defaulting to `true` does not
  survive `config.Load`'s merge; it would silently disable the feature for every
  repo with a `.paircli.json`. Now `*bool`.
- **Stale subtree defeats the opt-out (§7, new).** `bundle.go` reads the scan
  folder with no notion of the option, so an earlier run's `changed-files/`
  would be uploaded by a later `--no-file-contents`.
- **Base resolution order (§4, inverted).** The draft preferred local
  `merge-base`, which against a stale local branch yields a commit GitHub did
  not diff against — a silent line-number mismatch.
- **`commitFileToCommitFile` (§5, corrected).** The draft named a function that
  does not exist; it is `commitFileToDiffFile`. It also over-generalized its
  `Binary` behaviour to both paths; it is the API path only.
- **The 10,000-member cap (§7, new).** The draft bounded bytes only.
- **A missing manifest file (§Files, corrected).** `bundle.go` skips a listed
  file that does not exist, which would break the "`sides` describe the zip
  exactly" invariant.
- **`unsafe_path` justification (corrected).** The draft said it "cannot occur
  from `git diff`"; a Linux repo can hold a backslash path, and
  `unquoteGitPath` unescapes it for us.
- **Call sites and docs the draft missed.** Two e2e `scan.Run` sites,
  `internal/scan/scan_test.go`, `cmd/paircli/publish_test.go`, the e2e
  scenario's missing canned responses, that `cmd/paircli/publish.go` has no
  config object or repo root today, `CONFIGURATION.md:6-7` and `:99`,
  `CONTRACTS.md`'s output-files table, and `ARCHITECTURE.md:149`'s
  already-false "`--dry-run` never calls the network".
- **Confirmed correct, left alone:** every ai-pipeline claim; the raw-sessions
  fallback ordering; not bumping `bundle.json`'s `schema_version`; that
  `snapshot.Read` hard-rejects a version bump; that no golden changes; that hunk
  line numbers index the file at the merge base; that `Path` is the old path for
  a deletion; that preferring committed objects over the working tree and
  degrading a fetch failure to a recorded skip are right; and the privacy
  argument for a config-key opt-out.

## Review, round 2

Round 2 was the user's feedback, folded in above:

- The scan folder moves to `~/.paircli/scans/` (PR 1) rather than shipping
  whole files into the customer's repo — which also kills the old open question
  about whether a repo-relative copy of every changed file belongs in the scan
  folder.
- Binary and media files are decided from the path before downloading, with the
  deny list and the `Binary`-flag trust rule (§5).
- Oversized sides are cropped to windows around the hunks instead of dropped,
  with the budget fallback cropping too (§6), which turns the previous
  "skip over `maxFileBytes`" into "send the part that matters".
- Decided and now settled rather than open: caps stay constants; `sides:
  {new, old}` stands; the extra `gh api` calls are fine; never the working tree;
  a >255-byte path component is `unsafe_path` with no `entry` field; `copied`
  stays a documented limitation.
- New in this round because the cropping and pre-download rules demanded it:
  `window.go` and its tests, the download ceiling, the per-line cap,
  `model.PR.FilesSource`, the `media` reason, `partial` / `ranges` /
  `full_lines` / `full_bytes` in every side object, and the known added/deleted
  limit.

### And what a fresh review of those new sections changed

A second reviewer went over the round-2 sections against both repos. Every
finding below was re-verified against the code before being folded in.

- **PR 1 would have leaked a home path into a public PR comment.** `render.go`
  writes `opts.OutDir` into `comment.md`'s footer (`render.go:468-469`), and
  `scan --post` posts that file to GitHub verbatim (`scan.go:211`). PR 1's
  "render needs nothing" was wrong, and it contradicted the invariant in the
  very doc block the plan said only needed new wording
  (`ARCHITECTURE.md:316-317`). Now a real change; the footer was ultimately
  dropped rather than home-shortened (see the open questions).
- **§6 had no rule for a side with zero hunks** — which is precisely the set
  §5.2 exists to serve (`commitFileToDiffFile` returns `Hunks: nil` for every
  omitted patch, `pr.go:223-226`), plus 100 %-similarity renames and mode-only
  changes. Added: whole if it fits, `too_large` if not, never `partial` with an
  empty `ranges`. The "known limit" paragraph was also only true of the diff
  path; an oversized added file on the API path fails a different way, and is
  more likely to, since `gh pr diff` refuses at 20,000 lines.
- **`FileBytes` counted newlines over the wrong span.** `apply` replaces the
  submatch group, not the match (`redact.go:341-372`), and `sep`'s `\s*`
  (`redact.go:54`) can put a newline in the prefix. Counting the match's
  newlines would add a line and trip the plan's own `line_shift` assertion. The
  literals pass (`redact.go:313-318`) can move a newline too.
- **The `scan` opt-in flag contradicted the on-by-default config.** A `bool`
  defaulting to `false` passed into `scan.Options` — while `scan.go:109-136`
  already supplies the config — would silently disable the feature for `scan`
  in every repo whose config said on. Now `--no-file-contents` on both commands,
  with the resolution order stated.
- **The budget rule had no processing order**, so "deterministic" had nothing
  behind it and `partial` / `ranges` are contract fields. Now pinned: `pr.files`
  order, `new` before `old`.
- **`too_large` was overloaded** across a pre-download 20 MiB cap and a
  post-download 1 MiB one, and a `partial` side could not say whether the
  per-file cap or the running total cropped it. Added the `limit` field.
- **The example manifest contradicted its own arithmetic** — `[[1800,2100],
  [4400,4700]]` is 602 lines, not 601 — and its `totals` disagreed with its own
  entries (7 files, 6 sides written, 6 skipped). Fixed, and `lines` /
  `full_lines` are now defined for the empty file and the missing trailing
  newline.
- **PR 1's file modes.** `MkdirAll(outDir, 0o755)` (`scan.go:117`) would have
  created `~/.paircli` itself at 0755 while `state.Dir()` uses 0700. Now the
  parent chain is 0700.
- **The legacy-folder hint could not fire where it matters.** Keyed off the
  repo root, it is silent in the "someone else's repo" case (`RepoRoot` returns
  `""`, `scan.go:260-279`) — the case most likely to have a stale folder.
  `doctor` had the same limitation (`doctor.go:46-49`). Both now key off CWD.
- **`FilesSource` confirmed necessary and safe** (`diff.go:267-276` vs
  `pr.go:223-226` really are indistinguishable otherwise; the hash is computed
  at write time and only compared within one snapshot's replay; no golden pins
  `snapshot.json` or a config key), so it moved from an open question to a
  decision.
- **Smaller:** `CONFIGURATION.md`'s "non-zero scalar fields" sentence is on
  line 4, not 6-7; and the "added/deleted file crops to everything" test is a
  unit test of the window function, not an end-to-end assertion, since such a
  side never ends up `partial`.
- **Confirmed correct and left alone:** the reader/writer survey for the folder
  itself (nothing else resolves it by convention; `status`, `prtrigger`,
  `doctor`, the hook installers and publish's analysis polling never touch it);
  that the zip prefix and `bundle.json.scan_dir` are already decoupled from
  `o.ScanDir`; that nothing adds `.paircli/` to a customer's ignore files; that
  `os.UserHomeDir()` honours a `HOME` override and the tests already use that
  pattern; the `Binary` semantics on both paths; `apiStatusMap`'s missing
  `copied`; the `DiffLine` / `DiffHunk` / `parseHunkHeader` numbering the window
  maths rests on; the `e2eAssertNoSecret` reasoning; the `*bool` config
  argument; and every ai-pipeline claim.

## As built

Two deviations from the plan's letter, both noted in the final report:

- **`FileBytes` preserves the newline count, not `countLines`.** The guard in
  `filecontent` compares newline counts, and `full_lines` is taken from the
  original bytes — which is what the diff's line numbers index anyway. Before
  this, a key file with no trailing newline whose tail was a PEM block was
  dropped as `line_shift` instead of served. Found by review.
- **One contents call, not two.** `fetch` asked the contents endpoint for
  metadata and then again for the body; the same response carries both. Halves
  the round trips and the rate-limit cost per file on a large PR. Found by
  review.
- **`changed-files/` entry names are re-checked where they are read.**
  `internal/publish` joins a name from the manifest to a path, so it screens it
  again rather than trusting a file that anything could have written. Found by
  review.
- **The per-line cap applies to a side written whole, not only to a cropped one.**
  The plan put it in the cropping branch. A 900 KB single-line minified bundle
  is under `max_file_bytes`, so it would have been sent whole — exactly the wall
  the cap exists to refuse. The cap now tests the longest line of whatever is
  about to be written.
- **`Options.HeadSHA` was added.** The manifest's `head_sha` has to be the head
  `bundle.json` and the scan folder are named after, and GitHub caps its commit
  list, so a payload with no `headRefOid` carries the head only as its last
  commit. Reading `pr.HeadSHA` directly made the manifest disagree with the
  bundle, and made base resolution silently skip every old side. Caught by a
  test; the field is set by `scan.Run` from the same `headSHA(p)` it already
  uses.
