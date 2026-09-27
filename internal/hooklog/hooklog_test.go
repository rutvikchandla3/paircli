package hooklog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

func mustAppend(t *testing.T, dir string, rec model.HookRecord) {
	t.Helper()
	if err := Append(dir, rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestAppendRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	day1 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)

	mustAppend(t, dir, model.HookRecord{Harness: model.HarnessClaudeCode, Event: "SessionStart", SessionID: "s1", TS: day1})
	mustAppend(t, dir, model.HookRecord{Harness: model.HarnessClaudeCode, Event: "Stop", SessionID: "s1", TS: day1.Add(time.Minute)})
	mustAppend(t, dir, model.HookRecord{Harness: model.HarnessClaudeCode, Event: "SessionStart", SessionID: "s2", TS: day2})
	// Different harness: must not show up in a claude-code Read.
	mustAppend(t, dir, model.HookRecord{Harness: model.HarnessCodex, Event: "SessionStart", SessionID: "s1", TS: day1})

	// A malformed line alongside valid ones in the day-1 log must be skipped.
	path := filepath.Join(dir, "claude-code", day1.Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := Read(dir, model.HarnessClaudeCode, time.Time{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Read: got %d sessions, want 2 (%v)", len(got), keys(got))
	}
	if len(got["s1"]) != 2 {
		t.Fatalf("Read: s1 has %d records, want 2", len(got["s1"]))
	}
	if got["s1"][0].Event != "SessionStart" || got["s1"][1].Event != "Stop" {
		t.Errorf("s1 records out of order: %+v", got["s1"])
	}
	if len(got["s2"]) != 1 {
		t.Fatalf("Read: s2 has %d records, want 1", len(got["s2"]))
	}

	// since filter: only day2 onward.
	got2, err := Read(dir, model.HarnessClaudeCode, day2)
	if err != nil {
		t.Fatalf("Read since: %v", err)
	}
	if _, ok := got2["s1"]; ok {
		t.Errorf("Read since day2: s1 should be filtered out, got %+v", got2["s1"])
	}
	if len(got2["s2"]) != 1 {
		t.Errorf("Read since day2: s2 = %+v, want 1 record", got2["s2"])
	}
}

func keys(m map[string][]model.HookRecord) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestAppendRead_GroupOrderBySameTS(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	mustAppend(t, dir, model.HookRecord{Harness: model.HarnessClaudeCode, Event: "Stop", SessionID: "s1", TS: ts})
	mustAppend(t, dir, model.HookRecord{Harness: model.HarnessClaudeCode, Event: "PermissionDenied", SessionID: "s1", TS: ts})

	got, err := Read(dir, model.HarnessClaudeCode, time.Time{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	recs := got["s1"]
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Event != "PermissionDenied" || recs[1].Event != "Stop" {
		t.Errorf("same-TS records not sorted by Event: %+v", recs)
	}
}
