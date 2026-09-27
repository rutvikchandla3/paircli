package pi

import (
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

const mainFixture = "testdata/--work-shop--/2026-09-27T14-00-00-000Z_8f0c2c7e-0000-4000-8000-000000000003.jsonl"

func parseFixture(t *testing.T, path string) *model.Session {
	t.Helper()
	sessions, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", path, err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ParseFile(%s) returned %d sessions, want 1", path, len(sessions))
	}
	return sessions[0]
}

func eventByID(events []model.Event, id string) *model.Event {
	for i := range events {
		if events[i].ID == id {
			return &events[i]
		}
	}
	return nil
}

func findCommand(t *testing.T, sess *model.Session, cmd string) *model.Command {
	t.Helper()
	for _, e := range sess.Events {
		if e.Kind == model.KindCommand && e.Command != nil && e.Command.Cmd == cmd {
			return e.Command
		}
	}
	t.Fatalf("no command event with Cmd=%q", cmd)
	return nil
}

func equalStrings(a, b []string) bool {
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

func TestDiscover_Recursive(t *testing.T) {
	files, err := Discover("testdata", time.Time{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{
		"testdata/--legacy--/v1.jsonl",
		mainFixture,
		"testdata/--work-shop--/nested/run-1/session.jsonl",
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(files), len(want), files)
	}
	for i, w := range want {
		if files[i] != w {
			t.Errorf("file %d = %s, want %s", i, files[i], w)
		}
	}

	future := time.Now().Add(24 * time.Hour)
	none, err := Discover("testdata", future)
	if err != nil {
		t.Fatalf("Discover with future since: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected no files with a since in the future, got %v", none)
	}
}

func TestParse_Header(t *testing.T) {
	sess := parseFixture(t, mainFixture)
	if sess.Harness != model.HarnessPi {
		t.Errorf("Harness = %s, want pi", sess.Harness)
	}
	if sess.ID != "8f0c2c7e-0000-4000-8000-000000000003" {
		t.Errorf("ID = %s", sess.ID)
	}
	if sess.CWD != "/work/shop" {
		t.Errorf("CWD = %s", sess.CWD)
	}
	if sess.ParentID != "1111aaaa-0000-4000-8000-000000000001" {
		t.Errorf("ParentID = %s", sess.ParentID)
	}
	if sess.Title != "Add retry with backoff" {
		t.Errorf("Title = %s", sess.Title)
	}
	if sess.Capture != model.CaptureReconstructed {
		t.Errorf("Capture = %s, want reconstructed", sess.Capture)
	}
}

func TestParse_OffBranch(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	off := eventByID(sess.Events, "call_12")
	if off == nil || off.Kind != model.KindEdit {
		t.Fatalf("expected an edit event with ID call_12")
	}
	if !off.OffBranch {
		t.Errorf("call_12 edit: OffBranch = false, want true (it is on the abandoned branch)")
	}

	for _, id := range []string{"call_1", "call_2"} {
		e := eventByID(sess.Events, id)
		if e == nil || e.Kind != model.KindEdit {
			t.Fatalf("expected an edit event with ID %s", id)
		}
		if e.OffBranch {
			t.Errorf("%s edit: OffBranch = true, want false", id)
		}
	}
}

func TestParse_EditDiff(t *testing.T) {
	sess := parseFixture(t, mainFixture)
	e := eventByID(sess.Events, "call_1")
	if e == nil {
		t.Fatal("no event with ID call_1")
	}
	fe := e.Edit
	if fe.Path != "src/webhooks/retry.ts" || fe.Op != model.OpUpdate || fe.Via != "edit" {
		t.Errorf("unexpected edit: %+v", fe)
	}
	wantAdded := []string{"  return sendWithRetry(url, body);"}
	wantRemoved := []string{"  return post(url, body);"}
	if !equalStrings(fe.Added, wantAdded) {
		t.Errorf("Added = %q, want %q", fe.Added, wantAdded)
	}
	if !equalStrings(fe.Removed, wantRemoved) {
		t.Errorf("Removed = %q, want %q", fe.Removed, wantRemoved)
	}
}

func TestParse_EditFallback(t *testing.T) {
	sess := parseFixture(t, mainFixture)
	e := eventByID(sess.Events, "call_2")
	if e == nil {
		t.Fatal("no event with ID call_2")
	}
	fe := e.Edit
	wantAdded := []string{"const maxAttempts = 5;"}
	wantRemoved := []string{"const maxAttempts = 3;"}
	if !equalStrings(fe.Added, wantAdded) {
		t.Errorf("Added = %q, want %q", fe.Added, wantAdded)
	}
	if !equalStrings(fe.Removed, wantRemoved) {
		t.Errorf("Removed = %q, want %q", fe.Removed, wantRemoved)
	}
}

func TestParse_WriteReadBash(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	w := eventByID(sess.Events, "call_3")
	if w == nil || w.Kind != model.KindEdit {
		t.Fatal("expected a write (file_edit) event with ID call_3")
	}
	if w.Edit.Op != model.OpCreate || w.Edit.Via != "write" || w.Edit.Path != "src/webhooks/backoff.ts" {
		t.Errorf("unexpected write edit: %+v", w.Edit)
	}
	wantAdded := []string{
		"export function backoff(n: number): number {",
		"  return Math.min(1000 * 2 ** n, 30000);",
		"}",
	}
	if !equalStrings(w.Edit.Added, wantAdded) {
		t.Errorf("write Added = %q, want %q", w.Edit.Added, wantAdded)
	}

	r := eventByID(sess.Events, "call_4")
	if r == nil || r.Kind != model.KindRead || r.Read.Path != "src/webhooks/index.ts" {
		t.Fatalf("unexpected read event: %+v", r)
	}

	ok := eventByID(sess.Events, "call_5")
	if ok == nil || ok.Kind != model.KindCommand {
		t.Fatal("expected a command event with ID call_5")
	}
	if ok.Command.Status != model.CmdOK || ok.Command.ExitCode == nil || *ok.Command.ExitCode != 0 {
		t.Errorf("bash success: %+v", ok.Command)
	}

	fail := eventByID(sess.Events, "call_6")
	if fail == nil || fail.Kind != model.KindCommand {
		t.Fatal("expected a command event with ID call_6")
	}
	if fail.Command.Status != model.CmdFailed || fail.Command.ExitCode == nil || *fail.Command.ExitCode != 3 {
		t.Errorf("bash error: %+v", fail.Command)
	}
}

func TestParse_UserBash(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	cancelled := findCommand(t, sess, "sleep 100")
	if !cancelled.ByUser {
		t.Errorf("expected ByUser = true")
	}
	if cancelled.Status != model.CmdInterrupted {
		t.Errorf("Status = %s, want interrupted", cancelled.Status)
	}

	ok := findCommand(t, sess, "git status")
	if !ok.ByUser {
		t.Errorf("expected ByUser = true")
	}
	if ok.Status != model.CmdOK || ok.ExitCode == nil || *ok.ExitCode != 0 {
		t.Errorf("unexpected command: %+v", ok)
	}
}

func TestParse_Lookups(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	search := eventByID(sess.Events, "call_7")
	if search == nil || search.Kind != model.KindLookup {
		t.Fatal("expected a lookup event with ID call_7")
	}
	wantQuery := "golang backoff jitter | exponential backoff patterns"
	if search.Lookup.Kind != "search" || search.Lookup.Query != wantQuery {
		t.Errorf("search lookup = %+v, want Query %q", search.Lookup, wantQuery)
	}

	fetch := eventByID(sess.Events, "call_8")
	if fetch == nil || fetch.Kind != model.KindLookup {
		t.Fatal("expected a lookup event with ID call_8")
	}
	wantURLs := []string{"https://example.com/backoff"}
	if fetch.Lookup.Kind != "fetch" || !equalStrings(fetch.Lookup.URLs, wantURLs) {
		t.Errorf("fetch lookup = %+v, want URLs %q", fetch.Lookup, wantURLs)
	}
}

func TestParse_Subagent(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	sub := eventByID(sess.Events, "call_9")
	if sub == nil || sub.Kind != model.KindSubagent {
		t.Fatal("expected a subagent event with ID call_9")
	}
	if sub.Subagent.Type != "code-reviewer" {
		t.Errorf("Type = %s, want code-reviewer", sub.Subagent.Type)
	}
	if sub.Subagent.Prompt != "Review the retry helper for edge cases." {
		t.Errorf("Prompt = %s", sub.Subagent.Prompt)
	}
	if sub.Subagent.Status != "" {
		t.Errorf("Status = %s, want empty", sub.Subagent.Status)
	}

	if e := eventByID(sess.Events, "call_10"); e != nil {
		t.Errorf("expected the action:list subagent call to be ignored, got %+v", e)
	}
}

func TestParse_StopReasons(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	var interrupted, failed *model.Event
	for i, e := range sess.Events {
		switch e.Kind {
		case model.KindInterrupt:
			interrupted = &sess.Events[i]
		case model.KindFailure:
			failed = &sess.Events[i]
		}
	}
	if interrupted == nil || interrupted.Interrupt.Reason != "aborted" {
		t.Errorf("interrupt event = %+v", interrupted)
	}
	if failed == nil || failed.Failure.Type != "api_error" || failed.Failure.Message != "upstream 529 overloaded" {
		t.Errorf("failure event = %+v", failed)
	}
}

func TestParse_ModelChanges(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	var changes []model.Event
	for _, e := range sess.Events {
		if e.Kind == model.KindModelChange {
			changes = append(changes, e)
		}
	}
	if len(changes) != 2 {
		t.Fatalf("got %d model_change events, want 2: %+v", len(changes), changes)
	}
	if changes[0].ModelChange.Model != "claude-opus-5-5" || changes[0].ModelChange.Provider != "anthropic" {
		t.Errorf("first model_change = %+v", changes[0].ModelChange)
	}
	if changes[1].ModelChange.Model != "claude-opus-5-5" || changes[1].ModelChange.Effort != "high" {
		t.Errorf("second model_change (thinking level, carrying the current model) = %+v", changes[1].ModelChange)
	}
}

func TestParse_Instructions(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	var instr []model.Event
	for _, e := range sess.Events {
		if e.Kind == model.KindInstructions {
			instr = append(instr, e)
		}
	}
	if len(instr) != 2 {
		t.Fatalf("got %d instructions events, want 2", len(instr))
	}
	if instr[0].Instructions.Path != "AGENTS.md" || instr[0].Instructions.Scope != "project" {
		t.Errorf("first instructions = %+v", instr[0].Instructions)
	}
	if instr[0].Instructions.Content != "Run go test ./... before committing." {
		t.Errorf("first instructions content = %q", instr[0].Instructions.Content)
	}
	if instr[1].Instructions.Path != "docs/CONTRIBUTING.md" {
		t.Errorf("second instructions = %+v", instr[1].Instructions)
	}
	if instr[0].ID != "e01/1" || instr[1].ID != "e01/2" {
		t.Errorf("instructions IDs = %s, %s, want e01/1, e01/2", instr[0].ID, instr[1].ID)
	}
}

func TestParse_Usage(t *testing.T) {
	sess := parseFixture(t, mainFixture)

	first := eventByID(sess.Events, "e06/1")
	second := eventByID(sess.Events, "e06/2")
	if first == nil || second == nil {
		t.Fatalf("expected assistant_message events e06/1 and e06/2")
	}
	if first.Message.Text != "Adding the retry loop." {
		t.Errorf("first message text = %q", first.Message.Text)
	}
	if first.Message.Usage == nil || first.Message.Usage.CostUSD != 0.0412 {
		t.Errorf("first message usage = %+v, want CostUSD 0.0412", first.Message.Usage)
	}
	if second.Message.Text != "This should handle transient 5xx errors too." {
		t.Errorf("second message text = %q", second.Message.Text)
	}
	if second.Message.Usage != nil {
		t.Errorf("second message usage = %+v, want nil (usage only attaches to the first)", second.Message.Usage)
	}
}

func TestParse_V1(t *testing.T) {
	sess := parseFixture(t, "testdata/--legacy--/v1.jsonl")
	if sess.ID != "1111aaaa-0000-4000-8000-000000000001" {
		t.Errorf("ID = %s", sess.ID)
	}
	if len(sess.Events) == 0 {
		t.Fatal("expected events")
	}
	for _, e := range sess.Events {
		if e.OffBranch {
			t.Errorf("event %s: OffBranch = true, want false for a version-1 (no ids) file", e.ID)
		}
	}
}

func TestHookRecords(t *testing.T) {
	recs, err := HookRecords(mainFixture)
	if err != nil {
		t.Fatalf("HookRecords: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d hook records, want 2", len(recs))
	}

	commit := recs[0]
	if commit.SessionID != "8f0c2c7e-0000-4000-8000-000000000003" {
		t.Errorf("commit.SessionID = %s", commit.SessionID)
	}
	if commit.Harness != model.HarnessPi {
		t.Errorf("commit.Harness = %s", commit.Harness)
	}
	if commit.HeadSHA != "9c1e0d2f9c1e0d2f9c1e0d2f9c1e0d2f9c1e0d2f" {
		t.Errorf("commit.HeadSHA = %s", commit.HeadSHA)
	}
	wantTS := time.Date(2026, 9, 27, 14, 0, 39, 0, time.UTC)
	if !commit.TS.Equal(wantTS) {
		t.Errorf("commit.TS = %v, want %v", commit.TS, wantTS)
	}

	stop := recs[1]
	if stop.SessionID != "8f0c2c7e-0000-4000-8000-000000000003" {
		t.Errorf("stop.SessionID (fallback from header) = %s", stop.SessionID)
	}
	if stop.Harness != model.HarnessPi {
		t.Errorf("stop.Harness = %s", stop.Harness)
	}
	wantStopTS := time.Date(2026, 9, 27, 14, 0, 40, 0, time.UTC)
	if !stop.TS.Equal(wantStopTS) {
		t.Errorf("stop.TS (fallback from entry timestamp) = %v, want %v", stop.TS, wantStopTS)
	}
	if len(stop.Snapshot) != 1 || stop.Snapshot[0].Path != "src/webhooks/retry.ts" {
		t.Errorf("stop.Snapshot = %+v", stop.Snapshot)
	}

	sess := parseFixture(t, mainFixture)
	if e := eventByID(sess.Events, "e36"); e != nil {
		t.Errorf("expected no event for the paircli custom entry e36, got %+v", e)
	}
	if e := eventByID(sess.Events, "e37"); e != nil {
		t.Errorf("expected no event for the paircli custom entry e37, got %+v", e)
	}
}
