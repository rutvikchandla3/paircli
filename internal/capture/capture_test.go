package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	scope = "https://pair.example/acme__shop__7"
)

// fixture isolates the state dir and returns a fresh transcript directory.
func fixture(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return t.TempDir()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func claudeSession(id, path string) *model.Session {
	return &model.Session{Harness: model.HarnessClaudeCode, ID: id, SourcePath: path, CWD: "/work/shop"}
}

func stage(t *testing.T, head string, max int64, sessions ...*model.Session) *Staged {
	t.Helper()
	s, err := Stage(Options{Head: head, Scope: scope, Sessions: sessions, Redactor: redact.NewWith(nil, ""), MaxBytes: max})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	return s
}

func commit(t *testing.T, s *Staged) {
	t.Helper()
	if err := s.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// onlyFile returns the single staged file or fails.
func onlyFile(t *testing.T, s *Staged) (ManifestFile, File) {
	t.Helper()
	if len(s.Files) != 1 || len(s.Manifest.Sessions) != 1 || len(s.Manifest.Sessions[0].Files) != 1 {
		t.Fatalf("want exactly one staged file, got files=%d manifest=%+v notes=%v", len(s.Files), s.Manifest, s.Notes)
	}
	return s.Manifest.Sessions[0].Files[0], s.Files[0]
}

func TestStageCapturesFileAndDescribesRedactedBytes(t *testing.T) {
	dir := fixture(t)
	token := "ghp_" + strings.Repeat("aB3", 12)
	path := filepath.Join(dir, "sess1.jsonl")
	writeFile(t, path, `{"sessionId":"sess1","text":"hello"}`+"\n"+`{"sessionId":"sess1","text":"`+token+`"}`+"\n")

	s := stage(t, headA, 0, claudeSession("sess1", path))
	mf, f := onlyFile(t, s)

	if mf.Path != "claude-code/sess1/sess1.jsonl" || f.Path != mf.Path {
		t.Errorf("path = %q / %q", mf.Path, f.Path)
	}
	if strings.Contains(string(f.Data), token) || !strings.Contains(string(f.Data), "[REDACTED:github_token]") {
		t.Errorf("data not redacted: %s", f.Data)
	}
	sum := sha256.Sum256(f.Data)
	if mf.SHA256 != hex.EncodeToString(sum[:]) || mf.Bytes != len(f.Data) {
		t.Errorf("manifest describes %s/%d, data is %x/%d", mf.SHA256, mf.Bytes, sum, len(f.Data))
	}
	raw, _ := os.ReadFile(path)
	if mf.StartOffset != 0 || mf.EndOffset != int64(len(raw)) || mf.Kind != KindMain {
		t.Errorf("range/kind = %d..%d %s, want 0..%d main", mf.StartOffset, mf.EndOffset, mf.Kind, len(raw))
	}
	if s.Manifest.HeadSHA != headA || s.Manifest.SchemaVersion != 1 || s.Manifest.Sessions[0].CWD != "/work/shop" {
		t.Errorf("manifest = %+v", s.Manifest)
	}
	if _, err := s.ManifestJSON(); err != nil {
		t.Errorf("ManifestJSON: %v", err)
	}
}

func TestCursorsAdvanceOnlyOnCommit(t *testing.T) {
	dir := fixture(t)
	path := filepath.Join(dir, "sess1.jsonl")
	writeFile(t, path, `{"sessionId":"sess1","n":1}`+"\n")
	sess := claudeSession("sess1", path)

	first := stage(t, headA, 0, sess)
	firstFile, _ := onlyFile(t, first)

	// Without a Commit the same range is captured again.
	again := stage(t, headA, 0, sess)
	againFile, _ := onlyFile(t, again)
	if againFile.StartOffset != 0 || againFile.EndOffset != firstFile.EndOffset {
		t.Fatalf("uncommitted stage moved the cursor: %+v", againFile)
	}

	commit(t, first)
	appendFile(t, path, `{"sessionId":"sess1","n":2}`+"\n")

	next := stage(t, headB, 0, sess)
	mf, f := onlyFile(t, next)
	if mf.StartOffset != firstFile.EndOffset {
		t.Errorf("start = %d, want %d (only the new range)", mf.StartOffset, firstFile.EndOffset)
	}
	if string(f.Data) != `{"sessionId":"sess1","n":2}`+"\n" {
		t.Errorf("data = %q", f.Data)
	}
	commit(t, next)

	// The head just committed replays its range, so it is not empty.
	if replay := stage(t, headB, 0, sess); replay.Empty() {
		t.Error("republishing the committed head must repeat its range")
	}
	if fresh := stage(t, strings.Repeat("c", 40), 0, sess); !fresh.Empty() {
		t.Errorf("a new head with no new bytes captured %d files", len(fresh.Files))
	}
}

func TestSameHeadRepublishRepeatsItsRange(t *testing.T) {
	dir := fixture(t)
	path := filepath.Join(dir, "sess1.jsonl")
	writeFile(t, path, `{"sessionId":"sess1","n":1}`+"\n")
	sess := claudeSession("sess1", path)
	commit(t, stage(t, headA, 0, sess))
	appendFile(t, path, `{"sessionId":"sess1","n":2}`+"\n")
	second := stage(t, headB, 0, sess)
	secondFile, _ := onlyFile(t, second)
	commit(t, second)
	appendFile(t, path, `{"sessionId":"sess1","n":3}`+"\n")

	replay := stage(t, headB, 0, sess)
	mf, f := onlyFile(t, replay)
	if mf.StartOffset != secondFile.StartOffset {
		t.Errorf("replay start = %d, want %d", mf.StartOffset, secondFile.StartOffset)
	}
	if string(f.Data) != `{"sessionId":"sess1","n":2}`+"\n"+`{"sessionId":"sess1","n":3}`+"\n" {
		t.Errorf("replay data = %q", f.Data)
	}
}

func TestPartialTrailingLineWaitsForItsNewline(t *testing.T) {
	dir := fixture(t)
	path := filepath.Join(dir, "sess1.jsonl")
	writeFile(t, path, `{"sessionId":"sess1","n":1}`+"\n"+`{"sessionId":"sess1","n":`)
	sess := claudeSession("sess1", path)

	first := stage(t, headA, 0, sess)
	_, f := onlyFile(t, first)
	if string(f.Data) != `{"sessionId":"sess1","n":1}`+"\n" {
		t.Fatalf("data = %q, want only the complete line", f.Data)
	}
	commit(t, first)

	appendFile(t, path, `2}`+"\n")
	next := stage(t, headB, 0, sess)
	_, f = onlyFile(t, next)
	if string(f.Data) != `{"sessionId":"sess1","n":2}`+"\n" {
		t.Errorf("completed line = %q", f.Data)
	}
}

func TestTruncatedOrRewrittenFileRestartsFromZero(t *testing.T) {
	for name, rewrite := range map[string]func(path string){
		"truncated": func(path string) { writeFile(t, path, "{\"sessionId\":\"sess1\"}\n") },
		"rewritten same size": func(path string) {
			raw, _ := os.ReadFile(path)
			writeFile(t, path, strings.ToUpper(string(raw)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := fixture(t)
			path := filepath.Join(dir, "sess1.jsonl")
			writeFile(t, path, `{"sessionId":"sess1","a":"first"}`+"\n"+`{"sessionId":"sess1","a":"second"}`+"\n")
			sess := claudeSession("sess1", path)
			commit(t, stage(t, headA, 0, sess))

			rewrite(path)
			s := stage(t, headB, 0, sess)
			mf, _ := onlyFile(t, s)
			if mf.StartOffset != 0 {
				t.Errorf("start = %d, want 0 after rotation", mf.StartOffset)
			}
			if len(s.Notes) == 0 {
				t.Error("a reset must leave a note")
			}
		})
	}
}

func TestResumedFilesAndSubagentsAreCaptured(t *testing.T) {
	dir := fixture(t)
	main := filepath.Join(dir, "sess1.jsonl")
	writeFile(t, main, `{"sessionId":"sess1","n":1}`+"\n")
	writeFile(t, filepath.Join(dir, "resumed.jsonl"), `{"type":"summary"}`+"\n"+`{"sessionId":"sess1","n":2}`+"\n")
	writeFile(t, filepath.Join(dir, "unrelated.jsonl"), `{"sessionId":"other","n":3}`+"\n")
	writeFile(t, filepath.Join(dir, "sess1", "subagents", "agent-a1.jsonl"), `{"sessionId":"sess1","agent":1}`+"\n")
	writeFile(t, filepath.Join(dir, "sess1", "subagents", "agent-a1.meta.json"), `{}`)

	s := stage(t, headA, 0, claudeSession("sess1", main))
	if len(s.Manifest.Sessions) != 1 {
		t.Fatalf("sessions = %+v", s.Manifest.Sessions)
	}
	got := map[string]string{}
	for _, f := range s.Manifest.Sessions[0].Files {
		got[f.Path] = f.Kind
	}
	want := map[string]string{
		"claude-code/sess1/sess1.jsonl":    KindMain,
		"claude-code/sess1/resumed.jsonl":  KindMain,
		"claude-code/sess1/agent-a1.jsonl": KindSubagent,
	}
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for p, kind := range want {
		if got[p] != kind {
			t.Errorf("%s kind = %q, want %q (all: %v)", p, got[p], kind, got)
		}
	}
	if order := s.Manifest.Sessions[0].Files; order[0].Kind != KindMain || order[len(order)-1].Kind != KindSubagent {
		t.Errorf("files must be listed main first, subagents last: %+v", order)
	}
}

func TestCodexRolloutAndSymlinkHandling(t *testing.T) {
	dir := fixture(t)
	rollout := filepath.Join(dir, "rollout-2026-10-01-abc.jsonl")
	writeFile(t, rollout, `{"type":"session_meta"}`+"\n")
	link := filepath.Join(dir, "linked.jsonl")
	if err := os.Symlink(rollout, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	codex := &model.Session{Harness: model.HarnessCodex, ID: "abc", SourcePath: rollout, CWD: "/work"}
	linked := &model.Session{Harness: model.HarnessCodex, ID: "def", SourcePath: link}
	pi := &model.Session{Harness: model.HarnessPi, ID: "p", SourcePath: rollout}

	s := stage(t, headA, 0, codex, linked, pi)
	mf, _ := onlyFile(t, s)
	if mf.Path != "codex/abc/rollout-2026-10-01-abc.jsonl" {
		t.Errorf("path = %q", mf.Path)
	}
}

func TestOversizedRangeIsDroppedButCursorMovesOn(t *testing.T) {
	dir := fixture(t)
	path := filepath.Join(dir, "sess1.jsonl")
	big := `{"sessionId":"sess1","pad":"` + strings.Repeat("x", 200) + `"}` + "\n"
	writeFile(t, path, big)
	sess := claudeSession("sess1", path)

	dropped := stage(t, headA, 100, sess)
	if !dropped.Empty() || len(dropped.Notes) == 0 {
		t.Fatalf("want an empty capture with a note, got files=%d notes=%v", len(dropped.Files), dropped.Notes)
	}
	commit(t, dropped)

	appendFile(t, path, `{"sessionId":"sess1","n":2}`+"\n")
	next := stage(t, headB, 100, sess)
	_, f := onlyFile(t, next)
	if string(f.Data) != `{"sessionId":"sess1","n":2}`+"\n" {
		t.Errorf("data = %q, want only the line after the dropped range", f.Data)
	}
}

func TestBudgetIsSharedAcrossFiles(t *testing.T) {
	dir := fixture(t)
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	line := `{"sessionId":"s","pad":"` + strings.Repeat("y", 60) + `"}` + "\n"
	writeFile(t, a, strings.Replace(line, `"s"`, `"a"`, 1))
	writeFile(t, b, strings.Replace(line, `"s"`, `"b"`, 1))

	s := stage(t, headA, int64(len(line))+10, claudeSession("a", a), claudeSession("b", b))
	if len(s.Files) != 1 || len(s.Notes) != 1 {
		t.Errorf("files=%d notes=%v, want the first file kept and the second dropped", len(s.Files), s.Notes)
	}
}

func TestScopesAreIndependent(t *testing.T) {
	dir := fixture(t)
	path := filepath.Join(dir, "sess1.jsonl")
	writeFile(t, path, `{"sessionId":"sess1","n":1}`+"\n")
	sess := claudeSession("sess1", path)
	commit(t, stage(t, headA, 0, sess))

	other, err := Stage(Options{Head: headB, Scope: "https://other.example/acme__shop__7", Sessions: []*model.Session{sess}, Redactor: redact.NewWith(nil, "")})
	if err != nil {
		t.Fatal(err)
	}
	if mf, _ := onlyFile(t, other); mf.StartOffset != 0 {
		t.Errorf("a different server must start from 0, got %d", mf.StartOffset)
	}
}

func TestCommitKeepsOtherScopesCursors(t *testing.T) {
	dir := fixture(t)
	path := filepath.Join(dir, "sess1.jsonl")
	writeFile(t, path, `{"sessionId":"sess1","n":1}`+"\n")
	sess := claudeSession("sess1", path)
	commit(t, stage(t, headA, 0, sess))

	other, err := Stage(Options{Head: headA, Scope: "other-scope", Sessions: []*model.Session{sess}, Redactor: redact.NewWith(nil, "")})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, other)
	if got := loadCursors(); len(got.Scopes) != 2 {
		t.Errorf("scopes = %d, want both kept", len(got.Scopes))
	}
}

func TestStageRejectsBadHead(t *testing.T) {
	fixture(t)
	if _, err := Stage(Options{Head: "abc", Scope: scope}); err == nil {
		t.Error("want an error for a short head")
	}
}

func TestMissingSourceIsSkipped(t *testing.T) {
	dir := fixture(t)
	s := stage(t, headA, 0, claudeSession("gone", filepath.Join(dir, "gone.jsonl")))
	if !s.Empty() || len(s.Manifest.Sessions) != 0 {
		t.Errorf("staged = %+v", s)
	}
}
