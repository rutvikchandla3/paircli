package capture

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/rutvikchandla3/paircli/internal/state"
)

// cursorsName is the file under the state dir that holds every cursor, and the
// name of the lock that serializes writers of it.
const cursorsName = "capture-cursors"

const cursorsSchema = 1

// lockTimeout bounds the wait for the cursor file's lock.
const lockTimeout = 30 * time.Second

// cursor records how far one source file has been captured by a successful
// publication. PrefixSHA256 covers bytes [0, Offset) so a rewritten or
// rotated file is detected; Offset always sits just after a newline.
// Head and HeadStart remember which head's publication advanced the cursor and
// where its range began, so republishing that head repeats the same range.
type cursor struct {
	Path         string    `json:"path"`
	Session      string    `json:"session"`
	Offset       int64     `json:"offset"`
	PrefixSHA256 string    `json:"prefix_sha256"`
	Head         string    `json:"head"`
	HeadStart    int64     `json:"head_start"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// cursorFile is the on-disk shape: cursors per scope (one PR on one server),
// keyed by the SHA-256 of the source file's resolved path.
type cursorFile struct {
	SchemaVersion int                          `json:"schema_version"`
	Scopes        map[string]map[string]cursor `json:"scopes"`
}

func cursorsPath() string { return filepath.Join(state.Dir(), cursorsName+".json") }

// loadCursors reads the cursor file. A missing, malformed or foreign-version
// file is the empty one: capture then starts from the beginning, which costs
// bytes but never loses any.
func loadCursors() cursorFile {
	empty := cursorFile{SchemaVersion: cursorsSchema, Scopes: map[string]map[string]cursor{}}
	data, err := os.ReadFile(cursorsPath())
	if err != nil {
		return empty
	}
	var f cursorFile
	if json.Unmarshal(data, &f) != nil || f.SchemaVersion != cursorsSchema || f.Scopes == nil {
		return empty
	}
	return f
}

// saveCursors merges next into scope's cursors under the file lock, so
// publications of different PRs never overwrite each other's progress.
func saveCursors(scope string, next map[string]cursor) error {
	lock, err := state.WaitLock(cursorsName, lockTimeout)
	if err != nil {
		return err
	}
	defer lock.Release()

	f := loadCursors()
	if f.Scopes[scope] == nil {
		f.Scopes[scope] = map[string]cursor{}
	}
	for key, c := range next {
		f.Scopes[scope][key] = c
	}
	if err := state.WriteJSONAtomic(cursorsPath(), f); err != nil {
		return errors.New("capture: writing cursors: " + err.Error())
	}
	return nil
}
