package decisions

import (
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// --- DEC-1 ---

func TestDEC1_InterruptMapsToTurnEdits(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("implement the parser").
		Edit("a.go", "line one").
		Add(model.Event{Kind: model.KindInterrupt, Interrupt: &model.Interrupt{Reason: "user"}}).
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 10, "line one").Build()
	ctx := testkit.Ctx(pr, s)

	sigs := engine.RunIDs(ctx, "DEC-1")
	sig := testkit.Find(sigs, "DEC-1")
	if sig == nil {
		t.Fatal("DEC-1 signal missing")
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(sig.Findings), sig.Findings)
	}
	f := sig.Findings[0]
	if !strings.Contains(f.Summary, "Interrupted at") || !strings.Contains(f.Summary, "`a.go`") {
		t.Errorf("unexpected finding summary: %q", f.Summary)
	}
	if f.Severity != model.StateAlert {
		t.Errorf("want alert severity (anchors present), got %s", f.Severity)
	}
	if len(f.Anchors) == 0 {
		t.Errorf("want anchors, got none")
	}
	if sig.State != model.StateAlert {
		t.Errorf("want signal state alert, got %s", sig.State)
	}
}

func TestDEC1_CorrectionPromptPreviousTurn(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("implement the parser").
		Edit("a.go", "use mysql here").
		Prompt("No, use Postgres instead").
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 10, "use mysql here").Build()
	ctx := testkit.Ctx(pr, s)

	sigs := engine.RunIDs(ctx, "DEC-1")
	sig := testkit.Find(sigs, "DEC-1")
	if sig == nil {
		t.Fatal("DEC-1 signal missing")
	}
	if got := sig.Data["correction_prompts"]; got != 1 {
		t.Fatalf("want 1 correction prompt, got %v (findings=%+v)", got, sig.Findings)
	}
	var found *model.Finding
	for i := range sig.Findings {
		if sig.Findings[i].Data["kind"] == "correction_prompt" {
			found = &sig.Findings[i]
		}
	}
	if found == nil {
		t.Fatal("no correction_prompt finding")
	}
	if found.Data["candidate"] != true {
		t.Errorf("want candidate=true, got %v", found.Data["candidate"])
	}
	if !strings.Contains(found.Summary, "changes to `a.go`") {
		t.Errorf("expected anchor mention in summary, got %q", found.Summary)
	}
	if len(found.Anchors) == 0 {
		t.Errorf("want anchors from the previous-turn edit")
	}
}

func TestDEC1_NotCorrectionWithoutPriorAction(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("Stop, that is wrong from the start").
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-1")
	sig := testkit.Find(sigs, "DEC-1")
	if sig == nil {
		t.Fatal("DEC-1 signal missing")
	}
	if got := sig.Data["correction_prompts"]; got != 0 {
		t.Fatalf("want 0 correction prompts (no prior action in turn 0), got %v", got)
	}
	if sig.State != model.StateClear {
		t.Errorf("want clear state, got %s", sig.State)
	}
}

func TestDEC1_KeywordsBoundary(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("implement the parser").
		Edit("a.go", "hello").
		Prompt("nothing to add").
		Edit("a.go", "hello again").
		Prompt("No, use Postgres instead").
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-1")
	sig := testkit.Find(sigs, "DEC-1")
	if sig == nil {
		t.Fatal("DEC-1 signal missing")
	}
	if got := sig.Data["correction_prompts"]; got != 1 {
		t.Fatalf("want exactly 1 correction prompt (\"nothing to add\" must not match), got %v", got)
	}
	found := false
	for _, f := range sig.Findings {
		if strings.Contains(f.Summary, "Postgres") {
			found = true
		}
		if strings.Contains(f.Summary, "nothing to add") {
			t.Errorf("\"nothing to add\" prompt should not have produced a finding: %q", f.Summary)
		}
	}
	if !found {
		t.Errorf("expected a finding referencing the Postgres correction")
	}
}

func TestDEC1_RejectionAndDenial(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("do the thing").
		Add(model.Event{Kind: model.KindRejection, Rejection: &model.Rejection{Tool: "Bash"}}).
		Add(model.Event{Kind: model.KindPermission, Permission: &model.Permission{Tool: "Write", Decision: "deny", By: "human"}}).
		Add(model.Event{Kind: model.KindPermission, Permission: &model.Permission{Tool: "Write", Decision: "deny", By: "auto"}}).
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-1")
	sig := testkit.Find(sigs, "DEC-1")
	if sig == nil {
		t.Fatal("DEC-1 signal missing")
	}
	if got := sig.Data["rejections"]; got != 1 {
		t.Errorf("want 1 rejection, got %v", got)
	}
	if got := sig.Data["denials"]; got != 1 {
		t.Errorf("want 1 denial (auto excluded), got %v", got)
	}
	var sawRejection, sawDenial bool
	for _, f := range sig.Findings {
		switch f.Data["kind"] {
		case "rejection":
			sawRejection = true
			if !strings.Contains(f.Summary, "Rejected a `Bash` call") {
				t.Errorf("unexpected rejection summary: %q", f.Summary)
			}
		case "denial":
			sawDenial = true
			if !strings.Contains(f.Summary, "Denied a `Write` permission request") {
				t.Errorf("unexpected denial summary: %q", f.Summary)
			}
		}
	}
	if !sawRejection || !sawDenial {
		t.Errorf("expected both a rejection and a denial finding")
	}
}

func TestDEC1_SummaryHasNoPromptText(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("implement the parser").
		Edit("a.go", "use mysql here").
		Prompt("do not use MongoDB, use Postgres instead").
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-1")
	sig := testkit.Find(sigs, "DEC-1")
	if sig == nil {
		t.Fatal("DEC-1 signal missing")
	}
	if strings.Contains(sig.Summary, "MongoDB") || strings.Contains(sig.Summary, "Postgres") {
		t.Errorf("signal summary must not contain prompt text: %q", sig.Summary)
	}
	if len(sig.Summary) > 160 {
		t.Errorf("summary too long: %d", len(sig.Summary))
	}
}

func TestDEC1_Clear(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("implement the parser").
		Edit("a.go", "line one").
		Run("go test ./...", 0, "ok").
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-1")
	sig := testkit.Find(sigs, "DEC-1")
	if sig == nil {
		t.Fatal("DEC-1 signal missing")
	}
	if sig.State != model.StateClear {
		t.Errorf("want clear, got %s", sig.State)
	}
	if sig.Summary != "No interrupts, rejections or corrections." {
		t.Errorf("unexpected summary: %q", sig.Summary)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("want no findings, got %d", len(sig.Findings))
	}
}

// --- DEC-2 ---

func TestDEC2_RevertedEdit(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("try approach A").
		Edit("a.go", "line one", "line two", "line three").
		Prompt("undo that").
		Replace("a.go", []string{"line one", "line two", "line three"}, []string{"final line"}).
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "final line").Build()
	ctx := testkit.Ctx(pr, s)

	sigs := engine.RunIDs(ctx, "DEC-2")
	sig := testkit.Find(sigs, "DEC-2")
	if sig == nil {
		t.Fatal("DEC-2 signal missing")
	}
	if got := sig.Data["reverted_edits"]; got != 1 {
		t.Fatalf("want 1 reverted edit, got %v (findings=%+v)", got, sig.Findings)
	}
	var found *model.Finding
	for i := range sig.Findings {
		if sig.Findings[i].Data["kind"] == "reverted_edit" {
			found = &sig.Findings[i]
		}
	}
	if found == nil {
		t.Fatal("no reverted_edit finding")
	}
	if !strings.Contains(found.Summary, "`a.go`") || !strings.Contains(found.Summary, "was later removed") {
		t.Errorf("unexpected finding summary: %q", found.Summary)
	}
	if len(found.Evidence) != 2 {
		t.Errorf("want evidence on first and removing edit, got %d", len(found.Evidence))
	}
	if sig.State != model.StateInfo {
		t.Errorf("want info state, got %s", sig.State)
	}
}

func TestDEC2_KeptEditNotReverted(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("write the feature").
		Edit("a.go", "line one", "line two").
		Run("go test ./...", 0, "ok").
		Build()

	pr := testkit.PR("acme/shop", 1).Add("a.go", 1, "line one", "line two").Build()
	ctx := testkit.Ctx(pr, s)

	sigs := engine.RunIDs(ctx, "DEC-2")
	sig := testkit.Find(sigs, "DEC-2")
	if sig == nil {
		t.Fatal("DEC-2 signal missing")
	}
	if got := sig.Data["reverted_edits"]; got != 0 {
		t.Fatalf("want 0 reverted edits (lines still in PR), got %v", got)
	}
	if sig.State != model.StateClear {
		t.Errorf("want clear, got %s (findings=%+v)", sig.State, sig.Findings)
	}
}

func TestDEC2_CreatedThenDeleted(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("scaffold and clean up").
		Create("tmp.go", "package tmp").
		Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{Path: "/repo/tmp.go", RelPath: "tmp.go", Op: model.OpDelete}}).
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-2")
	sig := testkit.Find(sigs, "DEC-2")
	if sig == nil {
		t.Fatal("DEC-2 signal missing")
	}
	if got := sig.Data["created_then_deleted"]; got != 1 {
		t.Fatalf("want 1 created-then-deleted, got %v (findings=%+v)", got, sig.Findings)
	}
	var found *model.Finding
	for i := range sig.Findings {
		if sig.Findings[i].Data["kind"] == "created_then_deleted" {
			found = &sig.Findings[i]
		}
	}
	if found == nil {
		t.Fatal("no created_then_deleted finding")
	}
	if found.Summary != "Created and later deleted `tmp.go`." {
		t.Errorf("unexpected summary: %q", found.Summary)
	}
}

func TestDEC2_Discard(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("try something").
		Edit("a.go", "line one").
		Run("git reset --hard HEAD~1", 0, "").
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-2")
	sig := testkit.Find(sigs, "DEC-2")
	if sig == nil {
		t.Fatal("DEC-2 signal missing")
	}
	if got := sig.Data["discards"]; got != 1 {
		t.Fatalf("want 1 discard, got %v (findings=%+v)", got, sig.Findings)
	}
	var found *model.Finding
	for i := range sig.Findings {
		if sig.Findings[i].Data["kind"] == "discard" {
			found = &sig.Findings[i]
		}
	}
	if found == nil {
		t.Fatal("no discard finding")
	}
	if !strings.Contains(found.Summary, "Discarded changes with `git reset --hard HEAD~1`") {
		t.Errorf("unexpected summary: %q", found.Summary)
	}
}

func TestDEC2_Rollback(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("try something").
		Add(model.Event{Kind: model.KindReset, Reset: &model.Reset{Type: "rollback", Summary: "back to turn 1"}}).
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-2")
	sig := testkit.Find(sigs, "DEC-2")
	if sig == nil {
		t.Fatal("DEC-2 signal missing")
	}
	if got := sig.Data["rollbacks"]; got != 1 {
		t.Fatalf("want 1 rollback, got %v", got)
	}
	found := false
	for _, f := range sig.Findings {
		if strings.HasPrefix(f.Summary, "Rolled back the conversation at") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a rollback finding, got %+v", sig.Findings)
	}
}

func TestDEC2_PiBranch(t *testing.T) {
	s := testkit.Session(model.HarnessPi, "s1").
		Prompt("try branch A").
		Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{Path: "/repo/a.go", RelPath: "a.go", Op: model.OpUpdate, Added: []string{"x"}}, OffBranch: true}).
		Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{Path: "/repo/b.go", RelPath: "b.go", Op: model.OpUpdate, Added: []string{"y"}}, OffBranch: true}).
		Add(model.Event{Kind: model.KindReset, Reset: &model.Reset{Type: "branch_switch", Summary: "switched to branch B"}}).
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-2")
	sig := testkit.Find(sigs, "DEC-2")
	if sig == nil {
		t.Fatal("DEC-2 signal missing")
	}
	if got := sig.Data["branches"]; got != 1 {
		t.Fatalf("want 1 branch, got %v", got)
	}
	if got := sig.Data["off_branch_edits"]; got != 2 {
		t.Fatalf("want 2 off-branch edits, got %v", got)
	}
	var found *model.Finding
	for i := range sig.Findings {
		if sig.Findings[i].Data["kind"] == "branch_switch" {
			found = &sig.Findings[i]
		}
	}
	if found == nil {
		t.Fatal("no branch_switch finding")
	}
	if !strings.HasPrefix(found.Summary, "Left a branch at") || !strings.Contains(found.Summary, "switched to branch B") {
		t.Errorf("unexpected summary: %q", found.Summary)
	}
	if found.Data["off_branch_edits"] != 2 {
		t.Errorf("want per-finding off_branch_edits=2, got %v", found.Data["off_branch_edits"])
	}
}

func TestDEC2_Clear(t *testing.T) {
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("implement the parser").
		Edit("a.go", "line one").
		Run("go test ./...", 0, "ok").
		Build()

	ctx := testkit.Ctx(nil, s)
	sigs := engine.RunIDs(ctx, "DEC-2")
	sig := testkit.Find(sigs, "DEC-2")
	if sig == nil {
		t.Fatal("DEC-2 signal missing")
	}
	if sig.State != model.StateClear {
		t.Errorf("want clear, got %s", sig.State)
	}
	if sig.Summary != "No reverted code, discards, rollbacks or abandoned branches." {
		t.Errorf("unexpected summary: %q", sig.Summary)
	}
}
