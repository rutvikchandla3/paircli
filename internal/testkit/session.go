// Package testkit provides builders for sessions, PRs and engine.Context
// values used across detector and package tests. Nothing here is imported
// by non-test code.
package testkit

import (
	"fmt"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// clockEpoch is the fixed start time every SB clock begins at.
var clockEpoch = time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)

// dayBase is 2026-09-27T00:00:00Z, used by At to resolve "hh:mm".
var dayBase = time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)

// SB builds a *model.Session for tests.
type SB struct {
	s     *model.Session
	clock time.Time
	model string
	agent string
	n     int // event counter, for auto IDs
}

// Session starts a new session builder. Defaults: CWD/RepoRoot "/repo",
// RepoRemote "acme/shop", Branch "feat/test", Capture reconstructed,
// SourcePath "/fixtures/<id>.jsonl", model "test-model", clock starting at
// 2026-09-27T14:00:00Z and advancing one minute per event.
func Session(h model.Harness, id string) *SB {
	return &SB{
		s: &model.Session{
			Harness:    h,
			ID:         id,
			CWD:        "/repo",
			RepoRoot:   "/repo",
			RepoRemote: "acme/shop",
			Branch:     "feat/test",
			Capture:    model.CaptureReconstructed,
			SourcePath: "/fixtures/" + id + ".jsonl",
		},
		clock: clockEpoch,
		model: "test-model",
	}
}

// At sets the clock for the next event to 2026-09-27T<hhmm>:00Z. Later
// events continue advancing by one minute from there.
func (b *SB) At(hhmm string) *SB {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic("testkit: At: bad time " + hhmm + ": " + err.Error())
	}
	b.clock = dayBase.Add(time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute)
	return b
}

// Model sets the model used for subsequent assistant-side events.
func (b *SB) Model(m string) *SB {
	b.model = m
	return b
}

// Agent sets the agent id used for subsequent events ("" = main agent).
func (b *SB) Agent(id string) *SB {
	b.agent = id
	return b
}

// Prompt adds a human prompt event.
func (b *SB) Prompt(text string) *SB {
	return b.Add(model.Event{Kind: model.KindPrompt, Prompt: &model.Prompt{Text: text}})
}

// Steer adds a mid-turn steering prompt event.
func (b *SB) Steer(text string) *SB {
	return b.Add(model.Event{Kind: model.KindPrompt, Prompt: &model.Prompt{Text: text, Steering: true}})
}

// Say adds an assistant message event.
func (b *SB) Say(text string) *SB {
	return b.Add(model.Event{Kind: model.KindMessage, Message: &model.Message{Text: text}})
}

func toHunkLines(removed, added []string) []string {
	var lines []string
	for _, r := range removed {
		lines = append(lines, "-"+r)
	}
	for _, a := range added {
		lines = append(lines, "+"+a)
	}
	return lines
}

func (b *SB) addEdit(rel string, op model.EditOp, via string, removed, added []string) *SB {
	fe := &model.FileEdit{
		Path:    "/repo/" + rel,
		RelPath: rel,
		Op:      op,
		Added:   append([]string(nil), added...),
		Removed: append([]string(nil), removed...),
		Via:     via,
	}
	if len(removed) > 0 || len(added) > 0 {
		fe.Hunks = []model.Hunk{{Lines: toHunkLines(removed, added)}}
	}
	return b.Add(model.Event{Kind: model.KindEdit, Edit: fe})
}

// Edit adds a file_edit event that updates rel, adding the given lines.
func (b *SB) Edit(rel string, added ...string) *SB {
	return b.addEdit(rel, model.OpUpdate, "edit", nil, added)
}

// Replace adds a file_edit event that updates rel, removing and adding lines.
func (b *SB) Replace(rel string, removed, added []string) *SB {
	return b.addEdit(rel, model.OpUpdate, "edit", removed, added)
}

// Create adds a file_edit event that creates rel with the given lines.
func (b *SB) Create(rel string, lines ...string) *SB {
	return b.addEdit(rel, model.OpCreate, "write", nil, lines)
}

// Read adds a file_read event for rel.
func (b *SB) Read(rel string) *SB {
	return b.Add(model.Event{Kind: model.KindRead, Read: &model.FileRead{Path: "/repo/" + rel, RelPath: rel}})
}

func (b *SB) addCommand(cmd string, exit int, output string, byUser bool) *SB {
	c := &model.Command{Cmd: cmd, Output: output, ByUser: byUser}
	switch {
	case exit < 0:
		c.Status = model.CmdUnknown
	case exit == 0:
		c.Status = model.CmdOK
		c.ExitCode = model.IntPtr(exit)
	default:
		c.Status = model.CmdFailed
		c.ExitCode = model.IntPtr(exit)
	}
	return b.Add(model.Event{Kind: model.KindCommand, Command: c})
}

// Run adds an agent-run command event. exit < 0 means Status unknown with a
// nil ExitCode; exit == 0 means ok; exit > 0 means failed.
func (b *SB) Run(cmd string, exit int, output string) *SB {
	return b.addCommand(cmd, exit, output, false)
}

// UserRun adds a human-run command event (Command.ByUser = true).
func (b *SB) UserRun(cmd string, exit int) *SB {
	return b.addCommand(cmd, exit, "", true)
}

// needsModel reports whether e's kind gets a default Model stamped in by Add.
func needsModel(k model.EventKind) bool {
	switch k {
	case model.KindMessage, model.KindCommand, model.KindEdit, model.KindToolCall:
		return true
	}
	return false
}

// Add appends any event, filling a zero ID ("e<n>"), TS (the clock), Origin
// (transcript), Model (the current model, for message/command/edit/tool
// events) and AgentID (the current agent) when they are unset. The clock
// advances by one minute after every Add.
func (b *SB) Add(e model.Event) *SB {
	b.n++
	if e.ID == "" {
		e.ID = fmt.Sprintf("e%d", b.n)
	}
	if e.TS.IsZero() {
		e.TS = b.clock
	}
	if e.Origin == "" {
		e.Origin = model.OriginTranscript
	}
	if e.AgentID == "" {
		e.AgentID = b.agent
	}
	if e.Model == "" && needsModel(e.Kind) {
		byUser := e.Kind == model.KindCommand && e.Command != nil && e.Command.ByUser
		if !byUser {
			e.Model = b.model
		}
	}
	b.s.Events = append(b.s.Events, e)
	b.clock = b.clock.Add(time.Minute)
	return b
}

// Build calls Session.Finalize and returns the built session.
func (b *SB) Build() *model.Session {
	b.s.Finalize()
	return b.s
}
