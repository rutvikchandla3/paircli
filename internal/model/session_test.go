package model

import (
	"testing"
	"time"
)

func TestFinalize_SortsAndSequences(t *testing.T) {
	now := time.Now()
	s := &Session{
		Harness: HarnessClaudeCode,
		ID:      "test",
		Events: []Event{
			{ID: "e3", Kind: KindMessage, TS: now.Add(3 * time.Second), Message: &Message{}},
			{ID: "e1", Kind: KindMessage, TS: now, Message: &Message{}},
			{ID: "e2", Kind: KindMessage, TS: now.Add(time.Second), Message: &Message{}},
		},
	}

	s.Finalize()

	// Check that events are sorted by TS
	if s.Events[0].ID != "e1" || s.Events[1].ID != "e2" || s.Events[2].ID != "e3" {
		t.Errorf("Events not sorted by TS")
	}

	// Check that Seq is set to index
	for i, e := range s.Events {
		if e.Seq != i {
			t.Errorf("Event[%d].Seq = %d, want %d", i, e.Seq, i)
		}
	}
}

func TestFinalize_Turns(t *testing.T) {
	now := time.Now()
	s := &Session{
		Harness: HarnessClaudeCode,
		ID:      "test",
		Events: []Event{
			// Turn 0: before the first prompt
			{ID: "e0", Kind: KindMessage, TS: now, Message: &Message{}},

			// Turn 1: after the first prompt
			{ID: "e1", Kind: KindPrompt, TS: now.Add(time.Second), Prompt: &Prompt{Text: "test"}},
			{ID: "e2", Kind: KindMessage, TS: now.Add(2 * time.Second), Message: &Message{}},
			{ID: "e3", Kind: KindPrompt, TS: now.Add(3 * time.Second), Prompt: &Prompt{Text: "steer", Steering: true}},
			{ID: "e4", Kind: KindMessage, TS: now.Add(4 * time.Second), Message: &Message{}},

			// Turn 2: after the second prompt
			{ID: "e5", Kind: KindPrompt, TS: now.Add(5 * time.Second), Prompt: &Prompt{Text: "test2"}},
			{ID: "e6", Kind: KindMessage, TS: now.Add(6 * time.Second), Message: &Message{}},

			// Subagent prompt should not advance the turn
			{ID: "e7", Kind: KindPrompt, TS: now.Add(7 * time.Second), AgentID: "sub1", Prompt: &Prompt{Text: "sub"}},
			{ID: "e8", Kind: KindMessage, TS: now.Add(8 * time.Second), AgentID: "sub1", Message: &Message{}},
		},
	}

	s.Finalize()

	tests := []struct {
		id   string
		turn int
	}{
		{"e0", 0}, // before first prompt
		{"e1", 1}, // first regular prompt
		{"e2", 1}, // message after first prompt
		{"e3", 1}, // steering prompt (doesn't advance)
		{"e4", 1}, // message after steering
		{"e5", 2}, // second regular prompt
		{"e6", 2}, // message after second prompt
		{"e7", 2}, // subagent prompt (doesn't advance)
		{"e8", 2}, // subagent message
	}

	for _, tt := range tests {
		for _, e := range s.Events {
			if e.ID == tt.id {
				if e.Turn != tt.turn {
					t.Errorf("Event %s: Turn = %d, want %d", tt.id, e.Turn, tt.turn)
				}
				break
			}
		}
	}
}

func TestFinalize_FinalMessage(t *testing.T) {
	now := time.Now()
	s := &Session{
		Harness: HarnessClaudeCode,
		ID:      "test",
		Events: []Event{
			{ID: "e1", Kind: KindPrompt, TS: now, Prompt: &Prompt{Text: "test"}},

			// Two messages before a prompt
			{ID: "e2", Kind: KindMessage, TS: now.Add(time.Second), Message: &Message{Final: false}},
			{ID: "e3", Kind: KindMessage, TS: now.Add(2 * time.Second), Message: &Message{Final: false}},

			// Next prompt
			{ID: "e4", Kind: KindPrompt, TS: now.Add(3 * time.Second), Prompt: &Prompt{Text: "test2"}},

			// Last message of the session
			{ID: "e5", Kind: KindMessage, TS: now.Add(4 * time.Second), Message: &Message{Final: false}},

			// Subagent message (should never be final)
			{ID: "e6", Kind: KindMessage, TS: now.Add(5 * time.Second), AgentID: "sub1", Message: &Message{Final: true}},
		},
	}

	s.Finalize()

	for _, e := range s.Events {
		if e.Message == nil {
			continue
		}
		switch e.ID {
		case "e2":
			if e.Message.Final {
				t.Errorf("Event e2: Final = true, want false (not the last before prompt)")
			}
		case "e3":
			if !e.Message.Final {
				t.Errorf("Event e3: Final = false, want true (last before prompt)")
			}
		case "e5":
			if !e.Message.Final {
				t.Errorf("Event e5: Final = false, want true (last message of session)")
			}
		case "e6":
			if e.Message.Final {
				t.Errorf("Event e6: Final = true, want false (subagent message never final)")
			}
		}
	}
}

func TestFinalize_StartEnd(t *testing.T) {
	now := time.Now()
	s := &Session{
		Harness: HarnessClaudeCode,
		ID:      "test",
		Events: []Event{
			{ID: "e1", Kind: KindPrompt, TS: now, Prompt: &Prompt{Text: "test"}},
			{ID: "e2", Kind: KindMessage, TS: now.Add(time.Second), Message: &Message{}},
			{ID: "e3", Kind: KindMessage, TS: now.Add(2 * time.Second), Message: &Message{}},
		},
	}

	s.Finalize()

	if s.Start != now {
		t.Errorf("Session.Start = %v, want %v", s.Start, now)
	}

	if s.End != now.Add(2*time.Second) {
		t.Errorf("Session.End = %v, want %v", s.End, now.Add(2*time.Second))
	}
}
