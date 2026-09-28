package agenttrace_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/agenttrace"
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/scan"
)

// stubRunner serves the two gh calls the minimal scan below makes.
type stubRunner struct{ responses map[string][]byte }

func (r stubRunner) Run(args ...string) ([]byte, error) {
	if out, ok := r.responses[strings.Join(args, " ")]; ok {
		return out, nil
	}
	return nil, os.ErrNotExist
}

// TestScanRun_WritesRecord checks the pipeline wires the export in: scan.Run
// writes agent-trace.json next to the other outputs, it carries the PR head
// commit and reports a line no session explains as unknown, it embeds no line
// text — and Options.NoAgentTrace suppresses it.
func TestScanRun_WritesRecord(t *testing.T) {
	const view = `{"number":7,"url":"https://github.com/acme/shop/pull/7","title":"t","body":"b",
	"headRefName":"feat/retry","baseRefName":"main","createdAt":"2026-09-27T13:50:00Z",
	"commits":[{"oid":"aaaaaaa1","messageHeadline":"Add retry","messageBody":"","committedDate":"2026-09-27T14:05:00Z"}]}`
	const diff = `diff --git a/src/webhooks/retry.ts b/src/webhooks/retry.ts
index 1111111..2222222 100644
--- a/src/webhooks/retry.ts
+++ b/src/webhooks/retry.ts
@@ -10,1 +10,2 @@
 function send() {
+return sendWithRetry(url, body)
`
	r := stubRunner{responses: map[string][]byte{
		"pr view 7 -R acme/shop --json number,url,title,body,headRefName,baseRefName,createdAt,commits": []byte(view),
		"pr diff 7 -R acme/shop": []byte(diff),
	}}
	cfg := config.Default()
	cfg.Roots.ClaudeCode = t.TempDir()
	cfg.Roots.Codex = t.TempDir()
	cfg.Roots.Pi = t.TempDir()
	cfg.Roots.HookLog = t.TempDir()

	run := func(t *testing.T, noAgentTrace bool) string {
		t.Helper()
		outDir := filepath.Join(t.TempDir(), "pr-7")
		if _, err := scan.Run(context.Background(), scan.Options{
			Repo: "acme/shop", Number: 7, CWD: t.TempDir(), OutDir: outDir, Runner: r, Config: cfg,
			NoHooks: true, NoCommitPatches: true, NoAgentTrace: noAgentTrace,
			Now: time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC), Version: "0.0.0-test",
		}); err != nil {
			t.Fatalf("scan.Run: %v", err)
		}
		return outDir
	}

	outDir := run(t, false)
	raw, err := os.ReadFile(filepath.Join(outDir, agenttrace.FileName))
	if err != nil {
		t.Fatalf("scan.Run did not write %s: %v", agenttrace.FileName, err)
	}
	out := string(raw)
	if !strings.Contains(out, `"type": "unknown"`) {
		t.Errorf("a line no session explains is not reported as unknown:\n%s", out)
	}
	if !strings.Contains(out, `"revision": "aaaaaaa1"`) {
		t.Errorf("record carries no VCS revision:\n%s", out)
	}
	if strings.Contains(out, "sendWithRetry(url, body)") {
		t.Errorf("record leaks PR line text:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(run(t, true), agenttrace.FileName)); !os.IsNotExist(err) {
		t.Errorf("Options.NoAgentTrace still wrote %s (err = %v)", agenttrace.FileName, err)
	}
}
