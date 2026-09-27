// Package session defines the normalized Session schema that all harness
// integrations (Path A and Path B) converge on before correlation runs, per
// docs/ARCHITECTURE.md.
package session

import "time"

// Confidence mirrors docs/SIGNALS.md's confidence tagging.
type Confidence string

const (
	ConfidenceExact    Confidence = "exact"
	ConfidenceInferred Confidence = "inferred"
	ConfidenceUnknown  Confidence = "unknown"
)

// CapturePath records which of the two capture paths in
// docs/ARCHITECTURE.md produced a session record.
type CapturePath string

const (
	CapturePathA       CapturePath = "hooked"        // Path A: our installed hook ran live.
	CapturePathB       CapturePath = "reconstructed" // Path B: recovered from local transcripts.
	CapturePathUnknown CapturePath = "none"
)

// ToolCallSummary is a per-tool-name count of tool invocations within a
// session, per SIGNALS.md section 4.
type ToolCallSummary map[string]int

// TokenUsage is per-session token accounting, per SIGNALS.md section 5.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CachedTokens int `json:"cached_tokens"`
}

// Session is the normalized record both capture paths, for all harnesses,
// produce. The correlator operates only on this type and never needs to
// know which path or harness produced a given record.
type Session struct {
	Harness   string    `json:"harness"` // "claude-code" | "codex" | "pi"
	SessionID string    `json:"session_id"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Models    []string  `json:"models"`

	// Repo/branch identity used by the correlator's heuristic fallback.
	RepoRemote string `json:"repo_remote,omitempty"` // best-effort owner/repo
	RepoPath   string `json:"repo_path,omitempty"`   // local cwd the session ran in
	Branch     string `json:"branch,omitempty"`

	// CommitSHA is set only when the harness/hook recorded an exact SHA
	// (Path A always; Path B only where the harness's own transcript embeds
	// one — never true for Claude Code, per docs/ARCHITECTURE.md).
	CommitSHA string `json:"commit_sha,omitempty"`

	// Process signals, SIGNALS.md section 2.
	IterationCount    int `json:"iteration_count"`
	CorrectionSignals int `json:"correction_signals"`
	PlanRevisions     int `json:"plan_revisions"`

	// Tool/action signals, SIGNALS.md section 4.
	ToolCallSummary ToolCallSummary `json:"tool_call_summary,omitempty"`

	// Cost/efficiency signals, SIGNALS.md section 5.
	TokenUsage TokenUsage `json:"token_usage"`

	CapturePath CapturePath `json:"capture_path"`
	Confidence  Confidence  `json:"confidence"`

	// SourcePath points at the raw transcript/event-log file this record
	// was derived from, for drill-in (docs/ARCHITECTURE.md's raw/ dir).
	SourcePath string `json:"source_path,omitempty"`
}

// CommitAttribution records how (if at all) a PR commit was matched to a
// session, per SIGNALS.md section 3 / ARCHITECTURE.md's correlation algorithm.
type CommitAttribution struct {
	SHA        string     `json:"sha"`
	SessionRef string     `json:"session_ref,omitempty"` // "<harness>-<session_id>"
	Method     string     `json:"method,omitempty"`      // "sha_exact" | "heuristic"
	Confidence Confidence `json:"confidence"`
}

// Report is the aggregate signal set written to signals.json, matching
// SIGNALS.md's shape.
type Report struct {
	PRNumber int    `json:"pr_number"`
	Repo     string `json:"repo"`

	HarnessesUsed          []string       `json:"harnesses_used"`
	Sessions               []Session      `json:"sessions"`
	SessionCountPerHarness map[string]int `json:"session_count_per_harness"`
	SessionCount           int            `json:"session_count"`

	WallClockStart *time.Time `json:"wall_clock_start,omitempty"`
	WallClockEnd   *time.Time `json:"wall_clock_end,omitempty"`

	Commits             []CommitAttribution `json:"commits"`
	UnattributedCommits []string            `json:"unattributed_commits"`

	CorrectionSignalsTotal int `json:"correction_signals_total"`

	CaptureCompleteness string `json:"capture_completeness"` // "hooked"|"reconstructed"|"partial"|"none"
}
