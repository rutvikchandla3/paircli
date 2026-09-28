package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

var fixedNow = time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC)

// buildInput returns a small deterministic input: two claude-code sessions and
// one codex session, one commit, one alert-ish signal set.
func buildInput() BuildInput {
	pr := testkit.PR("acme/shop", 482).
		Body("Adds retry.").
		Add("src/webhooks/retry.ts", 40, "const max = 5", "retry()").
		Add("src/webhooks/retry.ts", 41, "sleep()").
		Remove("src/webhooks/sender.ts", 10, "old()").
		Commit("aaaaaaa1111111111111111111111111111111", "14:00").
		Commit("bbbbbbb2222222222222222222222222222222", "14:20").
		Build()

	main := testkit.Session(model.HarnessClaudeCode, "ab12cd34ef567890").
		Prompt("Add retry with backoff").
		Say("On it.").
		Edit("src/webhooks/retry.ts", "const max = 5").
		Build()

	sub := testkit.Session(model.HarnessClaudeCode, "subagent99").
		Prompt("second ask").
		Build()
	sub.Capture = model.CaptureHooked

	cx := testkit.Session(model.HarnessCodex, "019f0000aabb").
		Model("gpt-5-codex").
		Prompt("run tests").
		Build()

	attr := &model.Attribution{
		Files: []model.FileAttribution{{
			Path: "src/webhooks/retry.ts",
			Lines: []model.LineAttribution{
				{Line: 40, Label: model.LabelAgent, Source: &model.EventRef{Session: main.Ref(), Event: "e3"}},
				{Line: 41, Label: model.LabelUncaptured},
			},
			Counts: map[model.LineLabel]int{model.LabelAgent: 1, model.LabelUncaptured: 1},
		}},
		Total:     2,
		Explained: 1,
	}

	return BuildInput{
		PR:           pr,
		Sessions:     []*model.Session{main, sub, cx},
		Links:        []model.CommitLink{{SHA: "aaaaaaa1111111111111111111111111111111", Sessions: []string{main.Ref()}, Method: model.LinkSHAExact, Confidence: "exact"}},
		SessionLinks: map[string]model.LinkMethod{main.Ref(): model.LinkSHAExact},
		Attribution:  attr,
		Signals:      []model.Signal{},
		Dropped:      2,
		Unattributed: []string{"ccccccc3333333333333333333333333333333"},
		Version:      "0.2.0",
		Now:          fixedNow,
	}
}

func TestBuildReport_Coverage(t *testing.T) {
	base := buildInput()

	tests := []struct {
		name    string
		mutate  func(in *BuildInput)
		capture string
	}{
		{"no sessions", func(in *BuildInput) { in.Sessions = nil }, "none"},
		{"all hooked", func(in *BuildInput) {
			for _, s := range in.Sessions {
				s.Capture = model.CaptureHooked
			}
		}, "hooked"},
		{"none hooked", func(in *BuildInput) {
			for _, s := range in.Sessions {
				s.Capture = model.CaptureReconstructed
			}
		}, "reconstructed"},
		{"mixed", func(in *BuildInput) {
			in.Sessions[0].Capture = model.CaptureHooked
		}, "partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			tt.mutate(&in)
			rep := BuildReport(in)
			if rep.Coverage.Capture != tt.capture {
				t.Errorf("Capture = %q, want %q", rep.Coverage.Capture, tt.capture)
			}
			if rep.Coverage.Sessions != len(in.Sessions) {
				t.Errorf("Sessions = %d, want %d", rep.Coverage.Sessions, len(in.Sessions))
			}
			if rep.Coverage.DroppedSessions != 2 {
				t.Errorf("DroppedSessions = %d, want 2", rep.Coverage.DroppedSessions)
			}
			if rep.SchemaVersion != model.SchemaVersion {
				t.Errorf("SchemaVersion = %q, want %q", rep.SchemaVersion, model.SchemaVersion)
			}
			if rep.Tool != "paircli 0.2.0" {
				t.Errorf("Tool = %q, want %q", rep.Tool, "paircli 0.2.0")
			}
			if !rep.GeneratedAt.Equal(fixedNow) {
				t.Errorf("GeneratedAt = %v, want %v", rep.GeneratedAt, fixedNow)
			}
			if rep.Coverage.UnattributedCommits == nil || len(rep.Coverage.UnattributedCommits) != 1 {
				t.Errorf("UnattributedCommits = %#v, want 1 entry", rep.Coverage.UnattributedCommits)
			}
			if rep.Commits == nil || rep.Signals == nil || rep.Sessions == nil {
				t.Errorf("slices must never be nil: commits=%v signals=%v sessions=%v",
					rep.Commits == nil, rep.Signals == nil, rep.Sessions == nil)
			}
		})
	}

	t.Run("empty input has no nil slices", func(t *testing.T) {
		rep := BuildReport(BuildInput{Now: fixedNow})
		if rep.Coverage.UnattributedCommits == nil {
			t.Error("UnattributedCommits is nil")
		}
		if rep.Commits == nil || rep.Signals == nil || rep.Sessions == nil {
			t.Error("report slices must never be nil")
		}
		if rep.Coverage.Harnesses == nil {
			t.Error("Harnesses must never be nil")
		}
	})

	t.Run("counts and diff totals", func(t *testing.T) {
		rep := BuildReport(base)
		if rep.PR.Commits != 2 || rep.PR.Files != 2 {
			t.Errorf("PR counts = %d commits %d files, want 2 and 2", rep.PR.Commits, rep.PR.Files)
		}
		if rep.PR.Additions != 3 || rep.PR.Deletions != 1 {
			t.Errorf("diff totals = +%d -%d, want +3 -1", rep.PR.Additions, rep.PR.Deletions)
		}
		if rep.PR.Repo != "acme/shop" || rep.PR.Number != 482 {
			t.Errorf("PR header = %s #%d", rep.PR.Repo, rep.PR.Number)
		}
		if rep.Coverage.Harnesses[model.HarnessClaudeCode] != 2 ||
			rep.Coverage.Harnesses[model.HarnessCodex] != 1 {
			t.Errorf("Harnesses = %#v", rep.Coverage.Harnesses)
		}
		if rep.Coverage.LinesTotal != 2 || rep.Coverage.LinesExplained != 1 || rep.Coverage.Ratio != 0.5 {
			t.Errorf("coverage lines = %d/%d ratio %v", rep.Coverage.LinesExplained, rep.Coverage.LinesTotal, rep.Coverage.Ratio)
		}
	})
}

func TestBuildReport_SessionSummary(t *testing.T) {
	in := buildInput()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	homePath := filepath.Join(home, ".claude", "projects", "s1.jsonl")
	in.Sessions[0].SourcePath = homePath
	in.Sessions[0].Events = append(in.Sessions[0].Events,
		model.Event{ID: "e90", Kind: model.KindMessage, TS: fixedNow, Model: "claude-sonnet-4",
			Message: &model.Message{Usage: &model.Usage{Input: 10, Output: 20, CacheRead: 3, CacheWrite: 4, CostUSD: 0.25}}},
		model.Event{ID: "e91", Kind: model.KindMessage, TS: fixedNow, Model: "claude-opus-4",
			Message: &model.Message{Usage: &model.Usage{Input: 1, Output: 2}}})

	rep := BuildReport(in)
	got := rep.Sessions[0]

	if got.Ref != in.Sessions[0].Ref() {
		t.Errorf("Ref = %q, want %q", got.Ref, in.Sessions[0].Ref())
	}
	want := []string{"test-model", "claude-sonnet-4", "claude-opus-4"}
	if strings.Join(got.Models, ",") != strings.Join(want, ",") {
		t.Errorf("Models = %v, want %v", got.Models, want)
	}
	if got.Usage.Input != 11 || got.Usage.Output != 22 || got.Usage.CacheRead != 3 ||
		got.Usage.CacheWrite != 4 || got.Usage.CostUSD != 0.25 {
		t.Errorf("Usage = %+v, want input 11 output 22 cache 3/4 cost 0.25", got.Usage)
	}
	if got.LinesAuthored != 1 {
		t.Errorf("LinesAuthored = %d, want 1", got.LinesAuthored)
	}
	if got.SourcePath != "~/.claude/projects/s1.jsonl" {
		t.Errorf("SourcePath = %q, want %q", got.SourcePath, "~/.claude/projects/s1.jsonl")
	}
	if got.Link != model.LinkSHAExact {
		t.Errorf("Link = %q, want sha_exact", got.Link)
	}
	if len(got.Commits) != 1 || got.Commits[0] != "aaaaaaa1111111111111111111111111111111" {
		t.Errorf("Commits = %v", got.Commits)
	}
	if got.Capture != in.Sessions[0].Capture {
		t.Errorf("Capture = %q, want %q", got.Capture, in.Sessions[0].Capture)
	}
	if got.Counts[model.KindPrompt] != 1 || got.Counts[model.KindMessage] != 3 {
		t.Errorf("Counts = %#v", got.Counts)
	}
	if rep.Sessions[1].Link != model.LinkNone {
		t.Errorf("unlinked session Link = %q, want none", rep.Sessions[1].Link)
	}
}

// signalOf builds a catalog signal with the given state, summary and findings.
func signalOf(id string, state model.State, summary string, findings ...model.Finding) model.Signal {
	s := engine.NewSignal(id)
	s.State = state
	s.Summary = summary
	s.Findings = findings
	return s
}

// goldenReport is the hand-built report the golden tests render: alerts, info,
// clear and unknown signals, plus an inferred finding.
func goldenReport() *model.Report {
	ev := model.Evidence{
		Session: "claude-code:ab12cd34ef567890",
		Event:   "e7",
		TS:      time.Date(2026, 9, 27, 14, 10, 0, 0, time.UTC),
		Excerpt: "cap it at five",
	}
	inferred := model.Finding{
		Summary:    "Chose fixed backoff over jitter",
		Provenance: model.Inferred,
		Anchors:    []model.Anchor{{File: "src/webhooks/retry.ts", Lines: "40-45"}},
	}
	rep := BuildReport(buildInput())
	rep.PR.Title = "Add retry with backoff to webhook sender"
	rep.PR.HeadRef = "feat/retry"
	rep.PR.BaseRef = "main"
	rep.Signals = []model.Signal{
		signalOf("VER-2", model.StateAlert,
			"2 PR hunks were edited after the last passing test run (14:02 UTC).",
			model.Finding{
				Summary:  "`src/webhooks/retry.ts` lines 40-45 changed at 14:07 UTC, after the last passing run.",
				Severity: model.StateAlert,
				Anchors:  []model.Anchor{{File: "src/webhooks/retry.ts", Lines: "40-45"}},
				Evidence: []model.Evidence{ev},
			},
			model.Finding{
				Summary:  "`src/webhooks/sender.ts` line 10 changed later.",
				Severity: model.StateAlert,
				Anchors:  []model.Anchor{{File: "src/webhooks/sender.ts", Lines: "10"}},
			}),
		signalOf("VER-4", model.StateAlert, "1 test file was modified in the same session as the change it guards."),
		signalOf("INT-1", model.StateInfo,
			"2 prompts across 1 session; first: “Add retry with backoff…”.",
			model.Finding{
				Summary:  "(mid-task) cap at 5 attempts",
				Severity: model.StateInfo,
				Evidence: []model.Evidence{ev},
			}),
		signalOf("DEC-3", model.StateInfo,
			"One choice shaped the diff: retry budget.",
			inferred),
		signalOf("AUTH-1", model.StateClear, "Every added line traces to a captured edit."),
		signalOf("CON-3", model.StateUnknown, "LLM pass is off. Run with --llm to fill this in."),
	}
	rep.Signals[2].Support = map[string]string{"claude-code": "full", "codex": "partial"}
	rep.Sessions[0].Models = []string{"claude-sonnet-4"}
	rep.Sessions[0].LinesAuthored = 1
	return rep
}

func TestReportMarkdown_Golden(t *testing.T) {
	checkGolden(t, "report.md", ReportMarkdown(goldenReport()))
}

func TestCommentMarkdown_Golden(t *testing.T) {
	opts := Options{OutDir: ".paircli/pr-482", MaxCommentLines: 5}
	checkGolden(t, "comment.md", CommentMarkdown(goldenReport(), opts))
}

// checkGolden compares got against testdata/golden/<name>, rewriting it with -update.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run go test ./internal/render/... -update)", err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// commentReport builds a report with n alerts of mixed priority plus filler signals.
func commentReport(n int, filler bool) *model.Report {
	rep := BuildReport(buildInput())
	ids := []string{"VER-5", "INT-1", "VER-4", "FRI-1", "DEC-1", "OVS-1", "EXP-1"}
	for i := 0; i < n && i < len(ids); i++ {
		rep.Signals = append(rep.Signals, signalOf(ids[i], model.StateAlert, "alert "+ids[i]))
	}
	if filler {
		rep.Signals = append(rep.Signals,
			signalOf("FRI-1", model.StateInfo, "Most rework: `retry.ts` (11 edits).",
				model.Finding{Summary: "churn", Severity: model.StateInfo}),
			signalOf("OVS-1", model.StateInfo, "Edits ran without a permission prompt."),
			signalOf("INT-1", model.StateInfo, "2 prompts across 1 session."))
	}
	return rep
}

func TestComment_Budget(t *testing.T) {
	rep := commentReport(7, false)
	got := CommentMarkdown(rep, Options{OutDir: ".paircli/pr-482", MaxCommentLines: 5})
	var bullets []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "- ") {
			bullets = append(bullets, l)
		}
	}
	if len(bullets) != 5 {
		t.Fatalf("got %d bullets, want 5:\n%s", len(bullets), got)
	}
	// P0 alerts first, in catalog order: INT-1, VER-4, VER-5, DEC-1, EXP-1.
	want := []string{"INT-1", "VER-4", "VER-5", "DEC-1", "EXP-1"}
	for i, id := range want {
		if !strings.Contains(bullets[i], "`"+id+"`") {
			t.Errorf("bullet %d = %q, want signal %s", i, bullets[i], id)
		}
	}
	if strings.Contains(got, "`FRI-1`") {
		t.Errorf("budget must stop before the P1 alert:\n%s", got)
	}
}

func TestComment_NoAlerts(t *testing.T) {
	rep := BuildReport(buildInput())
	rep.Signals = []model.Signal{signalOf("AUTH-1", model.StateClear, "Every added line traces to a captured edit.")}
	got := CommentMarkdown(rep, Options{OutDir: ".paircli/pr-482"})
	if !strings.Contains(got, "- No alerts.") {
		t.Errorf("missing no-alerts bullet:\n%s", got)
	}
	if !strings.HasPrefix(got, CommentMarker+"\n") {
		t.Errorf("comment must start with the marker:\n%s", got)
	}
}

func TestComment_NoSessions(t *testing.T) {
	rep := BuildReport(buildInput())
	rep.Coverage.Sessions = 0
	got := CommentMarkdown(rep, Options{OutDir: ".paircli/pr-482"})
	if !strings.Contains(got, "No captured AI coding sessions matched this PR.") {
		t.Errorf("missing no-sessions line:\n%s", got)
	}
	if strings.Contains(got, "## ") || strings.Contains(got, "\n- ") {
		t.Errorf("no-sessions comment must have no bullets or sections:\n%s", got)
	}
}

func TestComment_PromptsExcludedByDefault(t *testing.T) {
	rep := commentReport(0, true)
	got := CommentMarkdown(rep, Options{OutDir: ".paircli/pr-482", IncludePrompts: false})
	if strings.Contains(got, "**Ask:**") || strings.Contains(got, "INT-1") {
		t.Errorf("INT-1 must be excluded by default:\n%s", got)
	}
	if !strings.Contains(got, "**Start here:**") || !strings.Contains(got, "**Autonomy:**") {
		t.Errorf("filler bullets missing:\n%s", got)
	}
	if !strings.Contains(got, CommentMarker) {
		t.Errorf("missing marker:\n%s", got)
	}
}

func TestComment_PromptsIncluded(t *testing.T) {
	rep := commentReport(0, true)
	got := CommentMarkdown(rep, Options{OutDir: ".paircli/pr-482", IncludePrompts: true, MaxCommentLines: 5})
	if !strings.Contains(got, "**Ask:**") || !strings.Contains(got, "`INT-1`") {
		t.Errorf("INT-1 must appear when IncludePrompts is set:\n%s", got)
	}
}

func TestWrite_FilesAndStaleCleanup(t *testing.T) {
	dir := t.TempDir()
	in := buildInput()
	rep := BuildReport(in)
	out := filepath.Join(dir, "pr-482")
	sessDir := filepath.Join(out, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(sessDir, "claude-code-stale.json")
	if err := os.WriteFile(stale, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Write(Options{OutDir: out}, rep, in.Attribution, in.Sessions); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale session file was not removed (err=%v)", err)
	}
	for _, name := range []string{"signals.json", "report.md", "comment.md", "authorship.json"} {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) == 0 || data[len(data)-1] != '\n' {
			t.Errorf("%s must end with a newline", name)
		}
	}
	want := filepath.Join(sessDir, "claude-code-ab12cd34ef567890.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected session file %s: %v", want, err)
	}
	data, err := os.ReadFile(filepath.Join(out, "comment.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.Join(out, "report.md")) {
		t.Errorf("comment should point at the report path:\n%s", data)
	}
}

func TestWrite_SanitizesSessionFileName(t *testing.T) {
	dir := t.TempDir()
	in := buildInput()
	s := testkit.Session(model.HarnessPi, "a/b:c d").Build()
	if err := Write(Options{OutDir: dir}, BuildReport(in), in.Attribution, []*model.Session{s}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "pi-a_b_c_d.json")); err != nil {
		t.Errorf("expected sanitized session file: %v", err)
	}
}

func TestDeterministic(t *testing.T) {
	rep := goldenReport()
	first := ReportMarkdown(rep)
	comment := CommentMarkdown(rep, Options{OutDir: ".paircli/pr-482"})
	signals := marshal(t, BuildReport(buildInput()))
	for i := 0; i < 3; i++ {
		if got := ReportMarkdown(rep); got != first {
			t.Fatalf("report render %d differs", i)
		}
		if got := CommentMarkdown(rep, Options{OutDir: ".paircli/pr-482"}); got != comment {
			t.Fatalf("comment render %d differs", i)
		}
		if got := marshal(t, BuildReport(buildInput())); got != signals {
			t.Fatalf("signal build %d differs", i)
		}
	}
}

// marshal renders a report the way signals.json stores it.
func marshal(t *testing.T, rep *model.Report) string {
	t.Helper()
	dir := t.TempDir()
	if err := Write(Options{OutDir: dir}, rep, nil, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "signals.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
