package deviceauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStore_RoundTrip checks Save/Load preserve every field and the file is
// 0600 in a 0700 directory.
func TestStore_RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "paircli")
	path := filepath.Join(dir, "auth.json")

	expires := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	want := StoredAuth{
		AccessToken: "tok-secret",
		TokenType:   "bearer",
		ExpiresAt:   expires,
		ServerURL:   "https://pair.example",
		User:        "Ada Lovelace",
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 700", perm)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

// TestSave_Overwrites checks a second Save replaces the file atomically and
// leaves no temp files behind.
func TestSave_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	if err := Save(path, StoredAuth{AccessToken: "one"}); err != nil {
		t.Fatalf("Save one: %v", err)
	}
	if err := Save(path, StoredAuth{AccessToken: "two"}); err != nil {
		t.Fatalf("Save two: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != "two" {
		t.Errorf("token = %q, want two", got.AccessToken)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "auth.json" {
		t.Errorf("leftover files: %v", entries)
	}
}

// TestLoad_Missing checks a missing file is an error, which callers read as
// "not logged in".
func TestLoad_Missing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("Load of a missing file should error")
	}
}

// TestRemove_Idempotent checks Remove works whether or not the file is there.
func TestRemove_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := Remove(path); err != nil {
		t.Fatalf("Remove absent: %v", err)
	}
	if err := Save(path, StoredAuth{AccessToken: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Remove(path); err != nil {
		t.Fatalf("Remove present: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still present: %v", err)
	}
}

// TestDefaultFile checks the PAIRCLI_AUTH_FILE override and the home fallback.
func TestDefaultFile(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv(EnvFile, override)
	if got := DefaultFile(); got != override {
		t.Errorf("DefaultFile = %q, want %q", got, override)
	}

	t.Setenv(EnvFile, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := DefaultFile(); got != filepath.Join(home, ".paircli", "auth.json") {
		t.Errorf("DefaultFile = %q, want the home path", got)
	}
}

// TestResolve_EnvWins checks PAIRCLI_AUTH_TOKEN beats the stored file.
func TestResolve_EnvWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	t.Setenv(EnvFile, path)
	if err := Save(path, StoredAuth{AccessToken: "from-file", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv(EnvToken, "from-env")

	got, ok := Resolve()
	if !ok || got.AccessToken != "from-env" {
		t.Errorf("Resolve = %+v/%v, want the env token", got, ok)
	}
}

// TestResolve_FileStates covers valid, expired and absent stored tokens.
func TestResolve_FileStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	t.Setenv(EnvFile, path)
	t.Setenv(EnvToken, "")

	if _, ok := Resolve(); ok {
		t.Error("Resolve with no file should report not logged in")
	}

	if err := Save(path, StoredAuth{AccessToken: "expired", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := Resolve(); ok {
		t.Error("an expired stored token should report not logged in")
	}

	if err := Save(path, StoredAuth{AccessToken: "live", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := Resolve()
	if !ok || got.AccessToken != "live" {
		t.Errorf("Resolve = %+v/%v, want the live stored token", got, ok)
	}
}
