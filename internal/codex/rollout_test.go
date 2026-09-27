package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

const (
	newFixture   = "testdata/sessions/2026/09/27/rollout-2026-09-27T14-00-00-019f0000-aaaa-7000-8000-000000000001.jsonl"
	oldFixture   = "testdata/sessions/2026/07/09/rollout-2026-07-09T19-00-00-019f0000-bbbb-7000-8000-000000000002.jsonl"
	patchFixture = "testdata/sessions/2026/06/08/rollout-2026-06-08T10-00-00-019f0000-cccc-7000-8000-000000000003.jsonl"
)

func parseOne(t *testing.T, path string) *model.Session {
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

func findAll(s *model.Session, k model.EventKind) []model.Event {
	var out []model.Event
	for _, e := range s.Events {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

func findOne(t *testing.T, s *model.Session, k model.EventKind) *model.Event {
	t.Helper()
	all := findAll(s, k)
	if len(all) != 1 {
		t.Fatalf("kind %s count = %d, want 1 (%+v)", k, len(all), all)
	}
	return &all[0]
}

func findByID(t *testing.T, s *model.Session, id string) *model.Event {
	t.Helper()
	for i := range s.Events {
		if s.Events[i].ID == id {
			return &s.Events[i]
		}
	}
	t.Fatalf("no event with ID %q", id)
	return nil
}

func TestDiscover(t *testing.T) {
	root := "testdata"
	got, err := Discover(root, time.Time{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{
		"testdata/archived_sessions/rollout-2026-05-01T08-00-00-019f0000-dddd-7000-8000-000000000004.jsonl",
		"testdata/sessions/2026/06/08/rollout-2026-06-08T10-00-00-019f0000-cccc-7000-8000-000000000003.jsonl",
		"testdata/sessions/2026/07/09/rollout-2026-07-09T19-00-00-019f0000-bbbb-7000-8000-000000000002.jsonl",
		"testdata/sessions/2026/09/27/rollout-2026-09-27T14-00-00-019f0000-aaaa-7000-8000-000000000001.jsonl",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Discover paths =\n%v\nwant\n%v", got, want)
	}

	// mtime filter.
	old := filepath.Join(root, "sessions", "2026", "06", "08", "rollout-2026-06-08T10-00-00-019f0000-cccc-7000-8000-000000000003.jsonl")
	oldTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	origInfo, statErr := os.Stat(old)
	if statErr != nil {
		t.Fatalf("stat fixture: %v", statErr)
	}
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chtimes(old, origInfo.ModTime(), origInfo.ModTime())
	})

	since := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	got2, err := Discover(root, since)
	if err != nil {
		t.Fatalf("Discover with since: %v", err)
	}
	for _, p := range got2 {
		if p == old {
			t.Fatalf("Discover with since=%v still returned old file %s", since, old)
		}
	}
	if len(got2) != 3 {
		t.Fatalf("Discover with since = %d files, want 3 (%v)", len(got2), got2)
	}

	// Missing directories contribute nothing, not an error.
	none, err := Discover(filepath.Join(root, "does-not-exist"), time.Time{})
	if err != nil {
		t.Fatalf("Discover missing root: %v", err)
	}
	if none != nil {
		t.Fatalf("Discover missing root = %v, want nil", none)
	}
}

func TestParse_Meta(t *testing.T) {
	s := parseOne(t, newFixture)
	if s.ID != "019f0000-aaaa-7000-8000-000000000001" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.CWD != "/work/shop" {
		t.Errorf("CWD = %q", s.CWD)
	}
	if s.HarnessVersion != "0.154.0" {
		t.Errorf("HarnessVersion = %q", s.HarnessVersion)
	}
	if s.StartSHA != "4be1c0ffee4be1c0ffee4be1c0ffee4be1c0ffee" {
		t.Errorf("StartSHA = %q", s.StartSHA)
	}
	if s.Branch != "feat/retry" {
		t.Errorf("Branch = %q", s.Branch)
	}
	if s.RepoRemote != "acme/shop" {
		t.Errorf("RepoRemote = %q", s.RepoRemote)
	}
	gh := findOne(t, s, model.KindGitHead)
	if gh.GitHead.SHA != s.StartSHA || gh.GitHead.Branch != "feat/retry" || gh.GitHead.Trigger != "transcript_meta" {
		t.Errorf("git_head = %+v", gh.GitHead)
	}
}

func TestParse_NoMetaGit(t *testing.T) {
	s := parseOne(t, oldFixture)
	if s.StartSHA != "" {
		t.Errorf("StartSHA = %q, want empty", s.StartSHA)
	}
	if len(findAll(s, model.KindGitHead)) != 0 {
		t.Errorf("unexpected git_head events for a session_meta without git")
	}
}

func TestParse_Modes(t *testing.T) {
	s := parseOne(t, newFixture)
	modes := findAll(s, model.KindMode)
	if len(modes) != 2 {
		t.Fatalf("mode_change count = %d, want 2", len(modes))
	}
	m1, m2 := modes[0].Mode, modes[1].Mode
	if m1.Approval != "never" || m1.Sandbox != "danger-full-access" || m1.Permission != "" {
		t.Errorf("mode[0] = %+v", m1)
	}
	if m2.Approval != "on-request" || m2.Sandbox != "workspace-write" || m2.Permission != model.PermPlan {
		t.Errorf("mode[1] = %+v, want Permission=plan", m2)
	}
}

func TestParse_ModelEffort(t *testing.T) {
	s := parseOne(t, newFixture)
	changes := findAll(s, model.KindModelChange)
	if len(changes) != 2 {
		t.Fatalf("model_change count = %d, want 2", len(changes))
	}
	if changes[0].ModelChange.Model != "gpt-5.6-luna" || changes[0].ModelChange.Effort != "xhigh" {
		t.Errorf("model_change[0] = %+v", changes[0].ModelChange)
	}
	if changes[1].ModelChange.Model != "gpt-5.6-nova" || changes[1].ModelChange.Effort != "medium" {
		t.Errorf("model_change[1] = %+v", changes[1].ModelChange)
	}

	preSwitch := findByID(t, s, "exec-3")
	if preSwitch.Model != "gpt-5.6-luna" {
		t.Errorf("exec-3 Event.Model = %q, want gpt-5.6-luna", preSwitch.Model)
	}
	postSwitch := findByID(t, s, "exec-5")
	if postSwitch.Model != "gpt-5.6-nova" {
		t.Errorf("exec-5 Event.Model = %q, want gpt-5.6-nova (later command)", postSwitch.Model)
	}
}

func TestParse_Instructions(t *testing.T) {
	s := parseOne(t, newFixture)
	instr := findAll(s, model.KindInstructions)
	if len(instr) != 1 {
		t.Fatalf("instructions count = %d, want 1 (once per distinct hash)", len(instr))
	}
	in := instr[0].Instructions
	if in.Path != "AGENTS.md" || in.Scope != "project" {
		t.Errorf("instructions = %+v", in)
	}
	if in.FirstLine != "# Project rules" {
		t.Errorf("FirstLine = %q", in.FirstLine)
	}
	if in.Hash == "" {
		t.Errorf("Hash empty")
	}
}

func TestParse_Commands(t *testing.T) {
	s := parseOne(t, newFixture)

	build := findByID(t, s, "exec-3")
	if build.Command.Cmd != "npm run build" {
		t.Errorf("exec-3 Cmd = %q, want unwrapped -lc argument", build.Command.Cmd)
	}
	if build.Command.CWD != "/work/shop" {
		t.Errorf("exec-3 CWD = %q", build.Command.CWD)
	}
	if build.Command.ExitCode == nil || *build.Command.ExitCode != 0 || build.Command.Status != model.CmdOK {
		t.Errorf("exec-3 exit/status = %v/%v", build.Command.ExitCode, build.Command.Status)
	}
	if build.Command.DurationMs != 3250 {
		t.Errorf("exec-3 DurationMs = %d, want 3250", build.Command.DurationMs)
	}
	if build.Command.Output != "Build succeeded" {
		t.Errorf("exec-3 Output = %q", build.Command.Output)
	}

	failed := findByID(t, s, "exec-4")
	if failed.Command.ExitCode == nil || *failed.Command.ExitCode != 1 || failed.Command.Status != model.CmdFailed {
		t.Errorf("exec-4 exit/status = %v/%v", failed.Command.ExitCode, failed.Command.Status)
	}

	old := parseOne(t, oldFixture)
	pull := findByID(t, old, "call-exec-1")
	if pull.Command.Cmd != "git pull origin main" {
		t.Errorf("call-exec-1 Cmd = %q", pull.Command.Cmd)
	}
	if pull.Command.ExitCode == nil || *pull.Command.ExitCode != 0 || pull.Command.Status != model.CmdOK {
		t.Errorf("call-exec-1 exit/status = %v/%v (string exit_code)", pull.Command.ExitCode, pull.Command.Status)
	}

	reads := findAll(s, model.KindRead)
	if len(reads) != 2 {
		t.Fatalf("file_read count = %d, want 2", len(reads))
	}
	paths := map[string]bool{}
	for _, r := range reads {
		paths[r.Read.Path] = true
	}
	if !paths["src/webhooks/retry.ts"] || !paths["src/webhooks/types.ts"] {
		t.Errorf("file_read paths = %v", paths)
	}
}

func TestParse_FamilyPreference(t *testing.T) {
	s := parseOne(t, newFixture)
	for _, e := range s.Events {
		if e.Kind == model.KindCommand && e.Command.Cmd == "echo legacy should be ignored" {
			t.Fatalf("legacy exec_command_end was not ignored despite newer CommandExecution items")
		}
	}
}

func TestParse_FileChange(t *testing.T) {
	s := parseOne(t, newFixture)
	edits := findAll(s, model.KindEdit)
	if len(edits) != 3 {
		t.Fatalf("file_edit count = %d, want 3", len(edits))
	}
	byID := map[string]*model.Event{}
	for i := range edits {
		byID[edits[i].ID] = &edits[i]
	}

	del := byID["exec-2/1"]
	if del == nil || del.Edit.Op != model.OpDelete || del.Edit.Path != "/work/shop/src/webhooks/legacy.ts" {
		t.Fatalf("exec-2/1 = %+v", del)
	}
	if len(del.Edit.Removed) != 1 || del.Edit.Removed[0] != "export function oldSend() {}" {
		t.Errorf("exec-2/1 removed = %v", del.Edit.Removed)
	}

	upd := byID["exec-2/2"]
	if upd == nil || upd.Edit.Op != model.OpUpdate || upd.Edit.Path != "/work/shop/src/webhooks/retry.ts" {
		t.Fatalf("exec-2/2 = %+v", upd)
	}
	if len(upd.Edit.Hunks) != 1 || upd.Edit.Hunks[0].OldStart != 38 || upd.Edit.Hunks[0].NewLines != 4 {
		t.Errorf("exec-2/2 hunks = %+v", upd.Edit.Hunks)
	}
	if len(upd.Edit.Added) != 2 || upd.Edit.Added[0] != "  const max = 5;" {
		t.Errorf("exec-2/2 added = %v", upd.Edit.Added)
	}

	add := byID["exec-2/3"]
	if add == nil || add.Edit.Op != model.OpCreate || add.Edit.Path != "/work/shop/src/webhooks/types.ts" {
		t.Fatalf("exec-2/3 = %+v", add)
	}
	if len(add.Edit.Added) != 1 || add.Edit.Added[0] != "export type Attempt = number;" {
		t.Errorf("exec-2/3 added = %v", add.Edit.Added)
	}

	old := parseOne(t, oldFixture)
	patches := findAll(old, model.KindEdit)
	if len(patches) != 2 {
		t.Fatalf("old patch_apply_end count = %d, want 2", len(patches))
	}
	var okPatch, failPatch *model.Event
	for i := range patches {
		switch patches[i].ID {
		case "call-patch-1":
			okPatch = &patches[i]
		case "call-patch-2":
			failPatch = &patches[i]
		}
	}
	if okPatch == nil || okPatch.Edit.Failed {
		t.Errorf("call-patch-1 (success:true) = %+v, want Failed=false", okPatch)
	}
	if failPatch == nil || !failPatch.Edit.Failed {
		t.Errorf("call-patch-2 (success:false) = %+v, want Failed=true", failPatch)
	}
}

func TestParse_ApplyPatchFallback(t *testing.T) {
	s := parseOne(t, patchFixture)
	edits := findAll(s, model.KindEdit)
	if len(edits) != 4 {
		t.Fatalf("file_edit count = %d, want 4", len(edits))
	}
	byPath := map[string]*model.Event{}
	for i := range edits {
		byPath[edits[i].Edit.Path] = &edits[i]
	}

	add := byPath["src/webhooks/new_helper.ts"]
	if add == nil || add.Edit.Op != model.OpCreate {
		t.Fatalf("add = %+v", add)
	}
	if len(add.Edit.Added) != 3 {
		t.Errorf("add.Added = %v, want 3 lines", add.Edit.Added)
	}

	upd := byPath["src/webhooks/retry.ts"]
	if upd == nil || upd.Edit.Op != model.OpUpdate {
		t.Fatalf("update = %+v", upd)
	}
	if len(upd.Edit.Added) != 1 || len(upd.Edit.Removed) != 1 {
		t.Errorf("update added/removed = %v/%v", upd.Edit.Added, upd.Edit.Removed)
	}

	mv := byPath["src/webhooks/old_name.ts"]
	if mv == nil || mv.Edit.Op != model.OpMove || mv.Edit.MovePath != "src/webhooks/renamed.ts" {
		t.Fatalf("move = %+v", mv)
	}

	del := byPath["src/webhooks/unused.ts"]
	if del == nil || del.Edit.Op != model.OpDelete {
		t.Fatalf("delete = %+v", del)
	}
}

func TestParse_CommandFallback(t *testing.T) {
	s := parseOne(t, patchFixture)
	cmds := findAll(s, model.KindCommand)
	if len(cmds) != 2 {
		t.Fatalf("command count = %d, want 2", len(cmds))
	}
	for _, c := range cmds {
		if c.Command.Status != model.CmdUnknown || c.Command.ExitCode != nil {
			t.Errorf("fallback command status = %+v, want unknown/nil exit code", c.Command)
		}
	}
	lint := findByID(t, s, "call-cmd-fallback-1")
	if lint.Command.Cmd != "npm run lint" {
		t.Errorf("lint cmd = %q", lint.Command.Cmd)
	}
	testCmd := findByID(t, s, "call-cmd-fallback-2")
	if testCmd.Command.Cmd != "npm test" {
		t.Errorf("test cmd = %q", testCmd.Command.Cmd)
	}
}

func TestParse_MCPAndWeb(t *testing.T) {
	newS := parseOne(t, newFixture)
	mcp := findOne(t, newS, model.KindMCP)
	if mcp.MCP.Server != "github" || mcp.MCP.Tool != "search_code" || !mcp.MCP.ReadOnly || mcp.MCP.IsError {
		t.Errorf("new mcp = %+v", mcp.MCP)
	}
	web := findOne(t, newS, model.KindLookup)
	if web.Lookup.Kind != "search" || web.Lookup.Query == "" {
		t.Errorf("new web = %+v", web.Lookup)
	}

	oldS := parseOne(t, oldFixture)
	mcpOld := findOne(t, oldS, model.KindMCP)
	if mcpOld.MCP.Server != "github" || mcpOld.MCP.Tool != "get_pull_request" || mcpOld.MCP.IsError {
		t.Errorf("old mcp (Ok result) = %+v", mcpOld.MCP)
	}
	webOld := findOne(t, oldS, model.KindLookup)
	if webOld.Lookup.Kind != "search" || webOld.Lookup.Query != "node retry backoff library" {
		t.Errorf("old web = %+v", webOld.Lookup)
	}
}

func TestParse_Subagent(t *testing.T) {
	newS := parseOne(t, newFixture)
	sub := findOne(t, newS, model.KindSubagent)
	if sub.Subagent.AgentID != "agent-thread-1" || sub.Subagent.Type != "reviewer" || sub.Subagent.Model != "gpt-5.6-nova-mini" {
		t.Errorf("new subagent = %+v", sub.Subagent)
	}
	if sub.AgentID != "agent-thread-1" {
		t.Errorf("event.AgentID = %q, want agent-thread-1", sub.AgentID)
	}

	oldS := parseOne(t, oldFixture)
	subOld := findOne(t, oldS, model.KindSubagent)
	if subOld.Subagent.AgentID != "agent-thread-old-1" || subOld.Subagent.Type != "tester" {
		t.Errorf("old subagent = %+v", subOld.Subagent)
	}
}

func TestParse_CompactionDedupe(t *testing.T) {
	s := parseOne(t, newFixture)
	c := findOne(t, s, model.KindCompaction)
	if c.Compaction.Summary != "Summarized 40 earlier turns." {
		t.Errorf("compaction summary = %q", c.Compaction.Summary)
	}
}

func TestParse_InterruptRollback(t *testing.T) {
	s := parseOne(t, newFixture)
	intr := findOne(t, s, model.KindInterrupt)
	if intr.Interrupt.Reason != "user" {
		t.Errorf("interrupt reason = %q, want user (mapped from turn_aborted reason:interrupted)", intr.Interrupt.Reason)
	}
	rst := findOne(t, s, model.KindReset)
	if rst.Reset.Type != "rollback" {
		t.Errorf("reset type = %q, want rollback", rst.Reset.Type)
	}
}

func TestParse_PlanTodoQuestion(t *testing.T) {
	s := parseOne(t, newFixture)

	plan := findOne(t, s, model.KindPlan)
	if plan.Plan.Source != "plan_item" || plan.Plan.Text == "" {
		t.Errorf("plan = %+v", plan.Plan)
	}

	todo := findOne(t, s, model.KindTodo)
	if !todo.Todo.Replace || len(todo.Todo.Items) != 3 {
		t.Fatalf("todo = %+v", todo.Todo)
	}
	if todo.Todo.Items[0].Status != "completed" || todo.Todo.Items[1].Status != "in_progress" || todo.Todo.Items[2].Status != "pending" {
		t.Errorf("todo items = %+v", todo.Todo.Items)
	}

	questions := findAll(s, model.KindQuestion)
	if len(questions) != 1 {
		t.Fatalf("question count = %d, want 1 (the 'unavailable' one must be skipped)", len(questions))
	}
	q := questions[0].Question
	if q.Tool != "request_user_input" || len(q.Items) != 1 {
		t.Fatalf("question = %+v", q)
	}
	if q.Items[0].Question != "Cap retries at 5 or make it configurable?" {
		t.Errorf("question text = %q", q.Items[0].Question)
	}
	if q.Items[0].Answer != "Make configurable" {
		t.Errorf("answer = %q", q.Items[0].Answer)
	}
}

func TestOwnerRepo(t *testing.T) {
	cases := []struct{ url, want string }{
		{"git@github.com:acme/shop.git", "acme/shop"},
		{"https://github.com/acme/shop.git", "acme/shop"},
		{"https://github.com/acme/shop", "acme/shop"},
		{"ssh://git@github.com/acme/shop.git", "acme/shop"},
	}
	for _, c := range cases {
		if got := ownerRepo(c.url); got != c.want {
			t.Errorf("ownerRepo(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
