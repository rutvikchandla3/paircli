package intent

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestINT1_OrderSteeringSlash(t *testing.T) {
	// Two sessions, a steering prompt, a slash command
	sb1 := testkit.Session(model.HarnessClaudeCode, "s1")
	sb1.Prompt("write a function").Prompt("what about error handling?").Steer("hurry up").Prompt("/help refactor")

	sb2 := testkit.Session(model.HarnessClaudeCode, "s2")
	sb2.Prompt("test this")

	s1 := sb1.Build()
	s2 := sb2.Build()

	ctx := testkit.Ctx(nil, s1, s2)
	sig := engine.Run(ctx)
	int1Sig := testkit.Find(sig, "INT-1")

	if int1Sig == nil {
		t.Fatal("INT-1 signal not found")
	}

	if int1Sig.State != model.StateInfo {
		t.Errorf("Expected State info, got %s", int1Sig.State)
	}

	if len(int1Sig.Findings) != 5 {
		t.Errorf("Expected 5 findings, got %d", len(int1Sig.Findings))
	}

	// Verify steering prompt is prefixed with "(mid-task)"
	var steeringFound bool
	for _, f := range int1Sig.Findings {
		if len(f.Summary) > 9 && f.Summary[:10] == "(mid-task)" {
			steeringFound = true
			break
		}
	}
	if !steeringFound {
		t.Error("Expected to find (mid-task) prefix in findings")
	}

	// Check summary length
	if len([]rune(int1Sig.Summary)) > 160 {
		t.Errorf("Summary too long (%d > 160): %s", len([]rune(int1Sig.Summary)), int1Sig.Summary)
	}

	// Check data
	if data, ok := int1Sig.Data["prompts"].(int); !ok || data != 5 {
		t.Errorf("Expected prompts count 5, got %v (type %T)", int1Sig.Data["prompts"], int1Sig.Data["prompts"])
	}
	if data, ok := int1Sig.Data["steering"].(int); !ok || data != 1 {
		t.Errorf("Expected steering count 1, got %v (type %T)", int1Sig.Data["steering"], int1Sig.Data["steering"])
	}
	if data, ok := int1Sig.Data["sessions"].(int); !ok || data != 2 {
		t.Errorf("Expected sessions count 2, got %v (type %T)", int1Sig.Data["sessions"], int1Sig.Data["sessions"])
	}
}

func TestINT1_SubagentPromptsIgnored(t *testing.T) {
	// Main agent and subagent prompts; only main agent prompts should be counted
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("main prompt")
	sb.Agent("sub1").Prompt("subagent prompt")
	sb.Agent("").Prompt("back to main")

	s := sb.Build()
	ctx := testkit.Ctx(nil, s)
	sig := engine.Run(ctx)
	int1Sig := testkit.Find(sig, "INT-1")

	if int1Sig == nil {
		t.Fatal("INT-1 signal not found")
	}

	// Should have 2 findings (only main agent prompts)
	if len(int1Sig.Findings) != 2 {
		t.Errorf("Expected 2 findings (main agent only), got %d", len(int1Sig.Findings))
	}

	if data, ok := int1Sig.Data["prompts"].(int); !ok || data != 2 {
		t.Errorf("Expected prompts count 2, got %v (type %T)", int1Sig.Data["prompts"], int1Sig.Data["prompts"])
	}
}

func TestINT1_NoSessions(t *testing.T) {
	// No sessions
	ctx := testkit.Ctx(nil)
	sig := engine.Run(ctx)
	int1Sig := testkit.Find(sig, "INT-1")

	if int1Sig == nil {
		t.Fatal("INT-1 signal not found")
	}

	if int1Sig.State != model.StateUnknown {
		t.Errorf("Expected State unknown, got %s", int1Sig.State)
	}

	if int1Sig.Summary != "No captured sessions for this PR." {
		t.Errorf("Expected no sessions message, got: %s", int1Sig.Summary)
	}
}

func TestINT2_Answered(t *testing.T) {
	// Agent asks questions, human answers them
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("help with this")

	// Manually create a question event
	q := model.Event{
		Kind: model.KindQuestion,
		Question: &model.Question{
			Tool: "ask_user",
			Items: []model.QA{
				{
					Question: "Should we use async or sync?",
					Answer:   "async please",
				},
			},
		},
	}
	sb.Add(q)
	sb.Say("got it, using async")

	s := sb.Build()
	ctx := testkit.Ctx(nil, s)
	sig := engine.Run(ctx)
	int2Sig := testkit.Find(sig, "INT-2")

	if int2Sig == nil {
		t.Fatal("INT-2 signal not found")
	}

	if int2Sig.State != model.StateInfo {
		t.Errorf("Expected State info, got %s", int2Sig.State)
	}

	if len(int2Sig.Findings) != 1 {
		t.Errorf("Expected 1 finding, got %d", len(int2Sig.Findings))
	}

	if !contains(int2Sig.Findings[0].Summary, "→") {
		t.Errorf("Expected answer arrow in summary: %s", int2Sig.Findings[0].Summary)
	}

	// Check summary length
	if len([]rune(int2Sig.Summary)) > 160 {
		t.Errorf("Summary too long (%d > 160): %s", len([]rune(int2Sig.Summary)), int2Sig.Summary)
	}

	if data, ok := int2Sig.Data["count"].(int); !ok || data != 1 {
		t.Errorf("Expected count 1, got %v (type %T)", int2Sig.Data["count"], int2Sig.Data["count"])
	}
}

func TestINT2_Unanswered(t *testing.T) {
	// Agent asks a question but human doesn't answer
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("help")

	q := model.Event{
		Kind: model.KindQuestion,
		Question: &model.Question{
			Tool: "ask_user",
			Items: []model.QA{
				{
					Question: "What color?",
					Answer:   "",
				},
			},
		},
	}
	sb.Add(q)

	s := sb.Build()
	ctx := testkit.Ctx(nil, s)
	sig := engine.Run(ctx)
	int2Sig := testkit.Find(sig, "INT-2")

	if int2Sig == nil {
		t.Fatal("INT-2 signal not found")
	}

	if !contains(int2Sig.Findings[0].Summary, "no answer recorded") {
		t.Errorf("Expected 'no answer recorded' in summary: %s", int2Sig.Findings[0].Summary)
	}
}

func TestINT2_None(t *testing.T) {
	// No questions asked
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("help").Say("done")

	s := sb.Build()
	ctx := testkit.Ctx(nil, s)
	sig := engine.Run(ctx)
	int2Sig := testkit.Find(sig, "INT-2")

	if int2Sig == nil {
		t.Fatal("INT-2 signal not found")
	}

	if int2Sig.State != model.StateClear {
		t.Errorf("Expected State clear, got %s", int2Sig.State)
	}

	if int2Sig.Summary != "The agent asked no questions." {
		t.Errorf("Expected no questions message, got: %s", int2Sig.Summary)
	}

	if data, ok := int2Sig.Data["count"].(int); !ok || data != 0 {
		t.Errorf("Expected count 0, got %v (type %T)", int2Sig.Data["count"], int2Sig.Data["count"])
	}
}

func TestINT3_ApprovedRejectedUnknown(t *testing.T) {
	// Three plans: approved, rejected, unknown
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("plan this")

	p1 := model.Event{
		Kind: model.KindPlan,
		Plan: &model.Plan{
			Text:     "Step 1\nStep 2\nStep 3",
			Approved: model.BoolPtr(true),
			Source:   "exit_plan_mode",
		},
	}
	sb.Add(p1)

	p2 := model.Event{
		Kind: model.KindPlan,
		Plan: &model.Plan{
			Text:     "Alternative approach",
			Approved: model.BoolPtr(false),
			Source:   "plan_item",
		},
	}
	sb.Add(p2)

	p3 := model.Event{
		Kind: model.KindPlan,
		Plan: &model.Plan{
			Text:     "Another plan",
			Approved: nil,
			Source:   "plan_mode",
		},
	}
	sb.Add(p3)

	s := sb.Build()
	ctx := testkit.Ctx(nil, s)
	sig := engine.Run(ctx)
	int3Sig := testkit.Find(sig, "INT-3")

	if int3Sig == nil {
		t.Fatal("INT-3 signal not found")
	}

	if len(int3Sig.Findings) != 3 {
		t.Errorf("Expected 3 findings, got %d", len(int3Sig.Findings))
	}

	if !contains(int3Sig.Findings[0].Summary, "approved") {
		t.Errorf("Expected 'approved' in first finding: %s", int3Sig.Findings[0].Summary)
	}
	if !contains(int3Sig.Findings[1].Summary, "rejected") {
		t.Errorf("Expected 'rejected' in second finding: %s", int3Sig.Findings[1].Summary)
	}
	if !contains(int3Sig.Findings[2].Summary, "proposed") {
		t.Errorf("Expected 'proposed' in third finding: %s", int3Sig.Findings[2].Summary)
	}

	// Summary should use approved plan
	if !contains(int3Sig.Summary, "approved") {
		t.Errorf("Expected 'approved' in summary: %s", int3Sig.Summary)
	}
	if !contains(int3Sig.Summary, "3 plans proposed") {
		t.Errorf("Expected '3 plans proposed' in summary: %s", int3Sig.Summary)
	}

	// Check summary length
	if len([]rune(int3Sig.Summary)) > 160 {
		t.Errorf("Summary too long (%d > 160): %s", len([]rune(int3Sig.Summary)), int3Sig.Summary)
	}
}

func TestINT4_DedupeAndHome(t *testing.T) {
	// Same file loaded twice (should dedupe), one with RelPath and one without
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("start")

	// First instructions event - no RelPath, so Home() will be used
	i1 := model.Event{
		Kind: model.KindInstructions,
		Instructions: &model.Instructions{
			Path:      "/some/path/.claude/CLAUDE.md",
			RelPath:   "",
			Scope:     "user",
			Hash:      "abc123",
			FirstLine: "# Global instructions",
			Content:   "Global rules",
		},
	}
	sb.Add(i1)

	// Same file loaded again with same hash (should be deduplicated)
	i2 := model.Event{
		Kind: model.KindInstructions,
		Instructions: &model.Instructions{
			Path:      "/some/path/.claude/CLAUDE.md",
			RelPath:   "",
			Scope:     "user",
			Hash:      "abc123",
			FirstLine: "# Global instructions",
			Content:   "Global rules",
		},
	}
	sb.Add(i2)

	// Different file with RelPath
	i3 := model.Event{
		Kind: model.KindInstructions,
		Instructions: &model.Instructions{
			Path:      "/repo/.claude/CLAUDE.md",
			RelPath:   ".claude/CLAUDE.md",
			Scope:     "project",
			Hash:      "def456",
			FirstLine: "# Project rules",
			Content:   "Project specific",
		},
	}
	sb.Add(i3)

	s := sb.Build()
	ctx := testkit.Ctx(nil, s)
	sig := engine.Run(ctx)
	int4Sig := testkit.Find(sig, "INT-4")

	if int4Sig == nil {
		t.Fatal("INT-4 signal not found")
	}

	// Should have only 2 findings (deduplicated)
	if len(int4Sig.Findings) != 2 {
		t.Errorf("Expected 2 findings after deduplication, got %d", len(int4Sig.Findings))
	}

	// Check that rel path is shown when available
	var relPathFound bool
	for _, f := range int4Sig.Findings {
		if contains(f.Summary, ".claude/CLAUDE.md") {
			relPathFound = true
			break
		}
	}
	if !relPathFound {
		t.Errorf("Expected .claude/CLAUDE.md path in findings. Findings: %v", int4Sig.Findings)
	}

	// Check summary length
	if len([]rune(int4Sig.Summary)) > 160 {
		t.Errorf("Summary too long (%d > 160): %s", len([]rune(int4Sig.Summary)), int4Sig.Summary)
	}

	// Check data deduplication
	if files, ok := int4Sig.Data["files"].([]map[string]any); ok {
		if len(files) != 2 {
			t.Errorf("Expected 2 files in data, got %d", len(files))
		}
	} else {
		t.Errorf("Expected files list in data")
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	if len(substr) == 0 || len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
