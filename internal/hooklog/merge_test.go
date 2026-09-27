package hooklog

import (
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func newSession() *model.Session {
	return testkit.Session(model.HarnessClaudeCode, "s1").Build()
}

func TestMerge_Conversions(t *testing.T) {
	base := time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		rec  model.HookRecord
		want func(t *testing.T, s *model.Session)
	}{
		{
			name: "HeadSHA -> git_head",
			rec:  model.HookRecord{Event: "SessionStart", SessionID: "s1", TS: base, HeadSHA: "deadbeef", Branch: "main", Trigger: "session_start", Reason: "startup"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindGitHead)
				if e.GitHead.SHA != "deadbeef" || e.GitHead.Branch != "main" || e.GitHead.Trigger != "session_start" {
					t.Errorf("git_head = %+v", e.GitHead)
				}
			},
		},
		{
			name: "Snapshot -> snapshot",
			rec:  model.HookRecord{Event: "UserPromptSubmit", SessionID: "s1", TS: base, Trigger: "prompt", Snapshot: []model.FileLines{{Path: "a.go", Hashes: []string{"h1"}}}},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindSnapshot)
				if e.Snapshot.Trigger != "prompt" || len(e.Snapshot.Files) != 1 || e.Snapshot.Files[0].Path != "a.go" {
					t.Errorf("snapshot = %+v", e.Snapshot)
				}
			},
		},
		{
			name: "SessionStart resume -> reset",
			rec:  model.HookRecord{Event: "SessionStart", SessionID: "s1", TS: base, Reason: "resume"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindReset)
				if e.Reset.Type != "resume" {
					t.Errorf("reset = %+v", e.Reset)
				}
			},
		},
		{
			name: "PermissionRequest -> permission ask/user",
			rec:  model.HookRecord{Event: "PermissionRequest", SessionID: "s1", TS: base, ToolName: "Bash"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindPermission)
				if e.Permission.Tool != "Bash" || e.Permission.Decision != "ask" || e.Permission.By != "user" {
					t.Errorf("permission = %+v", e.Permission)
				}
			},
		},
		{
			name: "PermissionDenied -> permission deny/auto",
			rec:  model.HookRecord{Event: "PermissionDenied", SessionID: "s1", TS: base, ToolName: "Bash"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindPermission)
				if e.Permission.Tool != "Bash" || e.Permission.Decision != "deny" || e.Permission.By != "auto" {
					t.Errorf("permission = %+v", e.Permission)
				}
			},
		},
		{
			name: "InstructionsLoaded -> instructions",
			rec:  model.HookRecord{Event: "InstructionsLoaded", SessionID: "s1", TS: base, Path: "/repo/CLAUDE.md", Reason: "session_start"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindInstructions)
				if e.Instructions.Path != "/repo/CLAUDE.md" || e.Instructions.Reason != "session_start" {
					t.Errorf("instructions = %+v", e.Instructions)
				}
			},
		},
		{
			name: "CwdChanged -> cwd_change",
			rec:  model.HookRecord{Event: "CwdChanged", SessionID: "s1", TS: base, CWD: "/repo/sub", CwdFrom: "/repo"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindCwdChange)
				if e.CwdChange.From != "/repo" || e.CwdChange.To != "/repo/sub" {
					t.Errorf("cwd_change = %+v", e.CwdChange)
				}
			},
		},
		{
			name: "PreCompact with no transcript match -> compaction",
			rec:  model.HookRecord{Event: "PreCompact", SessionID: "s1", TS: base, Reason: "auto"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindCompaction)
				if e.Compaction.Trigger != "auto" {
					t.Errorf("compaction = %+v", e.Compaction)
				}
			},
		},
		{
			name: "Interrupt with no transcript match -> interrupt",
			rec:  model.HookRecord{Event: "Interrupt", SessionID: "s1", TS: base},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindInterrupt)
				if e.Interrupt.Reason != "user" {
					t.Errorf("interrupt = %+v", e.Interrupt)
				}
			},
		},
		{
			name: "SessionEnd -> session_end",
			rec:  model.HookRecord{Event: "SessionEnd", SessionID: "s1", TS: base, Reason: "clear"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindSessionEnd)
				if e.SessionEnd.Reason != "clear" {
					t.Errorf("session_end = %+v", e.SessionEnd)
				}
			},
		},
		{
			name: "session_shutdown (Pi) -> session_end",
			rec:  model.HookRecord{Event: "session_shutdown", SessionID: "s1", TS: base, Reason: "logout"},
			want: func(t *testing.T, s *model.Session) {
				e := findKind(t, s, model.KindSessionEnd)
				if e.SessionEnd.Reason != "logout" {
					t.Errorf("session_end = %+v", e.SessionEnd)
				}
			},
		},
		{
			name: "record with only Error -> nothing",
			rec:  model.HookRecord{Event: "SessionStart", SessionID: "s1", TS: base, Error: "boom"},
			want: func(t *testing.T, s *model.Session) {
				if len(s.Events) != 0 {
					t.Errorf("expected no events, got %+v", s.Events)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSession()
			Merge(s, []model.HookRecord{tt.rec})
			tt.want(t, s)
		})
	}
}

func TestMerge_OneRecordMultipleEvents(t *testing.T) {
	base := time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)
	s := newSession()
	rec := model.HookRecord{
		Event: "SessionStart", SessionID: "s1", TS: base,
		HeadSHA: "deadbeef", Branch: "main", Trigger: "session_start", Reason: "resume",
	}
	Merge(s, []model.HookRecord{rec})

	if len(s.Events) != 2 {
		t.Fatalf("got %d events, want 2 (git_head + reset): %+v", len(s.Events), s.Events)
	}
	if s.Events[0].ID != "hook-1" || s.Events[1].ID != "hook-2" {
		t.Errorf("IDs = %q, %q; want hook-1, hook-2", s.Events[0].ID, s.Events[1].ID)
	}
	kinds := map[model.EventKind]bool{s.Events[0].Kind: true, s.Events[1].Kind: true}
	if !kinds[model.KindGitHead] || !kinds[model.KindReset] {
		t.Errorf("kinds = %v, want git_head and reset", kinds)
	}
}

func TestMerge_Dedupe(t *testing.T) {
	base := time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)

	t.Run("compaction within 10 min updates Trigger", func(t *testing.T) {
		s := newSession()
		s.Events = append(s.Events, model.Event{
			ID: "e1", Kind: model.KindCompaction, Origin: model.OriginTranscript,
			TS: base.Add(2 * time.Minute), Compaction: &model.Compaction{},
		})
		Merge(s, []model.HookRecord{{Event: "PreCompact", SessionID: "s1", TS: base, Reason: "auto"}})

		if got := len(compactionEvents(s)); got != 1 {
			t.Fatalf("got %d compaction events, want 1: %+v", got, s.Events)
		}
		if s.Events[0].Compaction.Trigger != "auto" {
			t.Errorf("Trigger = %q, want %q", s.Events[0].Compaction.Trigger, "auto")
		}
	})

	t.Run("compaction outside 10 min adds a new event", func(t *testing.T) {
		s := newSession()
		s.Events = append(s.Events, model.Event{
			ID: "e1", Kind: model.KindCompaction, Origin: model.OriginTranscript,
			TS: base.Add(20 * time.Minute), Compaction: &model.Compaction{},
		})
		Merge(s, []model.HookRecord{{Event: "PreCompact", SessionID: "s1", TS: base, Reason: "auto"}})

		if got := len(compactionEvents(s)); got != 2 {
			t.Fatalf("got %d compaction events, want 2", got)
		}
	})

	t.Run("interrupt within 5s skipped", func(t *testing.T) {
		s := newSession()
		s.Events = append(s.Events, model.Event{
			ID: "e1", Kind: model.KindInterrupt, Origin: model.OriginTranscript,
			TS: base.Add(3 * time.Second), Interrupt: &model.Interrupt{Reason: "user"},
		})
		Merge(s, []model.HookRecord{{Event: "Interrupt", SessionID: "s1", TS: base}})

		if got := len(interruptEvents(s)); got != 1 {
			t.Fatalf("got %d interrupt events, want 1 (hook one skipped)", got)
		}
	})

	t.Run("interrupt outside 5s adds a hook event", func(t *testing.T) {
		s := newSession()
		s.Events = append(s.Events, model.Event{
			ID: "e1", Kind: model.KindInterrupt, Origin: model.OriginTranscript,
			TS: base.Add(30 * time.Second), Interrupt: &model.Interrupt{Reason: "user"},
		})
		Merge(s, []model.HookRecord{{Event: "Interrupt", SessionID: "s1", TS: base}})

		if got := len(interruptEvents(s)); got != 2 {
			t.Fatalf("got %d interrupt events, want 2", got)
		}
	})

	t.Run("instructions with same path skipped", func(t *testing.T) {
		s := newSession()
		s.Events = append(s.Events, model.Event{
			ID: "e1", Kind: model.KindInstructions, Origin: model.OriginTranscript,
			TS: base, Instructions: &model.Instructions{Path: "/repo/CLAUDE.md"},
		})
		Merge(s, []model.HookRecord{{Event: "InstructionsLoaded", SessionID: "s1", TS: base, Path: "/repo/CLAUDE.md"}})

		if got := len(instructionEvents(s)); got != 1 {
			t.Fatalf("got %d instructions events, want 1 (hook one skipped)", got)
		}
	})

	t.Run("instructions with different path adds a hook event", func(t *testing.T) {
		s := newSession()
		s.Events = append(s.Events, model.Event{
			ID: "e1", Kind: model.KindInstructions, Origin: model.OriginTranscript,
			TS: base, Instructions: &model.Instructions{Path: "/repo/OTHER.md"},
		})
		Merge(s, []model.HookRecord{{Event: "InstructionsLoaded", SessionID: "s1", TS: base, Path: "/repo/CLAUDE.md"}})

		if got := len(instructionEvents(s)); got != 2 {
			t.Fatalf("got %d instructions events, want 2", got)
		}
	})

	t.Run("reset within 10s of same type skipped", func(t *testing.T) {
		s := newSession()
		s.Events = append(s.Events, model.Event{
			ID: "e1", Kind: model.KindReset, Origin: model.OriginTranscript,
			TS: base.Add(5 * time.Second), Reset: &model.Reset{Type: "resume"},
		})
		Merge(s, []model.HookRecord{{Event: "SessionStart", SessionID: "s1", TS: base, Reason: "resume"}})

		if got := len(resetEvents(s)); got != 1 {
			t.Fatalf("got %d reset events, want 1 (hook one skipped)", got)
		}
	})
}

func TestMerge_SetsCaptureAndFinalizes(t *testing.T) {
	s := newSession()
	if s.Capture != model.CaptureReconstructed {
		t.Fatalf("precondition: Capture = %q, want reconstructed", s.Capture)
	}

	base := time.Date(2026, 9, 27, 14, 5, 0, 0, time.UTC)
	Merge(s, []model.HookRecord{{Event: "SessionEnd", SessionID: "s1", TS: base, Reason: "clear"}})

	if s.Capture != model.CaptureHooked {
		t.Errorf("Capture = %q, want hooked", s.Capture)
	}
	// Finalize should have run: Seq set, End updated to the new event's TS.
	last := s.Events[len(s.Events)-1]
	if last.Seq != len(s.Events)-1 {
		t.Errorf("Seq = %d, want %d (Finalize not called)", last.Seq, len(s.Events)-1)
	}
	if !s.End.Equal(base) {
		t.Errorf("End = %v, want %v (Finalize not called)", s.End, base)
	}
}

func TestMerge_NoRecords_NoCaptureChange(t *testing.T) {
	s := newSession()
	Merge(s, nil)
	if s.Capture != model.CaptureReconstructed {
		t.Errorf("Capture = %q, want unchanged (reconstructed)", s.Capture)
	}
}

// --- test helpers ---

func findKind(t *testing.T, s *model.Session, k model.EventKind) model.Event {
	t.Helper()
	for _, e := range s.Events {
		if e.Kind == k {
			return e
		}
	}
	t.Fatalf("no event of kind %s found in %+v", k, s.Events)
	return model.Event{}
}

func compactionEvents(s *model.Session) []model.Event  { return byKind(s, model.KindCompaction) }
func interruptEvents(s *model.Session) []model.Event   { return byKind(s, model.KindInterrupt) }
func instructionEvents(s *model.Session) []model.Event { return byKind(s, model.KindInstructions) }
func resetEvents(s *model.Session) []model.Event       { return byKind(s, model.KindReset) }

func byKind(s *model.Session, k model.EventKind) []model.Event {
	var out []model.Event
	for _, e := range s.Events {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}
