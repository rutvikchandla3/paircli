# T11 — Attribution + AUTH-1, AUTH-2

**Model:** sonnet · **Wave:** C · **Depends on:** T02 T06 T07 · **Milestone:** M1

## Goal

Label every added PR line with who wrote it, map commits to the sessions whose
edits produced them, and emit the AUTH-1 and AUTH-2 signals.

## Read first

- `docs/plan/CONTRACTS.md` "internal/attrib", `model.Attribution`, engine and testkit APIs
- `internal/model/attribution.go`, `internal/model/text.go`
- `docs/SIGNALS.md` rows AUTH-1, AUTH-2

## Files you own

- `internal/attrib/*.go` + tests
- `internal/detect/authorship/auth1.go`, `auth2.go` + tests

## `attrib.Attribute(pr, sessions, cfg)`

Build indexes once over all sessions' events in timeline order (TS, session
ref, Seq). Per repo-relative file:
- `agentStrict[file][NormalizeLine(line)]` → list of edit items, from non-failed
  `file_edit` events (`RelPath == file`, including `OffBranch` and subagent edits).
- `agentLoose[file][LooseLine(line)]` → same, for loose matching.
- `agentLines[file]` → every distinct strict agent line (for similarity).
- `external[file][NormalizeLine(line)]` → `external_edit` items.
- Snapshot-based human lines: per session, walk `snapshot` events in order;
  for a `prompt` snapshot, a line hash `h` in file `f` is **human** when `h`
  was not in that session's previous `stop` snapshot for `f` and no earlier
  agent edit (by time) produced a line with that hash. Index
  `humanHash[file][h]` → the prompt snapshot item.

For each PR file (skip `Binary`; files matching `cfg.GeneratedPaths` via
`classify.Glob` get every line labeled `trivial`), for each added line `L`:

1. `model.IsTrivialLine(L.Text)` → `trivial`.
2. agent strict match → `agent`, `Source` = the **latest** matching item.
3. external strict match → `human_in_session`, `Source` = the **earliest** matching item.
4. `humanHash` match on `model.LineHash(L.Text)` → `human_in_session`, Source = that snapshot.
5. agent loose match → `agent`, `Reformatted: true`, Source = latest.
6. similarity: among `agentLines[file]` with length ratio in [0.5, 2.0], the
   best normalized Levenshtein similarity `1 - dist/max(len)` ≥ 0.6 →
   `agent_then_human`, Source = the latest edit containing that best line.
   Cap at 2000 comparisons per line; stop early at similarity 1.
7. otherwise `uncaptured`.

`Model` and `AgentID` come from the Source event. `Counts` per file,
`Total` = all non-trivial lines, `Explained` = agent + agent_then_human + human_in_session.
Files keep PR order; lines keep diff order. Deleted lines are not attributed.

## `attrib.CommitSessions(pr, sessions)`

For each commit with `Files`: for every added non-trivial line whose trimmed
length is at least 8 characters, find agent strict matches in the same file
and collect their session refs. Return `sha → sorted unique refs`; commits
with no matches are absent.

## AUTH-1 detector (`auth1.go`)

- No sessions → `engine.NoSessions`.
- `Attribution.Total == 0` → `Unknown("No added lines to attribute.")`.
- Summary: `Agent wrote {a} of {t} changed lines ({pct}); {m} edited by a human afterwards, {h} written by a human in session, {u} not captured.`
  (`a` = agent, `m` = agent_then_human, `h` = human_in_session, `u` = uncaptured).
- State `info`.
- Findings: one per PR file with non-trivial lines, sorted by total lines desc,
  at most 20: `` `path` — 118 agent · 9 agent→human · 4 human · 0 uncaptured ``;
  anchors = `Attribution.LinesFor(path, uncaptured)` (lines a reviewer should know nobody captured); no evidence.
- Data: `{"counts": {label: n}, "reformatted": n, "files": [{"path","counts"}]}`.

## AUTH-2 detector (`auth2.go`)

Uses `c.Sessions`, `c.SessionLinks`, `c.Links`, `c.Attribution`.
- No sessions → `NoSessions`.
- Lines per session: count attribution lines whose `Source.Session` is the ref.
- Summary: `{n} sessions ({per-harness counts, e.g. "2 claude-code, 1 codex"}) explain {pct} of changed lines.`
  plus ` {k} commits have no session.` when `k > 0` (keep ≤160 chars; drop the second sentence if needed).
- State: `alert` when `Total ≥ 10` and `Ratio < 0.5`; else `info`.
- Findings: one per linked session (start order):
  `` `claude-code:ab12cd34` (hooked) linked by sha_exact — 312 lines, commits a41f2c9, 9c1e0d2 `` (short IDs: first 8 chars of session id, 7 of SHA; method from `SessionLinks`, `"unknown"` when nil); severity info.
  One per unattributed commit: `Commit a41f2c9 has no matching session.` severity alert.
- Data: `{"sessions":[{"ref","link","capture","lines","commits"}], "unattributed":[…], "ratio": r}`.

## Tests

`internal/attrib` (testkit sessions + PR builder):
- `TestAttribute_AgentLatestWins` — same line in two edits; Source is the later one.
- `TestAttribute_ExternalHuman`.
- `TestAttribute_SnapshotHuman` — prompt snapshot introduces a hash absent from the previous stop snapshot and from agent edits.
- `TestAttribute_SnapshotNotHumanWhenAgentWrote` — hash produced by an agent edit before the snapshot.
- `TestAttribute_Reformatted` — `'x'` vs `"x"` and spacing.
- `TestAttribute_Mixed` — `const max = 5;` agent vs `const max = 6;` in PR.
- `TestAttribute_Uncaptured`, `TestAttribute_Trivial`, `TestAttribute_GeneratedFile`, `TestAttribute_FailedEditIgnored`, `TestAttribute_OffBranchCounts`.
- `TestAttribute_Counts` — totals and ratio.
- `TestCommitSessions` — two commits, two sessions; short lines ignored.
- `BenchmarkAttribute` — 50 files × 400 lines × 200 edits completes (report ns/op; no threshold).

`internal/detect/authorship`:
- `TestAUTH1_Summary`, `TestAUTH1_NoSessions`, `TestAUTH1_NoLines`.
- `TestAUTH2_Coverage` — ratio 0.4 with Total 20 → alert; unattributed commit finding; SessionLinks nil → "unknown".

## Acceptance

```sh
go test ./internal/attrib/... ./internal/detect/authorship/...
go test -bench=Attribute -run=^$ ./internal/attrib/
```
