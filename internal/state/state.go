// Package state is paircli's on-disk operational state: one record per
// published PR, plus the locks that keep a PR from being published twice at
// once. It holds no credentials (those live in deviceauth) and no evidence
// (that lives in the scan folder).
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// SchemaVersion is written into every record.
const SchemaVersion = 1

// Publication states, as seen by `paircli status`.
const (
	StateRunning    = "running"
	StateUploaded   = "uploaded"
	StateProcessing = "processing"
	StateReady      = "ready"
	StateFailed     = "failed"
)

// Publication steps, in the order `paircli publish` runs them.
const (
	StepScan     = "scan"
	StepCapture  = "capture"
	StepPackage  = "package"
	StepUpload   = "upload"
	StepFinalize = "finalize"
	StepAnalysis = "analysis"
)

// ErrLocked is returned by TryLock when another process holds the lock.
var ErrLocked = errors.New("state: locked by another process")

// Publication is the record of one PR's most recent publication attempt.
type Publication struct {
	SchemaVersion int       `json:"schema_version"`
	PRURL         string    `json:"pr_url"`
	Owner         string    `json:"owner"`
	Repo          string    `json:"repo"`
	Number        int       `json:"number"`
	HeadSHA       string    `json:"head_sha"`
	State         string    `json:"state"`
	Step          string    `json:"step"`
	Error         string    `json:"error"`
	PublicationID string    `json:"publication_id"`
	AnalysisJobID string    `json:"analysis_job_id"`
	AnalysisState string    `json:"analysis_state"`
	Log           string    `json:"log"`
	ReviewURL     string    `json:"review_url"`
	StartedAt     time.Time `json:"started_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ReviewURL is where a PR's review lives on pair.sh.
func ReviewURL(owner, repo string, number int) string {
	return fmt.Sprintf("https://pair.sh/%s/%s/pull/%d", owner, repo, number)
}

// Dir returns ~/.paircli/state. Tests point HOME at a temp dir instead of
// overriding the path.
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paircli", "state")
}

// LogDir returns the directory background runs write their logs to.
func LogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".paircli", "logs")
}

// Key is the file-name stem shared by a PR's record and lock.
func Key(owner, repo string, number int) string {
	return fmt.Sprintf("%s__%s__%d", strings.ToLower(owner), strings.ToLower(repo), number)
}

func publicationPath(owner, repo string, number int) string {
	return filepath.Join(Dir(), "publications", Key(owner, repo, number)+".json")
}

// WritePublication stamps SchemaVersion and UpdatedAt and replaces the PR's
// record atomically.
func WritePublication(p Publication) error {
	p.SchemaVersion = SchemaVersion
	p.UpdatedAt = time.Now().UTC()
	if p.StartedAt.IsZero() {
		p.StartedAt = p.UpdatedAt
	}
	return WriteJSONAtomic(publicationPath(p.Owner, p.Repo, p.Number), p)
}

// ReadPublication loads a PR's record. A missing record is os.ErrNotExist.
func ReadPublication(owner, repo string, number int) (Publication, error) {
	var p Publication
	data, err := os.ReadFile(publicationPath(owner, repo, number))
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(data, &p)
}

// ListPublications returns every readable record, most recently updated first.
// A malformed record is skipped rather than failing the listing.
func ListPublications() ([]Publication, error) {
	entries, err := os.ReadDir(filepath.Join(Dir(), "publications"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Publication
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(Dir(), "publications", entry.Name()))
		if err != nil {
			continue
		}
		var p Publication
		if json.Unmarshal(data, &p) == nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// WriteJSONAtomic writes v as indented JSON to path via a temp file and rename,
// creating the directory 0700 and the file 0600.
func WriteJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(append(data, '\n')); err != nil {
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
	return os.Rename(tmp.Name(), path)
}

// Lock is an advisory, process-scoped lock. The kernel drops it if the process dies.
type Lock struct{ file *os.File }

// TryLock takes the named lock without blocking. It returns ErrLocked when
// another process holds it.
func TryLock(name string) (*Lock, error) {
	dir := filepath.Join(Dir(), "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{file: file}, nil
}

// Release drops the lock. It is safe to call more than once.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

// WaitLock is TryLock that keeps trying until timeout. It guards short
// read-modify-write sections of shared files (revision journal, capture
// cursors); it returns ErrLocked when the timeout passes.
func WaitLock(name string, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		l, err := TryLock(name)
		if !errors.Is(err, ErrLocked) {
			return l, err
		}
		if time.Now().After(deadline) {
			return nil, ErrLocked
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// PendingRequest records that a publish was asked for while another run of the
// same PR held its lock, so the running one must go round again.
type PendingRequest struct {
	PRURL       string    `json:"pr_url"`
	RepoDir     string    `json:"repo_dir"`
	RequestedAt time.Time `json:"requested_at"`
}

func pendingPath(key string) string { return filepath.Join(Dir(), "locks", key+".pending") }

// MarkPending leaves the marker for the run that holds key's lock. Marking
// twice keeps one marker, the later request.
func MarkPending(key string, req PendingRequest) error {
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now().UTC()
	}
	return WriteJSONAtomic(pendingPath(key), req)
}

// HasPending reports whether a marker is waiting for key.
func HasPending(key string) bool {
	_, err := os.Stat(pendingPath(key))
	return err == nil
}

// TakePending removes key's marker and returns it. An unreadable marker still
// counts as a request, with no details.
func TakePending(key string) (PendingRequest, bool) {
	var req PendingRequest
	data, err := os.ReadFile(pendingPath(key))
	if err != nil {
		return req, false
	}
	_ = json.Unmarshal(data, &req)
	if err := os.Remove(pendingPath(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return req, false
	}
	return req, true
}
