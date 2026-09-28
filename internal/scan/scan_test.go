package scan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// scanRunner is a pr.Runner that serves canned gh output and records calls.
type scanRunner struct {
	responses map[string][]byte
	errs      map[string]error
	calls     []string
}

func newScanRunner() *scanRunner {
	return &scanRunner{responses: map[string][]byte{}, errs: map[string]error{}}
}

func (r *scanRunner) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	r.calls = append(r.calls, key)
	if err, ok := r.errs[key]; ok {
		return nil, err
	}
	if out, ok := r.responses[key]; ok {
		return out, nil
	}
	return nil, errUnexpectedCall(key)
}

type errUnexpectedCall string

func (e errUnexpectedCall) Error() string { return "scanRunner: unexpected gh call: " + string(e) }

func (r *scanRunner) called(substr string) bool {
	for _, c := range r.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// scanGHView is the `gh pr view --json` payload the minimal scenario serves.
const scanGHView = `{
  "number": 7,
  "url": "https://github.com/acme/shop/pull/7",
  "title": "Add retry with backoff",
  "body": "Adds retries to the webhook sender.",
  "headRefName": "feat/retry",
  "baseRefName": "main",
  "createdAt": "2026-09-27T13:50:00Z",
  "commits": [
    {"oid": "aaaaaaa1", "messageHeadline": "Add retry", "messageBody": "", "committedDate": "2026-09-27T14:05:00Z"}
  ]
}`

// scanDiff is the combined PR diff, whose single added line the transcript
// edit below reproduces verbatim.
const scanDiff = `diff --git a/src/webhooks/retry.ts b/src/webhooks/retry.ts
index 1111111..2222222 100644
--- a/src/webhooks/retry.ts
+++ b/src/webhooks/retry.ts
@@ -10,1 +10,2 @@
 function send() {
+return sendWithRetry(url, body)
`

const scanPRArg = "pr view 7 -R acme/shop --json number,url,title,body,headRefName,baseRefName,createdAt,commits"

// scanDiffArg is the gh argument list for the combined PR diff.
const scanDiffArg = "pr diff 7 -R acme/shop"

// scanGitRepo writes the minimal .git metadata internal/gitinfo reads (HEAD
// plus an origin remote) into dir.
func scanGitRepo(t *testing.T, dir, remote string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scanWrite(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat/retry\n")
	scanWrite(t, filepath.Join(gitDir, "config"),
		"[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = "+remote+"\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n")
}

// scanTranscript writes one synthetic Claude Code transcript for a session
// whose cwd is repoRoot, edits src/webhooks/retry.ts, and asks Discover to
// see it inside the scan window.
func scanTranscript(t *testing.T, projects, repoRoot string) {
	t.Helper()
	dir := filepath.Join(projects, "-tmp-shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repoRoot, "src", "webhooks", "retry.ts")
	lines := []string{
		`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-27T14:00:00.000Z","cwd":"` + repoRoot + `","gitBranch":"feat/retry","version":"2.1.283","promptSource":"typed","message":{"role":"user","content":"Add retry with backoff to the webhook sender."}}`,
		`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-27T14:00:30.000Z","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"I'll add the retry call."},{"type":"tool_use","id":"toolu_01A","name":"Write","input":{"file_path":"` + file + `","content":"return sendWithRetry(url, body)\n"}}]}}`,
		`{"type":"user","uuid":"u2","sessionId":"s1","timestamp":"2026-09-27T14:00:31.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01A","content":"File created"}]},"toolUseResult":{"type":"create","filePath":"` + file + `"}}`,
	}
	path := filepath.Join(dir, "s1.jsonl")
	scanWrite(t, path, strings.Join(lines, "\n")+"\n")
	// Pin the mtime inside the scan window so the test does not depend on
	// when it runs.
	stamp := time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func scanWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanConfig points every harness root at its own temp dir so the test never
// reads the developer's real session stores.
func scanConfig(t *testing.T, projects string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Roots.ClaudeCode = projects
	cfg.Roots.Codex = t.TempDir()
	cfg.Roots.Pi = t.TempDir()
	cfg.Roots.HookLog = t.TempDir()
	return cfg
}

// TestRun_Minimal scans one synthetic Claude Code session against a fake gh
// and checks the output folder, the AUTH-2 signal and the alert count.
func TestRun_Minimal(t *testing.T) {
	repoRoot := t.TempDir()
	scanGitRepo(t, repoRoot, "git@github.com:acme/shop.git")

	projects := t.TempDir()
	scanTranscript(t, projects, repoRoot)

	r := newScanRunner()
	r.responses[scanPRArg] = []byte(scanGHView)
	r.responses[scanDiffArg] = []byte(scanDiff)

	outDir := filepath.Join(t.TempDir(), "pr-7")
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	res, err := Run(context.Background(), Options{
		Repo:            "acme/shop",
		Number:          7,
		CWD:             repoRoot,
		OutDir:          outDir,
		Runner:          r,
		Config:          scanConfig(t, projects),
		NoHooks:         true,
		NoCommitPatches: true,
		Now:             now,
		Version:         "0.0.0-test",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.OutDir != outDir {
		t.Errorf("OutDir = %q, want %q", res.OutDir, outDir)
	}
	for _, name := range []string{"signals.json", "report.md", "comment.md", "authorship.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("output file %s: %v", name, err)
		}
	}
	sessFiles, err := os.ReadDir(filepath.Join(outDir, "sessions"))
	if err != nil {
		t.Fatalf("reading sessions dir: %v", err)
	}
	if len(sessFiles) != 1 {
		t.Errorf("session files = %d, want 1 (%v)", len(sessFiles), sessFiles)
	}

	if res.Report == nil {
		t.Fatal("nil report")
	}
	if got := res.Report.Coverage.Sessions; got != 1 {
		t.Errorf("coverage sessions = %d, want 1", got)
	}
	if got := res.Report.Coverage.LinesExplained; got != 1 {
		t.Errorf("lines explained = %d, want 1", got)
	}
	if got := res.Report.PR.Repo; got != "acme/shop" {
		t.Errorf("report repo = %q", got)
	}

	auth2 := scanFindSignal(res.Report.Signals, "AUTH-2")
	if auth2 == nil {
		t.Fatalf("AUTH-2 missing from %d signals", len(res.Report.Signals))
	}
	if auth2.State == model.StateUnknown {
		t.Errorf("AUTH-2 state = unknown (%s)", auth2.Summary)
	}
	// 30 catalog entries, minus the 3 inferred ones (DEC-3, CON-1, CON-3)
	// that only the T26 LLM pass produces.
	if len(res.Report.Signals) != 27 {
		t.Errorf("signals = %d, want 27", len(res.Report.Signals))
	}

	alerts := 0
	for _, s := range res.Report.Signals {
		if s.State == model.StateAlert {
			alerts++
		}
	}
	if alerts != res.Alerts {
		t.Errorf("Alerts = %d, want %d (state alert count)", res.Alerts, alerts)
	}

	// The report must be reachable from the written file, too.
	raw, err := os.ReadFile(filepath.Join(outDir, "signals.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written model.Report
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("signals.json: %v", err)
	}
	if len(written.Signals) != len(res.Report.Signals) {
		t.Errorf("signals.json has %d signals, want %d", len(written.Signals), len(res.Report.Signals))
	}
}

// scanFindSignal returns the signal with id, or nil.
func scanFindSignal(sigs []model.Signal, id string) *model.Signal {
	for i := range sigs {
		if sigs[i].ID == id {
			return &sigs[i]
		}
	}
	return nil
}

// TestPostComment_CreateAndUpdate checks that posting creates a comment when
// no marker comment exists and updates the existing one when it does.
func TestPostComment_CreateAndUpdate(t *testing.T) {
	dir := t.TempDir()
	body := filepath.Join(dir, "comment.md")
	scanWrite(t, body, CommentMarker+"\n# paircli\n")

	t.Run("create", func(t *testing.T) {
		r := newScanRunner()
		r.responses["api repos/acme/shop/issues/7/comments --paginate"] = []byte("[]")
		r.responses["pr comment 7 -R acme/shop --body-file "+body] = []byte("https://github.com/acme/shop/pull/7#issuecomment-1\n")

		if err := postComment(r, "acme/shop", 7, body); err != nil {
			t.Fatalf("postComment: %v", err)
		}
		if !r.called("pr comment 7 -R acme/shop") {
			t.Errorf("expected a new comment, calls = %v", r.calls)
		}
		if r.called("-X PATCH") {
			t.Errorf("did not expect a PATCH, calls = %v", r.calls)
		}
	})

	t.Run("update", func(t *testing.T) {
		r := newScanRunner()
		r.responses["api repos/acme/shop/issues/7/comments --paginate"] = []byte(
			`[{"id":11,"body":"unrelated\n"},{"id":999,"body":"` + CommentMarker + `\n# old"}]`)
		r.responses["api -X PATCH repos/acme/shop/issues/comments/999 -F body=@"+body] = []byte("{}")

		if err := postComment(r, "acme/shop", 7, body); err != nil {
			t.Fatalf("postComment: %v", err)
		}
		if !r.called("api -X PATCH repos/acme/shop/issues/comments/999") {
			t.Errorf("expected a PATCH of comment 999, calls = %v", r.calls)
		}
		if r.called("pr comment") {
			t.Errorf("did not expect a new comment, calls = %v", r.calls)
		}
	})

	t.Run("paginated update", func(t *testing.T) {
		// gh api --paginate concatenates one JSON array per page.
		r := newScanRunner()
		r.responses["api repos/acme/shop/issues/7/comments --paginate"] = []byte(
			`[{"id":11,"body":"unrelated"}]` + `[{"id":999,"body":"` + CommentMarker + `\n# old"}]`)
		r.responses["api -X PATCH repos/acme/shop/issues/comments/999 -F body=@"+body] = []byte("{}")

		if err := postComment(r, "acme/shop", 7, body); err != nil {
			t.Fatalf("postComment: %v", err)
		}
		if !r.called("comments/999") {
			t.Errorf("expected a PATCH of comment 999, calls = %v", r.calls)
		}
	})

	t.Run("missing body file", func(t *testing.T) {
		r := newScanRunner()
		if err := postComment(r, "acme/shop", 7, filepath.Join(dir, "nope.md")); err == nil {
			t.Fatal("expected an error for a missing comment body")
		}
	})
}
