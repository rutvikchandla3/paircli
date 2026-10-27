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
