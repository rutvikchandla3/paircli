package publish

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/rutvikchandla3/paircli/internal/capture"
	"github.com/rutvikchandla3/paircli/internal/filecontent"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

// BundleSchemaVersion is bundle.json's schema_version.
const BundleSchemaVersion = 1

// scanFiles are the files of a scan folder that go into a bundle. Anything
// else in the folder (leftovers of earlier tools, generated artifacts) stays
// local: the client never uploads text a model wrote.
var scanFiles = []string{"report.md", "comment.md", "signals.json", "authorship.json", "snapshot.json", "agent-trace.json"}

// zipTime is the modification time of every zip entry.
var zipTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// BundleOptions describes the bundle to build.
type BundleOptions struct {
	Repo          string // "owner/repo"
	Number        int
	Head          string // 40 lowercase hex
	ScanDir       string // the scan output folder
	Raw           *capture.Staged
	CaptureCounts map[string]int   // redaction counts of the raw capture
	ScanCounts    map[string]int   // redaction counts of the scan's rendered text
	Redactor      *redact.Redactor // redacts the scan files; nil = redact.New()
	Version       string
	Now           time.Time // zero = time.Now()
	OutDir        string    // where <head>.zip is written

	// ChangedFiles includes the changed-files/ subtree the scan wrote. It gates
	// on the same option the scan ran with, so a subtree left behind by an
	// earlier run is never uploaded by a run that turned the feature off.
	ChangedFiles bool
}

// Bundle is a built bundle on disk.
type Bundle struct {
	Path        string
	SHA256      string
	Size        int64
	Entries     []string // zip entry names, in order
	Counts      map[string]int
	RawIncluded bool     // false when raw sessions were absent or had to be left out
	Notes       []string // what was left out and why
}

// bundleJSON is bundle.json.
type bundleJSON struct {
	SchemaVersion int           `json:"schema_version"`
	CreatedBy     string        `json:"created_by"`
	CreatedAt     string        `json:"created_at"`
	Repo          string        `json:"repo"`
	PullNumber    int           `json:"pull_number"`
	HeadSHA       string        `json:"head_sha"`
	ScanDir       string        `json:"scan_dir"`
	Redaction     redactionJSON `json:"redaction"`
	RawSessions   bool          `json:"raw_sessions"`
}

type redactionJSON struct {
	Applied      bool           `json:"applied"`
	RulesVersion string         `json:"rules_version"`
	Counts       map[string]int `json:"counts"`
}

type entry struct {
	name string
	data []byte
}

// BuildBundle writes <OutDir>/<head>.zip: bundle.json, the scan folder under
// .paircli/pr-N/<head>/, and the raw-session capture when there is one. The
// zip is deterministic for equal inputs: sorted entries, fixed timestamps and
// modes. Every text file is redacted on its way in. If the zip would exceed
// the 100 MiB upload limit the raw sessions are left out and the bundle
// retried, so a large transcript can never stop a publication.
func BuildBundle(o BundleOptions) (*Bundle, error) {
	files, fileCounts, err := readScanFiles(o)
	if err != nil {
		return nil, err
	}
	withRaw := o.Raw != nil && !o.Raw.Empty()
	b, err := writeBundle(o, files, fileCounts, withRaw)
	if err != nil && withRaw && errors.Is(err, errTooLarge) {
		b, err = writeBundle(o, files, fileCounts, false)
		if err == nil {
			b.Notes = append(b.Notes, "raw sessions left out: the bundle would exceed 100 MiB with them")
		}
	}
	return b, err
}

var errTooLarge = errors.New("bundle exceeds the 100 MiB upload limit")

// readScanFiles reads and redacts the uploadable files of the scan folder.
func readScanFiles(o BundleOptions) ([]entry, map[string]int, error) {
	redactor := o.Redactor
	if redactor == nil {
		redactor = redact.New()
	}
	scanRoot := path.Join(".paircli", fmt.Sprintf("pr-%d", o.Number), o.Head)

	names := append([]string(nil), scanFiles...)
	sessions, err := filepath.Glob(filepath.Join(o.ScanDir, "sessions", "*.json"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(sessions)
	for _, s := range sessions {
		names = append(names, "sessions/"+filepath.Base(s))
	}

	var out []entry
	for _, name := range names {
		full := filepath.Join(o.ScanDir, filepath.FromSlash(name))
		info, err := os.Lstat(full)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("scan output %s is not a regular file", name)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, entry{name: path.Join(scanRoot, name), data: redactor.Bytes(data)})
	}
	if o.ChangedFiles {
		contents, err := readChangedFiles(o, scanRoot, redactor)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, contents...)
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("scan folder %s has no report files", o.ScanDir)
	}
	return out, redactor.Counts(), nil
}

// readChangedFiles reads the changed-files manifest and exactly the files it
// names, and nothing else the subtree may hold.
//
// The manifest is the list, the way scanFiles is the list for the scan folder:
// anything else that happens to sit under changed-files/ stays local.
func readChangedFiles(o BundleOptions, scanRoot string, redactor *redact.Redactor) ([]entry, error) {
	dir := filepath.Join(o.ScanDir, filecontent.DirName)
	raw, err := os.ReadFile(filepath.Join(dir, filecontent.ManifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// The scan was asked for contents and wrote no manifest, so
			// something removed it. Failing is the point: the alternative is a
			// bundle that silently carries no content.
			return nil, fmt.Errorf("scan output %s has no %s/%s", o.ScanDir, filecontent.DirName, filecontent.ManifestName)
		}
		return nil, err
	}
	var m filecontent.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing %s/%s: %w", filecontent.DirName, filecontent.ManifestName, err)
	}
	var out []entry
	// The manifest goes in with the files it names: a consumer with only the
	// paths would have to guess at the sides and the skips.
	out = append(out, entry{
		name: path.Join(scanRoot, filecontent.DirName, filecontent.ManifestName),
		data: redactor.Bytes(raw),
	})
	for _, f := range m.Files {
		for _, side := range []string{filecontent.SideNew, filecontent.SideOld} {
			s, ok := f.Sides[side]
			if !ok {
				continue
			}
			// The manifest is a file on disk, so its names are checked again
			// here rather than trusted: joining an unchecked one to the scan
			// folder would read any path on the machine.
			if filecontent.UnsafeName(s.File) != "" {
				return nil, fmt.Errorf("changed file %q named by the manifest is not a usable path", s.File)
			}
			name := path.Join(filecontent.DirName, s.File)
			full := filepath.Join(o.ScanDir, filepath.FromSlash(name))
			data, err := os.ReadFile(full)
			if err != nil {
				// Unlike an optional scan file, a file the manifest names is
				// promised by it: skipping it would leave the manifest
				// describing a zip that does not match.
				return nil, fmt.Errorf("changed file %s named by the manifest is unreadable: %w", name, err)
			}
			out = append(out, entry{name: path.Join(scanRoot, name), data: redactor.Bytes(data)})
		}
	}
	return out, nil
}

func writeBundle(o BundleOptions, files []entry, fileCounts map[string]int, withRaw bool) (*Bundle, error) {
	entries := append([]entry(nil), files...)
	counts := redact.Merge(o.ScanCounts, fileCounts)
	if withRaw {
		manifest, err := o.Raw.ManifestJSON()
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{name: "raw-sessions/manifest.json", data: manifest})
		for _, f := range o.Raw.Files {
			entries = append(entries, entry{name: path.Join("raw-sessions", f.Path), data: f.Data})
		}
		counts = redact.Merge(counts, o.CaptureCounts)
	}

	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	meta, err := json.MarshalIndent(bundleJSON{
		SchemaVersion: BundleSchemaVersion,
		CreatedBy:     "paircli " + o.Version,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Repo:          o.Repo,
		PullNumber:    o.Number,
		HeadSHA:       o.Head,
		ScanDir:       path.Join(".paircli", fmt.Sprintf("pr-%d", o.Number), o.Head),
		Redaction:     redactionJSON{Applied: true, RulesVersion: redact.RulesVersion, Counts: counts},
		RawSessions:   withRaw,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	entries = append(entries, entry{name: "bundle.json", data: append(meta, '\n')})
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	if err := os.MkdirAll(o.OutDir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(o.OutDir, ".bundle-*.tmp")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed

	digest := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(tmp, digest)}
	if err := writeZip(counter, entries); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if counter.n < 1 || counter.n > MaxBundleBytes {
		return nil, errTooLarge
	}
	dest := filepath.Join(o.OutDir, o.Head+".zip")
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return nil, err
	}

	b := &Bundle{
		Path:        dest,
		SHA256:      hex.EncodeToString(digest.Sum(nil)),
		Size:        counter.n,
		Counts:      counts,
		RawIncluded: withRaw,
	}
	for _, e := range entries {
		b.Entries = append(b.Entries, e.name)
	}
	return b, nil
}

// writeZip writes entries, already sorted, as a deterministic zip.
func writeZip(w io.Writer, entries []entry) error {
	zw := zip.NewWriter(w)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: zipTime}
		header.SetMode(0o644)
		fw, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := fw.Write(e.data); err != nil {
			return err
		}
	}
	return zw.Close()
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
