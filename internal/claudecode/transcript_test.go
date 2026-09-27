package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

const fixtureRoot = "testdata/projects"

func mustParseS1(t *testing.T) *model.Session {
	t.Helper()
	sessions, err := ParseFile(filepath.Join(fixtureRoot, "-work-shop", "s1.jsonl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	return sessions[0]
}

func eventsOf(s *model.Session, kind model.EventKind) []model.Event {
	var out []model.Event
	for _, e := range s.Events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("bad time %q: %v", s, err)
	}
	return tm.UTC()
}

func TestDiscover(t *testing.T) {
	files, err := Discover(fixtureRoot, time.Time{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{
		filepath.Join(fixtureRoot, "-work-shop", "s1.jsonl"),
		filepath.Join(fixtureRoot, "-work-shop", "s2.jsonl"),
	}
	if len(files) != len(want) {
		t.Fatalf("got %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("files[%d] = %q, want %q", i, files[i], want[i])
		}
	}
	for _, f := range files {
		if filepath.Base(filepath.Dir(f)) == "subagents" {
			t.Errorf("Discover returned a subagent file: %s", f)
		}
	}
}

func TestDiscover_Since(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "-proj")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(sub, "old.jsonl")
	newer := filepath.Join(sub, "new.jsonl")
	if err := os.WriteFile(older, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	files, err := Discover(dir, since)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(files) != 1 || files[0] != newer {
		t.Fatalf("got %v, want only %s", files, newer)
	}
}

func TestDiscover_MissingRoot(t *testing.T) {
	files, err := Discover(filepath.Join(fixtureRoot, "does-not-exist"), time.Time{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if files != nil {
		t.Fatalf("got %v, want nil", files)
	}
}

func TestParse_SessionFields(t *testing.T) {
	s := mustParseS1(t)

	if s.Harness != model.HarnessClaudeCode {
		t.Errorf("Harness = %q", s.Harness)
	}
	if s.ID != "s1" {
		t.Errorf("ID = %q, want s1", s.ID)
	}
	if s.CWD != "/work/shop" {
		t.Errorf("CWD = %q", s.CWD)
	}
	if s.Branch != "feat/retry" {
		t.Errorf("Branch = %q", s.Branch)
	}
	if s.HarnessVersion != "2.1.283" {
		t.Errorf("HarnessVersion = %q", s.HarnessVersion)
	}
	if s.Title != "Add retry with backoff" {
		t.Errorf("Title = %q", s.Title)
	}
	if s.Capture != model.CaptureReconstructed {
		t.Errorf("Capture = %q", s.Capture)
	}
	wantStart := mustTime(t, "2026-09-27T14:00:00.000Z")
	wantEnd := mustTime(t, "2026-09-27T14:00:44.000Z")
	if !s.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", s.Start, wantStart)
	}
	if !s.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v", s.End, wantEnd)
	}
}

func TestParse_Prompts(t *testing.T) {
	s := mustParseS1(t)
	prompts := eventsOf(s, model.KindPrompt)

	var typed, slash, steering *model.Event
	for i := range prompts {
		p := prompts[i].Prompt
		switch {
		case p.Text == "Add retry with backoff to the webhook sender. Don't touch the queue." && p.SlashCommand == "":
			typed = &prompts[i]
		case p.SlashCommand == "/deep-research":
			slash = &prompts[i]
		case p.Steering:
			steering = &prompts[i]
		}
	}

	if typed == nil {
		t.Fatal("typed prompt not found")
	}
	if typed.ID != "u1" {
		t.Errorf("typed prompt ID = %q, want u1", typed.ID)
	}

	if slash == nil {
		t.Fatal("slash command prompt not found")
	}
	if slash.Prompt.Text != "rate limiting patterns" {
		t.Errorf("slash args = %q", slash.Prompt.Text)
	}

	if steering == nil {
		t.Fatal("steering prompt (queued_command) not found")
	}
	if steering.Prompt.Text != "Actually also update the changelog." {
		t.Errorf("steering text = %q", steering.Prompt.Text)
	}

	// task-notification queued_command must be ignored: only one steering prompt.
	steeringCount := 0
	for _, p := range prompts {
		if p.Prompt.Steering {
			steeringCount++
		}
	}
	if steeringCount != 1 {
		t.Errorf("steering prompt count = %d, want 1", steeringCount)
	}

	// isMeta and local-command-stdout lines must not produce prompts.
	for _, p := range prompts {
		if p.Prompt.Text == "System note: context refreshed." {
			t.Error("isMeta line produced a prompt")
		}
		if p.Prompt.Text == "ok" {
			t.Error("local-command-stdout line produced a prompt")
		}
	}
}

func TestParse_Reset(t *testing.T) {
	s := mustParseS1(t)
	resets := eventsOf(s, model.KindReset)
	if len(resets) != 1 {
		t.Fatalf("got %d reset events, want 1", len(resets))
	}
	if resets[0].Reset.Type != "clear" {
		t.Errorf("Reset.Type = %q, want clear", resets[0].Reset.Type)
	}
	if resets[0].ID != "u3" {
		t.Errorf("Reset ID = %q, want u3", resets[0].ID)
	}
}

func TestParse_Edit(t *testing.T) {
	s := mustParseS1(t)
	edits := eventsOf(s, model.KindEdit)

	var e *model.Event
	for i := range edits {
		if edits[i].ID == "toolu_01A" {
			e = &edits[i]
		}
	}
	if e == nil {
		t.Fatal("Edit event toolu_01A not found")
	}
	if e.Edit.Path != "/work/shop/src/webhooks/retry.ts" {
		t.Errorf("Path = %q", e.Edit.Path)
	}
	if e.Edit.Op != model.OpUpdate {
		t.Errorf("Op = %q", e.Edit.Op)
	}
	if e.Edit.Via != "edit" {
		t.Errorf("Via = %q", e.Edit.Via)
	}
	if len(e.Edit.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(e.Edit.Hunks))
	}
	h := e.Edit.Hunks[0]
	if h.OldStart != 10 || h.NewStart != 10 {
		t.Errorf("hunk starts = %d/%d, want 10/10", h.OldStart, h.NewStart)
	}
	if len(e.Edit.Added) != 1 || e.Edit.Added[0] != "  return sendWithRetry(url, body);" {
		t.Errorf("Added = %v", e.Edit.Added)
	}
	if len(e.Edit.Removed) != 1 || e.Edit.Removed[0] != "  return post(url, body);" {
		t.Errorf("Removed = %v", e.Edit.Removed)
	}
	if e.Model != "claude-opus-5-5" {
		t.Errorf("Model = %q", e.Model)
	}

	// Write create
	var w *model.Event
	for i := range edits {
		if edits[i].ID == "toolu_01B" {
			w = &edits[i]
		}
	}
	if w == nil {
		t.Fatal("Write event toolu_01B not found")
	}
	if w.Edit.Op != model.OpCreate {
		t.Errorf("Write Op = %q, want create", w.Edit.Op)
	}
	if w.Edit.Via != "write" {
		t.Errorf("Write Via = %q", w.Edit.Via)
	}
	wantAdded := []string{"export const maxRetries = 3;", "export const baseDelayMs = 200;"}
	if len(w.Edit.Added) != len(wantAdded) {
		t.Fatalf("Write Added = %v, want %v", w.Edit.Added, wantAdded)
	}
	for i := range wantAdded {
		if w.Edit.Added[i] != wantAdded[i] {
			t.Errorf("Write Added[%d] = %q, want %q", i, w.Edit.Added[i], wantAdded[i])
		}
	}
}

func TestParse_EditFallback(t *testing.T) {
	// The subagent's Edit has no structuredPatch fallback scenario in this
	// fixture set (it does have structuredPatch); exercise the fallback
	// path directly against the input/result mapping logic instead.
	sess := &model.Session{Harness: model.HarnessClaudeCode, ID: "fallback"}
	b := newCCBuilder("fallback", "inline")
	call := &pendingCall{
		ID:   "toolu_fb",
		Name: "Edit",
		Input: mustRaw(t, map[string]any{
			"file_path":  "/repo/a.go",
			"old_string": "line1\nline2\n",
			"new_string": "line1\nline3\n",
		}),
		TS: time.Now().UTC(),
	}
	b.pending[call.ID] = call
	b.session = sess
	b.emitEdit(call, call.TS, false, nil)

	if len(sess.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(sess.Events))
	}
	edit := sess.Events[0].Edit
	if len(edit.Hunks) != 0 {
		t.Errorf("fallback edit should have no hunks, got %v", edit.Hunks)
	}
	if len(edit.Added) != 1 || edit.Added[0] != "line3" {
		t.Errorf("Added = %v, want [line3]", edit.Added)
	}
	if len(edit.Removed) != 1 || edit.Removed[0] != "line2" {
		t.Errorf("Removed = %v, want [line2]", edit.Removed)
	}
}

func TestParse_Bash(t *testing.T) {
	s := mustParseS1(t)
	cmds := eventsOf(s, model.KindCommand)

	byID := map[string]*model.Event{}
	for i := range cmds {
		byID[cmds[i].ID] = &cmds[i]
	}

	success, ok := byID["toolu_01C"]
	if !ok {
		t.Fatal("success command toolu_01C not found")
	}
	if success.Command.Status != model.CmdOK {
		t.Errorf("success Status = %q", success.Command.Status)
	}
	if success.Command.ExitCode == nil || *success.Command.ExitCode != 0 {
		t.Errorf("success ExitCode = %v, want 0", success.Command.ExitCode)
	}
	if len(success.Command.Output) <= 2*model.OutputLimit && len(success.Command.Output) > 0 {
		// fine either way, but the fixture's raw output exceeds the limit
	}
	if !containsTruncationMarker(success.Command.Output) {
		t.Errorf("expected truncation marker in output, got len=%d", len(success.Command.Output))
	}

	failure, ok := byID["toolu_01D"]
	if !ok {
		t.Fatal("failure command toolu_01D not found")
	}
	if failure.Command.Status != model.CmdFailed {
		t.Errorf("failure Status = %q", failure.Command.Status)
	}
	if failure.Command.ExitCode == nil || *failure.Command.ExitCode != 2 {
		t.Errorf("failure ExitCode = %v, want 2", failure.Command.ExitCode)
	}

	interrupted, ok := byID["toolu_01E"]
	if !ok {
		t.Fatal("interrupted command toolu_01E not found")
	}
	if interrupted.Command.Status != model.CmdInterrupted {
		t.Errorf("interrupted Status = %q", interrupted.Command.Status)
	}
	if !interrupted.Command.Background {
		t.Error("interrupted command should have Background = true")
	}
}

func containsTruncationMarker(s string) bool {
	return len(s) > 0 && (contains(s, "bytes truncated"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestParse_Rejection(t *testing.T) {
	s := mustParseS1(t)
	rejections := eventsOf(s, model.KindRejection)
	if len(rejections) != 1 {
		t.Fatalf("got %d rejections, want 1", len(rejections))
	}
	r := rejections[0]
	if r.Rejection.Tool != "Bash" || r.Rejection.ToolUseID != "toolu_01F" || r.Rejection.Reason != "user" {
		t.Errorf("Rejection = %+v", r.Rejection)
	}
	// Rejected calls must not also produce a command event.
	for _, c := range eventsOf(s, model.KindCommand) {
		if c.ID == "toolu_01F" {
			t.Error("rejected tool use also produced a command event")
		}
	}
}

func TestParse_QuestionPlanTodo(t *testing.T) {
	s := mustParseS1(t)

	questions := eventsOf(s, model.KindQuestion)
	if len(questions) != 1 {
		t.Fatalf("got %d questions, want 1", len(questions))
	}
	q := questions[0].Question
	if len(q.Items) != 1 {
		t.Fatalf("got %d question items, want 1", len(q.Items))
	}
	if q.Items[0].Answer != "Exponential backoff" {
		t.Errorf("Answer = %q", q.Items[0].Answer)
	}
	if len(q.Items[0].Options) != 2 {
		t.Errorf("Options = %v", q.Items[0].Options)
	}

	plans := eventsOf(s, model.KindPlan)
	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(plans))
	}
	p := plans[0].Plan
	if p.Source != "exit_plan_mode" {
		t.Errorf("Source = %q", p.Source)
	}
	if p.Approved == nil || !*p.Approved {
		t.Errorf("Approved = %v, want true", p.Approved)
	}

	todos := eventsOf(s, model.KindTodo)
	if len(todos) != 1 {
		t.Fatalf("got %d todos, want 1", len(todos))
	}
	td := todos[0].Todo
	if !td.Replace {
		t.Error("Replace = false, want true")
	}
	if len(td.Items) != 2 {
		t.Fatalf("got %d todo items, want 2", len(td.Items))
	}
	if td.Items[0].Text != "Add retry wrapper" || td.Items[0].Status != "completed" {
		t.Errorf("todo[0] = %+v", td.Items[0])
	}
	if td.Items[1].Text != "Add tests" || td.Items[1].Status != "in_progress" {
		t.Errorf("todo[1] = %+v", td.Items[1])
	}
}

func TestParse_Subagent(t *testing.T) {
	s := mustParseS1(t)

	subs := eventsOf(s, model.KindSubagent)
	if len(subs) != 1 {
		t.Fatalf("got %d subagent events, want 1", len(subs))
	}
	sub := subs[0].Subagent
	if sub.AgentID != "abc123" {
		t.Errorf("AgentID = %q", sub.AgentID)
	}
	if sub.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q", sub.Model)
	}
	if sub.Type != "code-reviewer" {
		t.Errorf("Type = %q", sub.Type)
	}
	if subs[0].AgentID != "" {
		t.Errorf("the invocation event itself should have empty AgentID, got %q", subs[0].AgentID)
	}

	// Subagent transcript events appended with AgentID abc123.
	var subEdits, subCmds int
	for _, e := range s.Events {
		if e.AgentID != "abc123" {
			continue
		}
		switch e.Kind {
		case model.KindEdit:
			subEdits++
		case model.KindCommand:
			subCmds++
		case model.KindPrompt:
			t.Error("subagent prompt event should have been skipped")
		}
	}
	if subEdits != 1 {
		t.Errorf("got %d subagent edits, want 1", subEdits)
	}
	if subCmds != 1 {
		t.Errorf("got %d subagent commands, want 1", subCmds)
	}
}

func TestParse_LookupsMCP(t *testing.T) {
	s := mustParseS1(t)

	lookups := eventsOf(s, model.KindLookup)
	var search, fetch *model.Lookup
	for _, l := range lookups {
		switch l.Lookup.Kind {
		case "search":
			search = l.Lookup
		case "fetch":
			fetch = l.Lookup
		}
	}
	if search == nil || search.Query != "exponential backoff jitter best practices" {
		t.Errorf("search lookup = %+v", search)
	}
	if fetch == nil || len(fetch.URLs) != 1 || fetch.URLs[0] != "https://example.com/backoff-guide" {
		t.Errorf("fetch lookup = %+v", fetch)
	}

	mcps := eventsOf(s, model.KindMCP)
	found := false
	for _, m := range mcps {
		if m.MCP.Server == "github" && m.MCP.Tool == "get_issue" {
			found = true
		}
	}
	if !found {
		t.Error("mcp__github__get_issue not split into server/tool correctly")
	}
}

func TestParse_Image(t *testing.T) {
	s := mustParseS1(t)
	images := eventsOf(s, model.KindImage)
	if len(images) != 1 {
		t.Fatalf("got %d images, want 1", len(images))
	}
	img := images[0].Image
	if img.MediaType != "image/png" {
		t.Errorf("MediaType = %q", img.MediaType)
	}
	if img.Bytes != 6 { // len("aGVsbG8=") == 8, 8*3/4 == 6
		t.Errorf("Bytes = %d, want 6", img.Bytes)
	}
	if img.Ref == "" {
		t.Error("Ref should not be empty")
	}
	if img.Tool != "mcp__playwright__screenshot" {
		t.Errorf("Tool = %q", img.Tool)
	}
}

func TestParse_Attachments(t *testing.T) {
	s := mustParseS1(t)

	ext := eventsOf(s, model.KindExternalEdit)
	if len(ext) != 1 {
		t.Fatalf("got %d external_edit events, want 1", len(ext))
	}
	wantLines := []string{"function send() {", "  return sendWithRetry(url, body);", "}"}
	if len(ext[0].External.Lines) != len(wantLines) {
		t.Fatalf("Lines = %v, want %v", ext[0].External.Lines, wantLines)
	}
	for i := range wantLines {
		if ext[0].External.Lines[i] != wantLines[i] {
			t.Errorf("Lines[%d] = %q, want %q", i, ext[0].External.Lines[i], wantLines[i])
		}
	}
	if ext[0].External.Source != "cc_attachment" {
		t.Errorf("Source = %q", ext[0].External.Source)
	}

	diags := eventsOf(s, model.KindDiagnostics)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics events, want 1", len(diags))
	}
	d := diags[0].Diagnostics
	if d.Errors != 1 || d.Warnings != 1 {
		t.Errorf("Errors/Warnings = %d/%d, want 1/1", d.Errors, d.Warnings)
	}
	if d.Path != "/work/shop/src/webhooks/retry.ts" {
		t.Errorf("Path = %q", d.Path)
	}

	instrs := eventsOf(s, model.KindInstructions)
	var project, nested *model.Instructions
	for i := range instrs {
		switch instrs[i].Instructions.Scope {
		case "project":
			project = instrs[i].Instructions
		case "nested":
			nested = instrs[i].Instructions
		}
	}
	if project == nil {
		t.Fatal("project instructions not found")
	}
	if project.FirstLine != "# Project rules" {
		t.Errorf("FirstLine = %q", project.FirstLine)
	}
	if project.Hash == "" {
		t.Error("Hash should not be empty")
	}
	if nested == nil {
		t.Fatal("nested_memory instructions not found")
	}
	if nested.Path != "/work/shop/src/webhooks/CLAUDE.md" {
		t.Errorf("nested Path = %q", nested.Path)
	}
}

func TestParse_Compaction(t *testing.T) {
	s := mustParseS1(t)
	compactions := eventsOf(s, model.KindCompaction)
	if len(compactions) != 1 {
		t.Fatalf("got %d compaction events, want 1", len(compactions))
	}
	c := compactions[0].Compaction
	if c.Trigger != "auto" {
		t.Errorf("Trigger = %q", c.Trigger)
	}
	if c.Summary != "Summary: implemented retry wrapper with exponential backoff; tests passing." {
		t.Errorf("Summary = %q", c.Summary)
	}
}

func TestParse_Modes(t *testing.T) {
	s := mustParseS1(t)
	modes := eventsOf(s, model.KindMode)
	if len(modes) != 2 {
		t.Fatalf("got %d mode_change events, want 2 (only-on-change)", len(modes))
	}
	if modes[0].Mode.Permission != model.PermAsk {
		t.Errorf("modes[0].Permission = %q, want ask", modes[0].Mode.Permission)
	}
	if modes[1].Mode.Permission != model.PermBypass {
		t.Errorf("modes[1].Permission = %q, want bypass", modes[1].Mode.Permission)
	}
	// The bypass entry had no timestamp; it must use the previous entry's ts.
	wantTS := mustTime(t, "2026-09-27T14:00:04.500Z")
	if !modes[1].TS.Equal(wantTS) {
		t.Errorf("modes[1].TS = %v, want %v", modes[1].TS, wantTS)
	}
}

func TestParse_ModelChange(t *testing.T) {
	s := mustParseS1(t)
	var mainChanges []model.Event
	for _, e := range eventsOf(s, model.KindModelChange) {
		if e.AgentID == "" {
			mainChanges = append(mainChanges, e)
		}
	}
	if len(mainChanges) != 1 {
		t.Fatalf("got %d main-session model_change events, want 1", len(mainChanges))
	}
	mc := mainChanges[0].ModelChange
	if mc.Model != "claude-opus-5-5" || mc.Effort != "high" {
		t.Errorf("ModelChange = %+v", mc)
	}
}

func TestParse_UserBash(t *testing.T) {
	s := mustParseS1(t)
	var found *model.Event
	for i, e := range s.Events {
		if e.Kind == model.KindCommand && e.Command.ByUser {
			found = &s.Events[i]
		}
	}
	if found == nil {
		t.Fatal("ByUser command not found")
	}
	if found.Command.Cmd != "git status" {
		t.Errorf("Cmd = %q", found.Command.Cmd)
	}
	if found.Command.Status != model.CmdOK {
		t.Errorf("Status = %q, want ok", found.Command.Status)
	}
	wantOutput := "On branch feat/retry\nnothing to commit, working tree clean\n"
	if found.Command.Output != wantOutput {
		t.Errorf("Output = %q, want %q", found.Command.Output, wantOutput)
	}
}

func TestParse_Dedupe(t *testing.T) {
	s := mustParseS1(t)
	count := 0
	for _, e := range s.Events {
		if e.ID == "u1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d events with ID u1, want 1 (dedup by uuid)", count)
	}
}

func TestParse_Malformed(t *testing.T) {
	// mustParseS1 already asserts ParseFile returns no error despite the
	// malformed line and unknown type in the fixture; also confirm the
	// unknown-type entry produced no event with its uuid.
	s := mustParseS1(t)
	for _, e := range s.Events {
		if e.ID == "unk1" {
			t.Errorf("unknown type entry produced an event: %+v", e)
		}
	}
}

func TestParse_Final(t *testing.T) {
	s := mustParseS1(t)
	messages := eventsOf(s, model.KindMessage)
	if len(messages) != 1 {
		t.Fatalf("got %d assistant_message events, want 1", len(messages))
	}
	if !messages[0].Message.Final {
		t.Error("the only assistant message in the session should be Final")
	}
	if messages[0].Message.Usage == nil {
		t.Error("usage should be attached to the first message of its entry")
	}
}

func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
