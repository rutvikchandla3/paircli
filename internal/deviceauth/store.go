package deviceauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// EnvToken names the environment variable that overrides the stored credential
// entirely: when it is set, paircli is logged in as that token and the file is
// never read.
const EnvToken = "PAIRCLI_AUTH_TOKEN"

// EnvFile names the environment variable that overrides the token file's path,
// mirroring hooklog's PAIRCLI_EVENTS_DIR. Tests set it so they never touch the
// real ~/.paircli/auth.json.
const EnvFile = "PAIRCLI_AUTH_FILE"

// StoredAuth is the on-disk auth.json. Nothing outside this package should
// read it.
type StoredAuth struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
	ServerURL   string    `json:"server_url"`
	User        string    `json:"user,omitempty"`
}

// DefaultFile returns $PAIRCLI_AUTH_FILE if set, else ~/.paircli/auth.json.
func DefaultFile() string {
	if p := os.Getenv(EnvFile); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paircli", "auth.json")
}

// Save writes a to path via a temp file and rename, so a reader never sees a
// half-written token. The directory is created 0700 and the file is always
// 0600: it holds a credential.
func Save(path string, a StoredAuth) error {
	if path == "" {
		return errors.New("deviceauth: no auth file path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "auth-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Load reads the stored auth. A missing or malformed file is an error; callers
// read that as "not logged in".
func Load(path string) (StoredAuth, error) {
	if path == "" {
		return StoredAuth{}, errors.New("deviceauth: no auth file path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return StoredAuth{}, err
	}
	var a StoredAuth
	if err := json.Unmarshal(data, &a); err != nil {
		return StoredAuth{}, err
	}
	return a, nil
}

// Remove deletes the stored auth. It is idempotent: a file that is already
// gone is not an error.
func Remove(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Resolve returns the credential to use: $PAIRCLI_AUTH_TOKEN when set,
// otherwise the stored file. A stored token whose expiry has passed counts as
// not logged in. The bool is false when neither source yields a live token.
func Resolve() (StoredAuth, bool) {
	if tok := os.Getenv(EnvToken); tok != "" {
		return StoredAuth{AccessToken: tok, TokenType: "bearer"}, true
	}
	a, err := Load(DefaultFile())
	if err != nil {
		return StoredAuth{}, false
	}
	if a.AccessToken == "" {
		return StoredAuth{}, false
	}
	if !a.ExpiresAt.IsZero() && time.Now().After(a.ExpiresAt) {
		return StoredAuth{}, false
	}
	return a, true
}
