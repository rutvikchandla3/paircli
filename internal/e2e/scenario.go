// Package e2e holds paircli's end-to-end golden scenario: one realistic,
// fully synthetic PR produced across all three harnesses and run through
// scan.Run. Everything is built under t.TempDir() at test time; no real
// harness session store is ever read.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/hooklog"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// The scenario is pinned to this instant and date (docs/plan/tasks/T25).
const (
	e2eDay      = "2026-09-27"
	e2eRepo     = "acme/shop"
	e2eNumber   = 482
	e2eRepoURL  = "https://github.com/acme/shop/pull/482"
	e2eHeadRef  = "feat/retry"
	e2eBaseRef  = "main"
	e2eTitle    = "Add retry with backoff to webhook sender"
	e2eNowHHMM  = "16:00"
	e2eVersion  = "test"
	e2eEnvValue = "sk-ant-api03-9f3c2ab7d41e6b8a2d5c7e1f" // planted in .env; must never reach any output file
)

// e2eCommitSHAs are the three PR commits. Commits 1 and 2 are pinned by hook
// records, so they link exactly; commit 3 contains a hand-written README
// change no session made, so it stays unattributed.
var e2eCommitSHAs = []string{
	"4be1c0ffee4be1c0ffee4be1c0ffee4be1c0ffee",
	"7a2d9f10ab7a2d9f10ab7a2d9f10ab7a2d9f10ab",
	"c31f4e77d0c31f4e77d0c31f4e77d0c31f4e77d0",
}

// e2eAt returns the fixed scenario timestamp for a "2006-01-02T15:04:05Z"
// style clock time on e2eDay.
func e2eAt(hhmm string) time.Time {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic("e2e: bad clock time " + hhmm + ": " + err.Error())
	}
	return time.Date(2026, 9, 27, t.Hour(), t.Minute(), 0, 0, time.UTC)
}

// e2eAtSec is e2eAt plus whole seconds, for events inside one minute.
func e2eAtSec(hhmm string, sec int) time.Time {
	return e2eAt(hhmm).Add(time.Duration(sec) * time.Second)
}

// e2eStamp renders a timestamp the way the harnesses write it (RFC3339 in
// UTC, millisecond precision).
func e2eStamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// world is one fully built scenario: the repo, the three harness roots, the
// hook log, the fake gh runner and the configuration that points at all of
// them.
type world struct {
	t *testing.T

	root      string // the scenario's t.TempDir()
	repoDir   string // repo checkout with .git/HEAD and .git/config
	claudeDir string // cfg.Roots.ClaudeCode
	codexDir  string // cfg.Roots.Codex
	piDir     string // cfg.Roots.Pi
	hookDir   string // cfg.Roots.HookLog and $PAIRCLI_EVENTS_DIR
	outDir    string // scan output folder

	cfg    *config.Config
	runner *fakeRunner
}

// newWorld creates the temp tree, the repo metadata and the fake gh runner.
// It does not write any transcripts; the callers below do that.
func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()

	w := &world{
		t:         t,
		root:      root,
		repoDir:   filepath.Join(root, "shop"),
		claudeDir: filepath.Join(root, "claude", "projects"),
		codexDir:  filepath.Join(root, "codex"),
		piDir:     filepath.Join(root, "pi", "agent", "sessions"),
		hookDir:   filepath.Join(root, "events"),
		outDir:    filepath.Join(root, "out", "pr-482"),
		runner:    newFakeRunner(),
	}
	for _, dir := range []string{w.repoDir, w.claudeDir, w.codexDir, w.piDir, w.hookDir, w.outDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("e2e: mkdir %s: %v", dir, err)
		}
	}

	w.writeGitMeta()
	w.cfg = w.buildConfig()
	w.runner.addE2EPR()
	return w
}

// writeGitMeta writes the minimal .git metadata internal/gitinfo reads: HEAD
// plus an origin remote pointing at the synthetic repository.
func (w *world) writeGitMeta() {
	w.t.Helper()
	gitDir := filepath.Join(w.repoDir, ".git")
	e2eWrite(w.t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/"+e2eHeadRef+"\n")
	e2eWrite(w.t, filepath.Join(gitDir, "config"),
		"[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n[remote \"origin\"]\n\turl = git@github.com:"+e2eRepo+".git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n")
}

// buildConfig points every harness root and the hook log at this scenario's
// temp dirs so nothing reads the developer's real session stores.
func (w *world) buildConfig() *config.Config {
	cfg := config.Default()
	cfg.Roots.ClaudeCode = w.claudeDir
	cfg.Roots.Codex = w.codexDir
	cfg.Roots.Pi = w.piDir
	cfg.Roots.HookLog = w.hookDir
	return cfg
}

// e2eWrite writes a file, creating parent directories.
func e2eWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("e2e: mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("e2e: write %s: %v", path, err)
	}
	// Pin the mtime inside the scan window so discovery never depends on
	// when the test actually runs.
	stamp := e2eAt("14:00")
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("e2e: chtimes %s: %v", path, err)
	}
}

// e2eWriteJSONL writes records as JSON lines (one object per line).
func e2eWriteJSONL(t *testing.T, path string, records []map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, r := range records {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("e2e: marshal record for %s: %v", path, err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	e2eWrite(t, path, b.String())
}

// e2eSlug renders a session cwd the way Claude Code names its project dir.
func e2eSlug(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// e2eRel joins the scenario repo dir and a repo-relative path.
func (w *world) e2eRel(rel string) string { return filepath.Join(w.repoDir, filepath.FromSlash(rel)) }

// fakeRunner is a pr.Runner serving canned gh output. It records the calls it
// received so the test can prove no unexpected command ran.
type fakeRunner struct {
	responses map[string][]byte
	calls     []string
}

// newFakeRunner returns an empty fake gh runner.
func newFakeRunner() *fakeRunner {
	return &fakeRunner{responses: map[string][]byte{}}
}

// Run returns the canned response for the exact argument list, or an error.
func (r *fakeRunner) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	r.calls = append(r.calls, key)
	if out, ok := r.responses[key]; ok {
		return out, nil
	}
	return nil, fmt.Errorf("fake gh: unexpected call: gh %s", key)
}

// set registers a canned response for one exact gh argument list.
func (r *fakeRunner) set(args string, body string) { r.responses[args] = []byte(body) }

// e2ePRViewArg is the `gh pr view` argument list internal/pr issues.
var e2ePRViewArg = fmt.Sprintf("pr view %d -R %s --json number,url,title,body,headRefName,baseRefName,createdAt,commits", e2eNumber, e2eRepo)

// e2ePRDiffArg is the `gh pr diff` argument list internal/pr issues.
var e2ePRDiffArg = fmt.Sprintf("pr diff %d -R %s", e2eNumber, e2eRepo)

// e2eCommitArg is the `gh api` argument list for one commit's patch.
func e2eCommitArg(sha string) string {
	return fmt.Sprintf("api repos/%s/commits/%s", e2eRepo, sha)
}

// addE2EPR registers the PR view JSON, the combined diff and the three
// per-commit patches with the fake runner.
func (r *fakeRunner) addE2EPR() {
	r.set(e2ePRViewArg, e2ePRView)
	r.set(e2ePRDiffArg, e2eCombinedDiff)
	for i, sha := range e2eCommitSHAs {
		r.set(e2eCommitArg(sha), e2eCommitPatch(i))
	}
}

// e2ePRView is the `gh pr view --json` payload for PR #482. The body makes two
// claims a reviewer can check against the recorded facts (CON-1 is off in M1).
var e2ePRView = `{
  "number": 482,
  "url": "` + e2eRepoURL + `",
  "title": "` + e2eTitle + `",
  "body": "Adds retry with backoff to the webhook sender.\n\nAll tests pass. Adds tests for the retry path.",
  "headRefName": "` + e2eHeadRef + `",
  "baseRefName": "` + e2eBaseRef + `",
  "createdAt": "2026-09-27T13:50:00Z",
  "commits": [
    {"oid": "` + e2eCommitSHAs[0] + `", "messageHeadline": "Add retry loop to webhook sender", "messageBody": "", "committedDate": "2026-09-27T14:10:00Z"},
    {"oid": "` + e2eCommitSHAs[1] + `", "messageHeadline": "Add idempotency key header", "messageBody": "", "committedDate": "2026-09-27T14:25:00Z"},
    {"oid": "` + e2eCommitSHAs[2] + `", "messageHeadline": "Document RETRY_MAX", "messageBody": "", "committedDate": "2026-09-27T14:50:00Z"}
  ]
}`

// e2eCombinedDiff is PR #482's combined diff. Every added line a session made
// is reproduced verbatim by that session's edit events, so attribution can
// match it; README.md's added line is made by nobody, so it stays uncaptured.
const e2eCombinedDiff = `diff --git a/src/webhooks/retry.ts b/src/webhooks/retry.ts
index 1111111..2222222 100644
--- a/src/webhooks/retry.ts
+++ b/src/webhooks/retry.ts
@@ -1,3 +1,14 @@
 import { post } from "./sender";

+export async function sendWithRetry(url: string, body: unknown) {
+  const max = 5;
+  for (let i = 1; i <= max; i++) {
+    try {
+      return await post(url, body);
+    } catch (err) {
+      await sleep(2 ** i * 100);
+    }
+  }
+  throw new Error("retry exhausted");
+}
 export function send(url, body) { return post(url, body); }
diff --git a/src/webhooks/sender.ts b/src/webhooks/sender.ts
index 3333333..4444444 100644
--- a/src/webhooks/sender.ts
+++ b/src/webhooks/sender.ts
@@ -12,3 +12,4 @@
 export async function post(url: string, body: unknown) {
   const headers: Record<string, string> = {};
+  headers["Idempotency-Key"] = String(Date.now());
   return fetch(url, { method: "POST", headers, body: JSON.stringify(body) });
 }
diff --git a/src/metrics.ts b/src/metrics.ts
index 5555555..6666666 100644
--- a/src/metrics.ts
+++ b/src/metrics.ts
@@ -1,2 +1,4 @@
 import { Counter } from "./metrics-core";

+export const retryAttempts = new Counter("webhook_retry_attempts");
+export const retryExhausted = new Counter("webhook_retry_exhausted");
diff --git a/test/webhook.test.ts b/test/webhook.test.ts
index 7777777..8888888 100644
--- a/test/webhook.test.ts
+++ b/test/webhook.test.ts
@@ -8,3 +8,3 @@
   it("retries until success", async () => {
-    expect(attempts).toBe(5);
+    expect(attempts).toBeGreaterThan(0);
   });
diff --git a/test/retry.test.ts b/test/retry.test.ts
new file mode 100644
index 0000000..9999999
--- /dev/null
+++ b/test/retry.test.ts
@@ -0,0 +1,4 @@
+import { sendWithRetry } from "../src/webhooks/retry";
+
+it("retries three times", async () => {
+  expect(true).toBe(true);
diff --git a/migrations/0042_retry.sql b/migrations/0042_retry.sql
new file mode 100644
index 0000000..aaaaaaa
--- /dev/null
+++ b/migrations/0042_retry.sql
@@ -0,0 +1,2 @@
+ALTER TABLE webhook_events ADD COLUMN retry_count integer DEFAULT 0;
+CREATE INDEX webhook_events_retry_count_idx ON webhook_events (retry_count);
diff --git a/.github/workflows/release.yml b/.github/workflows/release.yml
index bbbbbbb..ccccccc 100644
--- a/.github/workflows/release.yml
+++ b/.github/workflows/release.yml
@@ -20,2 +20,4 @@
       - name: Publish
         run: npm publish
+        env:
+          NODE_OPTIONS: --max-old-space-size=4096
diff --git a/package.json b/package.json
index ddddddd..eeeeeee 100644
--- a/package.json
+++ b/package.json
@@ -10,2 +10,3 @@
     "express": "4.19.2",
+    "p-retry": "6.2.1",
     "prisma": "5.19.0"
diff --git a/README.md b/README.md
index fffffff..1111111 100644
--- a/README.md
+++ b/README.md
@@ -4,2 +4,3 @@
 ## Retries

+Retries are configurable through the ` + "`" + `RETRY_MAX` + "`" + ` environment variable.
`

// e2eCommitPatch returns the `gh api repos/<repo>/commits/<sha>` payload for
// commit i: the same added lines the combined diff shows, scoped to the files
// that commit touched.
func e2eCommitPatch(i int) string {
	type file struct {
		name   string
		status string
		patch  string
	}
	var files []file
	switch i {
	case 0:
		files = []file{
			{"src/webhooks/retry.ts", "modified", "@@ -1,3 +1,14 @@\n import { post } from \"./sender\";\n \n+export async function sendWithRetry(url: string, body: unknown) {\n+  const max = 5;\n+  for (let i = 1; i <= max; i++) {\n+    try {\n+      return await post(url, body);\n+    } catch (err) {\n+      await sleep(2 ** i * 100);\n+    }\n+  }\n+  throw new Error(\"retry exhausted\");\n+}\n export function send(url, body) { return post(url, body); }"},
			{"test/retry.test.ts", "added", "@@ -0,0 +1,4 @@\n+import { sendWithRetry } from \"../src/webhooks/retry\";\n+\n+it(\"retries three times\", async () => {\n+  expect(true).toBe(true);"},
			{"migrations/0042_retry.sql", "added", "@@ -0,0 +1,2 @@\n+ALTER TABLE webhook_events ADD COLUMN retry_count integer DEFAULT 0;\n+CREATE INDEX webhook_events_retry_count_idx ON webhook_events (retry_count);"},
			{"package.json", "modified", "@@ -10,2 +10,3 @@\n     \"express\": \"4.19.2\",\n+    \"p-retry\": \"6.2.1\",\n     \"prisma\": \"5.19.0\""},
		}
	case 1:
		files = []file{
			{"src/webhooks/sender.ts", "modified", "@@ -12,3 +12,4 @@\n export async function post(url: string, body: unknown) {\n   const headers: Record<string, string> = {};\n+  headers[\"Idempotency-Key\"] = String(Date.now());\n   return fetch(url, { method: \"POST\", headers, body: JSON.stringify(body) });\n }"},
			{"test/webhook.test.ts", "modified", "@@ -8,3 +8,3 @@\n   it(\"retries until success\", async () => {\n-    expect(attempts).toBe(5);\n+    expect(attempts).toBeGreaterThan(0);\n   });"},
			{"src/metrics.ts", "modified", "@@ -1,2 +1,4 @@\n import { Counter } from \"./metrics-core\";\n \n+export const retryAttempts = new Counter(\"webhook_retry_attempts\");\n+export const retryExhausted = new Counter(\"webhook_retry_exhausted\");"},
		}
	case 2:
		// The hand-written README change: no session made this line, so it
		// stays unattributed and commit 3 has no session link.
		files = []file{
			{"README.md", "modified", "@@ -4,2 +4,3 @@\n ## Retries\n \n+Retries are configurable through the `RETRY_MAX` environment variable."},
		}
	}
	entries := make([]map[string]string, 0, len(files))
	for _, f := range files {
		entries = append(entries, map[string]string{
			"filename": f.name,
			"status":   f.status,
			"patch":    f.patch,
		})
	}
	raw, err := json.Marshal(map[string]any{"files": entries})
	if err != nil {
		panic("e2e: marshal commit patch: " + err.Error())
	}
	return string(raw)
}

// e2eSignal returns the signal with id from the report, failing the test when
// it is missing.
func e2eSignal(t *testing.T, rep *model.Report, id string) model.Signal {
	t.Helper()
	for _, s := range rep.Signals {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("e2e: signal %s missing from report (%d signals)", id, len(rep.Signals))
	return model.Signal{}
}

// e2eRequireState fails with a readable message when sig is not in state want.
func e2eRequireState(t *testing.T, sig model.Signal, want model.State) {
	t.Helper()
	if sig.State != want {
		t.Errorf("e2e: %s state = %q, want %q (summary: %s)", sig.ID, sig.State, want, sig.Summary)
	}
}

// e2eHasAnchor reports whether any finding of sig anchors onto file, ignoring
// the line range.
func e2eHasAnchor(sig model.Signal, file string) bool {
	for _, f := range sig.Findings {
		for _, a := range f.Anchors {
			if a.File == file {
				return true
			}
		}
	}
	return false
}

// e2eFindingMentions reports whether any finding summary contains substr and
// whether any evidence excerpt or finding data does too.
func e2eFindingMentions(sig model.Signal, substr string) bool {
	for _, f := range sig.Findings {
		if strings.Contains(f.Summary, substr) {
			return true
		}
		for _, e := range f.Evidence {
			if strings.Contains(e.Excerpt, substr) {
				return true
			}
		}
		for _, v := range f.Data {
			if strings.Contains(fmt.Sprint(v), substr) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Claude Code session A: hooked, the main body of the PR.
// ---------------------------------------------------------------------------

// e2eCCSessionID is session A's Claude Code session id. Hook records in the
// log carry it, so the commit SHAs link exactly.
const e2eCCSessionID = "d41f7a0e-4c1b-4f2a-9a11-4821a7c9e000"

// e2eCCAgentID is the subagent that writes test/retry.test.ts.
const e2eCCAgentID = "b7c2f1a0"

// e2eRetryAdded is the body of sendWithRetry as it lands in the PR diff.
// Session A's edit events reproduce these exact lines, so attribution can
// match every one of them.
var e2eRetryAdded = []string{
	"export async function sendWithRetry(url: string, body: unknown) {",
	"  const max = 5;",
	"  for (let i = 1; i <= max; i++) {",
	"    try {",
	"      return await post(url, body);",
	"    } catch (err) {",
	"      await sleep(2 ** i * 100);",
	"    }",
	"  }",
	"  throw new Error(\"retry exhausted\");",
	"}",
}

// e2eRetryTestAdded is the body of test/retry.test.ts, written by the subagent.
var e2eRetryTestAdded = []string{
	"import { sendWithRetry } from \"../src/webhooks/retry\";",
	"",
	"it(\"retries three times\", async () => {",
	"  expect(true).toBe(true);",
}

// ccTranscript accumulates one Claude Code transcript file.
type ccTranscript struct {
	w     *world
	id    string
	lines []map[string]any
	n     int
}

// newCCSession starts a Claude Code transcript for session id.
func (w *world) newCCSession(id string) *ccTranscript {
	return &ccTranscript{w: w, id: id}
}

// entry builds a common entry with the fields every Claude Code line carries.
//
// The entries deliberately carry no `gitBranch`. The transcript is the only
// source of Session.Branch, and internal/link's branch+time heuristic links
// every commit within two hours of a session on the PR's head ref; recording
// the branch would therefore tie the hand-written commit 3 to all three
// sessions instead of leaving it unattributed (T25, AUTH-2).
func (b *ccTranscript) entry(typ, hhmm string, sec int) map[string]any {
	b.n++
	return map[string]any{
		"type":      typ,
		"uuid":      fmt.Sprintf("%s-e%03d", b.id, b.n),
		"sessionId": b.id,
		"timestamp": e2eStamp(e2eAtSec(hhmm, sec)),
		"cwd":       b.w.repoDir,
		"version":   "2.1.283",
	}
}

// prompt appends a human prompt.
func (b *ccTranscript) prompt(hhmm string, sec int, text string) {
	e := b.entry("user", hhmm, sec)
	e["promptSource"] = "typed"
	e["origin"] = map[string]any{"kind": "human"}
	e["message"] = map[string]any{"role": "user", "content": text}
	b.lines = append(b.lines, e)
}

// say appends an assistant text block under the given model.
func (b *ccTranscript) say(hhmm string, sec int, model, text string) {
	e := b.entry("assistant", hhmm, sec)
	e["message"] = map[string]any{
		"role":    "assistant",
		"model":   model,
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
	b.lines = append(b.lines, e)
}

// call appends an assistant tool_use and its tool_result.
func (b *ccTranscript) call(hhmm string, sec int, toolUseID, name string, input map[string]any, result any, raw any, isError bool) {
	e := b.entry("assistant", hhmm, sec)
	e["message"] = map[string]any{
		"role":  "assistant",
		"model": "claude-opus-5-5",
		"content": []any{map[string]any{
			"type":  "tool_use",
			"id":    toolUseID,
			"name":  name,
			"input": input,
		}},
	}
	b.lines = append(b.lines, e)

	r := b.entry("user", hhmm, sec+1)
	block := map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": result}
	if isError {
		block["is_error"] = true
	}
	r["message"] = map[string]any{"role": "user", "content": []any{block}}
	if raw != nil {
		r["toolUseResult"] = raw
	}
	b.lines = append(b.lines, r)
}

// edit appends an Edit tool call that replaces removed with added in rel, with
// the structured patch Claude Code records for it.
func (b *ccTranscript) edit(hhmm string, sec int, id, rel string, oldStart int, removed, added []string) {
	abs := b.w.e2eRel(rel)
	lines := make([]string, 0, len(removed)+len(added))
	for _, r := range removed {
		lines = append(lines, "-"+r)
	}
	for _, a := range added {
		lines = append(lines, "+"+a)
	}
	b.call(hhmm, sec, id, "Edit",
		map[string]any{
			"file_path":  abs,
			"old_string": strings.Join(removed, "\n"),
			"new_string": strings.Join(added, "\n"),
		},
		"Applied 1 edit to "+rel,
		map[string]any{
			"filePath": abs,
			"structuredPatch": []any{map[string]any{
				"oldStart": oldStart,
				"oldLines": len(removed),
				"newStart": oldStart,
				"newLines": len(added),
				"lines":    lines,
			}},
		},
		false)
}

// bashTool appends a Bash tool call that succeeded.
func (b *ccTranscript) bashTool(hhmm string, sec int, id, cmd, stdout string) {
	b.call(hhmm, sec, id, "Bash",
		map[string]any{"command": cmd},
		stdout,
		map[string]any{"stdout": stdout, "stderr": "", "interrupted": false},
		false)
}

// bashFailTool appends a Bash tool call that failed with exit code.
func (b *ccTranscript) bashFailTool(hhmm string, sec int, id, cmd string, exit int, output string) {
	b.call(hhmm, sec, id, "Bash",
		map[string]any{"command": cmd},
		fmt.Sprintf("Exit code %d\n%s", exit, output),
		fmt.Sprintf("Error: Exit code %d\n%s", exit, output),
		true)
}

// permissionMode appends a permission-mode entry.
func (b *ccTranscript) permissionMode(hhmm string, sec int, mode string) {
	e := b.entry("permission-mode", hhmm, sec)
	e["permissionMode"] = mode
	b.lines = append(b.lines, e)
}

// compactBoundary appends a system compact_boundary entry.
func (b *ccTranscript) compactBoundary(hhmm string, sec int, trigger string) {
	e := b.entry("system", hhmm, sec)
	e["subtype"] = "compact_boundary"
	e["content"] = "Conversation compacted"
	e["compactMetadata"] = map[string]any{"trigger": trigger, "preTokens": 168000, "postTokens": 24000}
	b.lines = append(b.lines, e)
}

// interrupt appends the user entry Claude Code writes when a human stops the
// agent mid-turn.
func (b *ccTranscript) interrupt(hhmm string, sec int) {
	e := b.entry("user", hhmm, sec)
	e["message"] = map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}},
	}
	b.lines = append(b.lines, e)
}

// write serialises the transcript to <claude root>/<slug>/<id>.jsonl.
func (b *ccTranscript) write() {
	path := filepath.Join(b.w.claudeDir, e2eSlug(b.w.repoDir), b.id+".jsonl")
	e2eWriteJSONL(b.w.t, path, b.lines)
}

// writeSubagent writes the subagent transcript for session A. Its events are
// appended to the parent session with AgentID set.
func (b *ccTranscript) writeSubagent(agentID string) {
	sub := &ccTranscript{w: b.w, id: b.id, n: 500}
	e := sub.entry("assistant", "14:12", 30)
	e["message"] = map[string]any{
		"role":  "assistant",
		"model": "claude-sonnet-5",
		"content": []any{map[string]any{
			"type":  "tool_use",
			"id":    "toolu_sub_write",
			"name":  "Write",
			"input": map[string]any{"file_path": b.w.e2eRel("test/retry.test.ts"), "content": strings.Join(e2eRetryTestAdded, "\n")},
		}},
	}
	sub.lines = append(sub.lines, e)

	r := sub.entry("user", "14:12", 31)
	r["message"] = map[string]any{"role": "user", "content": []any{map[string]any{
		"type": "tool_result", "tool_use_id": "toolu_sub_write", "content": "File created successfully",
	}}}
	r["toolUseResult"] = map[string]any{"type": "create", "filePath": b.w.e2eRel("test/retry.test.ts")}
	sub.lines = append(sub.lines, r)

	path := filepath.Join(b.w.claudeDir, e2eSlug(b.w.repoDir), b.id, "subagents", "agent-"+agentID+".jsonl")
	e2eWriteJSONL(b.w.t, path, sub.lines)
}

// writeClaudeSessionA builds and writes Claude Code session A and its hook
// records.
func (w *world) writeClaudeSessionA() {
	b := w.newCCSession(e2eCCSessionID)

	b.permissionMode("14:00", 0, "bypassPermissions")
	b.prompt("14:00", 5, "Add retry with backoff to the webhook sender. Don't touch the queue.")
	b.say("14:00", 9, "claude-opus-5-5", "I'll add a retry loop around the webhook sender.")

	// INT-2: a question the agent asked and the option the human picked.
	question := map[string]any{
		"question": "Where should the retry attempt count live between deploys?",
		"header":   "Retry state",
		"options":  []any{map[string]any{"label": "Redis"}, map[string]any{"label": "Postgres"}},
	}
	b.call("14:00", 20, "toolu_ask", "AskUserQuestion",
		map[string]any{"questions": []any{question}},
		"= Postgres",
		map[string]any{
			"questions": []any{question},
			"answers":   map[string]any{"Where should the retry attempt count live between deploys?": "Postgres"},
		},
		false)

	// INT-3: a plan a human approved, four steps including "add jitter".
	b.call("14:00", 30, "toolu_plan", "ExitPlanMode",
		map[string]any{"plan": "1. Add sendWithRetry to src/webhooks/retry.ts\n2. Wire the webhook sender through it\n3. Add a metrics counter for retries\n4. add jitter to the backoff"},
		"User has approved your plan. You can now start coding.",
		"User has approved your plan. You can now start coding.",
		false)

	// The retry body lands across nine edits with a few edit -> test cycles.
	for i, line := range e2eRetryAdded {
		hhmm, sec := "14:00", 40+i*2
		if i >= 4 {
			hhmm, sec = "14:01", (i-4)*10
		}
		b.edit(hhmm, sec, fmt.Sprintf("toolu_edit_%02d", i+1), "src/webhooks/retry.ts", 3, nil, []string{line})
		if i == 2 || i == 4 {
			b.bashFailTool(hhmm, sec+1, fmt.Sprintf("toolu_test_%d", i), "npm test", 1,
				"Tests: 1 failed, 60 passed\n")
		}
	}

	// VER-4: a failing run, then the test is loosened, then it passes.
	b.bashFailTool("14:01", 40, "toolu_test_fail", "npm test", 1,
		"Tests: 1 failed, 60 passed\n  retries until success\n    Expected: 5\n    Received: 2\n")
	b.edit("14:01", 50, "toolu_edit_test", "test/webhook.test.ts", 8,
		[]string{"    expect(attempts).toBe(5);"},
		[]string{"    expect(attempts).toBeGreaterThan(0);"})
	b.bashTool("14:02", 0, "toolu_test_pass", "npm test", "Tests: 61 passed, 61 total\n")

	// Two more edits to the retry path, then dependency churn.
	b.edit("14:02", 20, "toolu_edit_10", "src/webhooks/retry.ts", 3, nil, []string{e2eRetryAdded[5]})
	b.edit("14:02", 40, "toolu_edit_11", "src/webhooks/retry.ts", 3, nil, []string{e2eRetryAdded[6]})

	// EXP-2: one dependency the agent chose, one name that does not exist.
	b.bashTool("14:03", 0, "toolu_install_ok", "npm i p-retry@6.2.1", "added 1 package in 1s\n")
	b.edit("14:03", 20, "toolu_edit_pkg", "package.json", 10, nil,
		[]string{"    \"p-retry\": \"6.2.1\","})
	b.bashFailTool("14:04", 0, "toolu_install_bad", "npm i express-retry-webhook", 1,
		"npm error code E404\nnpm error 404 Not Found - GET https://registry.npmjs.org/express-retry-webhook\n")

	// DEC-1: an interrupt while editing the migration.
	b.edit("14:05", 0, "toolu_edit_migration", "migrations/0042_retry.sql", 1, nil,
		[]string{"ALTER TABLE webhook_events ADD COLUMN retry_count integer DEFAULT 0;"})
	b.interrupt("14:05", 10)

	// EXP-1: a side effect outside the diff.
	b.bashTool("14:06", 0, "toolu_migrate", "prisma migrate deploy", "1 migration applied\n")

	// FRI-1 churn and VER-2: two more edits to the retry path, with no check
	// run after them, so the retry body is stale relative to the last pass.
	b.edit("14:07", 0, "toolu_edit_12", "src/webhooks/retry.ts", 3, nil, []string{e2eRetryAdded[9]})
	b.edit("14:07", 20, "toolu_edit_13", "src/webhooks/retry.ts", 3, nil, []string{e2eRetryAdded[10]})

	// EXP-3: untrusted input read, then a sensitive workflow file edited.
	b.bashTool("14:08", 0, "toolu_gh_issue", "gh issue view 312", "Issue #312: release job runs out of memory\n")
	b.edit("14:09", 0, "toolu_edit_workflow", ".github/workflows/release.yml", 20, nil,
		[]string{"        env:", "          NODE_OPTIONS: --max-old-space-size=4096"})

	// EXP-4: a secret file read whose output carries a secret-shaped value.
	b.bashTool("14:10", 0, "toolu_cat_env", "cat .env",
		"DATABASE_URL=postgres://shop:devpass@localhost:5432/shop\nANTHROPIC_API_KEY="+e2eEnvValue+"\n")

	// CON-2: a todo list left with one item still open.
	b.call("14:11", 0, "toolu_todo", "TodoWrite",
		map[string]any{"todos": []any{
			map[string]any{"content": "Add the retry loop", "status": "completed"},
			map[string]any{"content": "Wire metrics", "status": "completed"},
			map[string]any{"content": "add jitter", "status": "pending"},
		}},
		"Todos updated",
		map[string]any{"oldTodos": []any{}, "newTodos": []any{}},
		false)

	// OVS-3: a subagent writes test/retry.test.ts.
	b.call("14:12", 0, "toolu_agent", "Agent",
		map[string]any{"subagent_type": "general-purpose", "prompt": "Write a unit test for the retry path in test/retry.test.ts"},
		"Subagent finished",
		map[string]any{"agentId": e2eCCAgentID, "resolvedModel": "claude-sonnet-5", "status": "completed"},
		false)
	b.writeSubagent(e2eCCAgentID)

	// FRI-3: a compaction, then more PR work after it.
	b.compactBoundary("14:30", 0, "auto")
	b.edit("14:31", 0, "toolu_edit_metrics", "src/metrics.ts", 1, nil,
		[]string{"export const retryAttempts = new Counter(\"webhook_retry_attempts\");"})
	b.edit("14:31", 20, "toolu_edit_metrics2", "src/metrics.ts", 1, nil,
		[]string{"export const retryExhausted = new Counter(\"webhook_retry_exhausted\");"})

	b.write()
	w.writeClaudeHooks()
}

// writeClaudeHooks writes the hook log records for session A: one at each of
// the first two commits, so AUTH-2 can link those commits to the session by
// exact SHA.
func (w *world) writeClaudeHooks() {
	recs := []model.HookRecord{
		{Harness: model.HarnessClaudeCode, Event: "SessionStart", SessionID: e2eCCSessionID,
			TS: e2eAt("13:59"), CWD: w.repoDir, HeadSHA: e2eCommitSHAs[0], Branch: e2eHeadRef,
			Trigger: "session_start", Reason: "startup"},
		{Harness: model.HarnessClaudeCode, Event: "PostToolUse", SessionID: e2eCCSessionID,
			TS: e2eAt("14:35"), CWD: w.repoDir, HeadSHA: e2eCommitSHAs[0], Branch: e2eHeadRef,
			Trigger: "commit", ToolName: "Bash", Command: "git commit -m \"Add retry loop to webhook sender\""},
		{Harness: model.HarnessClaudeCode, Event: "PostToolUse", SessionID: e2eCCSessionID,
			TS: e2eAt("14:40"), CWD: w.repoDir, HeadSHA: e2eCommitSHAs[1], Branch: e2eHeadRef,
			Trigger: "commit", ToolName: "Bash", Command: "git commit -m \"Add idempotency key header\""},
	}
	for _, r := range recs {
		if err := hooklog.Append(w.hookDir, r); err != nil {
			w.t.Fatalf("e2e: append hook record: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Codex session B: reconstructed, linked by the commit it started on.
// ---------------------------------------------------------------------------

// e2eCodexSessionID is session B's Codex thread id.
const e2eCodexSessionID = "019f0000-aaaa-7000-8000-000000000482"

// e2eSenderAdded is the Idempotency-Key line both session B and session C add.
const e2eSenderAdded = `  headers["Idempotency-Key"] = String(Date.now());`

// writeCodexSessionB writes the Codex rollout: a session_meta pinned to commit
// 2, the settings turn, a web search, the sender edit and a passing typecheck.
func (w *world) writeCodexSessionB() {
	senderDiff := "@@ -12,3 +12,4 @@\n export async function post(url: string, body: unknown) {\n   const headers: Record<string, string> = {};\n+" + e2eSenderAdded + "\n   return fetch(url, { method: \"POST\", headers, body: JSON.stringify(body) });\n }"

	lines := []map[string]any{
		{
			"timestamp": e2eStamp(e2eAt("14:41")),
			"type":      "session_meta",
			"payload": map[string]any{
				"id":          e2eCodexSessionID,
				"timestamp":   e2eStamp(e2eAt("14:41")),
				"cwd":         w.repoDir,
				"originator":  "codex_cli_rs",
				"cli_version": "0.154.0",
				"source":      "cli",
				"git": map[string]any{
					"commit_hash":    e2eCommitSHAs[1],
					"repository_url": "git@github.com:" + e2eRepo + ".git",
				},
			},
		},
		{
			"timestamp": e2eStamp(e2eAtSec("14:41", 1)),
			"type":      "turn_context",
			"payload": map[string]any{
				"turn_id":         "t1",
				"cwd":             w.repoDir,
				"approval_policy": "never",
				"sandbox_policy":  map[string]any{"type": "danger-full-access"},
				"model":           "gpt-5.6-luna",
				"effort":          "xhigh",
			},
		},
		{
			"timestamp": e2eStamp(e2eAtSec("14:41", 2)),
			"type":      "event_msg",
			"payload": map[string]any{
				"type":    "item_completed",
				"turn_id": "t1",
				"item": map[string]any{
					"type":    "UserMessage",
					"id":      "m1",
					"content": []any{map[string]any{"type": "text", "text": "Add an idempotency key header to the webhook sender."}},
				},
			},
		},
		// FRI-4: a web lookup before the edit it informed.
		{
			"timestamp": e2eStamp(e2eAtSec("14:42", 0)),
			"type":      "event_msg",
			"payload": map[string]any{
				"type":    "item_completed",
				"turn_id": "t1",
				"item": map[string]any{
					"type":  "Extension",
					"id":    "ext-1",
					"kind":  "web.search",
					"query": "stripe webhook idempotency key header",
					"action": map[string]any{
						"type":    "search",
						"queries": []any{"stripe webhook idempotency key header"},
					},
				},
			},
		},
		{
			"timestamp": e2eStamp(e2eAtSec("14:43", 0)),
			"type":      "event_msg",
			"payload": map[string]any{
				"type":    "item_completed",
				"turn_id": "t1",
				"item": map[string]any{
					"type":   "FileChange",
					"id":     "exec-2",
					"status": "completed",
					"changes": map[string]any{
						w.e2eRel("src/webhooks/sender.ts"): map[string]any{
							"type":         "update",
							"unified_diff": senderDiff,
							"move_path":    nil,
						},
					},
				},
			},
		},
		{
			"timestamp": e2eStamp(e2eAtSec("14:45", 0)),
			"type":      "event_msg",
			"payload": map[string]any{
				"type":    "item_completed",
				"turn_id": "t1",
				"item": map[string]any{
					"type":              "CommandExecution",
					"id":                "exec-3",
					"command":           []any{"/bin/zsh", "-lc", "npx tsc --noEmit"},
					"cwd":               w.repoDir,
					"parsed_cmd":        []any{},
					"status":            "completed",
					"exit_code":         0,
					"aggregated_output": "",
					"duration":          map[string]any{"secs": 6, "nanos": 0},
				},
			},
		},
	}
	e2eWriteJSONL(w.t, w.codexSessionPath(), lines)
}

// codexSessionPath is where Codex keeps one day's rollout files.
func (w *world) codexSessionPath() string {
	return filepath.Join(w.codexDir, "sessions", "2026", "09", "27",
		"rollout-2026-09-27T14-41-00-"+e2eCodexSessionID+".jsonl")
}

// ---------------------------------------------------------------------------
// Pi session C: extension hook records, plus an abandoned branch.
// ---------------------------------------------------------------------------

// e2ePiSessionID is session C's Pi session id.
const e2ePiSessionID = "8f0c2c7e-0000-4000-8000-000000000482"

// writePiSessionC writes the Pi session file: a user prompt, an edit that is
// abandoned by a branch_summary, the surviving edit to the same file, and a
// paircli extension hook record pinning commit 2.
func (w *world) writePiSessionC() {
	senderDiff := "  40   const headers: Record<string, string> = {};\n+ 41   " + strings.TrimSpace(e2eSenderAdded) + "\n  42   return fetch(url);"

	lines := []map[string]any{
		{
			"type":      "session",
			"version":   3,
			"id":        e2ePiSessionID,
			"timestamp": e2eStamp(e2eAt("14:49")),
			"cwd":       w.repoDir,
		},
		{
			"type":      "message",
			"id":        "pi000001",
			"timestamp": e2eStamp(e2eAtSec("14:49", 5)),
			"message": map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "text", "text": "Make the webhook sender idempotent."}},
			},
		},
		// An attempt on a branch the agent then abandons.
		{
			"type":      "message",
			"id":        "pi000002",
			"parentId":  "pi000001",
			"timestamp": e2eStamp(e2eAtSec("14:50", 0)),
			"message": map[string]any{
				"role":  "assistant",
				"model": "claude-opus-5-5",
				"content": []any{
					map[string]any{"type": "text", "text": "Trying a per-attempt key first."},
					map[string]any{"type": "toolCall", "id": "call_1", "name": "edit", "arguments": map[string]any{
						"path":  "src/webhooks/sender.ts",
						"edits": []any{map[string]any{"oldText": "  const headers = {};", "newText": "  headers[\"Idempotency-Key\"] = attemptKey();"}},
					}},
				},
			},
		},
		{
			"type":      "message",
			"id":        "pi000003",
			"parentId":  "pi000002",
			"timestamp": e2eStamp(e2eAtSec("14:50", 5)),
			"message": map[string]any{
				"role":       "toolResult",
				"toolCallId": "call_1",
				"toolName":   "edit",
				"content":    []any{map[string]any{"type": "text", "text": "Replaced 1 block in src/webhooks/sender.ts."}},
			},
			"isError": false,
		},
		// DEC-2: the branch is abandoned.
		{
			"type":      "branch_summary",
			"id":        "pi000004",
			"parentId":  "pi000003",
			"timestamp": e2eStamp(e2eAtSec("14:51", 0)),
			"summary":   "Discarded the per-attempt key approach and went back to a stable event key.",
			"fromId":    "pi000002",
		},
		// The surviving edit, on the branch that is actually kept.
		{
			"type":      "message",
			"id":        "pi000005",
			"parentId":  "pi000001",
			"timestamp": e2eStamp(e2eAtSec("14:52", 0)),
			"message": map[string]any{
				"role":  "assistant",
				"model": "claude-opus-5-5",
				"content": []any{
					map[string]any{"type": "text", "text": "Using a stable key derived from the payload."},
					map[string]any{"type": "toolCall", "id": "call_2", "name": "edit", "arguments": map[string]any{
						"path":  "src/webhooks/sender.ts",
						"edits": []any{map[string]any{"oldText": "  const headers: Record<string, string> = {};", "newText": e2eSenderAdded}},
					}},
				},
			},
		},
		{
			"type":      "message",
			"id":        "pi000006",
			"parentId":  "pi000005",
			"timestamp": e2eStamp(e2eAtSec("14:52", 5)),
			"message": map[string]any{
				"role":       "toolResult",
				"toolCallId": "call_2",
				"toolName":   "edit",
				"content":    []any{map[string]any{"type": "text", "text": "Replaced 1 block in src/webhooks/sender.ts."}},
				"details":    map[string]any{"diff": senderDiff},
			},
			"isError": false,
		},
		// The paircli extension's own hook record.
		{
			"type":       "custom",
			"id":         "pi000007",
			"parentId":   "pi000006",
			"timestamp":  e2eStamp(e2eAtSec("14:55", 0)),
			"customType": "paircli",
			"data": map[string]any{
				"v":          model.HookRecordVersion,
				"harness":    string(model.HarnessPi),
				"event":      "tool_execution_end",
				"session_id": e2ePiSessionID,
				"ts":         e2eStamp(e2eAtSec("14:55", 0)),
				"cwd":        w.repoDir,
				"head_sha":   e2eCommitSHAs[1],
				"branch":     e2eHeadRef,
				"trigger":    "commit",
				"command":    "git commit -m \"Add idempotency key header\"",
			},
		},
	}
	path := filepath.Join(w.piDir, "--"+e2eSlug(w.repoDir)+"--", "2026-09-27T14-49-00-000Z_"+e2ePiSessionID+".jsonl")
	e2eWriteJSONL(w.t, path, lines)
}

// build writes every transcript and hook record for the scenario.
func (w *world) build() {
	w.writeClaudeSessionA()
	w.writeCodexSessionB()
	w.writePiSessionC()
}
