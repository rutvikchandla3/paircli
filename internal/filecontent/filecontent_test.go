package filecontent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// built runs a Build against a small fake source and returns the manifest.
func built(t *testing.T, files ...model.DiffFile) *Manifest {
	t.Helper()
	g := newFakeGit()
	g.put(headSHA, "src/app.go", "package app\n")
	g.put(baseSHA, "src/app.go", "package app\n")
	return Build(optionsWith(t, g, nil, files...))
}

func TestWriteLayoutAndModes(t *testing.T) {
	dir := t.TempDir()
	m := built(t, model.DiffFile{Path: "src/app.go", Status: model.StatusModified,
		Hunks: []model.DiffHunk{hunk(1, 1, "d1", "a1")}})
	if err := Write(dir, m); err != nil {
		t.Fatalf("Write: %v", err)
	}

	root := filepath.Join(dir, DirName)
	for _, name := range []string{
		ManifestName,
		"new/src/app.go",
		"old/src/app.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name != ManifestName && info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}

	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("changed-files mode = %o, want 700", got)
	}

	// The manifest describes what is on disk, hash and all.
	raw, err := os.ReadFile(filepath.Join(root, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("the manifest does not end with a newline")
	}
	var read Manifest
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}
	if read.Totals.Files != 1 || read.Totals.SidesWritten != 2 {
		t.Errorf("totals = %+v", read.Totals)
	}
	for _, f := range read.Files {
		for side, s := range f.Sides {
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.File)))
			if err != nil {
				t.Fatalf("%s: %v", s.File, err)
			}
			if sha256Hex(body) != s.SHA256 {
				t.Errorf("%s/%s: sha256 does not match the file on disk", f.Path, side)
			}
			if len(body) != s.Bytes {
				t.Errorf("%s/%s: bytes = %d, file is %d", f.Path, side, s.Bytes, len(body))
			}
			if countLines(string(body)) != s.Lines {
				t.Errorf("%s/%s: lines = %d, file has %d", f.Path, side, s.Lines, countLines(string(body)))
			}
		}
	}
}

// TestWriteManifestShape pins the fields a consumer indexes without a nil
// check: sides is always an object, ranges is always an array.
func TestWriteManifestShape(t *testing.T) {
	dir := t.TempDir()
	m := built(t, model.DiffFile{Path: "src/app.go", Status: model.StatusAdded,
		Hunks: []model.DiffHunk{hunk(1, 1, "a1")}})
	if err := Write(dir, m); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, DirName, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, `"sides": null`) {
		t.Errorf("sides marshalled as null:\n%s", got)
	}
	if strings.Contains(got, `"ranges": null`) {
		t.Errorf("ranges marshalled as null:\n%s", got)
	}
	if strings.Contains(got, `"skipped": null`) {
		t.Errorf("skipped marshalled as null:\n%s", got)
	}
	if !strings.Contains(got, `"ranges": []`) {
		t.Errorf("a whole side should carry an empty ranges array:\n%s", got)
	}
	// A side that was not written has no skipped entry: skipped only ever
	// describes a side the status called for.
	if strings.Contains(got, `"skipped"`) {
		t.Errorf("an added file has nothing to skip:\n%s", got)
	}
}

// TestWriteRefusesAnUnsafeEntryName covers the one place a name from a manifest
// becomes a filesystem path: it is checked here and again in internal/publish,
// because a manifest on disk is not a source of truth about paths.
func TestWriteRefusesAnUnsafeEntryName(t *testing.T) {
	for _, bad := range []string{"../escape.go", "/etc/passwd", `new\\win.go`, "new/../../x", ""} {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			m := built(t, model.DiffFile{Path: "src/app.go", Status: model.StatusAdded,
				Hunks: []model.DiffHunk{hunk(1, 1, "a1")}})
			f := &m.Files[0]
			s := f.Sides[SideNew]
			s.File = bad
			f.Sides[SideNew] = s
			if err := Write(dir, m); err == nil {
				t.Errorf("Write accepted the entry name %q", bad)
			}
			// Nothing may have been created outside the subtree either.
			if _, err := os.Stat(filepath.Join(dir, "escape.go")); err == nil {
				t.Error("a file was written outside changed-files/")
			}
		})
	}
}

func TestWriteRefusesNilAndEmptyDir(t *testing.T) {
	if err := Write(t.TempDir(), nil); err == nil {
		t.Error("Write(nil manifest) should fail")
	}
	if err := Write("", &Manifest{}); err == nil {
		t.Error("Write with no directory should fail")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	m := built(t, model.DiffFile{Path: "src/app.go", Status: model.StatusAdded,
		Hunks: []model.DiffHunk{hunk(1, 1, "a1")}})
	if err := Write(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName)); !os.IsNotExist(err) {
		t.Errorf("Remove left %s behind: %v", DirName, err)
	}
	// Removing what is not there is not an error, and neither is an empty dir.
	if err := Remove(dir); err != nil {
		t.Errorf("Remove of a missing subtree: %v", err)
	}
	if err := Remove(""); err != nil {
		t.Errorf("Remove(\"\") = %v", err)
	}
}

func TestWriteOverwritesAnEarlierRun(t *testing.T) {
	dir := t.TempDir()
	first := built(t, model.DiffFile{Path: "src/app.go", Status: model.StatusAdded,
		Hunks: []model.DiffHunk{hunk(1, 1, "a1")}})
	if err := Write(dir, first); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, DirName, "new", "src", "gone.go")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("an earlier run's file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := built(t, model.DiffFile{Path: "src/app.go", Status: model.StatusAdded,
		Hunks: []model.DiffHunk{hunk(1, 1, "a1")}})
	if err := Write(dir, second); err != nil {
		t.Fatal(err)
	}
	// Write leaves what it does not name: internal/publish reads the manifest,
	// not the directory, so a file nobody names is never uploaded. A scan that
	// did not ask for contents calls Remove instead.
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("Write removed a file it does not name: %v", err)
	}
}
