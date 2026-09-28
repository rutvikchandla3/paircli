// Package agenttrace exports paircli's AUTH-1 line attribution as an Agent
// Trace record (https://agent-trace.dev/), so other tools can consume the
// attribution without a paircli-specific format.
//
// The package follows Agent Trace specification version 0.1.0 as published at
// https://agent-trace.dev/ (§6 Core Specification). One Trace Record carries a
// files[] entry per attributed PR file; a file carries one conversation per
// (contributor type, source session) pair it was attributed to, and each
// conversation carries the contiguous line ranges that pair produced.
//
// A conversation is scoped to one contributor type because the spec puts the
// contributor at the conversation level: a session that produced both plainly
// agent lines and human-reworked ("mixed") lines gets one conversation per
// type, so no range ever needs a per-range contributor override.
//
// The record holds no source text: file paths, session references, harness
// names, transcript paths and line numbers only. Transcript content is never
// embedded, so the file is safe to hand to another tool.
package agenttrace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// SpecVersion is the Agent Trace specification version this package emits.
// The spec's schema (https://agent-trace.dev/ §6.1) documents an example
// "0.1.0" and an example record using "0.1.0" in §6.2 and Appendix A, so the
// full three-component version string is what goes in version.
const SpecVersion = "0.1.0"

// FileName is the record written into the scan output folder.
const FileName = "agent-trace.json"

// MIMEType is the media type the spec registers for a Trace Record.
const MIMEType = "application/vnd.agent-trace.record+json"

// Contributor types, exactly as the spec enumerates them.
const (
	ContributorHuman   = "human"
	ContributorAI      = "ai"
	ContributorMixed   = "mixed"
	ContributorUnknown = "unknown"
)

// VCS types the spec enumerates; paircli only ever reports git.
const VCSTypeGit = "git"

// Record is the Agent Trace Trace Record (§6.1). Required: Version, ID,
// Timestamp, Files. VCS, Tool and Metadata are optional.
type Record struct {
	Version   string         `json:"version"`
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	VCS       *VCS           `json:"vcs,omitempty"`
	Tool      *Tool          `json:"tool,omitempty"`
	Files     []File         `json:"files"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// VCS records the revision the line numbers in this record refer to (§6.4).
// Required members: type, revision.
type VCS struct {
	Type     string `json:"type"`
	Revision string `json:"revision"`
}

// Tool names the program that generated the record. Both members optional.
type Tool struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// File is one attributed file (§6.1 file). Required: path, conversations.
// Path is relative to the repository root, which is how the PR diff and
// model.Attribution already spell it.
type File struct {
	Path          string         `json:"path"`
	Conversations []Conversation `json:"conversations"`
}

// Conversation groups the ranges one contributor produced in one session
// (§6.1 conversation). Required: ranges. URL, contributor and related are
// optional.
type Conversation struct {
	URL         string       `json:"url,omitempty"`
	Contributor *Contributor `json:"contributor,omitempty"`
	Ranges      []Range      `json:"ranges"`
	Related     []Related    `json:"related,omitempty"`
}

// Contributor says who produced a conversation's ranges (§6.1 contributor).
// Required: type, one of human | ai | mixed | unknown. ModelID is optional and
// follows the models.dev "provider/model-name" convention.
type Contributor struct {
	Type    string `json:"type"`
	ModelID string `json:"model_id,omitempty"`
}

// Range is a contiguous run of attributed lines (§6.1 range). Required:
// start_line, end_line; both are 1-indexed positions at VCS.Revision.
type Range struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

// Related points at a resource that helps look the attribution up (§6.8).
type Related struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Related types paircli emits.
const (
	RelatedHarness    = "harness"
	RelatedTranscript = "transcript"
)

// Options carries the two values Write's frozen signature has no room for.
type Options struct {
	// GeneratedAt is the record timestamp. Zero means "derive it from the PR
	// and the sessions" (see DeriveTimestamp); scan.Run passes its injected
	// now, so CLI output never depends on the wall clock.
	GeneratedAt time.Time
	// Version is paircli's own version, written to tool.version. Empty omits it.
	Version string
}

// Write writes dir/agent-trace.json for pr, attr and sessions, timestamping it
// with the latest time the PR and sessions themselves record. It is the
// docs/plan/CONTRACTS.md "internal/agenttrace" entry point.
func Write(dir string, pr *model.PR, attr *model.Attribution, sessions []*model.Session) error {
	return WriteWith(dir, Options{}, pr, attr, sessions)
}

// WriteWith is Write with an explicit timestamp and tool version.
func WriteWith(dir string, o Options, pr *model.PR, attr *model.Attribution, sessions []*model.Session) error {
	at := o.GeneratedAt
	if at.IsZero() {
		at = DeriveTimestamp(pr, sessions)
	}
	rec := Build(pr, attr, sessions, at, o.Version)

	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("agenttrace: write: %w", err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("agenttrace: write: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), b, 0o644); err != nil {
		return fmt.Errorf("agenttrace: write: %w", err)
	}
	return nil
}

// Build returns the Trace Record for pr, attr and sessions, timestamped at
// timestamp (UTC) with tool.version set to version. The result is a pure
// function of its inputs: the same inputs always produce the same record.
func Build(pr *model.PR, attr *model.Attribution, sessions []*model.Session, timestamp time.Time, version string) Record {
	byRef := make(map[string]*model.Session, len(sessions))
	for _, s := range sessions {
		if s != nil {
			byRef[s.Ref()] = s
		}
	}

	files := buildFiles(attr, byRef)

	rec := Record{
		Version:   SpecVersion,
		Timestamp: timestamp.UTC().Format(time.RFC3339),
		Files:     files,
		Tool:      &Tool{Name: "paircli", Version: version},
		ID:        recordID(pr, files, timestamp),
	}
	if sha := headSHA(pr); sha != "" {
		rec.VCS = &VCS{Type: VCSTypeGit, Revision: sha}
	}
	// The spec has no repository field under vcs (§6.4), so the repo and the
	// MIME type ride along in metadata, which the spec reserves for
	// implementation-specific data under reverse-domain keys (§7.2).
	if pr != nil && (pr.Repo != "" || pr.Number != 0) {
		rec.Metadata = map[string]any{
			"dev.paircli": map[string]any{
				"repo":      pr.Repo,
				"pr":        pr.Number,
				"url":       pr.URL,
				"mime_type": MIMEType,
			},
		}
	}
	return rec
}

// buildFiles turns the attribution into files[] entries, dropping files and
// lines with nothing to report (trivial labels, and any label this package
// does not map to a spec contributor type).
func buildFiles(attr *model.Attribution, byRef map[string]*model.Session) []File {
	if attr == nil {
		return []File{}
	}
	files := []File{}
	for _, fa := range attr.Files {
		cons := buildConversations(fa, byRef)
		if len(cons) == 0 {
			continue
		}
		files = append(files, File{Path: fa.Path, Conversations: cons})
	}
	// Deterministic order: by path.
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// group is the running state of one (contributor type, session) pair.
type group struct {
	ref   string // session ref, "" for lines no session explains
	kind  string // spec contributor type
	model string // the model that produced the lines, "" when unknown
	lines []int
}

// buildConversations groups a file's lines by contributor type and source
// session, turning each group's lines into contiguous ranges (§6.3).
func buildConversations(fa model.FileAttribution, byRef map[string]*model.Session) []Conversation {
	groups := map[string]*group{}
	var order []string
	for _, l := range fa.Lines {
		kind, ok := contributorType(l.Label)
		if !ok {
			continue // trivial lines are omitted from the record
		}
		ref := ""
		if l.Source != nil {
			ref = l.Source.Session
		}
		key := kind + "\x00" + ref
		g := groups[key]
		if g == nil {
			g = &group{ref: ref, kind: kind}
			groups[key] = g
			order = append(order, key)
		}
		if g.model == "" && producesModel(kind) {
			g.model = l.Model
		}
		g.lines = append(g.lines, l.Line)
	}

	cons := make([]Conversation, 0, len(order))
	for _, key := range order {
		g := groups[key]
		ranges := contiguous(g.lines)
		if len(ranges) == 0 {
			continue
		}
		s := byRef[g.ref]
		c := Conversation{Ranges: ranges}
		if g.ref != "" {
			// The session ref ("<harness>:<id>") is what identifies the
			// conversation paircli recorded; it is itself a URI.
			c.URL = g.ref
		}
		c.Contributor = &Contributor{Type: g.kind}
		if producesModel(g.kind) {
			c.Contributor.ModelID = modelID(s, g.model)
		}
		if s != nil {
			// Harness and the transcript path go in related: the path is
			// engine.Home'd so no "/Users/<name>/" prefix ever lands in the
			// record, and only the path — never the transcript's content.
			c.Related = []Related{
				{Type: RelatedHarness, URL: string(s.Harness)},
				{Type: RelatedTranscript, URL: engine.Home(s.SourcePath)},
			}
		}
		cons = append(cons, c)
	}
	// Deterministic order: by first attributed line, then by the identity of
	// the group, so equal inputs always produce equal bytes.
	sort.SliceStable(cons, func(i, j int) bool {
		if cons[i].Ranges[0].StartLine != cons[j].Ranges[0].StartLine {
			return cons[i].Ranges[0].StartLine < cons[j].Ranges[0].StartLine
		}
		if cons[i].URL != cons[j].URL {
			return cons[i].URL < cons[j].URL
		}
		return contributorTypeOf(cons[i]) < contributorTypeOf(cons[j])
	})
	return cons
}

func contributorTypeOf(c Conversation) string {
	if c.Contributor == nil {
		return ""
	}
	return c.Contributor.Type
}

// contributorType maps a paircli line label to the spec's contributor enum. ok
// is false for labels the record must not carry: trivial lines are omitted by
// design, and so is any label this package does not recognise (better to leave
// a file out than to claim an origin for it).
func contributorType(l model.LineLabel) (kind string, ok bool) {
	switch l {
	case model.LabelAgent:
		return ContributorAI, true
	case model.LabelMixed:
		return ContributorMixed, true
	case model.LabelHuman:
		return ContributorHuman, true
	case model.LabelUncaptured:
		return ContributorUnknown, true
	}
	return "", false
}

// contiguous returns the maximal runs of consecutive, 1-indexed line numbers
// in nums, sorted ascending, with duplicates collapsed.
func contiguous(nums []int) []Range {
	if len(nums) == 0 {
		return nil
	}
	sorted := append([]int(nil), nums...)
	sort.Ints(sorted)
	ranges := []Range{}
	start, prev := sorted[0], sorted[0]
	for _, n := range sorted[1:] {
		if n == prev || n == prev+1 {
			prev = n
			continue
		}
		ranges = append(ranges, Range{StartLine: start, EndLine: prev})
		start, prev = n, n
	}
	return append(ranges, Range{StartLine: start, EndLine: prev})
}

// producesModel reports whether a contributor type is one a model can be
// named for. A human contributor has no model_id even when the line's session
// names one: the field would then describe the harness's model, not who wrote
// the line.
func producesModel(kind string) bool {
	return kind == ContributorAI || kind == ContributorMixed
}

// providerFor maps a harness to the provider prefix the models.dev convention
// uses. An unknown harness yields "", and modelID then reports the model name
// paircli saw rather than inventing a provider.
func providerFor(h model.Harness) string {
	switch h {
	case model.HarnessClaudeCode:
		return "anthropic"
	case model.HarnessCodex:
		return "openai"
	}
	return ""
}

// modelID renders the spec's "provider/model-name" model identifier (§6.7),
// preferring the model recorded on the attributed lines and falling back to
// the model of the session's last event. It returns "" when no model is known,
// which omits the optional model_id.
func modelID(s *model.Session, lineModel string) string {
	name := lineModel
	if name == "" && s != nil {
		name = lastModel(s)
	}
	if name == "" {
		return ""
	}
	if strings.Contains(name, "/") {
		return name // already provider-qualified
	}
	if s == nil {
		return name
	}
	if p := providerFor(s.Harness); p != "" {
		return p + "/" + name
	}
	return name
}

// lastModel returns the model of the session's last event that names one.
func lastModel(s *model.Session) string {
	for i := len(s.Events) - 1; i >= 0; i-- {
		if s.Events[i].Model != "" {
			return s.Events[i].Model
		}
	}
	return ""
}

// headSHA returns the PR head commit SHA: the last commit the scan fetched.
func headSHA(pr *model.PR) string {
	if pr == nil || len(pr.Commits) == 0 {
		return ""
	}
	return pr.Commits[len(pr.Commits)-1].SHA
}

// DeriveTimestamp is the timestamp Write uses when the caller injects none:
// the latest time the PR and its sessions actually record. It is read from the
// data, never from the wall clock, so the record stays deterministic; a PR and
// session set with no timestamps at all yields the zero time.
func DeriveTimestamp(pr *model.PR, sessions []*model.Session) time.Time {
	var latest time.Time
	take := func(t time.Time) {
		if !t.IsZero() && t.After(latest) {
			latest = t
		}
	}
	if pr != nil {
		for _, c := range pr.Commits {
			take(c.Time)
		}
		take(pr.CreatedAt)
	}
	for _, s := range sessions {
		if s != nil {
			take(s.End)
			take(s.Start)
		}
	}
	return latest
}

// recordID returns the record's id as a UUID. It is derived from the PR and
// the record's own content (SHA-256, version-5 name-based shape: same inputs,
// same id), so no clock or randomness is involved.
func recordID(pr *model.PR, files []File, timestamp time.Time) string {
	var parts []string
	parts = append(parts, "agent-trace", SpecVersion)
	if pr != nil {
		parts = append(parts, pr.Repo, pr.URL, fmt.Sprintf("#%d", pr.Number))
	}
	parts = append(parts, headSHA(pr), timestamp.UTC().Format(time.RFC3339))
	for _, f := range files {
		parts = append(parts, f.Path)
		for _, c := range f.Conversations {
			parts = append(parts, c.URL, contributorTypeOf(c))
			for _, r := range c.Ranges {
				parts = append(parts, fmt.Sprintf("%d-%d", r.StartLine, r.EndLine))
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50 // version 5: name-based
	b[8] = (b[8] & 0x3f) | 0x80 // variant: RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
