package publish

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/rutvikchandla3/paircli/internal/state"
)

const (
	journalName   = "revisions"
	journalSchema = 1
	journalLock   = 30 * time.Second
)

// nowNanos is the clock revisions are drawn from; tests replace it.
var nowNanos = func() int64 { return time.Now().UnixNano() }

type journalEntry struct {
	Server   string `json:"server"`
	Head     string `json:"head"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Revision int64  `json:"revision"`
}

type journalFile struct {
	SchemaVersion int            `json:"schema_version"`
	Entries       []journalEntry `json:"entries"`
}

func journalPath() string { return filepath.Join(state.Dir(), journalName+".json") }

// Revision returns the publication revision for a bundle. The same {server,
// head, sha256, size} always gets the same revision, so a replayed publish is
// recognized by the server; new content gets max(clock, latest+1) for that
// server, so revisions keep rising across heads even if the clock moves back.
// The journal holds bundle metadata only, and is read and written under a file
// lock so concurrent publishes of different PRs cannot share a revision.
func Revision(server, head, sha256 string, size int64) (int64, error) {
	lock, err := state.WaitLock(journalName, journalLock)
	if err != nil {
		return 0, err
	}
	defer lock.Release()

	var j journalFile
	if data, err := os.ReadFile(journalPath()); err == nil {
		// A journal that cannot be read is replaced: revisions only need to
		// rise, and the clock alone guarantees that.
		if json.Unmarshal(data, &j) != nil || j.SchemaVersion != journalSchema {
			j = journalFile{}
		}
	}

	var latest int64
	for _, e := range j.Entries {
		if e.Server != server {
			continue
		}
		if e.Head == head && e.SHA256 == sha256 && e.Size == size {
			return e.Revision, nil
		}
		if e.Revision > latest {
			latest = e.Revision
		}
	}
	revision := nowNanos()
	if revision <= latest {
		revision = latest + 1
	}
	if revision <= 0 || revision == math.MaxInt64 {
		return 0, errors.New("local publication revision is outside the backend integer range")
	}
	j.SchemaVersion = journalSchema
	j.Entries = append(j.Entries, journalEntry{Server: server, Head: head, SHA256: sha256, Size: size, Revision: revision})
	if err := state.WriteJSONAtomic(journalPath(), j); err != nil {
		return 0, err
	}
	return revision, nil
}
