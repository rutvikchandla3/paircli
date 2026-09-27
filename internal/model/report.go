package model

import "time"

// SchemaVersion is the signals.json schema version.
const SchemaVersion = "2"

// Report is the full content of signals.json.
type Report struct {
	SchemaVersion string           `json:"schema_version"`
	GeneratedAt   time.Time        `json:"generated_at"`
	Tool          string           `json:"tool"` // "paircli <version>"
	PR            PRSummary        `json:"pr"`
	Coverage      Coverage         `json:"coverage"`
	Sessions      []SessionSummary `json:"sessions"`
	Commits       []CommitLink     `json:"commits"`
	Signals       []Signal         `json:"signals"`
}

// PRSummary is the PR header in the report.
type PRSummary struct {
	Repo      string `json:"repo"`
	Number    int    `json:"number"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	HeadRef   string `json:"head_ref"`
	BaseRef   string `json:"base_ref"`
	Commits   int    `json:"commits"`
	Files     int    `json:"files"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// Coverage says how much of the PR the captured sessions explain.
type Coverage struct {
	Capture             string          `json:"capture"` // "hooked" | "partial" | "reconstructed" | "none"
	Sessions            int             `json:"sessions"`
	Harnesses           map[Harness]int `json:"harnesses"`
	LinesTotal          int             `json:"lines_total"`
	LinesExplained      int             `json:"lines_explained"`
	Ratio               float64         `json:"ratio"`
	UnattributedCommits []string        `json:"unattributed_commits"`
	DroppedSessions     int             `json:"dropped_sessions"` // candidates in the window that turned out unrelated
}

// LinkMethod is how a session or commit was tied to the PR.
type LinkMethod string

const (
	LinkSHAExact    LinkMethod = "sha_exact"    // a hook/transcript recorded a PR commit SHA
	LinkSHAAncestor LinkMethod = "sha_ancestor" // the session started on top of a PR commit
	LinkContent     LinkMethod = "content"      // the session's edits produced PR lines
	LinkHeuristic   LinkMethod = "heuristic"    // repo + branch + time window only
	LinkNone        LinkMethod = "none"
)

// SessionSummary is one linked session in the report.
type SessionSummary struct {
	Ref           string            `json:"ref"`
	Harness       Harness           `json:"harness"`
	ID            string            `json:"id"`
	Title         string            `json:"title,omitempty"`
	Start         time.Time         `json:"start"`
	End           time.Time         `json:"end"`
	Models        []string          `json:"models"`
	Capture       Capture           `json:"capture"`
	Link          LinkMethod        `json:"link"`
	Commits       []string          `json:"commits,omitempty"`
	SourcePath    string            `json:"source_path"`
	Counts        map[EventKind]int `json:"counts"`
	Usage         Usage             `json:"usage"`
	LinesAuthored int               `json:"lines_authored"` // PR lines attributed to this session
}

// CommitLink ties one PR commit to the sessions that produced it.
type CommitLink struct {
	SHA        string     `json:"sha"`
	Sessions   []string   `json:"sessions,omitempty"` // Session.Ref()
	Method     LinkMethod `json:"method"`
	Confidence string     `json:"confidence"` // "exact" | "inferred" | "unknown"
}
