package e2e

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/scan"
)

// update rewrites the golden files in testdata/golden. Update with
// `go test ./internal/e2e/... -run E2E -update`, never with `./...`.
var update = flag.Bool("update", false, "rewrite golden files")

// e2eOutputs are the reviewer-facing files the scenario writes. `exact` says
// whether the file is compared byte for byte; see e2eCompareGolden.
var e2eOutputs = []struct {
	name  string
	exact bool
}{
	{"signals.json", false},
	{"report.md", true},
	{"comment.md", true},
}

// TestE2E_GoldenScenario runs the whole product over one synthetic PR
// produced across all three harnesses and checks the golden outputs plus the
// signal-level assertions in docs/plan/tasks/T25-e2e-golden.md.
func TestE2E_GoldenScenario(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	res, err := scan.Run(context.Background(), scan.Options{
		Repo:    e2eRepo,
		Number:  e2eNumber,
		CWD:     w.repoDir,
		OutDir:  w.outDir,
		Runner:  w.runner,
		Config:  w.cfg,
		Now:     e2eAt(e2eNowHHMM),
		Version: e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: scan.Run: %v", err)
	}
	if res.Report == nil {
		t.Fatal("e2e: scan.Run returned a nil report")
	}

	t.Run("golden", func(t *testing.T) {
		for _, out := range e2eOutputs {
			got, err := os.ReadFile(filepath.Join(w.outDir, out.name))
			if err != nil {
				t.Fatalf("e2e: reading %s: %v", out.name, err)
			}
			e2eCompareGolden(t, out.name, e2eNormalize(w, got), out.exact)
		}
	})

	t.Run("assertions", func(t *testing.T) {
		e2eAssertScenario(t, w, res)
	})
}

// e2eAssertScenario implements every row of the T25 assertion table plus the
// two cross-cutting checks.
func e2eAssertScenario(t *testing.T, w *world, res *scan.Result) {
	rep := res.Report
	attr := e2eReadAttribution(t, w)

	// AUTH-2 — 3 sessions, 1 unattributed commit, state per ratio.
	auth2 := e2eSignal(t, rep, "AUTH-2")
	if got := rep.Coverage.Sessions; got != 3 {
		t.Errorf("AUTH-2: coverage sessions = %d, want 3 (%v)", got, rep.Coverage.Harnesses)
	}
	if got := len(rep.Coverage.UnattributedCommits); got != 1 {
		t.Errorf("AUTH-2: unattributed commits = %d (%v), want 1 (%s)",
			got, rep.Coverage.UnattributedCommits, e2eCommitSHAs[2])
	} else if rep.Coverage.UnattributedCommits[0] != e2eCommitSHAs[2] {
		t.Errorf("AUTH-2: unattributed commit = %s, want commit 3 %s",
			rep.Coverage.UnattributedCommits[0], e2eCommitSHAs[2])
	}
	if auth2.State == model.StateUnknown {
		t.Errorf("AUTH-2: state = unknown, want alert or info (%s)", auth2.Summary)
	}
	if want := e2eCoverageState(rep.Coverage.LinesTotal, rep.Coverage.Ratio); auth2.State != want {
		t.Errorf("AUTH-2: state = %q at %d lines / ratio %.2f, want %q (%s)",
			auth2.State, rep.Coverage.LinesTotal, rep.Coverage.Ratio, want, auth2.Summary)
	}

	// AUTH-1 — agent lines attributed, README.md uncaptured.
	auth1 := e2eSignal(t, rep, "AUTH-1")
	if got := e2eCountLabel(attr, model.LabelAgent); got == 0 {
		t.Errorf("AUTH-1: agent lines = 0, want > 0 (%s)", auth1.Summary)
	}
	readme := e2eFileAttribution(attr, "README.md")
	if readme == nil {
		t.Fatalf("AUTH-1: README.md missing from attribution (%v)", e2eAttrPaths(attr))
	}
	for _, l := range readme.Lines {
		if l.Label != model.LabelUncaptured {
			t.Errorf("AUTH-1: README.md line %d label = %q, want uncaptured", l.Line, l.Label)
		}
	}

	// INT-2 — one decision mentioning Postgres.
	int2 := e2eSignal(t, rep, "INT-2")
	if !e2eFindingMentions(int2, "Postgres") {
		t.Errorf("INT-2: no finding mentions the Postgres answer (%s, %d findings)", int2.Summary, len(int2.Findings))
	}

	// INT-3 — the plan was approved.
	int3 := e2eSignal(t, rep, "INT-3")
	if !e2eApproved(int3) {
		t.Errorf("INT-3: plan not reported as approved (state %q, summary %s, data %v)", int3.State, int3.Summary, int3.Data)
	}

	// VER-1 — the npm test group, last status pass.
	ver1 := e2eSignal(t, rep, "VER-1")
	if !e2eFindingMentions(ver1, "npm test") {
		t.Errorf("VER-1: no finding mentions `npm test` (%s)", ver1.Summary)
	}
	if !e2eLastStatusPass(ver1) {
		t.Errorf("VER-1: npm test group does not end in a pass (%v)", ver1.Data)
	}

	// VER-2 — an alert anchored in src/webhooks/retry.ts.
	ver2 := e2eSignal(t, rep, "VER-2")
	e2eRequireState(t, ver2, model.StateAlert)
	if !e2eHasAnchor(ver2, "src/webhooks/retry.ts") {
		t.Errorf("VER-2: no anchor in src/webhooks/retry.ts (%s)", ver2.Summary)
	}

	// VER-4 — an alert for the fail -> edit -> pass on the test file.
	ver4 := e2eSignal(t, rep, "VER-4")
	e2eRequireState(t, ver4, model.StateAlert)
	if !e2eHasAnchor(ver4, "test/webhook.test.ts") {
		t.Errorf("VER-4: no anchor in test/webhook.test.ts (%s)", ver4.Summary)
	}

	// DEC-1 — an alert anchored in the interrupted migration.
	dec1 := e2eSignal(t, rep, "DEC-1")
	e2eRequireState(t, dec1, model.StateAlert)
	if !e2eHasAnchor(dec1, "migrations/0042_retry.sql") {
		t.Errorf("DEC-1: no anchor in migrations/0042_retry.sql (%s)", dec1.Summary)
	}

	// DEC-2 — the abandoned Pi branch.
	dec2 := e2eSignal(t, rep, "DEC-2")
	if !e2eFindingMentions(dec2, "branch") && !e2eFindingMentions(dec2, "Discarded") {
		t.Errorf("DEC-2: no finding describes the abandoned Pi branch (%s, %d findings)", dec2.Summary, len(dec2.Findings))
	}

	// FRI-1 — src/webhooks/retry.ts is the hotspot.
	fri1 := e2eSignal(t, rep, "FRI-1")
	if !e2eFindingMentions(fri1, "src/webhooks/retry.ts") {
		t.Errorf("FRI-1: no finding names src/webhooks/retry.ts as a hotspot (%s)", fri1.Summary)
	}

	// FRI-3 — the 14:30 compaction, with src/metrics.ts edited after it.
	//
	// FRI-3 reports the resets and the number of PR files edited afterwards,
	// not their names, so the file is pinned from both ends: the compaction
	// finding itself, and the PR lines FRI-3 counts as written after a reset.
	// The only attributed PR lines written after the 14:30 compaction are the
	// two src/metrics.ts ones (the third post-reset line is the Pi branch
	// switch at 14:51, which is why the assertion below is a lower bound).
	fri3 := e2eSignal(t, rep, "FRI-3")
	e2eRequireState(t, fri3, model.StateInfo)
	if !e2eFindingMentions(fri3, "14:30") {
		t.Errorf("FRI-3: no finding reports the 14:30 compaction (%s)", fri3.Summary)
	}
	if !e2eFindingMentions(fri3, "compacted") {
		t.Errorf("FRI-3: the 14:30 finding does not describe a compaction (%s)", fri3.Summary)
	}
	if got := e2eIntData(fri3, "lines_after"); got < 2 {
		t.Errorf("FRI-3: lines_after = %d, want at least 2 (the src/metrics.ts lines written after the 14:30 compaction)", got)
	}

	// EXP-1 — the migration side effect.
	exp1 := e2eSignal(t, rep, "EXP-1")
	if !e2eFindingMentions(exp1, "migration") && !e2eFindingMentions(exp1, "0042_retry.sql") {
		t.Errorf("EXP-1: no finding reports the migration (%s)", exp1.Summary)
	}

	// EXP-2 — the agent-chosen dependency, and the package that does not exist.
	exp2 := e2eSignal(t, rep, "EXP-2")
	if !e2eFindingMentions(exp2, "p-retry") {
		t.Errorf("EXP-2: no finding mentions p-retry (%s)", exp2.Summary)
	}
	if !e2eFindingMentions(exp2, "express-retry-webhook") {
		t.Errorf("EXP-2: no finding mentions the failed express-retry-webhook install (%s)", exp2.Summary)
	}
	if !e2eFindingMentions(exp2, "404") && !e2eFindingMentions(exp2, "does not exist") {
		t.Errorf("EXP-2: express-retry-webhook not reported as non-existent (%s)", exp2.Summary)
	}

	// EXP-3 — gh issue view 312 -> .github/workflows/release.yml.
	exp3 := e2eSignal(t, rep, "EXP-3")
	e2eRequireState(t, exp3, model.StateAlert)
	if !e2eFindingMentions(exp3, "gh issue view 312") {
		t.Errorf("EXP-3: no finding names the untrusted read (%s)", exp3.Summary)
	}
	if !e2eHasAnchor(exp3, ".github/workflows/release.yml") && !e2eFindingMentions(exp3, ".github/workflows/release.yml") {
		t.Errorf("EXP-3: no finding names .github/workflows/release.yml (%s)", exp3.Summary)
	}

	// EXP-4 — the .env read.
	exp4 := e2eSignal(t, rep, "EXP-4")
	e2eRequireState(t, exp4, model.StateAlert)
	if !e2eFindingMentions(exp4, ".env") {
		t.Errorf("EXP-4: no finding names the .env read (%s)", exp4.Summary)
	}

	// OVS-1 — bypass for the hooked Claude Code session, approvals for Codex.
	ovs1 := e2eSignal(t, rep, "OVS-1")
	if !e2eFindingMentions(ovs1, "bypass") {
		t.Errorf("OVS-1: no finding reports the Claude Code bypass mode (%s)", ovs1.Summary)
	}
	if !e2eFindingMentions(ovs1, "never") && !e2eFindingMentions(ovs1, "approval") {
		t.Errorf("OVS-1: no finding reports the Codex approval policy (%s)", ovs1.Summary)
	}

	// OVS-3 — the subagent's lines in test/retry.test.ts.
	ovs3 := e2eSignal(t, rep, "OVS-3")
	if !e2eFindingMentions(ovs3, "test/retry.test.ts") && !e2eHasAnchor(ovs3, "test/retry.test.ts") {
		t.Errorf("OVS-3: no finding names the subagent's test/retry.test.ts (%s)", ovs3.Summary)
	}

	// CON-2 — the open "add jitter" task.
	con2 := e2eSignal(t, rep, "CON-2")
	if !e2eFindingMentions(con2, "add jitter") {
		t.Errorf("CON-2: no finding reports the open \"add jitter\" task (%s)", con2.Summary)
	}

	// DEC-3, CON-1, CON-3 — inferred signals, absent or unknown while the LLM
	// pass is off.
	for _, id := range []string{"DEC-3", "CON-1", "CON-3"} {
		sig := e2eFindSignal(rep, id)
		if sig == nil {
			continue
		}
		if sig.State != model.StateUnknown {
			t.Errorf("%s: state = %q, want unknown while the LLM pass is off (%s)", id, sig.State, sig.Summary)
		}
	}

	e2eAssertComment(t, w)
	e2eAssertNoSecret(t, w)
}

// e2eCoverageState is the AUTH-2 state internal/detect/authorship derives from
// a coverage ratio: a large PR that sessions explain less than half of is an
// alert, everything else is info.
func e2eCoverageState(total int, ratio float64) model.State {
	if total >= 10 && ratio < 0.5 {
		return model.StateAlert
	}
	return model.StateInfo
}

// e2eAssertComment checks the PR comment budget and that no prompt text
// leaked into it.
func e2eAssertComment(t *testing.T, w *world) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w.outDir, "comment.md"))
	if err != nil {
		t.Fatalf("e2e: reading comment.md: %v", err)
	}
	body := string(raw)

	bullets := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			bullets++
		}
	}
	if bullets > 5 {
		t.Errorf("comment.md: %d bullet lines, want at most 5:\n%s", bullets, body)
	}
	if bullets == 0 {
		t.Errorf("comment.md: no bullet lines, want the alert summary:\n%s", body)
	}

	// The prompts in this scenario, and fragments of them that must not reach
	// a reviewer-facing comment.
	for _, prompt := range []string{
		"Add retry with backoff to the webhook sender",
		"Don't touch the queue",
		"Make the webhook sender idempotent",
		"Add an idempotency key header to the webhook sender",
	} {
		if strings.Contains(body, prompt) {
			t.Errorf("comment.md contains prompt text %q:\n%s", prompt, body)
		}
	}
}

// e2eAssertNoSecret checks the raw .env value planted in the Claude Code
// transcript never reaches a rendered output file.
//
// The verbatim session copy under <outDir>/sessions/ is deliberately exempt:
// .paircli/pr-<n>/sessions/<harness>-<id>.json is the transcript itself, one
// click deep and gitignored by design (docs/plan/CONTRACTS.md "Output files",
// docs/SIGNALS.md "Deferred"), and redaction is an explicit no-op seam, so the
// harness output it replays is expected to be byte-identical. Every file a
// reviewer or a PR comment actually reads must be clean, so the value is
// checked there in full and the exemption is asserted to be the only one.
func e2eAssertNoSecret(t *testing.T, w *world) {
	t.Helper()
	sessDir := filepath.Join(w.outDir, "sessions")
	leaked, transcriptCopies := 0, 0

	err := filepath.WalkDir(w.outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(raw), e2eEnvValue) {
			return nil
		}
		rel, _ := filepath.Rel(w.outDir, path)
		if strings.HasPrefix(path, sessDir+string(filepath.Separator)) {
			transcriptCopies++
			return nil
		}
		leaked++
		t.Errorf("e2e: the raw .env value appears in the output file %s", rel)
		return nil
	})
	if err != nil {
		t.Fatalf("e2e: walking output dir: %v", err)
	}
	if leaked == 0 {
		t.Logf("e2e: raw .env value confined to %d transcript copy/copies under sessions/", transcriptCopies)
	}
}

// e2eCompareGolden compares got with testdata/golden/name, or rewrites it when
// -update is set.
//
// exact files are compared, and stored, byte for byte. signals.json goes
// through e2eCanonicalJSON on both sides instead: one payload it carries is
// not stable, so the golden is stored in canonical form and -update stays
// idempotent. Everything else in the file is still compared exactly.
func e2eCompareGolden(t *testing.T, name, got string, exact bool) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if !exact {
		got = e2eCanonicalJSON(t, got)
	}
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("e2e: mkdir testdata/golden: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("e2e: writing golden %s: %v", name, err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("e2e: reading golden %s: %v (run `go test ./internal/e2e/... -run E2E -update`)", name, err)
	}
	if want := string(raw); got != want {
		t.Errorf("e2e: golden %s differs:\n--- golden ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// e2eCanonicalJSON re-marshals a JSON document with every "files" array under
// a signal's Data sorted by path, so the comparison does not depend on the
// order the encoder happened to walk a Go map.
//
// One signal payload is genuinely unordered today: internal/detect/friction
// builds FRI-1's Data["files"] by ranging over a map and sorts it only by edit
// count, so files with the same count come out in a different order on every
// run. That is a determinism defect against docs/plan/README.md
// ("Determinism"), it lives outside internal/e2e, and fixing it there is a
// one-line change (add a path tie-break to the dataFiles sort in
// internal/detect/friction/fri1.go). Until then this keeps the golden
// comparison about content rather than incidental ordering.
func e2eCanonicalJSON(t *testing.T, raw string) string {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("e2e: parsing output for comparison: %v", err)
	}
	e2eSortFilesArrays(doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("e2e: re-encoding output for comparison: %v", err)
	}
	return string(out) + "\n"
}

// e2eSortFilesArrays sorts every "files" array of objects by their path, in
// place and recursively.
func e2eSortFilesArrays(v any) {
	switch node := v.(type) {
	case map[string]any:
		if arr, ok := node["files"].([]any); ok {
			sort.SliceStable(arr, func(i, j int) bool {
				return e2eNodePath(arr[i]) < e2eNodePath(arr[j])
			})
		}
		for _, child := range node {
			e2eSortFilesArrays(child)
		}
	case []any:
		for _, child := range node {
			e2eSortFilesArrays(child)
		}
	}
}

// e2eNodePath returns the "path" field of a decoded JSON object, or "".
func e2eNodePath(v any) string {
	if m, ok := v.(map[string]any); ok {
		if p, ok := m["path"].(string); ok {
			return p
		}
	}
	return ""
}

// e2eNormalize rewrites the run's temp directories to fixed placeholders so
// the golden files are reproducible on any machine.
func e2eNormalize(w *world, raw []byte) string {
	out := string(raw)
	// The session directories are named after the cwd slug, which replaces
	// path separators, so the raw repo path never appears there: replace the
	// slug before the path it came from.
	for _, sub := range []struct{ path, placeholder string }{
		{e2eSlug(w.repoDir), "{repo}"},
		{w.repoDir, "{repo}"},
		{w.root, "{root}"},
	} {
		out = strings.ReplaceAll(out, sub.path, sub.placeholder)
	}
	return out
}

// e2eReadAttribution loads the authorship.json the scan wrote.
func e2eReadAttribution(t *testing.T, w *world) *model.Attribution {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w.outDir, "authorship.json"))
	if err != nil {
		t.Fatalf("e2e: reading authorship.json: %v", err)
	}
	var attr model.Attribution
	if err := json.Unmarshal(raw, &attr); err != nil {
		t.Fatalf("e2e: parsing authorship.json: %v", err)
	}
	return &attr
}

// e2eFileAttribution returns the attribution for one file, or nil.
func e2eFileAttribution(attr *model.Attribution, path string) *model.FileAttribution {
	for i := range attr.Files {
		if attr.Files[i].Path == path {
			return &attr.Files[i]
		}
	}
	return nil
}

// e2eAttrPaths lists the files attribution covers.
func e2eAttrPaths(attr *model.Attribution) []string {
	var out []string
	for _, f := range attr.Files {
		out = append(out, f.Path)
	}
	return out
}

// e2eCountLabel counts lines carrying one label across every file.
func e2eCountLabel(attr *model.Attribution, label model.LineLabel) int {
	n := 0
	for _, f := range attr.Files {
		for _, l := range f.Lines {
			if l.Label == label {
				n++
			}
		}
	}
	return n
}

// e2eFindSignal returns the signal with id, or nil.
func e2eFindSignal(rep *model.Report, id string) *model.Signal {
	for i := range rep.Signals {
		if rep.Signals[i].ID == id {
			return &rep.Signals[i]
		}
	}
	return nil
}

// e2eApproved reports whether an INT-3 signal says a plan was approved.
func e2eApproved(sig model.Signal) bool {
	if v, ok := sig.Data["approved"]; ok {
		if b, ok := v.(bool); ok && b {
			return true
		}
	}
	if v, ok := sig.Data["plans_approved"]; ok {
		if n, ok := v.(float64); ok && n > 0 {
			return true
		}
	}
	for _, f := range sig.Findings {
		if strings.Contains(f.Summary, "approv") {
			return true
		}
	}
	return false
}

// e2eLastStatusPass reports whether a VER-1 signal says its last check passed.
func e2eLastStatusPass(sig model.Signal) bool {
	if v, ok := sig.Data["last_status"]; ok && strings.EqualFold(strings.TrimSpace(toString(v)), "pass") {
		return true
	}
	if v, ok := sig.Data["status"]; ok && strings.EqualFold(strings.TrimSpace(toString(v)), "pass") {
		return true
	}
	for _, f := range sig.Findings {
		if strings.Contains(f.Summary, "pass") && strings.Contains(f.Summary, "npm test") {
			return true
		}
	}
	return false
}

// e2eIntData reads an integer from a signal's Data payload, 0 when absent.
func e2eIntData(sig model.Signal, key string) int {
	v, ok := sig.Data[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// toString renders a decoded JSON scalar.
func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}
