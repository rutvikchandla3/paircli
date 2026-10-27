package publish

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/capture"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

var bundleNow = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func bundleToken() string { return "ghp_" + strings.Repeat("aB3", 12) }

// scanFolder writes a scan output folder the way paircli scan lays it out.
func scanFolder(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".paircli", "pr-42", testHead)
	files := map[string]string{
		"report.md":                    "# report\n",
		"comment.md":                   "comment\n",
		"signals.json":                 `{"signals":[]}` + "\n",
		"authorship.json":              "{}\n",
		"snapshot.json":                `{"cmd":"export TOKEN=` + bundleToken() + `"}` + "\n",
		"agent-trace.json":             "{}\n",
		"sessions/claude-code-s1.json": `{"id":"s1"}` + "\n",
		"sessions/codex-s2.json":       `{"id":"s2"}` + "\n",
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func buildOptions(t *testing.T, scanDir string) BundleOptions {
	t.Helper()
	return BundleOptions{
		Repo: "acme/repo", Number: 42, Head: testHead, ScanDir: scanDir,
		Redactor: redact.NewWith(nil, ""), ScanCounts: map[string]int{"github_token": 1},
		Version: "0.3.0", Now: bundleNow, OutDir: t.TempDir(),
	}
}

func readZip(t *testing.T, path string) (names []string, files map[string][]byte, entries []*zip.File) {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	files = map[string][]byte{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		names = append(names, f.Name)
		files[f.Name] = content
		entries = append(entries, f)
	}
	return names, files, entries
}

func TestBuildBundleLayoutAndBundleJSON(t *testing.T) {
	b, err := BuildBundle(buildOptions(t, scanFolder(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(b.Path) != testHead+".zip" {
		t.Errorf("name = %s", filepath.Base(b.Path))
	}
	names, files, entries := readZip(t, b.Path)
	prefix := ".paircli/pr-42/" + testHead + "/"
	want := []string{
		prefix + "agent-trace.json", prefix + "authorship.json", prefix + "comment.md", prefix + "report.md",
		prefix + "sessions/claude-code-s1.json", prefix + "sessions/codex-s2.json", prefix + "signals.json", prefix + "snapshot.json",
		"bundle.json",
	}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(names, "\n"), strings.Join(want, "\n"))
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("entries are not sorted: %v", names)
	}
	for _, e := range entries {
		if !e.Modified.Equal(zipTime) || e.Mode().Perm() != 0o644 || e.Method != zip.Deflate {
			t.Errorf("%s: modified %v mode %v method %d", e.Name, e.Modified, e.Mode(), e.Method)
		}
	}

	var meta map[string]any
	if err := json.Unmarshal(files["bundle.json"], &meta); err != nil {
		t.Fatal(err)
	}
	redaction := meta["redaction"].(map[string]any)
	counts := redaction["counts"].(map[string]any)
	if meta["schema_version"] != float64(1) || meta["created_by"] != "paircli 0.3.0" || meta["repo"] != "acme/repo" ||
		meta["pull_number"] != float64(42) || meta["head_sha"] != testHead || meta["scan_dir"] != ".paircli/pr-42/"+testHead ||
		meta["created_at"] != "2026-10-11T12:00:00Z" || meta["raw_sessions"] != false ||
		redaction["applied"] != true || redaction["rules_version"] != "1" {
		t.Errorf("bundle.json = %s", files["bundle.json"])
	}
	// One redaction from the scan's rendered text, one found in snapshot.json here.
	if counts["github_token"] != float64(2) {
		t.Errorf("counts = %v, want github_token 2", counts)
	}
	snapshot := string(files[prefix+"snapshot.json"])
	if strings.Contains(snapshot, bundleToken()) || !strings.Contains(snapshot, "[REDACTED:github_token]") {
		t.Errorf("snapshot.json was not redacted: %s", snapshot)
	}
	if b.RawIncluded || b.SHA256 == "" || b.Size <= 0 {
		t.Errorf("bundle = %+v", b)
	}
	raw, _ := os.ReadFile(b.Path)
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != b.SHA256 || int64(len(raw)) != b.Size {
		t.Error("Bundle.SHA256/Size do not describe the file")
	}
}

func TestBuildBundleIsDeterministicAcrossFileTimestamps(t *testing.T) {
	dir := scanFolder(t, nil)
	first, err := BuildBundle(buildOptions(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := os.ReadFile(first.Path)

	old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil {
			os.Chtimes(path, old, old)
		}
		return nil
	})
	second, err := BuildBundle(buildOptions(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, _ := os.ReadFile(second.Path)
	if !bytes.Equal(firstBytes, secondBytes) || first.SHA256 != second.SHA256 {
		t.Error("equal inputs produced different zip bytes")
	}
}

func TestBuildBundleOnlyIncludesScanOutput(t *testing.T) {
	dir := scanFolder(t, map[string]string{
		"summary.md":                      "model-written\n",
		"generated_artifacts/overview.md": "model-written\n",
		"diagnostics/raw.txt":             "raw model output\n",
		"sessions/notes.txt":              "not json\n",
		"../" + testHead + ".zip":         "stale zip sibling",
		"sessions/nested/deeper.json":     "{}",
	})
	b, err := BuildBundle(buildOptions(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range b.Entries {
		for _, banned := range []string{"summary.md", "generated_artifacts", "diagnostics", "notes.txt", ".zip", "deeper.json"} {
			if strings.Contains(name, banned) {
				t.Errorf("bundle contains %s", name)
			}
		}
	}
}

func TestBuildBundleRejectsSymlinksAndEmptyFolders(t *testing.T) {
	dir := scanFolder(t, nil)
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("leak"), 0o600)
	os.Remove(filepath.Join(dir, "report.md"))
	if err := os.Symlink(secret, filepath.Join(dir, "report.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := BuildBundle(buildOptions(t, dir)); err == nil {
		t.Error("a symlinked report was bundled")
	}
	if _, err := BuildBundle(buildOptions(t, t.TempDir())); err == nil {
		t.Error("an empty scan folder was bundled")
	}
}

func TestBuildBundleIncludesRawSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	transcripts := t.TempDir()
	transcript := filepath.Join(transcripts, "s1.jsonl")
	os.WriteFile(transcript, []byte(`{"sessionId":"s1","text":"`+bundleToken()+`"}`+"\n"), 0o644)
	capRed := redact.NewWith(nil, "")
	staged, err := capture.Stage(capture.Options{
		Head: testHead, Scope: "s", Redactor: capRed,
		Sessions: []*model.Session{{Harness: model.HarnessClaudeCode, ID: "s1", SourcePath: transcript, CWD: "/w"}},
	})
	if err != nil || staged.Empty() {
		t.Fatalf("Stage: %v (empty=%v)", err, staged.Empty())
	}

	o := buildOptions(t, scanFolder(t, nil))
	o.Raw, o.CaptureCounts = staged, capRed.Counts()
	b, err := BuildBundle(o)
	if err != nil {
		t.Fatal(err)
	}
	_, files, _ := readZip(t, b.Path)
	if !b.RawIncluded {
		t.Fatal("raw sessions were not included")
	}
	manifest := files["raw-sessions/manifest.json"]
	if !bytes.Contains(manifest, []byte(`"head_sha": "`+testHead+`"`)) {
		t.Errorf("manifest = %s", manifest)
	}
	raw := files["raw-sessions/claude-code/s1/s1.jsonl"]
	if len(raw) == 0 || bytes.Contains(raw, []byte(bundleToken())) {
		t.Errorf("raw transcript = %q", raw)
	}
	var meta struct {
		RawSessions bool `json:"raw_sessions"`
		Redaction   struct {
			Counts map[string]int `json:"counts"`
		} `json:"redaction"`
	}
	json.Unmarshal(files["bundle.json"], &meta)
	if !meta.RawSessions || meta.Redaction.Counts["github_token"] != 3 {
		t.Errorf("bundle.json = %s (want raw_sessions and github_token 1+1+1)", files["bundle.json"])
	}
}

func TestBuildBundleWithoutCaptureHasNoRawSessions(t *testing.T) {
	o := buildOptions(t, scanFolder(t, nil))
	o.Raw = &capture.Staged{}
	b, err := BuildBundle(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range b.Entries {
		if strings.HasPrefix(name, "raw-sessions/") {
			t.Errorf("unexpected %s", name)
		}
	}
}

// changedManifest is a manifest naming one written side and one skipped one,
// the shape internal/filecontent produces.
func changedManifest(t *testing.T, withFile bool) string {
	t.Helper()
	files := []map[string]any{{
		"path":   "src/app.go",
		"status": "added",
		"sides":  map[string]any{},
		"skipped": map[string]any{
			"new": map[string]any{"reason": "media"},
		},
	}}
	if withFile {
		files[0]["sides"] = map[string]any{"new": map[string]any{
			"file": "new/src/app.go", "sha256": strings.Repeat("0", 64), "bytes": 20,
			"lines": 1, "partial": false, "ranges": [][2]int{}, "full_lines": 1,
			"full_bytes": 20, "source": "git",
		}}
	}
	m := map[string]any{
		"schema_version": 1, "tool": "paircli test", "created_at": "2026-10-11T12:00:00Z",
		"repo": "acme/repo", "pull_number": 42, "head_sha": testHead, "base_sha": "",
		"base_ref": "main", "base_kind": "",
		"redaction": map[string]any{"rules_version": "1"},
		"limits":    map[string]any{},
		"totals":    map[string]any{"files": 1, "sides_written": 1},
		"files":     files,
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// changedScanFolder is a scan folder that also holds the changed-files subtree.
func changedScanFolder(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"changed-files/manifest.json":  changedManifest(t, true),
		"changed-files/new/src/app.go": "package app\n\nvar token = \"" + bundleToken() + "\"\n",
	}
	for name, content := range extra {
		files[name] = content
	}
	return scanFolder(t, files)
}

func TestBuildBundleIncludesChangedFiles(t *testing.T) {
	o := buildOptions(t, changedScanFolder(t, nil))
	o.ChangedFiles = true
	b, err := BuildBundle(o)
	if err != nil {
		t.Fatal(err)
	}
	_, files, _ := readZip(t, b.Path)

	prefix := ".paircli/pr-42/" + testHead + "/changed-files/"
	manifest, ok := files[prefix+"manifest.json"]
	if !ok {
		t.Fatalf("no manifest in the bundle: %v", b.Entries)
	}
	if !bytes.Contains(manifest, []byte(`"reason": "media"`)) {
		t.Errorf("manifest = %s", manifest)
	}
	body, ok := files[prefix+"new/src/app.go"]
	if !ok {
		t.Fatalf("no content in the bundle: %v", b.Entries)
	}
	// Content is redacted on the way in, like every other scan file.
	if bytes.Contains(body, []byte(bundleToken())) {
		t.Errorf("the content was not redacted: %s", body)
	}
	if !bytes.Contains(body, []byte("[REDACTED:github_token]")) {
		t.Errorf("content = %s", body)
	}
	// The entry list stays sorted.
	names := b.Entries
	if !sort.StringsAreSorted(names) {
		t.Errorf("entries are not sorted: %v", names)
	}
	// The content's redactions are part of the bundle's summary.
	if b.Counts["github_token"] < 1 {
		t.Errorf("counts = %v, want the content's redaction counted", b.Counts)
	}
}

func TestBuildBundleWithoutTheOptionLeavesChangedFilesOut(t *testing.T) {
	// The subtree is there, the option says no: nothing from it is uploaded.
	o := buildOptions(t, changedScanFolder(t, nil))
	b, err := BuildBundle(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range b.Entries {
		if strings.Contains(name, "changed-files") {
			t.Errorf("bundle contains %s", name)
		}
	}
}

func TestBuildBundleOnlyUploadsWhatTheManifestNames(t *testing.T) {
	dir := changedScanFolder(t, map[string]string{
		"changed-files/new/src/unlisted.go": "model-written\n",
		"changed-files/notes.txt":           "not named anywhere\n",
		"changed-files/old/src/also-not.go": "not named anywhere\n",
	})
	o := buildOptions(t, dir)
	o.ChangedFiles = true
	b, err := BuildBundle(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range b.Entries {
		for _, banned := range []string{"unlisted.go", "notes.txt", "also-not.go"} {
			if strings.Contains(name, banned) {
				t.Errorf("bundle contains %s", name)
			}
		}
	}
}

func TestBuildBundleRejectsAMissingOrBrokenManifest(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		o := buildOptions(t, scanFolder(t, nil))
		o.ChangedFiles = true
		if _, err := BuildBundle(o); err == nil {
			t.Error("a scan asked for contents and has no manifest, and the bundle was built anyway")
		}
	})
	t.Run("malformed", func(t *testing.T) {
		o := buildOptions(t, scanFolder(t, map[string]string{"changed-files/manifest.json": "{not json"}))
		o.ChangedFiles = true
		if _, err := BuildBundle(o); err == nil {
			t.Error("a malformed manifest was accepted")
		}
	})
	t.Run("a named file that is absent", func(t *testing.T) {
		o := buildOptions(t, scanFolder(t, map[string]string{
			"changed-files/manifest.json": changedManifest(t, true),
		}))
		o.ChangedFiles = true
		if _, err := BuildBundle(o); err == nil {
			t.Error("the manifest named a file that is not there, and the bundle was built anyway")
		}
	})
}

// TestBuildBundleOrdersContentBeforeRawSessions is what makes the size
// fallback safe: BuildBundle retries with the raw sessions left out, so
// anything already in the entry list before them survives. Content is built
// first, so an oversized transcript can never cost a bundle its code.
func TestBuildBundleOrdersContentBeforeRawSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	transcripts := t.TempDir()
	transcript := filepath.Join(transcripts, "s1.jsonl")
	os.WriteFile(transcript, []byte(`{"sessionId":"s1"}`+"\n"), 0o644)
	capRed := redact.NewWith(nil, "")
	staged, err := capture.Stage(capture.Options{
		Head: testHead, Scope: "s", Redactor: capRed,
		Sessions: []*model.Session{{Harness: model.HarnessClaudeCode, ID: "s1", SourcePath: transcript, CWD: "/w"}},
	})
	if err != nil || staged.Empty() {
		t.Fatalf("Stage: %v (empty=%v)", err, staged.Empty())
	}

	o := buildOptions(t, changedScanFolder(t, nil))
	o.ChangedFiles = true
	o.Raw, o.CaptureCounts = staged, capRed.Counts()
	b, err := BuildBundle(o)
	if err != nil {
		t.Fatal(err)
	}
	if !b.RawIncluded {
		t.Fatal("the raw session should be here")
	}
	var lastContent, firstRaw = -1, len(b.Entries)
	for i, name := range b.Entries {
		if strings.Contains(name, "changed-files/") {
			lastContent = i
		}
		if strings.HasPrefix(name, "raw-sessions/") && i < firstRaw {
			firstRaw = i
		}
	}
	if lastContent < 0 {
		t.Fatalf("no content in the bundle: %v", b.Entries)
	}
	if lastContent > firstRaw {
		t.Errorf("content is interleaved with the raw sessions, so the fallback could drop it: %v", b.Entries)
	}
}

// TestBuildBundleRejectsATamperedManifestName covers the one place a name from
// a manifest becomes a filesystem path. The manifest lives in the user's own
// scan folder, so this is defence in depth rather than a live hole — but the
// name is joined to a path, so it is checked rather than trusted.
func TestBuildBundleRejectsATamperedManifestName(t *testing.T) {
	for _, bad := range []string{"../../../../etc/passwd", "/etc/passwd", `new\win.go`} {
		t.Run(bad, func(t *testing.T) {
			manifest := strings.Replace(changedManifest(t, true), "new/src/app.go", bad, 1)
			o := buildOptions(t, scanFolder(t, map[string]string{"changed-files/manifest.json": manifest}))
			o.ChangedFiles = true
			if _, err := BuildBundle(o); err == nil {
				t.Errorf("a manifest naming %q was accepted", bad)
			}
		})
	}
}

// The scan's snapshot.json holds source text with JSON escapes next to
// secret-looking names (`ExecutionToken: \u0026token`). It must still parse
// after redaction, or the pipeline cannot read the snapshot at all.
func TestBuildBundleKeepsSnapshotValidJSON(t *testing.T) {
	snapshot := `{"lines":["models.Job{ExecutionToken: \u0026token, Lease: \u0026live}",` +
		`"TOKEN=` + bundleToken() + `","x\tapi_key:\tabcdefgh12345678"]}` + "\n"
	b, err := BuildBundle(buildOptions(t, scanFolder(t, map[string]string{"snapshot.json": snapshot})))
	if err != nil {
		t.Fatal(err)
	}
	_, files, _ := readZip(t, b.Path)
	got := files[".paircli/pr-42/"+testHead+"/snapshot.json"]
	if !json.Valid(got) {
		t.Fatalf("snapshot.json is not valid JSON after redaction: %s", got)
	}
	if strings.Contains(string(got), bundleToken()) {
		t.Errorf("token was not redacted: %s", got)
	}
}

func TestRedactScanFileRefusesToBreakJSON(t *testing.T) {
	breaks := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"x"`), []byte(`\[REDACTED]`)) }
	keeps := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"x"`), []byte(`"[REDACTED]"`)) }
	in := []byte(`{"a":"x","b":"x"}`)

	if _, err := redactScanFile("snapshot.json", in, breaks); err == nil || !strings.Contains(err.Error(), "snapshot.json") {
		t.Errorf("breaking JSON: err = %v, want one naming snapshot.json", err)
	}
	if out, err := redactScanFile("snapshot.json", in, keeps); err != nil || !json.Valid(out) {
		t.Errorf("valid result: out %q err %v", out, err)
	}
	// Markdown is not parsed, and a file that was never valid JSON is not the redactor's doing.
	if _, err := redactScanFile("report.md", in, breaks); err != nil {
		t.Errorf("report.md: %v", err)
	}
	if _, err := redactScanFile("signals.json", []byte(`not json "x"`), breaks); err != nil {
		t.Errorf("already-invalid input: %v", err)
	}
}
