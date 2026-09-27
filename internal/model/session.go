// Package model holds paircli's frozen v2 contracts: the normalized session
// timeline every harness parser produces, the PR/diff model, attribution
// results, and the Signal envelope every detector emits.
//
// Do not edit this package inside a task. If a task needs a change, stop and
// report a CONTRACT GAP (see docs/plan/README.md).
package model

import (
	"sort"
	"time"
)

// Harness identifies the coding agent that produced a session.
type Harness string

const (
	HarnessClaudeCode Harness = "claude-code"
	HarnessCodex      Harness = "codex"
	HarnessPi         Harness = "pi"
)

// Capture says how a session's data was obtained.
type Capture string

const (
	CaptureHooked        Capture = "hooked"        // transcript plus at least one paircli hook/extension record
	CaptureReconstructed Capture = "reconstructed" // transcript only
)

// Session is one harness session: one transcript (plus its subagent
// transcripts and any merged hook records), normalized to a timeline.
// Parsers fill everything except RepoRoot and the RelPath fields inside
// events; internal/link fills those.
type Session struct {
	Harness        Harness   `json:"harness"`
	ID             string    `json:"id"`
	ParentID       string    `json:"parent_id,omitempty"`
	Title          string    `json:"title,omitempty"`
	SourcePath     string    `json:"source_path"`
	HarnessVersion string    `json:"harness_version,omitempty"`
	CWD            string    `json:"cwd"`
	RepoRoot       string    `json:"repo_root,omitempty"`
	RepoRemote     string    `json:"repo_remote,omitempty"` // "owner/repo"
	Branch         string    `json:"branch,omitempty"`
	StartSHA       string    `json:"start_sha,omitempty"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	Capture        Capture   `json:"capture"`
	Events         []Event   `json:"events"`
}

// Ref returns "<harness>:<id>", the session reference used in evidence,
// links and citations.
func (s *Session) Ref() string { return string(s.Harness) + ":" + s.ID }

// EventRef identifies one event across all sessions.
type EventRef struct {
	Session string `json:"session"` // Session.Ref()
	Event   string `json:"event"`   // Event.ID
}

// Finalize must be called by every parser after it has appended all events,
// and again by anything that inserts events later (internal/hooklog).
// It:
//   - stable-sorts Events by TS (events with equal TS keep their input order),
//   - sets Seq to the event's index,
//   - sets Turn to the number of non-steering prompts at or before the event
//     (subagent events with AgentID != "" never count as prompts),
//   - marks Message.Final on the last main-agent (AgentID == "") assistant
//     message before each non-steering prompt and before the end of the session,
//     clearing any Final flag set earlier,
//   - sets Start and End to the first and last event timestamps when there are events.
func (s *Session) Finalize() {
	sort.SliceStable(s.Events, func(i, j int) bool { return s.Events[i].TS.Before(s.Events[j].TS) })
	turn := 0
	lastMsg := -1
	markFinal := func() {
		if lastMsg >= 0 {
			s.Events[lastMsg].Message.Final = true
		}
		lastMsg = -1
	}
	for i := range s.Events {
		e := &s.Events[i]
		e.Seq = i
		if e.Kind == KindPrompt && e.Prompt != nil && !e.Prompt.Steering && e.AgentID == "" {
			markFinal()
			turn++
		}
		e.Turn = turn
		if e.Kind == KindMessage && e.Message != nil {
			e.Message.Final = false
			if e.AgentID == "" {
				lastMsg = i
			}
		}
	}
	markFinal()
	if n := len(s.Events); n > 0 {
		s.Start = s.Events[0].TS
		s.End = s.Events[n-1].TS
	}
}
