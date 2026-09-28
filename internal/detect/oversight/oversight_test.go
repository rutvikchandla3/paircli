package oversight

import (
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// ovsMode builds a mode_change event with the given normalized permission.
func ovsMode(perm model.PermissionMode, raw string) model.Event {
	return model.Event{Kind: model.KindMode, Mode: &model.Mode{Permission: perm, RawPermission: raw}}
}

// ovsCodexMode builds a Codex mode_change event.
func ovsCodexMode(approval, sandbox string) model.Event {
	return model.Event{Kind: model.KindMode, Mode: &model.Mode{Approval: approval, Sandbox: sandbox}}
}

// ovsCheckSummary fails a test when a signal's summary breaks the 160-rune cap.
func ovsCheckSummary(t *testing.T, sig *model.Signal) {
	t.Helper()
	if n := len([]rune(sig.Summary)); n > 160 {
		t.Errorf("%s summary too long (%d > 160): %s", sig.ID, n, sig.Summary)
	}
}

// ovsDataInt reads an int field out of a signal's Data map.
func ovsDataInt(t *testing.T, sig *model.Signal, key string) int {
	t.Helper()
	v, ok := sig.Data[key].(int)
	if !ok {
		t.Fatalf("%s data[%q] is %T, want int", sig.ID, key, sig.Data[key])
	}
	return v
}

func TestOVS1_ClaudeModes(t *testing.T) {
	// bypass, then accept_edits (which splits edits from other tools), then ask.
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Add(ovsMode(model.PermBypass, "bypass"))
	sb.Run("git status", 0, "ok")
	sb.Add(ovsMode(model.PermAcceptEdits, "accept_edits"))
	sb.Edit("file.txt", "added line")
	sb.Run("go test ./...", 0, "ok")
	sb.Add(ovsMode(model.PermAsk, "ask"))
	sb.Run("npm install", 0, "ok")

	sig := testkit.Find(engine.Run(testkit.Ctx(nil, sb.Build())), "OVS-1")
	if sig == nil {
		t.Fatal("OVS-1 signal not found")
	}
	if sig.State != model.StateInfo {
		t.Errorf("state = %s, want info", sig.State)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}

	// bypass run + accept_edits edit are unapproved; the other two prompt.
	wantFinding := "`claude-code:s1`: 50% of 4 tool calls ran with no approval step (modes: accept_edits, ask, bypass)."
	if sig.Findings[0].Summary != wantFinding {
		t.Errorf("finding =\n  %s\nwant\n  %s", sig.Findings[0].Summary, wantFinding)
	}
	if got, want := sig.Summary, "50% of 4 tool calls ran with no approval step."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	for key, want := range map[string]int{
		"tool_calls": 4, "unapproved": 2, "auto_approved": 0,
		"approval_possible": 2, "unknown": 0, "prompts_shown": 0,
	} {
		if got := ovsDataInt(t, sig, key); got != want {
			t.Errorf("data[%q] = %d, want %d", key, got, want)
		}
	}
	ovsCheckSummary(t, sig)
}

func TestOVS1_Codex(t *testing.T) {
	sb := testkit.Session(model.HarnessCodex, "s1")
	sb.Add(ovsCodexMode("never", "workspace-write"))
	sb.Run("cargo build", 0, "ok")
	sb.Run("cargo test", 0, "ok")
	sb.Add(ovsCodexMode("on-request", "read-only"))
	sb.Run("cargo run", 0, "ok")

	sig := testkit.Find(engine.Run(testkit.Ctx(nil, sb.Build())), "OVS-1")
	if sig == nil {
		t.Fatal("OVS-1 signal not found")
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	// "never" governed two of the three calls, so it is the pair reported.
	want := "`codex:s1`: approvals never, sandbox workspace-write for 67% of 3 tool calls."
	if sig.Findings[0].Summary != want {
		t.Errorf("finding =\n  %s\nwant\n  %s", sig.Findings[0].Summary, want)
	}
	if got := ovsDataInt(t, sig, "unapproved"); got != 2 {
		t.Errorf("data[unapproved] = %d, want 2", got)
	}
	ovsCheckSummary(t, sig)
}

func TestOVS1_Pi(t *testing.T) {
	sb := testkit.Session(model.HarnessPi, "s1")
	sb.Run("git commit -m test", 0, "ok")
	sb.Run("git push", 0, "ok")

	sig := testkit.Find(engine.Run(testkit.Ctx(nil, sb.Build())), "OVS-1")
	if sig == nil {
		t.Fatal("OVS-1 signal not found")
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	want := "`pi:s1`: no permission layer; 2 tool calls."
	if sig.Findings[0].Summary != want {
		t.Errorf("finding = %q, want %q", sig.Findings[0].Summary, want)
	}
	// Pi has no permission layer, so every call counts as unapproved.
	if got := ovsDataInt(t, sig, "unapproved"); got != 2 {
		t.Errorf("data[unapproved] = %d, want 2", got)
	}
	ovsCheckSummary(t, sig)
}

func TestOVS1_UnknownBeforeFirstMode(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("ls", 0, "ok") // no mode recorded yet
	sb.Add(ovsMode(model.PermAsk, "ask"))
	sb.Run("pwd", 0, "ok")

	sig := testkit.Find(engine.Run(testkit.Ctx(nil, sb.Build())), "OVS-1")
	if sig == nil {
		t.Fatal("OVS-1 signal not found")
	}
	if got := ovsDataInt(t, sig, "tool_calls"); got != 2 {
		t.Errorf("data[tool_calls] = %d, want 2", got)
	}
	if got := ovsDataInt(t, sig, "unknown"); got != 1 {
		t.Errorf("data[unknown] = %d, want 1 (the call before the first mode)", got)
	}
	if got := ovsDataInt(t, sig, "approval_possible"); got != 1 {
		t.Errorf("data[approval_possible] = %d, want 1", got)
	}
	ovsCheckSummary(t, sig)
}

func TestOVS1_PromptsShown(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Add(ovsMode(model.PermAsk, "ask"))
	sb.Add(model.Event{Kind: model.KindPermission, Permission: &model.Permission{
		Tool: "bash", Decision: "ask", By: "harness",
	}})
	sb.Add(model.Event{Kind: model.KindPermission, Permission: &model.Permission{
		Tool: "edit", Decision: "allow", By: "user",
	}})
	sb.Run("rm -rf build", 0, "ok")

	sig := testkit.Find(engine.Run(testkit.Ctx(nil, sb.Build())), "OVS-1")
	if sig == nil {
		t.Fatal("OVS-1 signal not found")
	}
	if got := ovsDataInt(t, sig, "prompts_shown"); got != 1 {
		t.Errorf("data[prompts_shown] = %d, want 1", got)
	}
	if !strings.Contains(sig.Summary, "1 permission prompts were shown.") {
		t.Errorf("summary does not mention the prompt: %s", sig.Summary)
	}
	ovsCheckSummary(t, sig)
}

func TestOVS2_LongestStretch(t *testing.T) {
	// Two prompts at 14:00 and 14:03; the two tool calls in between are the
	// only unattended stretch with work in it.
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("start")
	sb.Run("ls", 0, "ok")
	sb.Run("pwd", 0, "ok")
	sb.Prompt("next")

	sig := testkit.Find(engine.Run(testkit.Ctx(nil, sb.Build())), "OVS-2")
	if sig == nil {
		t.Fatal("OVS-2 signal not found")
	}
	if sig.State != model.StateInfo {
		t.Errorf("state = %s, want info", sig.State)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	want := "`claude-code:s1`: longest unattended stretch 3 min, 2 tool calls, 0 PR lines written."
	if sig.Findings[0].Summary != want {
		t.Errorf("finding =\n  %s\nwant\n  %s", sig.Findings[0].Summary, want)
	}
	wantSummary := "Longest unattended stretch: 3 min and 2 tool calls in `claude-code:s1`; 0 PR lines were written in it."
	if sig.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", sig.Summary, wantSummary)
	}
	if got := ovsDataInt(t, sig, "human_events"); got != 2 {
		t.Errorf("data[human_events] = %d, want 2", got)
	}
	ovsCheckSummary(t, sig)
}

func TestOVS2_LinesInStretch(t *testing.T) {
	pr := testkit.PR("acme/shop", 1)
	pr.Add("file.txt", 1, "line 1", "line 2", "line 3")

	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("make changes")
	sb.Edit("file.txt", "line 1", "line 2", "line 3")
	sb.Prompt("done")

	sig := testkit.Find(engine.Run(testkit.Ctx(pr.Build(), sb.Build())), "OVS-2")
	if sig == nil {
		t.Fatal("OVS-2 signal not found")
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	want := "`claude-code:s1`: longest unattended stretch 2 min, 1 tool calls, 3 PR lines written."
	if sig.Findings[0].Summary != want {
		t.Errorf("finding =\n  %s\nwant\n  %s", sig.Findings[0].Summary, want)
	}
	// The lines were written by the edit inside the stretch, so the finding
	// anchors them.
	anchors := sig.Findings[0].Anchors
	if len(anchors) != 1 || anchors[0].File != "file.txt" || anchors[0].Lines != "1-3" {
		t.Errorf("anchors = %+v, want one anchor file.txt:1-3", anchors)
	}
	ovsCheckSummary(t, sig)
}

func TestOVS2_UserRunIsHuman(t *testing.T) {
	// A human-run command splits the session, so the agent's later call sits
	// alone in its own stretch.
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.UserRun("git status", 0)
	sb.Run("ls", 0, "ok")

	sig := testkit.Find(engine.Run(testkit.Ctx(nil, sb.Build())), "OVS-2")
	if sig == nil {
		t.Fatal("OVS-2 signal not found")
	}
	if got := ovsDataInt(t, sig, "human_events"); got != 1 {
		t.Errorf("data[human_events] = %d, want 1", got)
	}
	longest, ok := sig.Data["longest"].(map[string]any)
	if !ok {
		t.Fatalf("data[longest] is %T, want map", sig.Data["longest"])
	}
	if got := longest["tool_calls"]; got != 1 {
		t.Errorf("longest[tool_calls] = %v, want 1", got)
	}
	ovsCheckSummary(t, sig)
}

func TestOVS3_SubagentLines(t *testing.T) {
	pr := testkit.PR("acme/shop", 1)
	pr.Add("file.txt", 1, "line 1", "line 2")

	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Add(model.Event{Kind: model.KindSubagent, Subagent: &model.Subagent{
		AgentID: "sub-123", Type: "code-reviewer", Model: "claude-opus",
	}})
	sb.Agent("sub-123").Edit("file.txt", "line 1", "line 2")
	sb.Agent("").Say("done")

	sig := testkit.Find(engine.Run(testkit.Ctx(pr.Build(), sb.Build())), "OVS-3")
	if sig == nil {
		t.Fatal("OVS-3 signal not found")
	}
	if sig.State != model.StateInfo {
		t.Errorf("state = %s, want info", sig.State)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(sig.Findings))
	}
	want := "Subagent `code-reviewer` (claude-opus) wrote 2 PR lines in `file.txt`."
	if sig.Findings[0].Summary != want {
		t.Errorf("finding =\n  %s\nwant\n  %s", sig.Findings[0].Summary, want)
	}
	if got, want := sig.Summary, "1 subagents; 2 PR lines were written by subagents."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	ovsCheckSummary(t, sig)
}

func TestOVS3_None(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("help")
	sb.Edit("file.txt", "line 1")

	sig := testkit.Find(engine.Run(testkit.Ctx(nil, sb.Build())), "OVS-3")
	if sig == nil {
		t.Fatal("OVS-3 signal not found")
	}
	if sig.State != model.StateClear {
		t.Errorf("state = %s, want clear", sig.State)
	}
	if sig.Summary != "No subagents were used." {
		t.Errorf("summary = %q, want %q", sig.Summary, "No subagents were used.")
	}
	if len(sig.Findings) != 0 {
		t.Errorf("findings = %d, want 0", len(sig.Findings))
	}
}
