package filecontent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

var (
	headSHA = strings.Repeat("a", 40)
	baseSHA = strings.Repeat("b", 40)
	gitSHA  = strings.Repeat("c", 40)
	blobSHA = strings.Repeat("d", 40)
	when    = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
)

// fakeGit serves objects from a map keyed "<rev>:<path>".
type fakeGit struct {
	blobs   map[string][]byte
	base    string
	baseErr error
	calls   []string
}

// newFakeGit serves objects and resolves the merge base to baseSHA unless a
// test overrides it.
func newFakeGit() *fakeGit {
	return &fakeGit{blobs: map[string][]byte{}, base: baseSHA}
}

func (g *fakeGit) put(rev, path, body string) { g.blobs[rev+":"+path] = []byte(body) }

func (g *fakeGit) BlobSize(rev, path string) (int64, error) {
	g.calls = append(g.calls, "size "+rev+":"+path)
	b, ok := g.blobs[rev+":"+path]
	if !ok {
		return 0, fmt.Errorf("fake git: no such object %s:%s", rev, path)
	}
	return int64(len(b)), nil
}

func (g *fakeGit) Blob(rev, path string) ([]byte, error) {
	g.calls = append(g.calls, "blob "+rev+":"+path)
	b, ok := g.blobs[rev+":"+path]
	if !ok {
		return nil, fmt.Errorf("fake git: no such object %s:%s", rev, path)
	}
	return b, nil
}

func (g *fakeGit) MergeBase(a, b string) (string, error) {
	g.calls = append(g.calls, "merge-base "+a+" "+b)
	if g.baseErr != nil {
		return "", g.baseErr
	}
	return g.base, nil
}

// fakeRunner serves canned gh output, and errors on anything it was not taught
// so an unexpected call is loud rather than silent.
type fakeRunner struct {
	responses map[string][]byte
	calls     []string
}

func newFakeRunner() *fakeRunner { return &fakeRunner{responses: map[string][]byte{}} }

func (r *fakeRunner) set(args string, body []byte) { r.responses[args] = body }

func (r *fakeRunner) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	r.calls = append(r.calls, key)
	if out, ok := r.responses[key]; ok {
		return out, nil
	}
	return nil, fmt.Errorf("fake gh: unexpected call: gh %s", key)
}

// compareArg is the `gh api` call that resolves the merge base.
func compareArg(base, head string) string {
	return fmt.Sprintf("api repos/acme/shop/compare/%s...%s", base, head)
}

// contentsArg is the `gh api` call that reads one file's metadata.
func contentsArg(rev, path string) string {
	return fmt.Sprintf("api repos/acme/shop/contents/%s?ref=%s", escapePath(path), rev)
}

// setCompare teaches the runner the merge-base answer.
func (r *fakeRunner) setCompare(sha string) {
	r.set(compareArg("main", headSHA), []byte(`{"merge_base_commit":{"sha":"`+sha+`"}}`))
}

// setContents teaches the runner one file's inline content.
func (r *fakeRunner) setContents(rev, path, body string) {
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	r.set(contentsArg(rev, path), []byte(
		`{"size":`+fmt.Sprint(len(body))+`,"sha":"`+blobSHA+`","encoding":"base64","content":"`+enc+`"}`))
}

// setContentsMeta teaches only the metadata, the way GitHub answers above
// 1 MiB: a size and a blob id, with no inline content.
func (r *fakeRunner) setContentsMeta(rev, path string, size int64) {
	r.set(contentsArg(rev, path), []byte(
		`{"size":`+fmt.Sprint(size)+`,"sha":"`+blobSHA+`","encoding":"none","content":""}`))
}

// setBlob teaches the runner the blobs endpoint.
func (r *fakeRunner) setBlob(body string) {
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	r.set("api repos/acme/shop/git/blobs/"+blobSHA, []byte(`{"encoding":"base64","content":"`+enc+`"}`))
}

func setNotFound(r *fakeRunner, args string) {
	r.set(args, nil)
	delete(r.responses, args)
	r.responses[args] = nil
}

// options assembles a Build for a PR with the given files.
func options(t *testing.T, files ...model.DiffFile) Options {
	t.Helper()
	return Options{
		Repo:     "acme/shop",
		Number:   482,
		PR:       &model.PR{Repo: "acme/shop", Number: 482, HeadSHA: headSHA, BaseRef: "main", FilesSource: model.SourceDiff, Files: files},
		Redactor: redact.NewWith(nil, ""),
		Now:      when,
		Tool:     "paircli test",
	}
}

// manyLines is a body of n numbered lines, so a test can reason about the
// window maths (which counts lines) separately from the byte caps.
func manyLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// sideOf returns the side written for a file, and whether it was.
func sideOf(m *Manifest, path, side string) (Side, bool) {
	for _, f := range m.Files {
		if f.Path == path {
			s, ok := f.Sides[side]
			return s, ok
		}
	}
	return Side{}, false
}

// skipOf returns the skip recorded for a file's side.
func skipOf(m *Manifest, path, side string) (Skipped, bool) {
	for _, f := range m.Files {
		if f.Path == path {
			sk, ok := f.Skipped[side]
			return sk, ok
		}
	}
	return Skipped{}, false
}

func TestBuildWritesBothSidesOfAModifiedFile(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "src/app.go", "new one\nnew two\n")
	g.put(baseSHA, "src/app.go", "old one\n")

	f := model.DiffFile{Path: "src/app.go", Status: model.StatusModified,
		Hunks: []model.DiffHunk{hunk(1, 1, "d1", "a1", "a2")}}
	m := Build(optionsWith(t, g, nil, f))

	newSide, ok := sideOf(m, "src/app.go", SideNew)
	if !ok {
		t.Fatalf("no new side: %+v", m.Files)
	}
	if newSide.File != "new/src/app.go" || newSide.Source != "git" || newSide.Partial {
		t.Errorf("new side = %+v", newSide)
	}
	if newSide.Lines != 2 || newSide.FullLines != 2 {
		t.Errorf("new side lines = %d/%d, want 2/2", newSide.Lines, newSide.FullLines)
	}
	oldSide, ok := sideOf(m, "src/app.go", SideOld)
	if !ok {
		t.Fatalf("no old side: %+v", m.Files)
	}
	if oldSide.File != "old/src/app.go" || oldSide.Source != "git" {
		t.Errorf("old side = %+v", oldSide)
	}
	// The old side is read at the merge base, the new one at the head.
	for _, want := range []string{"size " + headSHA + ":src/app.go", "size " + baseSHA + ":src/app.go"} {
		if !contains(g.calls, want) {
			t.Errorf("git was not asked for %q; calls = %v", want, g.calls)
		}
	}
	if m.BaseSHA != baseSHA || m.BaseKind != baseMergeBase || m.BaseSource != "git" {
		t.Errorf("base = %q %q %q", m.BaseSHA, m.BaseKind, m.BaseSource)
	}
}

func TestBuildSideSelectionPerStatus(t *testing.T) {
	tests := []struct {
		status model.FileStatus
		want   []string
	}{
		{model.StatusAdded, []string{SideNew}},
		{model.StatusDeleted, []string{SideOld}},
		{model.StatusModified, []string{SideNew, SideOld}},
		{model.StatusRenamed, []string{SideNew, SideOld}},
	}
	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			g := newFakeGit()
			g.put(headSHA, "path.txt", "content\n")
			g.put(baseSHA, "path.txt", "content\n")

			f := model.DiffFile{Path: "path.txt", Status: tc.status,
				Hunks: []model.DiffHunk{hunk(1, 1, " 1")}}
			m := Build(optionsWith(t, g, nil, f))

			var got []string
			for _, side := range []string{SideNew, SideOld} {
				if _, ok := sideOf(m, "path.txt", side); ok {
					got = append(got, side)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sides = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildReadsTheOldSideOfARenameAtTheOldPath(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "src/new.go", "same\n")
	g.put(baseSHA, "src/old.go", "same\n")

	f := model.DiffFile{Path: "src/new.go", OldPath: "src/old.go", Status: model.StatusRenamed,
		Hunks: []model.DiffHunk{hunk(1, 1, " 1")}}
	m := Build(optionsWith(t, g, nil, f))

	if s, ok := sideOf(m, "src/new.go", SideOld); !ok {
		t.Fatalf("no old side: %+v", m.Files)
	} else if s.File != "old/src/old.go" {
		t.Errorf("old side file = %q, want old/src/old.go", s.File)
	}
	if !contains(g.calls, "size "+baseSHA+":src/old.go") {
		t.Errorf("the old side was not read at the old path: %v", g.calls)
	}
}

// optionsWith wires one file into a Build with a git source and an optional API.
func optionsWith(t *testing.T, g *fakeGit, r *fakeRunner, files ...model.DiffFile) Options {
	t.Helper()
	o := options(t, files...)
	o.Git = g
	if r != nil {
		o.Runner = r
	}
	return o
}

// mentions reports whether any recorded call names the given path, which is
// how a test asks "was this file fetched" without tripping over the merge-base
// lookup that runs for every build.
func mentions(calls []string, path string) bool {
	if path == "" {
		return false
	}
	for _, c := range calls {
		if strings.Contains(c, path) {
			return true
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestBuildPrefersGitAndFallsBackToTheAPI(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "local.go", "from git\n")
	g.put(baseSHA, "local.go", "from git\n")
	// remote.go is not in the checkout, so only the API can serve it.

	r := newFakeRunner()
	r.setCompare(baseSHA)
	r.setContents(headSHA, "remote.go", "from api\n")
	r.setContents(baseSHA, "remote.go", "from api\n")

	files := []model.DiffFile{
		{Path: "local.go", Status: model.StatusModified, Hunks: []model.DiffHunk{hunk(1, 1, " 1")}},
		{Path: "remote.go", Status: model.StatusModified, Hunks: []model.DiffHunk{hunk(1, 1, " 1")}},
	}
	m := Build(optionsWith(t, g, r, files...))
	if m.BaseSource != "api" {
		t.Errorf("base came from %q, want api: the two sources are ordered, not raced", m.BaseSource)
	}

	if s, ok := sideOf(m, "local.go", SideNew); !ok || s.Source != "git" {
		t.Errorf("local.go new side = %+v, ok=%v, want source git", s, ok)
	}
	if s, ok := sideOf(m, "remote.go", SideNew); !ok || s.Source != "api" {
		t.Errorf("remote.go new side = %+v, ok=%v, want source api", s, ok)
	}
}

func TestBuildResolvesTheBaseFromTheAPIWhenItCan(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "a.go", "x\n")
	g.put(baseSHA, "a.go", "x\n")
	g.base = gitSHA // the local answer, which must lose

	r := newFakeRunner()
	r.setCompare(baseSHA)

	f := model.DiffFile{Path: "a.go", Status: model.StatusModified, Hunks: []model.DiffHunk{hunk(1, 1, " 1")}}
	m := Build(optionsWith(t, g, r, f))

	if m.BaseSHA != baseSHA || m.BaseSource != "api" {
		t.Errorf("base = %q from %q, want %q from api", m.BaseSHA, m.BaseSource, baseSHA)
	}
	if contains(g.calls, "merge-base "+headSHA+" origin/main") {
		t.Error("git merge-base ran even though the API answered")
	}
	if !strings.Contains(m.BaseSource, "api") {
		t.Errorf("BaseSource = %q", m.BaseSource)
	}
}

func TestBuildFallsBackToGitForTheBase(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "a.go", "x\n")
	g.put(gitSHA, "a.go", "x\n")
	g.base = gitSHA

	// No runner at all: offline.
	f := model.DiffFile{Path: "a.go", Status: model.StatusModified, Hunks: []model.DiffHunk{hunk(1, 1, " 1")}}
	m := Build(optionsWith(t, g, nil, f))

	if m.BaseSHA != gitSHA || m.BaseSource != "git" {
		t.Errorf("base = %q from %q, want %q from git", m.BaseSHA, m.BaseSource, gitSHA)
	}
	// The remote-tracking ref, never a local branch that may be stale.
	if !contains(g.calls, "merge-base "+headSHA+" origin/main") {
		t.Errorf("git was not asked for the remote-tracking merge base: %v", g.calls)
	}
}

func TestBuildOmitsOldSidesWhenThereIsNoBase(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "a.go", "x\n")
	g.base = ""
	g.baseErr = fmt.Errorf("fake git: no such ref")

	f := model.DiffFile{Path: "a.go", Status: model.StatusModified, Hunks: []model.DiffHunk{hunk(1, 1, " 1")}}
	m := Build(optionsWith(t, g, nil, f))

	if m.BaseSHA != "" || m.BaseKind != "" {
		t.Errorf("base = %q/%q, want empty", m.BaseSHA, m.BaseKind)
	}
	sk, ok := skipOf(m, "a.go", SideOld)
	if !ok || sk.Reason != ReasonNoBase {
		t.Errorf("old side skip = %+v (present=%v), want no_base", sk, ok)
	}
	if _, ok := sideOf(m, "a.go", SideNew); !ok {
		t.Error("the new side should still be written")
	}
}

func TestBuildNeverFetchesMedia(t *testing.T) {
	paths := []string{
		"assets/logo.PNG", "media/clip.mp4", "fonts/inter.woff2", "dist/app.zip",
		"docs/spec.pdf", "lib/native.so", "data/cache.sqlite", "model/weights.safetensors",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			g := newFakeGit()
			g.put(headSHA, path, "\x89PNG\r\n\x1a\n")
			r := newFakeRunner()

			f := model.DiffFile{Path: path, Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}}
			m := Build(optionsWith(t, g, r, f))

			sk, ok := skipOf(m, path, SideNew)
			if !ok || sk.Reason != ReasonMedia {
				t.Fatalf("skip = %+v (present=%v), want media", sk, ok)
			}
			if mentions(g.calls, path) || mentions(r.calls, path) {
				t.Errorf("a media file was fetched: git %v, gh %v", g.calls, r.calls)
			}
		})
	}
}

func TestBuildKeepsTextFilesThatLookLikeMedia(t *testing.T) {
	// svg is text and can carry script and hidden text a reading guide flags.
	// .pem and .key are text and must be redacted, not skipped.
	for _, path := range []string{"icon.svg", "deploy.key", "server.pem", "data.csv", "notes.md"} {
		t.Run(path, func(t *testing.T) {
			g := newFakeGit()
			g.put(headSHA, path, "text\n")
			f := model.DiffFile{Path: path, Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}}
			m := Build(optionsWith(t, g, nil, f))
			if _, ok := skipOf(m, path, SideNew); ok {
				t.Errorf("%s was skipped: %+v", path, m.Files)
			}
		})
	}
}

func TestBuildTrustsBinaryOnlyFromTheDiffEndpoint(t *testing.T) {
	big := strings.Repeat("package a\n// text\n", 100)

	t.Run("diff endpoint", func(t *testing.T) {
		g := newFakeGit()
		g.put(headSHA, "a.go", big)
		f := model.DiffFile{Path: "a.go", Status: model.StatusAdded, Binary: true}
		m := Build(optionsWith(t, g, nil, f))
		if sk, _ := skipOf(m, "a.go", SideNew); sk.Reason != ReasonBinary {
			t.Errorf("skip = %+v, want binary", sk)
		}
		if mentions(g.calls, "a.go") {
			t.Errorf("a file the diff called binary was still fetched: %v", g.calls)
		}
	})

	t.Run("files API", func(t *testing.T) {
		g := newFakeGit()
		g.put(headSHA, "a.go", big)
		o := optionsWith(t, g, nil, model.DiffFile{Path: "a.go", Status: model.StatusAdded, Binary: true})
		o.PR.FilesSource = model.SourceAPI // the flag means "no patch came", not "binary"
		m := Build(o)

		s, ok := sideOf(m, "a.go", SideNew)
		if !ok {
			t.Fatalf("a large text file with no patch was not served: %+v", m.Files)
		}
		if s.Bytes != len(big) {
			t.Errorf("bytes = %d, want %d", s.Bytes, len(big))
		}
	})
}

func TestBuildByteBackstopCatchesWhatTheExtensionCannot(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"nul byte", "package a\x00binary\n"},
		{"invalid utf-8", "package a\n\xff\xfe\xfd\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGit()
			g.put(headSHA, "noext", tc.body)
			f := model.DiffFile{Path: "noext", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}}
			m := Build(optionsWith(t, g, nil, f))
			if sk, _ := skipOf(m, "noext", SideNew); sk.Reason != ReasonBinary {
				t.Errorf("skip = %+v, want binary", sk)
			}
		})
	}
}

func TestBuildRefusesToDownloadAnOversizedFile(t *testing.T) {
	r := newFakeRunner()
	r.setCompare(baseSHA)
	r.setContentsMeta(headSHA, "huge.txt", 50<<20)

	f := model.DiffFile{Path: "huge.txt", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}}
	o := options(t, f)
	o.Runner = r
	m := Build(o)

	sk, ok := skipOf(m, "huge.txt", SideNew)
	if !ok || sk.Reason != ReasonTooLarge || sk.Limit != LimitDownloadBytes {
		t.Fatalf("skip = %+v, want too_large/download_bytes", sk)
	}
	if sk.Bytes != 50<<20 {
		t.Errorf("bytes = %d, want the reported size", sk.Bytes)
	}
	for _, call := range r.calls {
		if strings.Contains(call, "git/blobs") {
			t.Errorf("the blob was downloaded anyway: %v", r.calls)
		}
	}
}

func TestBuildUsesTheBlobsEndpointAboveTheInlineLimit(t *testing.T) {
	body := strings.Repeat("x", 4096)
	r := newFakeRunner()
	r.setCompare(baseSHA)
	r.setContentsMeta(headSHA, "big.txt", int64(len(body)))
	r.setBlob(body)

	f := model.DiffFile{Path: "big.txt", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}}
	o := options(t, f)
	o.Runner = r
	m := Build(o)

	s, ok := sideOf(m, "big.txt", SideNew)
	if !ok {
		t.Fatalf("no content: %+v", m.Files)
	}
	if s.Bytes != len(body) || s.Source != "api" {
		t.Errorf("side = %+v", s)
	}
}

func TestBuildRecordsAMissingFile(t *testing.T) {
	r := newFakeRunner()
	r.setCompare(baseSHA)
	r.setContents(headSHA, "gone.go", "x\n")
	r.set(contentsArg(baseSHA, "gone.go"), nil) // taught, but empty: see below

	f := model.DiffFile{Path: "gone.go", Status: model.StatusModified, Hunks: []model.DiffHunk{hunk(1, 1, " 1")}}
	o := options(t, f)
	o.Runner = &notFoundRunner{inner: r, args: contentsArg(baseSHA, "gone.go")}
	m := Build(o)

	sk, ok := skipOf(m, "gone.go", SideOld)
	if !ok || sk.Reason != ReasonMissing {
		t.Fatalf("skip = %+v, want missing", sk)
	}
	if _, ok := sideOf(m, "gone.go", SideNew); !ok {
		t.Error("the new side should still be written")
	}
}

// notFoundRunner turns one argument list into a 404 the way gh reports one.
type notFoundRunner struct {
	inner *fakeRunner
	args  string
}

func (n *notFoundRunner) Run(args ...string) ([]byte, error) {
	if strings.Join(args, " ") == n.args {
		n.inner.calls = append(n.inner.calls, strings.Join(args, " "))
		return nil, fmt.Errorf("gh %s: exit status 1: HTTP 404: Not Found", strings.Join(args, " "))
	}
	return n.inner.Run(args...)
}

func TestBuildRecordsAFailureAndKeepsGoing(t *testing.T) {
	r := newFakeRunner()
	r.setCompare(baseSHA)
	r.setContents(headSHA, "ok.go", "fine\n")
	r.setContentsMeta(headSHA, "broken.go", 10)

	files := []model.DiffFile{
		{Path: "broken.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}},
		{Path: "ok.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}},
	}
	// broken.go's metadata is served but its blob call is not, so it fails
	// after a successful metadata read.
	o := options(t, files...)
	o.Runner = r
	m := Build(o)

	sk, ok := skipOf(m, "broken.go", SideNew)
	if !ok || sk.Reason != ReasonError {
		t.Fatalf("skip = %+v, want error", sk)
	}
	if sk.Error == "" {
		t.Error("no failure recorded")
	}
	if _, ok := sideOf(m, "ok.go", SideNew); !ok {
		t.Error("one bad file stopped the others")
	}
}

func TestBuildCropping(t *testing.T) {
	// A long file changed near its middle: the side is cropped to the window
	// around the change, which is far smaller than the file.
	body := manyLines(4000)
	g := newFakeGit()
	g.put(headSHA, "long.go", body)
	g.put(baseSHA, "long.go", body)

	f := model.DiffFile{Path: "long.go", Status: model.StatusModified,
		Hunks: []model.DiffHunk{hunk(2000, 2000, "d2000", "a2000")}}
	o := optionsWith(t, g, nil, f)
	o.Limits = DefaultLimits()
	o.Limits.MaxFileBytes = 8192 // fits the window, not the file
	m := Build(o)

	s, ok := sideOf(m, "long.go", SideNew)
	if !ok {
		t.Fatalf("no side: %+v", m.Files)
	}
	if !s.Partial || s.Limit != LimitFileBytes {
		t.Errorf("side = %+v, want partial/file_bytes", s)
	}
	want := [][2]int{{1700, 2300}}
	if !reflect.DeepEqual(s.Ranges, want) {
		t.Errorf("ranges = %v, want %v", s.Ranges, want)
	}
	if s.Lines != 601 || s.Bytes != len(s.data) {
		t.Errorf("lines = %d (want 601), bytes = %d (want %d)", s.Lines, s.Bytes, len(s.data))
	}
	if int64(s.Bytes) > o.Limits.MaxFileBytes {
		t.Errorf("bytes = %d, over the cap", s.Bytes)
	}
	if s.FullLines != 4000 || s.FullBytes <= s.Bytes {
		t.Errorf("full = %d lines/%d bytes, kept %d/%d", s.FullLines, s.FullBytes, s.Lines, s.Bytes)
	}
	if !strings.HasPrefix(string(s.data), "line 1700\n") || !strings.HasSuffix(string(s.data), "line 2300\n") {
		t.Errorf("kept lines do not match the ranges")
	}
	if m.Totals.SidesPartial != 2 {
		t.Errorf("sides partial = %d, want 2 (both sides of the change)", m.Totals.SidesPartial)
	}
}

func TestBuildRefusesAWindowOverTheLineCap(t *testing.T) {
	// A minified file: one enormous line, so the window is the whole file.
	body := "var x=" + strings.Repeat("1+", 5000) + "1;\n"
	g := newFakeGit()
	g.put(headSHA, "app.min.js", body)
	g.put(baseSHA, "app.min.js", body)

	f := model.DiffFile{Path: "app.min.js", Status: model.StatusModified,
		Hunks: []model.DiffHunk{hunk(1, 1, "d1", "a1")}}
	m := Build(optionsWith(t, g, nil, f))

	sk, ok := skipOf(m, "app.min.js", SideNew)
	if !ok || sk.Reason != ReasonTooLarge || sk.Limit != LimitLineBytes {
		t.Fatalf("skip = %+v, want too_large/line_bytes", sk)
	}
}

func TestBuildDoesNotCropAnOversizedAddedFileToNothing(t *testing.T) {
	// The hunk of an added file is the whole file, so the window is the whole
	// file, so it is still over the cap: cropping cannot rescue it.
	body := strings.Repeat("some line of code\n", 200)
	g := newFakeGit()
	g.put(headSHA, "added.go", body)

	var lines []model.DiffLine
	for i := 1; i <= 200; i++ {
		lines = append(lines, model.DiffLine{Kind: model.LineAdd, NewNo: i, Text: "x"})
	}
	f := model.DiffFile{Path: "added.go", Status: model.StatusAdded,
		Hunks: []model.DiffHunk{{NewStart: 1, Lines: lines}}}

	o := optionsWith(t, g, nil, f)
	o.Limits = DefaultLimits()
	o.Limits.MaxFileBytes = 256
	m := Build(o)

	sk, ok := skipOf(m, "added.go", SideNew)
	if !ok || sk.Reason != ReasonTooLarge || sk.Limit != LimitFileBytes {
		t.Fatalf("skip = %+v, want too_large/file_bytes", sk)
	}
}

func TestBuildSkipsAnOversizedFileWithNoHunks(t *testing.T) {
	// A large file whose patch GitHub omitted: there is no window to build, so
	// it is all of the file or none of it, and it is over the cap.
	body := strings.Repeat("some line of code\n", 200)
	g := newFakeGit()
	g.put(headSHA, "patchless.go", body)

	o := optionsWith(t, g, nil, model.DiffFile{Path: "patchless.go", Status: model.StatusAdded})
	o.PR.FilesSource = model.SourceAPI
	o.Limits = DefaultLimits()
	o.Limits.MaxFileBytes = 256
	m := Build(o)

	sk, ok := skipOf(m, "patchless.go", SideNew)
	if !ok || sk.Reason != ReasonTooLarge || sk.Limit != LimitFileBytes {
		t.Fatalf("skip = %+v, want too_large/file_bytes", sk)
	}
	if _, ok := sideOf(m, "patchless.go", SideNew); ok {
		t.Error("a side with no windows must not be written as partial")
	}
}

func TestBuildWritesASmallFileWithNoHunksWhole(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "rename-only.go", "same content\n")
	o := optionsWith(t, g, nil, model.DiffFile{Path: "rename-only.go", Status: model.StatusAdded})
	m := Build(o)

	s, ok := sideOf(m, "rename-only.go", SideNew)
	if !ok {
		t.Fatalf("no side: %+v", m.Files)
	}
	if s.Partial || !reflect.DeepEqual(s.Ranges, [][2]int{}) {
		t.Errorf("side = %+v, want a whole side with empty ranges", s)
	}
}

func TestBuildCropsRatherThanSkipsWhenTheTotalIsSpent(t *testing.T) {
	body := manyLines(5000)
	g := newFakeGit()
	g.put(headSHA, "first.go", body)
	g.put(headSHA, "a.go", body)
	g.put(baseSHA, "a.go", body)

	files := []model.DiffFile{
		{Path: "first.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}},
		{Path: "a.go", Status: model.StatusModified,
			Hunks: []model.DiffHunk{hunk(2500, 2500, "d2500", "a2500")}},
	}
	o := optionsWith(t, g, nil, files...)
	o.Limits = DefaultLimits()
	// Room for the first file whole, plus enough for the second file's windows
	// but not for the second file.
	o.Limits.MaxTotalBytes = int64(len(body)) + 10000
	m := Build(o)

	if s, ok := sideOf(m, "first.go", SideNew); !ok || s.Partial {
		t.Fatalf("the first file should fit whole: %+v", m.Files)
	}
	s, ok := sideOf(m, "a.go", SideNew)
	if !ok {
		t.Fatalf("the second file was dropped instead of cropped: %+v", m.Files)
	}
	if !s.Partial || s.Limit != LimitTotalBytes {
		t.Errorf("second side = %+v, want partial/total_bytes", s)
	}
	if want := [][2]int{{2200, 2800}}; !reflect.DeepEqual(s.Ranges, want) {
		t.Errorf("ranges = %v, want %v", s.Ranges, want)
	}
}

func TestBuildReportsBudgetOnlyWhenEvenTheWindowsDoNotFit(t *testing.T) {
	// Both files change at their first line, so each window is 301 lines: the
	// first file's windows fit whole and spend the budget, the second's do not.
	body := manyLines(5000)
	g := newFakeGit()
	g.put(headSHA, "first.go", body)
	g.put(headSHA, "a.go", body)

	files := []model.DiffFile{
		{Path: "first.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}},
		{Path: "a.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}},
	}
	o := optionsWith(t, g, nil, files...)
	o.Limits = DefaultLimits()
	o.Limits.MaxTotalBytes = int64(len(body)) // everything, and no more
	m := Build(o)

	sk, ok := skipOf(m, "a.go", SideNew)
	if !ok || sk.Reason != ReasonBudget || sk.Limit != LimitTotalBytes {
		t.Fatalf("skip = %+v, want budget/total_bytes", sk)
	}
}

func TestBuildStopsAtTheSideCap(t *testing.T) {
	g := newFakeGit()
	files := []model.DiffFile{}
	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("f%d.go", i)
		g.put(headSHA, path, "x\n")
		files = append(files, model.DiffFile{Path: path, Status: model.StatusAdded,
			Hunks: []model.DiffHunk{hunk(1, 1, "a1")}})
	}
	o := optionsWith(t, g, nil, files...)
	o.Limits = DefaultLimits()
	o.Limits.MaxSides = 2
	m := Build(o)

	if m.Totals.SidesWritten != 2 {
		t.Errorf("sides written = %d, want 2", m.Totals.SidesWritten)
	}
	sk, ok := skipOf(m, "f2.go", SideNew)
	if !ok || sk.Reason != ReasonBudget || sk.Limit != LimitSides {
		t.Errorf("skip = %+v, want budget/sides", sk)
	}
}

// TestBuildRedactsBeforeCropping is the ordering that matters: a secret
// straddling a window edge is redacted as part of the whole file, not cut away
// first and left in the part that was kept.
func TestBuildRedactsBeforeCropping(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\n" +
		"MIIEowIBAAKCAQEA7abcDEF\n" +
		"-----END OPENSSH PRIVATE KEY-----"
	lines := strings.Split(manyLines(2000), "\n")
	// Replace file lines 700 to 702 with the key, so the window that starts at
	// line 700 begins inside it.
	kept := append([]string{}, lines[:699]...)
	kept = append(kept, key)
	kept = append(kept, lines[702:]...)
	withKey := strings.Join(kept, "\n")

	g := newFakeGit()
	g.put(headSHA, "secrets.go", withKey)
	g.put(baseSHA, "secrets.go", withKey)

	f := model.DiffFile{Path: "secrets.go", Status: model.StatusModified,
		Hunks: []model.DiffHunk{hunk(1000, 1000, "d1000", "a1000")}}
	o := optionsWith(t, g, nil, f)
	o.Limits = DefaultLimits()
	o.Limits.MaxFileBytes = 8192
	m := Build(o)

	s, ok := sideOf(m, "secrets.go", SideNew)
	if !ok {
		t.Fatalf("no side: %+v", m.Files)
	}
	if !s.Partial {
		t.Fatalf("expected a cropped side, got %+v", s)
	}
	if want := [][2]int{{700, 1300}}; !reflect.DeepEqual(s.Ranges, want) {
		t.Fatalf("ranges = %v, want %v", s.Ranges, want)
	}
	if bytes.Contains(s.data, []byte("MIIEowIBAAKCAQEA7abcDEF")) {
		t.Errorf("the key survived:\n%s", s.data)
	}
	if !bytes.Contains(s.data, []byte("[REDACTED:private_key]")) {
		t.Errorf("no marker in the kept lines:\n%s", s.data)
	}
	// The line count of the written block still matches the ranges, which is
	// what keeps it indexable against the diff.
	if s.Lines != keptLines(s.Ranges) {
		t.Errorf("lines = %d, ranges select %d", s.Lines, keptLines(s.Ranges))
	}
	// Both sides are redacted, so the pass counts two.
	if m.Redaction.Counts[redact.KindPrivateKey] != 2 {
		t.Errorf("redaction counts = %v, want two private_key (one per side)", m.Redaction.Counts)
	}
}

func TestBuildRecordsASecretInAWholeFile(t *testing.T) {
	token := "ghp_" + strings.Repeat("aB3", 12)
	g := newFakeGit()
	g.put(headSHA, "config.go", "package a\n\nvar t = \""+token+"\"\n")

	f := model.DiffFile{Path: "config.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1", "a2", "a3")}}
	m := Build(optionsWith(t, g, nil, f))

	s, ok := sideOf(m, "config.go", SideNew)
	if !ok {
		t.Fatalf("no side: %+v", m.Files)
	}
	if bytes.Contains(s.data, []byte(token)) {
		t.Errorf("the token survived:\n%s", s.data)
	}
	if !bytes.Contains(s.data, []byte("[REDACTED:github_token]")) {
		t.Errorf("no marker:\n%s", s.data)
	}
	if m.Redaction.Counts[redact.KindGitHubToken] != 1 {
		t.Errorf("redaction counts = %v", m.Redaction.Counts)
	}
}

func TestBuildRecordsAnUnsafePath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"backslash", `src\win.go`},
		{"dot dot", "src/../../etc/passwd"},
		{"dot", "src/./app.go"},
		{"absolute", "/etc/passwd"},
		{"drive letter", "C:/Windows/system32"},
		{"long component", strings.Repeat("x", 256) + ".go"},
		{"empty", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGit()
			f := model.DiffFile{Path: tc.path, Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}}
			m := Build(optionsWith(t, g, nil, f))

			if len(m.Files) != 1 {
				t.Fatalf("files = %d", len(m.Files))
			}
			sk, ok := m.Files[0].Skipped[SideNew]
			if !ok || sk.Reason != ReasonUnsafePath {
				t.Errorf("skip = %+v, want unsafe_path", sk)
			}
			if mentions(g.calls, tc.path) {
				t.Errorf("an unsafe path was fetched: %v", g.calls)
			}
		})
	}
}

func TestBuildResolvesCaseCollisions(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "Foo.go", "a\n")
	g.put(headSHA, "foo.go", "b\n")

	// A case-insensitive duplicate entry makes the service reject the whole
	// bundle, so the later path loses.
	files := []model.DiffFile{
		{Path: "Foo.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}},
		{Path: "foo.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1")}},
	}
	m := Build(optionsWith(t, g, nil, files...))

	if _, ok := sideOf(m, "Foo.go", SideNew); !ok {
		t.Error("the first path should win")
	}
	sk, ok := skipOf(m, "foo.go", SideNew)
	if !ok || sk.Reason != ReasonCollision {
		t.Errorf("skip = %+v, want collision", sk)
	}
	if m.Totals.SidesWritten != 1 || m.Totals.SidesSkipped != 1 {
		t.Errorf("totals = %+v", m.Totals)
	}
}

// TestBuildSidesAndSkipsCoverEveryExpectedSide is the manifest's core promise:
// for every status, Sides ∪ Skipped is exactly the set of sides the status
// calls for, so a consumer never has to guess whether something was forgotten.
func TestBuildSidesAndSkipsCoverEveryExpectedSide(t *testing.T) {
	for _, status := range []model.FileStatus{model.StatusAdded, model.StatusDeleted, model.StatusModified, model.StatusRenamed} {
		t.Run(string(status), func(t *testing.T) {
			g := newFakeGit() // nothing local, so every read fails
			f := model.DiffFile{Path: "a.go", OldPath: "old.go", Status: status,
				Hunks: []model.DiffHunk{hunk(1, 1, "a1", "d1")}}
			m := Build(optionsWith(t, g, nil, f))

			got := map[string]bool{}
			for side := range m.Files[0].Sides {
				got[side] = true
			}
			for side := range m.Files[0].Skipped {
				if got[side] {
					t.Errorf("%s is both written and skipped", side)
				}
				got[side] = true
			}
			want := map[string]bool{}
			for _, side := range sidesFor(status) {
				want[side] = true
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("sides ∪ skipped = %v, want %v", got, want)
			}
		})
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	g := newFakeGit()
	g.put(headSHA, "a.go", "one\ntwo\nthree\n")
	g.put(baseSHA, "a.go", "one\ntwo\n")
	f := model.DiffFile{Path: "a.go", Status: model.StatusModified, Hunks: []model.DiffHunk{hunk(2, 2, "d2", "a2")}}

	first, err := json.MarshalIndent(Build(optionsWith(t, g, nil, f)), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.MarshalIndent(Build(optionsWith(t, g, nil, f)), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two builds differ:\n%s\n%s", first, second)
	}
}

func TestBuildCarriesTheHeadAndRepo(t *testing.T) {
	m := Build(options(t))
	if m.SchemaVersion != SchemaVersion || m.HeadSHA != headSHA || m.Repo != "acme/shop" || m.PullNumber != 482 {
		t.Errorf("manifest = %+v", m)
	}
	if m.Tool != "paircli test" || m.CreatedAt != "2026-10-27T12:00:00Z" {
		t.Errorf("tool/created_at = %q/%q", m.Tool, m.CreatedAt)
	}
	if m.Files == nil || m.Limits.WindowMargin != DefaultLimits().WindowMargin {
		t.Errorf("manifest = %+v", m)
	}
}

func TestBuildWithoutAPRIsEmpty(t *testing.T) {
	m := Build(Options{Repo: "acme/shop", Number: 1, Now: when})
	if len(m.Files) != 0 || m.Totals.Files != 0 {
		t.Errorf("manifest = %+v", m)
	}
	if m.Files == nil {
		t.Error("Files should marshal as an empty array, not null")
	}
}

// TestBuildServesAFileWithNoTrailingNewline is the one place where redaction
// does not settle the line count: a multi-line secret at the end of a file that
// does not end in a newline. The file is still served — nothing after the match
// moves, so the line numbers a reader indexes by are intact — and full_lines
// still reports the file's own length rather than the redacted text's.
func TestBuildServesAFileWithNoTrailingNewline(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\n" +
		"MIIEowIBAAKCAQEA7abcDEF\n" +
		"-----END OPENSSH PRIVATE KEY-----"
	// No trailing newline, so the marker's re-emitted newlines end the file.
	body := "package a\n\nvar key = `" + key + "`"

	g := newFakeGit()
	g.put(headSHA, "keys.go", body)
	f := model.DiffFile{Path: "keys.go", Status: model.StatusAdded, Hunks: []model.DiffHunk{hunk(1, 1, "a1", "a2", "a3")}}
	m := Build(optionsWith(t, g, nil, f))

	s, ok := sideOf(m, "keys.go", SideNew)
	if !ok {
		t.Fatalf("a file whose tail is a secret was dropped: %+v", m.Files)
	}
	if bytes.Contains(s.data, []byte("MIIEowIBAAKCAQEA7abcDEF")) {
		t.Errorf("the key survived:\n%s", s.data)
	}
	if want := countLines(body); s.FullLines != want {
		t.Errorf("full_lines = %d, want %d: it is the file's own line count", s.FullLines, want)
	}
	if s.Lines != len(splitLines(string(s.data))) {
		t.Errorf("lines = %d, but the bytes written have %d", s.Lines, len(splitLines(string(s.data))))
	}
}

// TestBuildCropsWithoutRunningOffTheEnd covers the same edge through the crop
// path, where a window the file's numbering produced is longer than the text
// redaction left behind.
func TestBuildCropsWithoutRunningOffTheEnd(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nMIIEowIBAAKCAQEA7abcDEF\n-----END OPENSSH PRIVATE KEY-----"
	body := manyLines(2000) + "var key = `" + key + "`" // no trailing newline

	g := newFakeGit()
	g.put(headSHA, "keys.go", body)
	g.put(baseSHA, "keys.go", body)

	f := model.DiffFile{Path: "keys.go", Status: model.StatusModified,
		Hunks: []model.DiffHunk{hunk(1999, 1999, "d1999", "a1999")}}
	o := optionsWith(t, g, nil, f)
	o.Limits = DefaultLimits()
	o.Limits.MaxFileBytes = 8192
	m := Build(o)

	s, ok := sideOf(m, "keys.go", SideNew)
	if !ok {
		t.Fatalf("no side: %+v", m.Files)
	}
	lines := splitLines(string(s.data))
	if s.Lines != len(lines) {
		t.Errorf("lines = %d, bytes hold %d", s.Lines, len(lines))
	}
	if s.Lines != keptLines(s.Ranges) {
		t.Errorf("lines = %d, ranges select %d", s.Lines, keptLines(s.Ranges))
	}
	// The ranges are in the file's own numbering, so they are bounded by
	// full_lines and not by how many lines were written.
	for _, r := range s.Ranges {
		if r[1] > s.FullLines || r[0] < 1 {
			t.Errorf("range %v is outside the %d-line file", r, s.FullLines)
		}
	}
	if bytes.Contains(s.data, []byte("MIIEowIBAAKCAQEA7abcDEF")) {
		t.Errorf("the key survived")
	}
}
