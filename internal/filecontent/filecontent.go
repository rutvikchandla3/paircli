// Package filecontent writes the whole text of a pull request's changed files
// into the scan folder, beside the diff the snapshot already carries.
//
// It exists because a diff hunk holds about three lines of context, and more is
// needed: a reader has to be able to see a changed line inside the function
// around it. What this package produces is redacted, size-capped and, when a
// file is too large to send whole, cropped to windows around the changes.
//
// The result is one JSON manifest plus one file per side of each changed file:
//
//	changed-files/manifest.json
//	changed-files/new/<repo path>   the file at the PR head
//	changed-files/old/<repo path>   the file at the merge base
//
// Everything here degrades rather than fails. A file that cannot be read is
// recorded as skipped with a reason; only a programming error returns one.
package filecontent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

// DirName is the subtree of the scan folder these files are written to.
const DirName = "changed-files"

// ManifestName is the file the subtree describes itself with.
const ManifestName = "manifest.json"

// SchemaVersion is the manifest's schema_version.
const SchemaVersion = 1

// base kinds.
const (
	baseMergeBase = "merge_base"
)

// Skip reasons, exactly the set the manifest may carry.
const (
	ReasonMedia      = "media"       // an image, recording, archive, compiled file: never fetched
	ReasonBinary     = "binary"      // the bytes are not text
	ReasonTooLarge   = "too_large"   // over a cap, see Limit
	ReasonBudget     = "budget"      // the artifact's running total was already spent
	ReasonMissing    = "missing"     // the file is not at that revision
	ReasonError      = "error"       // the read failed
	ReasonNoBase     = "no_base"     // the merge base could not be resolved
	ReasonLineShift  = "line_shift"  // redaction changed the line count
	ReasonCollision  = "collision"   // another path differs only by case
	ReasonUnsafePath = "unsafe_path" // the path cannot be stored or extracted safely
)

// Limit names which cap produced a too_large or budget skip, and which one
// cropped a partial side. too_large alone cannot say.
const (
	LimitDownloadBytes = "download_bytes"
	LimitFileBytes     = "file_bytes"
	LimitLineBytes     = "line_bytes"
	LimitTotalBytes    = "total_bytes"
	LimitSides         = "sides"
)

// Limits caps one artifact. The zero value is not usable; call DefaultLimits.
type Limits struct {
	MaxFileBytes     int64 `json:"max_file_bytes"`     // written per side
	MaxDownloadBytes int64 `json:"max_download_bytes"` // read per side, before any cropping
	MaxTotalBytes    int64 `json:"max_total_bytes"`    // written across the artifact
	MaxSides         int   `json:"max_sides"`          // sides written
	MaxLineBytes     int   `json:"max_line_bytes"`     // one kept line
	WindowMargin     int   `json:"window_margin"`      // lines either side of a change
}

// DefaultLimits is what a scan uses unless told otherwise.
//
// MaxSides exists because the service rejects a whole bundle over 10,000
// archive members, counted across everything in it: a 3,000-file PR doubles to
// 6,000 content entries on top of the scan files and the raw sessions. No byte
// budget bounds that.
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:     1 << 20,
		MaxDownloadBytes: 20 << 20,
		MaxTotalBytes:    16 << 20,
		MaxSides:         6000,
		MaxLineBytes:     8 << 10,
		WindowMargin:     300,
	}
}

// Manifest is changed-files/manifest.json.
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Tool          string `json:"tool"`
	CreatedAt     string `json:"created_at"`
	Repo          string `json:"repo"`
	PullNumber    int    `json:"pull_number"`

	HeadSHA string `json:"head_sha"`
	BaseSHA string `json:"base_sha"`
	BaseRef string `json:"base_ref"`
	// BaseKind is how BaseSHA was arrived at. Always "merge_base" when there is
	// one: the old side has to be the file GitHub diffed against, or its line
	// numbers do not line up with the hunks.
	BaseKind string `json:"base_kind"`
	// BaseSource is "api" or "git", a debugging aid.
	BaseSource string `json:"base_source,omitempty"`

	Redaction Redaction      `json:"redaction"`
	Limits    Limits         `json:"limits"`
	Totals    Totals         `json:"totals"`
	Files     []ManifestFile `json:"files"`
}

// Redaction describes what the content pass removed.
type Redaction struct {
	RulesVersion string         `json:"rules_version"`
	Counts       map[string]int `json:"counts,omitempty"`
}

// Totals describes the whole artifact.
type Totals struct {
	Files        int   `json:"files"`
	SidesWritten int   `json:"sides_written"`
	SidesSkipped int   `json:"sides_skipped"`
	SidesPartial int   `json:"sides_partial"`
	Bytes        int64 `json:"bytes"`
}

// ManifestFile is one changed file, in the same order as the snapshot's
// pr.files, keyed by the same path.
type ManifestFile struct {
	Path    string           `json:"path"`
	OldPath string           `json:"old_path,omitempty"`
	Status  model.FileStatus `json:"status"`

	// Sides holds the sides that were written, keyed by SideNew and SideOld.
	// It is never null, so a consumer can index it without a nil check.
	Sides map[string]Side `json:"sides"`
	// Skipped holds a reason for every side the status calls for but that Sides
	// does not have, so the two together are always exactly the expected set.
	Skipped map[string]Skipped `json:"skipped,omitempty"`
}

// Side is one side of one file as written.
type Side struct {
	File   string `json:"file"` // relative to changed-files/
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Lines  int    `json:"lines"`

	// Partial is true when the side was cropped. Ranges is then the list of
	// kept ranges, 1-based and inclusive in the original file's numbering, and
	// walking it in order reconstructs Bytes exactly.
	Partial bool     `json:"partial"`
	Ranges  [][2]int `json:"ranges"`
	// Limit names why a partial side was cropped: LimitFileBytes or
	// LimitTotalBytes.
	Limit string `json:"limit,omitempty"`

	// FullLines and FullBytes describe the whole redacted file, which is what
	// the diff's line numbers index. Equal to Lines and Bytes when not partial.
	FullLines int `json:"full_lines"`
	FullBytes int `json:"full_bytes"`

	// Source is "git" or "api".
	Source string `json:"source"`

	data []byte // the bytes to write; never marshalled
}

// Skipped is a side that has no content, and why.
type Skipped struct {
	Reason string `json:"reason"`
	// Limit names which cap produced a too_large or budget reason.
	Limit string `json:"limit,omitempty"`
	// Bytes is the known size of the file that was left out, when known.
	Bytes int64 `json:"bytes,omitempty"`
	// Error is the failure, for ReasonError.
	Error string `json:"error,omitempty"`
}

// Write writes the manifest and every file it names into dir/changed-files.
//
// A file the manifest names but that could not be written is an error, not a
// skip: the manifest's whole job is to describe what is on disk.
func Write(dir string, m *Manifest) error {
	if m == nil {
		return errors.New("filecontent: write: nil manifest")
	}
	if dir == "" {
		return errors.New("filecontent: write: no directory")
	}
	root := filepath.Join(dir, DirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("filecontent: write: %w", err)
	}
	for _, f := range m.Files {
		for _, side := range []string{SideNew, SideOld} {
			s, ok := f.Sides[side]
			if !ok {
				continue
			}
			if UnsafeName(s.File) != "" {
				return fmt.Errorf("filecontent: write: %q is not a usable path", s.File)
			}
			path := filepath.Join(root, filepath.FromSlash(s.File))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("filecontent: write: %w", err)
			}
			if err := os.WriteFile(path, s.data, 0o644); err != nil {
				return fmt.Errorf("filecontent: write %s: %w", s.File, err)
			}
		}
	}
	if m.Files == nil {
		m.Files = []ManifestFile{}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("filecontent: marshal manifest: %w", err)
	}
	path := filepath.Join(root, ManifestName)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("filecontent: write %s: %w", path, err)
	}
	return nil
}

// Remove deletes the subtree Write creates, when it is there. A scan that was
// not asked for content must not leave an earlier run's behind for a later
// bundle to pick up.
func Remove(dir string) error {
	if dir == "" {
		return nil
	}
	err := os.RemoveAll(filepath.Join(dir, DirName))
	if err != nil {
		return fmt.Errorf("filecontent: remove: %w", err)
	}
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newManifest starts a manifest for one scan.
func newManifest(o Options) *Manifest {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	pr := o.PR
	baseRef := ""
	if pr != nil {
		baseRef = pr.BaseRef
	}
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		Tool:          o.Tool,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Repo:          o.Repo,
		PullNumber:    o.Number,
		BaseRef:       baseRef,
		Redaction:     Redaction{RulesVersion: redact.RulesVersion},
		Limits:        o.limits(),
		Files:         []ManifestFile{},
	}
	m.HeadSHA = o.HeadSHA
	if m.HeadSHA == "" && pr != nil {
		m.HeadSHA = pr.HeadSHA
	}
	return m
}
