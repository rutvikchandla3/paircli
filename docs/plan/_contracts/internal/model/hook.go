package model

import "time"

// HookRecordVersion is the current HookRecord schema version.
const HookRecordVersion = 1

// HookRecord is one line of the paircli hook log
// (~/.paircli/events/<harness>/<YYYY-MM-DD>.jsonl). Hook handlers write it;
// internal/hooklog reads it and merges it into sessions as OriginHook events.
type HookRecord struct {
	V         int         `json:"v"`
	Harness   Harness     `json:"harness"`
	Event     string      `json:"event"` // harness-native event name, e.g. "UserPromptSubmit"
	SessionID string      `json:"session_id"`
	TS        time.Time   `json:"ts"`
	CWD       string      `json:"cwd,omitempty"`
	HeadSHA   string      `json:"head_sha,omitempty"`
	Branch    string      `json:"branch,omitempty"`
	Trigger   string      `json:"trigger,omitempty"` // "session_start" | "commit" | "prompt" | "stop" | "session_end"
	ToolName  string      `json:"tool_name,omitempty"`
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Command   string      `json:"command,omitempty"`
	Decision  string      `json:"decision,omitempty"` // permission events: "allow" | "deny" | "ask"
	Path      string      `json:"path,omitempty"`     // InstructionsLoaded / FileChanged file
	Reason    string      `json:"reason,omitempty"`   // load_reason, end_reason, compaction trigger, interrupt reason
	CwdFrom   string      `json:"cwd_from,omitempty"`
	Snapshot  []FileLines `json:"snapshot,omitempty"`
	Error     string      `json:"error,omitempty"` // capture problem, if any
}
