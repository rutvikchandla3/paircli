package codex

import "testing"

func TestParseUnifiedHunks(t *testing.T) {
	diff := "@@ -1,3 +1,4 @@\n" +
		" context one\n" +
		"-old line\n" +
		"+new line\n" +
		"+another new line\n" +
		" context two\n" +
		"@@ -10,2 +11,2 @@\n" +
		"-tail old\n" +
		"+tail new\n" +
		"\\ No newline at end of file\n"

	hunks, added, removed := parseUnifiedHunks(diff)

	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(hunks))
	}
	h0 := hunks[0]
	if h0.OldStart != 1 || h0.OldLines != 3 || h0.NewStart != 1 || h0.NewLines != 4 {
		t.Errorf("hunk[0] header = %+v", h0)
	}
	if len(h0.Lines) != 5 {
		t.Errorf("hunk[0] lines = %d, want 5", len(h0.Lines))
	}
	if h0.Lines[0] != " context one" || h0.Lines[1] != "-old line" {
		t.Errorf("hunk[0] lines = %v", h0.Lines)
	}

	h1 := hunks[1]
	if h1.OldStart != 10 || h1.OldLines != 2 || h1.NewStart != 11 || h1.NewLines != 2 {
		t.Errorf("hunk[1] header = %+v", h1)
	}
	// "\ No newline at end of file" must not become a Lines entry.
	if len(h1.Lines) != 2 {
		t.Errorf("hunk[1] lines = %v, want 2 (no-newline marker dropped)", h1.Lines)
	}

	wantAdded := []string{"new line", "another new line", "tail new"}
	if !stringsEqual(added, wantAdded) {
		t.Errorf("added = %v, want %v", added, wantAdded)
	}
	wantRemoved := []string{"old line", "tail old"}
	if !stringsEqual(removed, wantRemoved) {
		t.Errorf("removed = %v, want %v", removed, wantRemoved)
	}
}

func TestParseUnifiedHunks_Empty(t *testing.T) {
	hunks, added, removed := parseUnifiedHunks("")
	if hunks != nil || added != nil || removed != nil {
		t.Errorf("parseUnifiedHunks(\"\") = %v, %v, %v, want all nil", hunks, added, removed)
	}
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
