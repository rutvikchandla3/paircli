package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanDirIsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	head := strings.Repeat("a", 40)

	want := filepath.Join(home, ".paircli", "scans", "acme__shop", "pr-7", head)
	if got := ScanDir("acme", "shop", 7, head); got != want {
		t.Errorf("ScanDir = %q, want %q", got, want)
	}
	if !strings.HasPrefix(want, home) {
		t.Fatalf("%q is not under the home directory", want)
	}
}

func TestRepoKeyNamingIsAContract(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// These strings are what a user finds when they go looking for a PR's
	// record, its lock or its scan folder, and what an existing install already
	// has on disk: renaming them would orphan every one of them.
	if got, want := repoKey("Acme", "Shop"), "acme__shop"; got != want {
		t.Errorf("repoKey = %q, want %q", got, want)
	}
	if got, want := Key("Acme", "Shop", 7), "acme__shop__7"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
	sep := string(filepath.Separator)
	if dir := ScanDir("Acme", "Shop", 7, "abc"); !strings.Contains(dir, sep+"acme__shop"+sep) {
		t.Errorf("ScanDir = %q, want it to carry the repo key", dir)
	}
}

func TestScanDirWithoutAHomeIsEmpty(t *testing.T) {
	t.Setenv("HOME", "")
	if got := ScanDir("acme", "shop", 7, "abc"); got != "" {
		t.Errorf("ScanDir = %q, want empty", got)
	}
	if got := ScanRoot(); got != "" {
		t.Errorf("ScanRoot = %q, want empty", got)
	}
}

func TestLegacyScanDir(t *testing.T) {
	tests := []struct {
		name  string
		setup []string // directories to create under <dir>/.paircli
		want  bool
	}{
		{name: "no .paircli at all"},
		{name: "empty .paircli"},
		{name: "only pr folders", setup: []string{"pr-7"}, want: true},
		{name: "pr folder and leftovers", setup: []string{"pr-7", "logs"}, want: true},
		{name: "unrelated contents only", setup: []string{"logs", "state"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tc.setup {
				if err := os.MkdirAll(filepath.Join(dir, ".paircli", name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			got := LegacyScanDir(dir)
			if tc.want != (got != "") {
				t.Fatalf("LegacyScanDir = %q, want a match: %v", got, tc.want)
			}
			if tc.want && got != filepath.Join(dir, ".paircli") {
				t.Errorf("LegacyScanDir = %q, want %q", got, filepath.Join(dir, ".paircli"))
			}
		})
	}
}

func TestLegacyScanDirIgnoresAFileNamedPrSomething(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".paircli"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".paircli", "pr-7"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LegacyScanDir(dir); got != "" {
		t.Errorf("LegacyScanDir = %q, want empty for a non-directory entry", got)
	}
	if got := LegacyScanDir(""); got != "" {
		t.Errorf("LegacyScanDir(\"\") = %q, want empty", got)
	}
}
