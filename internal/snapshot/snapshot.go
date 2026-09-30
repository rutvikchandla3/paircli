// Package snapshot persists a complete record of one scan: the PR, the linked
// sessions, the attribution, the commit links and the config that was in
// effect. Rebuilding an engine.Context from it reproduces the scan's
// deterministic output, which is what lets the grounded LLM pass run later, or
// on another machine, instead of during the scan.
//
// Two things make the record faithful rather than approximate:
//
//   - It is taken after linking, so the fields the pipeline computes rather
//     than parses are already set and unmarshal verbatim: a session's Start and
//     End, each event's Seq, Turn and Final flag, and every payload's RelPath.
//     Re-deriving those would need the repo root and a second link pass.
//   - It stores the merged config, not .paircli.json. config.Load appends list
//     fields to the defaults and only replaces non-zero scalars, so the file
//     alone cannot reproduce the config a scan actually ran with.
//
// Timeline is deliberately not stored: engine.NewContext rebuilds it from the
// sessions, taking pointers into the same events.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// Version is the snapshot schema version. Read and Context reject a snapshot
// written by a different version rather than guessing at its shape.
const Version = "1"

// FileName is the file a snapshot is written to inside a scan's output dir.
const FileName = "snapshot.json"

// Snapshot is one scan's full state.
type Snapshot struct {
	// SchemaVersion is the format this file was written in (Version).
	SchemaVersion string `json:"schema_version"`
	// Tool is the paircli version that produced it. Detectors change between
	// releases, so a replay must check this before trusting the result.
	Tool string `json:"tool"`

	PR           *model.PR                   `json:"pr"`
	Sessions     []*model.Session            `json:"sessions"`
	Attribution  *model.Attribution          `json:"attribution"`
	Links        []model.CommitLink          `json:"links"`
	SessionLinks map[string]model.LinkMethod `json:"session_links,omitempty"`

	// Judgments is set only when a scan wrote its snapshot after the LLM pass.
	Judgments *model.Judgments `json:"judgments,omitempty"`

	Dropped      int            `json:"dropped"`
	Unattributed []string       `json:"unattributed,omitempty"`
	Config       *config.Config `json:"config"`

	// Hash is a digest of this record with Hash cleared, so a set of judgments
	// can be tied to the exact input that produced it.
	Hash string `json:"hash"`
}

// Input is what Build needs: the pieces of one scan, in the order the pipeline
// produced them.
type Input struct {
	PR           *model.PR
	Sessions     []*model.Session
	Attribution  *model.Attribution
	Links        []model.CommitLink
	SessionLinks map[string]model.LinkMethod
	Dropped      int
	Unattributed []string
	Config       *config.Config
	Tool         string
	Judgments    *model.Judgments
}

// Build assembles a snapshot and stamps its hash.
func Build(in Input) *Snapshot {
	s := &Snapshot{
		SchemaVersion: Version,
		Tool:          in.Tool,
		PR:            in.PR,
		Sessions:      in.Sessions,
		Attribution:   in.Attribution,
		Links:         in.Links,
		SessionLinks:  in.SessionLinks,
		Judgments:     in.Judgments,
		Dropped:       in.Dropped,
		Unattributed:  in.Unattributed,
		Config:        in.Config,
	}
	s.Hash = s.computeHash()
	return s
}

// computeHash digests the record with Hash cleared. encoding/json orders struct
// fields by declaration and sorts map keys, so the digest is stable.
func (s *Snapshot) computeHash() string {
	plain := *s
	plain.Hash = ""
	raw, err := json.Marshal(plain)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Write writes s to dir/FileName. It follows the same JSON convention as the
// other artifacts: two-space indent and exactly one trailing newline.
func Write(dir string, s *Snapshot) error {
	if s == nil {
		return fmt.Errorf("snapshot: write: nil snapshot")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("snapshot: marshal: %w", err)
	}
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("snapshot: write %s: %w", path, err)
	}
	return nil
}

// Read reads and parses the snapshot at path, rejecting a different schema
// version.
func Read(path string) (*Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("snapshot: read %s: %w", path, err)
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("snapshot: parse %s: %w", path, err)
	}
	if s.SchemaVersion != Version {
		return nil, fmt.Errorf("snapshot: %s has schema version %q, want %q",
			path, s.SchemaVersion, Version)
	}
	return &s, nil
}

// Context rebuilds the engine context this snapshot was taken from. Timeline is
// rebuilt from the sessions rather than stored.
//
// Detectors register themselves from package init(), so a caller that rebuilds a
// context outside the paircli binary must import internal/scan (which blank-
// imports every internal/detect package) as well as this one. Without that
// import engine.Run finds an empty registry and returns no signals at all —
// silently, with no error. That is the first thing to check when a replayed
// context produces an empty report.
func (s *Snapshot) Context() (*engine.Context, error) {
	if s == nil {
		return nil, fmt.Errorf("snapshot: context: nil snapshot")
	}
	if s.SchemaVersion != Version {
		return nil, fmt.Errorf("snapshot: schema version %q, want %q", s.SchemaVersion, Version)
	}
	cfg := s.Config
	if cfg == nil {
		cfg = config.Default()
	}
	ec := engine.NewContext(s.PR, s.Sessions, s.Attribution, s.Links, cfg)
	ec.SessionLinks = s.SessionLinks
	ec.Judgments = s.Judgments
	return ec, nil
}
