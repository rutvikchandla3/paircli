package model

import "time"

// EventKind names what happened. Exactly one payload pointer on Event is
// non-nil, and it matches the kind (see the table in docs/plan/CONTRACTS.md).
type EventKind string

const (
	KindPrompt       EventKind = "prompt"            // Prompt
	KindMessage      EventKind = "assistant_message" // Message
	KindCommand      EventKind = "command"           // Command
	KindEdit         EventKind = "file_edit"         // Edit
	KindRead         EventKind = "file_read"         // Read
	KindExternalEdit EventKind = "external_edit"     // External
	KindQuestion     EventKind = "question"          // Question
	KindPlan         EventKind = "plan"              // Plan
	KindMode         EventKind = "mode_change"       // Mode
	KindModelChange  EventKind = "model_change"      // ModelChange
	KindInterrupt    EventKind = "interrupt"         // Interrupt
	KindRejection    EventKind = "tool_rejected"     // Rejection
	KindPermission   EventKind = "permission"        // Permission
	KindCompaction   EventKind = "compaction"        // Compaction
	KindReset        EventKind = "reset"             // Reset
	KindSubagent     EventKind = "subagent"          // Subagent
	KindLookup       EventKind = "lookup"            // Lookup
	KindMCP          EventKind = "mcp_call"          // MCP
	KindToolCall     EventKind = "tool_call"         // ToolCall (any tool without a dedicated kind)
	KindTodo         EventKind = "todo"              // Todo
	KindInstructions EventKind = "instructions"      // Instructions
	KindDiagnostics  EventKind = "diagnostics"       // Diagnostics
	KindImage        EventKind = "image"             // Image
	KindGitHead      EventKind = "git_head"          // GitHead
	KindSnapshot     EventKind = "snapshot"          // Snapshot
	KindCwdChange    EventKind = "cwd_change"        // CwdChange
	KindFailure      EventKind = "failure"           // Failure
	KindSessionEnd   EventKind = "session_end"       // SessionEnd
)

// Origin says where an event came from.
type Origin string

const (
	OriginTranscript Origin = "transcript"
	OriginHook       Origin = "hook"
)

// Event is one normalized thing that happened in a session.
type Event struct {
	Seq       int       `json:"seq"`  // index in Session.Events after sorting
	ID        string    `json:"id"`   // stable harness id (tool_use id, entry uuid, item id); unique within the session
	Kind      EventKind `json:"kind"` //
	TS        time.Time `json:"ts"`   //
	Turn      int       `json:"turn"` // human prompts seen so far in this session, counting this one (0 = before the first prompt)
	Model     string    `json:"model,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`   // set when a subagent produced the event
	OffBranch bool      `json:"off_branch,omitempty"` // Pi only: entry is not on the final active branch
	Origin    Origin    `json:"origin"`

	Prompt       *Prompt       `json:"prompt,omitempty"`
	Message      *Message      `json:"message,omitempty"`
	Command      *Command      `json:"command,omitempty"`
	Edit         *FileEdit     `json:"edit,omitempty"`
	Read         *FileRead     `json:"read,omitempty"`
	External     *ExternalEdit `json:"external,omitempty"`
	Question     *Question     `json:"question,omitempty"`
	Plan         *Plan         `json:"plan,omitempty"`
	Mode         *Mode         `json:"mode,omitempty"`
	ModelChange  *ModelChange  `json:"model_change,omitempty"`
	Interrupt    *Interrupt    `json:"interrupt,omitempty"`
	Rejection    *Rejection    `json:"rejection,omitempty"`
	Permission   *Permission   `json:"permission,omitempty"`
	Compaction   *Compaction   `json:"compaction,omitempty"`
	Reset        *Reset        `json:"reset,omitempty"`
	Subagent     *Subagent     `json:"subagent,omitempty"`
	Lookup       *Lookup       `json:"lookup,omitempty"`
	MCP          *MCPCall      `json:"mcp,omitempty"`
	ToolCall     *ToolCall     `json:"tool_call,omitempty"`
	Todo         *Todo         `json:"todo,omitempty"`
	Instructions *Instructions `json:"instructions,omitempty"`
	Diagnostics  *Diagnostics  `json:"diagnostics,omitempty"`
	Image        *Image        `json:"image,omitempty"`
	GitHead      *GitHead      `json:"git_head,omitempty"`
	Snapshot     *Snapshot     `json:"snapshot,omitempty"`
	CwdChange    *CwdChange    `json:"cwd_change,omitempty"`
	Failure      *Failure      `json:"failure,omitempty"`
	SessionEnd   *SessionEnd   `json:"session_end,omitempty"`
}

// Prompt is text a human typed (or a slash command they ran).
type Prompt struct {
	Text         string `json:"text"`
	SlashCommand string `json:"slash_command,omitempty"` // e.g. "/deep-research"; Text then holds the arguments
	Steering     bool   `json:"steering,omitempty"`      // sent while the agent was mid-turn
	Images       int    `json:"images,omitempty"`
}

// Usage is model token accounting for one assistant message.
type Usage struct {
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	CostUSD    float64 `json:"cost_usd,omitempty"` // only Pi records dollars
}

// Message is assistant-authored text.
type Message struct {
	Text  string `json:"text"`
	Final bool   `json:"final,omitempty"` // last assistant text before the next human prompt or the session end
	Usage *Usage `json:"usage,omitempty"`
}

// CommandStatus is the outcome of a shell command.
type CommandStatus string

const (
	CmdOK          CommandStatus = "ok"
	CmdFailed      CommandStatus = "failed"
	CmdInterrupted CommandStatus = "interrupted"
	CmdTimeout     CommandStatus = "timeout"
	CmdUnknown     CommandStatus = "unknown"
)

// Command is one shell command run by the agent or, with ByUser, by the human.
type Command struct {
	Cmd        string        `json:"cmd"`
	CWD        string        `json:"cwd,omitempty"`
	ExitCode   *int          `json:"exit_code,omitempty"`
	Status     CommandStatus `json:"status"`
	Output     string        `json:"output,omitempty"` // always passed through TruncateOutput
	DurationMs int64         `json:"duration_ms,omitempty"`
	Background bool          `json:"background,omitempty"`
	ByUser     bool          `json:"by_user,omitempty"`
}

// EditOp is the kind of file change.
type EditOp string

const (
	OpCreate EditOp = "create"
	OpUpdate EditOp = "update"
	OpDelete EditOp = "delete"
	OpMove   EditOp = "move"
)

// Hunk is one unified-diff hunk. Lines keep their ' ', '+' or '-' prefix.
type Hunk struct {
	OldStart int      `json:"old_start"`
	OldLines int      `json:"old_lines"`
	NewStart int      `json:"new_start"`
	NewLines int      `json:"new_lines"`
	Lines    []string `json:"lines"`
}

// FileEdit is one file change made by the agent.
type FileEdit struct {
	Path     string   `json:"path"`               // as recorded: absolute, or relative to the session CWD
	RelPath  string   `json:"rel_path,omitempty"` // repo-relative, set by internal/link; "" when outside the repo
	Op       EditOp   `json:"op"`
	MovePath string   `json:"move_path,omitempty"`
	Added    []string `json:"added,omitempty"`   // full text of added lines, without the '+' prefix
	Removed  []string `json:"removed,omitempty"` // full text of removed lines, without the '-' prefix
	Hunks    []Hunk   `json:"hunks,omitempty"`
	Via      string   `json:"via"` // "edit" | "write" | "apply_patch" | "bash_edit" | "notebook"
	Failed   bool     `json:"failed,omitempty"`
}

// FileRead is a file the agent read with a read tool (or a read-classified command in Codex).
type FileRead struct {
	Path    string `json:"path"`
	RelPath string `json:"rel_path,omitempty"`
}

// ExternalEdit is a file change the agent did not make: a human, a formatter
// or another process changed the file between agent actions.
type ExternalEdit struct {
	Path    string   `json:"path"`
	RelPath string   `json:"rel_path,omitempty"`
	Lines   []string `json:"lines,omitempty"` // file content lines visible after the change (may be partial)
	Source  string   `json:"source"`          // "cc_attachment" | "hook_snapshot"
}

// QA is one question the agent asked and the human's answer.
type QA struct {
	Header   string   `json:"header,omitempty"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Answer   string   `json:"answer"`
}

// Question is an agent-to-human question tool call with its answers.
type Question struct {
	Tool  string `json:"tool"`
	Items []QA   `json:"items"`
}

// PlanStep is one step of a structured plan.
type PlanStep struct {
	Text   string `json:"text"`
	Status string `json:"status,omitempty"`
}

// Plan is a plan the agent proposed. Approved is nil when unknown.
type Plan struct {
	Text     string     `json:"text,omitempty"`
	Steps    []PlanStep `json:"steps,omitempty"`
	Approved *bool      `json:"approved,omitempty"`
	Source   string     `json:"source"` // "exit_plan_mode" | "plan_item" | "plan_mode"
}

// PermissionMode is the normalized permission posture.
type PermissionMode string

const (
	PermAsk         PermissionMode = "ask"          // Claude Code default: tools not on an allowlist prompt the human
	PermAcceptEdits PermissionMode = "accept_edits" // file edits auto-approved, other tools prompt
	PermPlan        PermissionMode = "plan"         // read-only planning
	PermAuto        PermissionMode = "auto"         // a classifier approves
	PermBypass      PermissionMode = "bypass"       // nothing prompts
	PermUngated     PermissionMode = "ungated"      // harness has no permission layer (Pi)
)

// Mode records the permission/approval/sandbox posture in force from this event on.
type Mode struct {
	Permission    PermissionMode `json:"permission,omitempty"`
	RawPermission string         `json:"raw_permission,omitempty"`
	Approval      string         `json:"approval,omitempty"` // Codex approval_policy: never | on-request | on-failure | untrusted
	Sandbox       string         `json:"sandbox,omitempty"`  // Codex sandbox_policy.type
}

// ModelChange records the model/effort in force from this event on.
type ModelChange struct {
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	Effort   string `json:"effort,omitempty"`
}

// Interrupt is a human stopping the agent mid-turn.
type Interrupt struct {
	Reason string `json:"reason"` // "user" | "aborted" | raw harness reason
}

// Rejection is a human refusing a specific tool call.
type Rejection struct {
	Tool      string `json:"tool"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Permission is a permission prompt or decision (hook-captured).
type Permission struct {
	Tool     string `json:"tool"`
	Decision string `json:"decision"` // "allow" | "deny" | "ask"
	By       string `json:"by,omitempty"`
}

// Compaction is context being summarized.
type Compaction struct {
	Trigger string `json:"trigger,omitempty"` // "auto" | "manual" | "extension"
	Summary string `json:"summary,omitempty"`
}

// Reset is a context discontinuity other than compaction.
type Reset struct {
	Type    string `json:"type"` // "clear" | "resume" | "fork" | "rollback" | "branch_switch"
	Summary string `json:"summary,omitempty"`
}

// Subagent is a delegated agent run.
type Subagent struct {
	AgentID        string `json:"agent_id,omitempty"`
	Type           string `json:"type,omitempty"`
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt,omitempty"` // first 500 chars
	TranscriptPath string `json:"transcript_path,omitempty"`
	Status         string `json:"status,omitempty"`
}

// Lookup is a web search or fetch.
type Lookup struct {
	Kind  string   `json:"kind"` // "search" | "fetch"
	Query string   `json:"query,omitempty"`
	URLs  []string `json:"urls,omitempty"`
}

// MCPCall is a call to an MCP server tool.
type MCPCall struct {
	Server   string `json:"server"`
	Tool     string `json:"tool"`
	ReadOnly bool   `json:"read_only,omitempty"`
	Args     string `json:"args,omitempty"` // JSON, first 500 chars
	IsError  bool   `json:"is_error,omitempty"`
}

// ToolCall is any tool call without a dedicated kind.
type ToolCall struct {
	Name    string `json:"name"`
	Input   string `json:"input,omitempty"` // JSON, first 500 chars
	IsError bool   `json:"is_error,omitempty"`
}

// TodoItem is one task-list entry.
type TodoItem struct {
	ID     string `json:"id,omitempty"`
	Text   string `json:"text,omitempty"`
	Status string `json:"status,omitempty"` // "pending" | "in_progress" | "completed" | raw
}

// Todo is a task-list write. With Replace the Items are the complete list.
type Todo struct {
	Tool    string     `json:"tool"`
	Items   []TodoItem `json:"items"`
	Replace bool       `json:"replace,omitempty"`
}

// Instructions is a rules/memory file loaded into the agent's context.
type Instructions struct {
	Path      string `json:"path"`
	RelPath   string `json:"rel_path,omitempty"`
	Scope     string `json:"scope,omitempty"` // "user" | "project" | "local" | "nested" | "memory" | raw
	Hash      string `json:"hash,omitempty"`  // sha256 hex of Content, when content is known
	FirstLine string `json:"first_line,omitempty"`
	Content   string `json:"content,omitempty"` // first 16 KB
	Reason    string `json:"reason,omitempty"`  // hook load_reason
}

// Diagnostics is an editor/LSP diagnostics snapshot for one file.
type Diagnostics struct {
	Path     string   `json:"path"`
	RelPath  string   `json:"rel_path,omitempty"`
	Errors   int      `json:"errors"`
	Warnings int      `json:"warnings"`
	Messages []string `json:"messages,omitempty"` // up to 5
}

// Image is an image produced in the session (e.g. a screenshot). Bytes are
// never copied; Ref says where to extract them from later.
type Image struct {
	MediaType string `json:"media_type,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Ref       string `json:"ref"` // "<source_path>#<event id>" or a file path
}

// GitHead is the repository HEAD observed at a moment in the session.
type GitHead struct {
	SHA     string `json:"sha"`
	Branch  string `json:"branch,omitempty"`
	Trigger string `json:"trigger"` // "session_start" | "commit" | "prompt" | "stop" | "session_end" | "transcript_meta"
}

// FileLines is a set of line hashes (see LineHash) for one repo-relative file.
type FileLines struct {
	Path   string   `json:"path"`
	Hashes []string `json:"hashes"`
}

// Snapshot is hook-captured line hashes of dirty files at a turn boundary.
type Snapshot struct {
	Trigger string      `json:"trigger"` // "prompt" | "stop"
	Files   []FileLines `json:"files"`
}

// CwdChange is the agent changing working directory.
type CwdChange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Failure is a harness/API failure that cut a turn short.
type Failure struct {
	Type    string `json:"type"` // "api_error" | "rate_limit" | "length" | raw
	Message string `json:"message,omitempty"`
}

// SessionEnd records why a session ended, when known.
type SessionEnd struct {
	Reason string `json:"reason,omitempty"`
}
