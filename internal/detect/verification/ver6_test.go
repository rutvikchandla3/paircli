package verification

import (
	"encoding/json"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

func TestVER6_Mixed(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.At("14:00").
		Say("Starting checks").
		Run("npm test", 0, "tests pass").
		At("14:05").
		Add(model.Event{
			Kind: model.KindImage,
			Image: &model.Image{
				Tool: "screenshot",
				Ref:  "s1#img-1",
			},
		}).
		At("14:10").
		Run("npm start", 0, "server running").
		At("14:15").
		Run("curl http://localhost:3000", 0, "response ok").
		At("14:20").
		Add(model.Event{
			Kind: model.KindImage,
			Image: &model.Image{
				Ref: "s1#img-2",
			},
		})

	sess := sb.Build()
	pr := testkit.PR("acme/shop", 42).Build()
	ctx := testkit.Ctx(pr, sess)

	sig := ver6{}.Detect(ctx)

	if sig.ID != "VER-6" {
		t.Errorf("ID = %q, want VER-6", sig.ID)
	}
	if sig.State != model.StateInfo {
		t.Errorf("State = %q, want info", sig.State)
	}

	// Should have 3 findings: 2 images, 1 dev_server, 1 local_http
	if len(sig.Findings) != 4 {
		t.Errorf("len(Findings) = %d, want 4; findings: %v", len(sig.Findings), sig.Findings)
	}

	data := sig.Data
	if data["images"] != 2 {
		t.Errorf("images = %v, want 2", data["images"])
	}
	if data["dev_servers"] != 1 {
		t.Errorf("dev_servers = %v, want 1", data["dev_servers"])
	}
	if data["local_requests"] != 1 {
		t.Errorf("local_requests = %v, want 1", data["local_requests"])
	}
}

func TestVER6_OpenErrorsLatestOnly(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.At("14:00").
		Create("src/main.go", "package main").
		At("14:05").
		Add(model.Event{
			Kind: model.KindDiagnostics,
			Diagnostics: &model.Diagnostics{
				Path:     "/repo/src/main.go",
				RelPath:  "src/main.go",
				Errors:   1,
				Messages: []string{"syntax error"},
			},
		}).
		At("14:10").
		Edit("src/main.go", "func main()").
		At("14:15").
		Add(model.Event{
			Kind: model.KindDiagnostics,
			Diagnostics: &model.Diagnostics{
				Path:     "/repo/src/main.go",
				RelPath:  "src/main.go",
				Errors:   0,
				Messages: []string{},
			},
		})

	sess := sb.Build()
	pr := testkit.PR("acme/shop", 42).
		Add("src/main.go", 1, "package main", "func main() {}").
		Build()

	ctx := testkit.Ctx(pr, sess)
	sig := ver6{}.Detect(ctx)

	if sig.State != model.StateInfo {
		t.Errorf("State = %q, want info (latest has no errors)", sig.State)
	}

	data := sig.Data
	openErrors, ok := data["open_errors"].([]map[string]any)
	if !ok {
		t.Errorf("open_errors not found in data or wrong type")
		return
	}
	if len(openErrors) != 0 {
		t.Errorf("len(open_errors) = %d, want 0", len(openErrors))
	}
}

func TestVER6_None(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.At("14:00").Say("hello")

	sess := sb.Build()
	pr := testkit.PR("acme/shop", 42).Build()
	ctx := testkit.Ctx(pr, sess)

	sig := ver6{}.Detect(ctx)

	if sig.State != model.StateInfo {
		t.Errorf("State = %q, want info", sig.State)
	}
	if sig.Summary != "No screenshots, dev servers or local requests were captured." {
		t.Errorf("Summary = %q", sig.Summary)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("len(Findings) = %d, want 0", len(sig.Findings))
	}

	data := sig.Data
	if data["images"] != 0 || data["dev_servers"] != 0 || data["local_requests"] != 0 {
		t.Errorf("unexpected non-zero counts in data: %v", data)
	}
}

// TestVER6_OpenErrorsAreOrdered runs the detector repeatedly over a PR with
// several files carrying errors. Go randomizes map iteration, so before the
// files were sorted this produced a different order on almost every run, while
// the report promises byte-identical output for identical input. A one-file
// fixture cannot catch it: with a single entry there is no order to differ.
func TestVER6_OpenErrorsAreOrdered(t *testing.T) {
	files := []string{"src/a.go", "src/b.go", "src/c.go", "src/d.go", "src/e.go"}
	times := []string{"14:01", "14:02", "14:03", "14:04", "14:05"}

	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.At("14:00").Create("src/a.go", "package a")
	for i, f := range files {
		sb.At(times[i]).Add(model.Event{
			Kind: model.KindDiagnostics,
			Diagnostics: &model.Diagnostics{
				Path:     "/repo/" + f,
				RelPath:  f,
				Errors:   i + 1,
				Messages: []string{"boom"},
			},
		})
	}

	pb := testkit.PR("acme/shop", 42)
	for _, f := range files {
		pb = pb.Add(f, 1, "package x", "var v = 1")
	}
	ctx := testkit.Ctx(pb.Build(), sb.Build())

	// The same input must produce the same output every time.
	var first string
	for run := 0; run < 30; run++ {
		raw, err := json.Marshal(ver6{}.Detect(ctx))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if run == 0 {
			first = string(raw)
			continue
		}
		if string(raw) != first {
			t.Fatalf("VER-6 output changed between runs (run %d): map iteration order is reaching the report", run)
		}
	}

	// And the files come out in sorted order.
	openErrors, ok := ver6{}.Detect(ctx).Data["open_errors"].([]map[string]any)
	if !ok {
		t.Fatalf("open_errors missing or wrong type")
	}
	if len(openErrors) != len(files) {
		t.Fatalf("open_errors = %d, want %d", len(openErrors), len(files))
	}
	for i, oe := range openErrors {
		if got := oe["file"]; got != files[i] {
			t.Errorf("open_errors[%d] = %v, want %q", i, got, files[i])
		}
	}
}

func TestVER6_NoSessions(t *testing.T) {
	pr := testkit.PR("acme/shop", 42).Build()
	ctx := testkit.Ctx(pr)

	sig := ver6{}.Detect(ctx)

	if sig.State != model.StateUnknown {
		t.Errorf("State = %q, want unknown", sig.State)
	}
}
