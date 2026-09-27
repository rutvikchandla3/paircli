package hooklog

import (
	"fmt"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// resetReasons are the SessionStart Reason values that produce a reset event.
var resetReasons = map[string]bool{
	"resume":  true,
	"clear":   true,
	"compact": true,
	"fork":    true,
}

// Merge converts recs into OriginHook events appended to s, then calls
// s.Finalize(). Events get IDs "hook-1", "hook-2", … in record order; a
// single record can produce several events (e.g. one with both HeadSHA and
// Snapshot set). s.Capture becomes CaptureHooked when at least one record
// changed the session. See docs/plan/tasks/T08-hooklog-claude-hooks.md for
// the record → event table.
func Merge(s *model.Session, recs []model.HookRecord) {
	n := 0
	touched := false

	appendEvent := func(e model.Event) {
		n++
		e.ID = fmt.Sprintf("hook-%d", n)
		e.Origin = model.OriginHook
		s.Events = append(s.Events, e)
		touched = true
	}

	for _, rec := range recs {
		if rec.HeadSHA != "" {
			appendEvent(model.Event{
				TS:      rec.TS,
				Kind:    model.KindGitHead,
				GitHead: &model.GitHead{SHA: rec.HeadSHA, Branch: rec.Branch, Trigger: rec.Trigger},
			})
		}
		if rec.Snapshot != nil {
			appendEvent(model.Event{
				TS:       rec.TS,
				Kind:     model.KindSnapshot,
				Snapshot: &model.Snapshot{Trigger: rec.Trigger, Files: rec.Snapshot},
			})
		}

		switch rec.Event {
		case "SessionStart":
			if resetReasons[rec.Reason] && !hasTranscriptReset(s, rec.Reason, rec.TS, 10*time.Second) {
				appendEvent(model.Event{
					TS:    rec.TS,
					Kind:  model.KindReset,
					Reset: &model.Reset{Type: rec.Reason},
				})
			}

		case "PermissionRequest":
			appendEvent(model.Event{
				TS:         rec.TS,
				Kind:       model.KindPermission,
				Permission: &model.Permission{Tool: rec.ToolName, Decision: "ask", By: "user"},
			})

		case "PermissionDenied":
			appendEvent(model.Event{
				TS:         rec.TS,
				Kind:       model.KindPermission,
				Permission: &model.Permission{Tool: rec.ToolName, Decision: "deny", By: "auto"},
			})

		case "InstructionsLoaded":
			if !hasTranscriptInstructions(s, rec.Path) {
				appendEvent(model.Event{
					TS:           rec.TS,
					Kind:         model.KindInstructions,
					Instructions: &model.Instructions{Path: rec.Path, Reason: rec.Reason},
				})
			}

		case "CwdChanged":
			appendEvent(model.Event{
				TS:        rec.TS,
				Kind:      model.KindCwdChange,
				CwdChange: &model.CwdChange{From: rec.CwdFrom, To: rec.CWD},
			})

		case "PreCompact", "PostCompact":
			if idx := transcriptCompactionWithin(s, rec.TS, 10*time.Minute); idx >= 0 {
				if s.Events[idx].Compaction.Trigger == "" {
					s.Events[idx].Compaction.Trigger = rec.Reason
					touched = true
				}
			} else if rec.Event == "PreCompact" {
				appendEvent(model.Event{
					TS:         rec.TS,
					Kind:       model.KindCompaction,
					Compaction: &model.Compaction{Trigger: rec.Reason},
				})
			}

		case "Interrupt":
			if !hasTranscriptInterrupt(s, rec.TS, 5*time.Second) {
				appendEvent(model.Event{
					TS:        rec.TS,
					Kind:      model.KindInterrupt,
					Interrupt: &model.Interrupt{Reason: "user"},
				})
			}

		case "SessionEnd", "session_shutdown":
			appendEvent(model.Event{
				TS:         rec.TS,
				Kind:       model.KindSessionEnd,
				SessionEnd: &model.SessionEnd{Reason: rec.Reason},
			})
		}
	}

	if touched {
		s.Capture = model.CaptureHooked
	}
	s.Finalize()
}

// withinWindow reports whether a and b are at most d apart, in either direction.
func withinWindow(a, b time.Time, d time.Duration) bool {
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= d
}

// hasTranscriptReset reports whether s already has a transcript-origin reset
// event of the given type within d of ts.
func hasTranscriptReset(s *model.Session, typ string, ts time.Time, d time.Duration) bool {
	for _, e := range s.Events {
		if e.Origin != model.OriginTranscript || e.Kind != model.KindReset || e.Reset == nil {
			continue
		}
		if e.Reset.Type == typ && withinWindow(e.TS, ts, d) {
			return true
		}
	}
	return false
}

// hasTranscriptInstructions reports whether s already has a transcript-origin
// instructions event for the same path.
func hasTranscriptInstructions(s *model.Session, path string) bool {
	for _, e := range s.Events {
		if e.Origin != model.OriginTranscript || e.Kind != model.KindInstructions || e.Instructions == nil {
			continue
		}
		if e.Instructions.Path == path {
			return true
		}
	}
	return false
}

// transcriptCompactionWithin returns the index of the first transcript-origin
// compaction event within d of ts, or -1.
func transcriptCompactionWithin(s *model.Session, ts time.Time, d time.Duration) int {
	for i, e := range s.Events {
		if e.Origin != model.OriginTranscript || e.Kind != model.KindCompaction || e.Compaction == nil {
			continue
		}
		if withinWindow(e.TS, ts, d) {
			return i
		}
	}
	return -1
}

// hasTranscriptInterrupt reports whether s already has a transcript-origin
// interrupt event within d of ts.
func hasTranscriptInterrupt(s *model.Session, ts time.Time, d time.Duration) bool {
	for _, e := range s.Events {
		if e.Origin != model.OriginTranscript || e.Kind != model.KindInterrupt {
			continue
		}
		if withinWindow(e.TS, ts, d) {
			return true
		}
	}
	return false
}
