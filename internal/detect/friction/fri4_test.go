package friction

import (
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestFRI4_SearchFollowedByEdit(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.At("14:00").
		Add(model.Event{
			Kind: model.KindLookup,
			Lookup: &model.Lookup{
				Kind:  "search",
				Query: "how to parse JSON in Go",
			},
		}).
		At("14:02").
		Edit("src/parser.go", "import \"encoding/json\"")

	sess := sb.Build()
	pr := testkit.PR("acme/shop", 42).
		Add("src/parser.go", 1, "package main", "import \"encoding/json\"").
		Build()

	ctx := testkit.Ctx(pr, sess)
	sig := fri4{}.Detect(ctx)

	if sig.ID != "FRI-4" {
		t.Errorf("ID = %q, want FRI-4", sig.ID)
	}
	if sig.State != model.StateInfo {
		t.Errorf("State = %q, want info", sig.State)
	}
	if len(sig.Findings) != 1 {
		t.Errorf("len(Findings) = %d, want 1", len(sig.Findings))
	}

	finding := sig.Findings[0]
	if !contains(finding.Summary, "Searched") || !contains(finding.Summary, "edits followed") {
		t.Errorf("Summary = %q, should mention search and edits", finding.Summary)
	}

	data := sig.Data
	if data["lookups"] != 1 {
		t.Errorf("lookups = %v, want 1", data["lookups"])
	}
	if data["followed_by_edits"] != 1 {
		t.Errorf("followed_by_edits = %v, want 1", data["followed_by_edits"])
	}

	if len(finding.Anchors) == 0 {
		t.Errorf("Anchors should not be empty when edit followed")
	}
}

func TestFRI4_WindowLimit(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.At("14:00").
		Add(model.Event{
			Kind: model.KindLookup,
			Lookup: &model.Lookup{
				Kind:  "fetch",
				Query: "",
				URLs:  []string{"https://golang.org/pkg/encoding/json"},
			},
		}).
		At("14:20"). // 20 minutes later, outside 15-minute window
		Edit("src/parser.go", "// edit far away")

	sess := sb.Build()
	pr := testkit.PR("acme/shop", 42).
		Add("src/parser.go", 1, "package main").
		Build()

	ctx := testkit.Ctx(pr, sess)
	sig := fri4{}.Detect(ctx)

	// Edit is outside the 15-minute window, so not followed
	data := sig.Data
	if data["followed_by_edits"] != 0 {
		t.Errorf("followed_by_edits = %v, want 0 (edit is 20 minutes later)", data["followed_by_edits"])
	}

	finding := sig.Findings[0]
	if contains(finding.Summary, "edits followed") {
		t.Errorf("Summary should not mention edits when outside window: %q", finding.Summary)
	}
}

func TestFRI4_DocsMCP(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.At("14:00").
		Add(model.Event{
			Kind: model.KindMCP,
			MCP: &model.MCPCall{
				Server: "docs",
				Tool:   "search",
				Args:   "{\"query\": \"recursion\"}",
			},
		}).
		At("14:02").
		Edit("src/algorithm.go", "func fibonacci(n int) int {")

	sess := sb.Build()
	pr := testkit.PR("acme/shop", 42).
		Add("src/algorithm.go", 1, "package main").
		Build()

	ctx := testkit.Ctx(pr, sess)
	sig := fri4{}.Detect(ctx)

	if sig.State != model.StateInfo {
		t.Errorf("State = %q, want info", sig.State)
	}

	finding := sig.Findings[0]
	if !contains(finding.Summary, "Looked up docs") || !contains(finding.Summary, "docs") {
		t.Errorf("Summary = %q, should mention docs", finding.Summary)
	}

	data := sig.Data
	if data["lookups"] != 1 {
		t.Errorf("lookups = %v, want 1", data["lookups"])
	}
	if data["followed_by_edits"] != 1 {
		t.Errorf("followed_by_edits = %v, want 1", data["followed_by_edits"])
	}
}

func TestFRI4_None(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.At("14:00").Say("hello").At("14:05").Edit("src/main.go", "package main")

	sess := sb.Build()
	pr := testkit.PR("acme/shop", 42).Build()
	ctx := testkit.Ctx(pr, sess)

	sig := fri4{}.Detect(ctx)

	if sig.State != model.StateClear {
		t.Errorf("State = %q, want clear", sig.State)
	}
	if sig.Summary != "No web or docs lookups." {
		t.Errorf("Summary = %q", sig.Summary)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("len(Findings) = %d, want 0", len(sig.Findings))
	}

	data := sig.Data
	if data["lookups"] != 0 {
		t.Errorf("lookups = %v, want 0", data["lookups"])
	}
}

func TestFRI4_NoSessions(t *testing.T) {
	pr := testkit.PR("acme/shop", 42).Build()
	ctx := testkit.Ctx(pr)

	sig := fri4{}.Detect(ctx)

	if sig.State != model.StateUnknown {
		t.Errorf("State = %q, want unknown", sig.State)
	}
}

// Helper function
func contains(s, substr string) bool {
	if len(s) == 0 || len(substr) == 0 {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
