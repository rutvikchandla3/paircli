// Package capture stages the raw agent transcripts behind a PR for upload: for
// each linked Claude Code or Codex session, the bytes of its transcript files
// that are new since the last successful publication, redacted before they
// are hashed.
//
// Capture is two-phase. Stage reads and redacts but changes nothing on disk;
// the cursors that record how far each file has been captured only advance
// when the caller reports the publication finalized by calling Commit. A
// failed publication therefore re-sends the same bytes next time.
package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

// MaxBytes caps the raw transcript bytes one publication carries. A range
// that would pass it is dropped with a note; it never fails the publication.
const MaxBytes = 80 << 20

// SchemaVersion is the manifest's schema_version.
const SchemaVersion = 1

var headRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Options configures one Stage.
type Options struct {
	Head     string           // PR head commit, 40 lowercase hex
	Scope    string           // cursor namespace: one PR on one server
	Sessions []*model.Session // sessions linked to the PR
	Redactor *redact.Redactor // nil = redact.New()
	MaxBytes int64            // 0 = MaxBytes
	Now      time.Time        // cursor timestamp; zero = time.Now()
}

// Manifest is raw-sessions/manifest.json.
type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	HeadSHA       string            `json:"head_sha"`
	Sessions      []ManifestSession `json:"sessions"`
}

// ManifestSession lists one session's captured files.
type ManifestSession struct {
	Harness   string         `json:"harness"`
	SessionID string         `json:"session_id"`
	CWD       string         `json:"cwd"`
	Files     []ManifestFile `json:"files"`
}

// ManifestFile is one captured byte range. Path is relative to raw-sessions/,
// SHA256 and Bytes describe the redacted content, and the offsets are in the
// original file.
type ManifestFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Bytes       int    `json:"bytes"`
	StartOffset int64  `json:"start_offset"`
	EndOffset   int64  `json:"end_offset"`
	Kind        string `json:"kind"`
}

// File is one staged file, ready to be written under raw-sessions/.
type File struct {
	Path string // relative to raw-sessions/
	Data []byte
}

// Staged is the result of Stage.
type Staged struct {
	Manifest Manifest
	Files    []File
	Notes    []string // what was skipped or reset, for the publish log

	scope  string
	cursor map[string]cursor
}

// Empty reports whether nothing new was captured.
func (s *Staged) Empty() bool { return len(s.Files) == 0 }

// ManifestJSON is the bytes of raw-sessions/manifest.json.
func (s *Staged) ManifestJSON() ([]byte, error) {
	data, err := json.MarshalIndent(s.Manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Commit advances the cursors past everything Stage read. Call it only after
// the publication is finalized.
func (s *Staged) Commit() error {
	if len(s.cursor) == 0 {
		return nil
	}
	return saveCursors(s.scope, s.cursor)
}

// Stage captures the new transcript ranges of o.Sessions.
func Stage(o Options) (*Staged, error) {
	if !headRe.MatchString(o.Head) {
		return nil, fmt.Errorf("capture: head %q is not a 40-hex commit", o.Head)
	}
	redactor := o.Redactor
	if redactor == nil {
		redactor = redact.New()
	}
	budget := o.MaxBytes
	if budget <= 0 {
		budget = MaxBytes
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}

	total := budget
	prior := loadCursors().Scopes[o.Scope]
	out := &Staged{
		Manifest: Manifest{SchemaVersion: SchemaVersion, HeadSHA: o.Head, Sessions: []ManifestSession{}},
		scope:    o.Scope,
		cursor:   map[string]cursor{},
	}

	sessions := append([]*model.Session(nil), o.Sessions...)
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Ref() < sessions[j].Ref() })
	seenSession := map[string]bool{}
	for _, s := range sessions {
		if s == nil || seenSession[s.Ref()] || (s.Harness != model.HarnessClaudeCode && s.Harness != model.HarnessCodex) {
			continue
		}
		seenSession[s.Ref()] = true
		entry := ManifestSession{Harness: string(s.Harness), SessionID: s.ID, CWD: s.CWD}
		names := map[string]bool{}
		for _, src := range sourcesFor(s) {
			real, err := resolve(src.path)
			if err != nil {
				continue // vanished, a symlink or not a file: nothing to capture
			}
			key := pathKey(real)
			res, err := readSource(real, prior[key], o.Head, budget)
			if err != nil {
				out.Notes = append(out.Notes, fmt.Sprintf("skipped %s: %v", filepath.Base(src.path), err))
				continue
			}
			if res.reset {
				out.Notes = append(out.Notes, fmt.Sprintf("%s changed since the last capture; captured it from the start", filepath.Base(src.path)))
			}
			out.cursor[key] = cursor{
				Path: real, Session: s.Ref(), Offset: res.end, PrefixSHA256: res.sum,
				Head: o.Head, HeadStart: res.start, UpdatedAt: now.UTC(),
			}
			if res.dropped {
				out.Notes = append(out.Notes, fmt.Sprintf("skipped %s: it does not fit the %d-byte raw-session budget", filepath.Base(src.path), total))
				continue
			}
			if len(res.data) == 0 {
				continue
			}
			budget -= int64(len(res.data))
			data := redactor.Bytes(res.data)
			name := safeComponent(filepath.Base(src.path))
			if names[name] {
				name = key[:8] + "-" + name
			}
			names[name] = true
			rel := path.Join(string(s.Harness), safeComponent(s.ID), name)
			sum := sha256.Sum256(data)
			entry.Files = append(entry.Files, ManifestFile{
				Path: rel, SHA256: hex.EncodeToString(sum[:]), Bytes: len(data),
				StartOffset: res.start, EndOffset: res.end, Kind: src.kind,
			})
			out.Files = append(out.Files, File{Path: rel, Data: data})
		}
		if len(entry.Files) > 0 {
			out.Manifest.Sessions = append(out.Manifest.Sessions, entry)
		}
	}
	return out, nil
}
