package consistency

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// con2Todo builds a todo event. Replace makes Items the whole list.
func con2Todo(replace bool, items ...model.TodoItem) model.Event {
	return model.Event{Kind: model.KindTodo, Todo: &model.Todo{Tool: "TodoWrite", Replace: replace, Items: items}}
}

// con2Find runs every detector and returns the CON-2 signal.
func con2Find(t *testing.T, ctx *engine.Context) *model.Signal {
	t.Helper()
	sig := testkit.Find(engine.Run(ctx), "CON-2")
	if sig == nil {
		t.Fatal("CON-2 signal not found")
	}
	return sig
}

// con2CheckSummary fails when the summary breaks the 160-rune cap.
func con2CheckSummary(t *testing.T, sig *model.Signal) {
	t.Helper()
	if n := len([]rune(sig.Summary)); n > 160 {
		t.Errorf("CON-2 summary too long (%d > 160): %s", n, sig.Summary)
	}
}

func TestCON2_ReplaceThenIncremental(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Add(con2Todo(true,
		model.TodoItem{ID: "1", Text: "write tests", Status: "pending"},
		model.TodoItem{ID: "2", Text: "fix bug", Status: "pending"},
	))
	// A later incremental update completes task 1 and leaves task 2 open.
	sb.Add(con2Todo(false, model.TodoItem{ID: "1", Status: "completed"}))

	sig := con2Find(t, testkit.Ctx(nil, sb.Build()))

	if sig.State != model.StateAlert {
		t.Errorf("state = %s, want alert", sig.State)
	}
	if got, want := sig.Summary, "1 tasks still open at session end."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	want := "Task still open at session end: “fix bug”"
	if sig.Findings[0].Summary != want {
		t.Errorf("finding = %q, want %q", sig.Findings[0].Summary, want)
	}
	if sig.Findings[0].Severity != model.StateAlert {
		t.Errorf("severity = %s, want alert", sig.Findings[0].Severity)
	}
	if got := sig.Data["open_tasks"]; got != 1 {
		t.Errorf("data[open_tasks] = %v, want 1", got)
	}
	if got := sig.Data["todo_comments"]; got != 0 {
		t.Errorf("data[todo_comments] = %v, want 0", got)
	}
	con2CheckSummary(t, sig)
}

func TestCON2_CompletedNotOpen(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Add(con2Todo(true,
		model.TodoItem{ID: "1", Text: "a", Status: "completed"},
		model.TodoItem{ID: "2", Text: "b", Status: "done"},
		model.TodoItem{ID: "3", Text: "c", Status: "cancelled"},
		model.TodoItem{ID: "4", Text: "d", Status: "canceled"},
		model.TodoItem{ID: "5", Text: "e", Status: "deleted"},
	))

	sig := con2Find(t, testkit.Ctx(nil, sb.Build()))

	if sig.State != model.StateClear {
		t.Errorf("state = %s, want clear", sig.State)
	}
	if sig.Summary != "No open tasks and no TODO/FIXME comments added." {
		t.Errorf("summary = %q", sig.Summary)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("findings = %d, want 0", len(sig.Findings))
	}
	con2CheckSummary(t, sig)
}

func TestCON2_TodoComments(t *testing.T) {
	pr := testkit.PR("acme/shop", 1)
	pr.Add("file.txt", 1,
		"// TODO: fix this",
		"plain line",
		"// FIXME: later",
	)

	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("edit")

	sig := con2Find(t, testkit.Ctx(pr.Build(), sb.Build()))

	if sig.State != model.StateInfo {
		t.Errorf("state = %s, want info", sig.State)
	}
	if got, want := sig.Summary, "2 TODO/FIXME comments added."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if len(sig.Findings) != 2 {
		t.Fatalf("findings = %d, want 2: %#v", len(sig.Findings), sig.Findings)
	}
	want := []string{
		"`file.txt:1` adds a TODO: // TODO: fix this",
		"`file.txt:3` adds a FIXME: // FIXME: later",
	}
	for i, w := range want {
		if sig.Findings[i].Summary != w {
			t.Errorf("finding[%d] = %q, want %q", i, sig.Findings[i].Summary, w)
		}
		if sig.Findings[i].Severity != model.StateInfo {
			t.Errorf("finding[%d] severity = %s, want info", i, sig.Findings[i].Severity)
		}
		if len(sig.Findings[i].Anchors) != 1 || sig.Findings[i].Anchors[0].File != "file.txt" {
			t.Errorf("finding[%d] anchors = %+v, want file.txt", i, sig.Findings[i].Anchors)
		}
	}
	if got := sig.Data["todo_comments"]; got != 2 {
		t.Errorf("data[todo_comments] = %v, want 2", got)
	}
	con2CheckSummary(t, sig)
}

func TestCON2_GeneratedIgnored(t *testing.T) {
	pr := testkit.PR("acme/shop", 1)
	pr.Add("internal/api.pb.go", 1, "// TODO: regenerate me")

	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("edit")

	sig := con2Find(t, testkit.Ctx(pr.Build(), sb.Build()))

	if len(sig.Findings) != 0 {
		t.Errorf("findings = %d, want 0 (generated files are skipped)", len(sig.Findings))
	}
	if got := sig.Data["todo_comments"]; got != 0 {
		t.Errorf("data[todo_comments] = %v, want 0", got)
	}
	if sig.State != model.StateClear {
		t.Errorf("state = %s, want clear", sig.State)
	}
	con2CheckSummary(t, sig)
}

func TestCON2_Clear(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("nothing to do").Say("ok")

	sig := con2Find(t, testkit.Ctx(nil, sb.Build()))

	if sig.State != model.StateClear {
		t.Errorf("state = %s, want clear", sig.State)
	}
	if sig.Summary != "No open tasks and no TODO/FIXME comments added." {
		t.Errorf("summary = %q", sig.Summary)
	}
	con2CheckSummary(t, sig)
}
