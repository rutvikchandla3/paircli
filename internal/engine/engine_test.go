package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// testDetector is a fixture Detector for engine tests.
type testDetector struct {
	id string
	fn func(c *Context) model.Signal
}

func (d testDetector) ID() string { return d.id }

func (d testDetector) Detect(c *Context) model.Signal {
	if d.fn != nil {
		return d.fn(c)
	}
	return NewSignal(d.id)
}

func TestCatalog_30UniqueIDs(t *testing.T) {
	cat := Catalog()
	if len(cat) != 30 {
		t.Fatalf("want 30 catalog entries, got %d", len(cat))
	}
	seen := map[string]bool{}
	for _, m := range cat {
		if seen[m.ID] {
			t.Fatalf("duplicate catalog id %s", m.ID)
		}
		seen[m.ID] = true
		if _, ok := support[m.ID]; !ok {
			t.Fatalf("no support row for %s", m.ID)
		}
	}
}

func TestNewContext_TimelineOrder(t *testing.T) {
	base := time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)

	s1 := &model.Session{Harness: model.HarnessClaudeCode, ID: "a", Events: []model.Event{
		{ID: "e1", Kind: model.KindPrompt, TS: base.Add(2 * time.Minute)},
		{ID: "e2", Kind: model.KindMessage, TS: base},
	}}
	s1.Finalize()
	s2 := &model.Session{Harness: model.HarnessCodex, ID: "b", Events: []model.Event{
		{ID: "f1", Kind: model.KindPrompt, TS: base},
	}}
	s2.Finalize()

	c := NewContext(nil, []*model.Session{s1, s2}, nil, nil, nil)
	if len(c.Timeline) != 3 {
		t.Fatalf("want 3 timeline items, got %d", len(c.Timeline))
	}
	// s1's "e2" and s2's "f1" tie on TS == base; "claude-code:a" < "codex:b" so s1 comes first.
	if c.Timeline[0].S.Ref() != "claude-code:a" || c.Timeline[0].E.ID != "e2" {
		t.Fatalf("unexpected first item: %+v", c.Timeline[0])
	}
	if c.Timeline[1].S.Ref() != "codex:b" || c.Timeline[1].E.ID != "f1" {
		t.Fatalf("unexpected second item: %+v", c.Timeline[1])
	}
	if c.Timeline[2].E.ID != "e1" {
		t.Fatalf("unexpected third item: %+v", c.Timeline[2])
	}
}

func TestEvidence_ClipsAndFlattens(t *testing.T) {
	s := &model.Session{Harness: model.HarnessClaudeCode, ID: "a", Events: []model.Event{
		{ID: "e1", Kind: model.KindMessage, TS: time.Date(2026, 9, 27, 14, 0, 0, 0, time.UTC)},
	}}
	s.Finalize()
	c := NewContext(nil, []*model.Session{s}, nil, nil, nil)
	it := c.Timeline[0]

	ev := c.Evidence(it, "a\nb\tc  d")
	if ev.Excerpt != "a b c d" {
		t.Fatalf("want flattened excerpt %q, got %q", "a b c d", ev.Excerpt)
	}

	long := strings.Repeat("z", 250)
	ev2 := c.Evidence(it, long)
	want := strings.Repeat("z", 200) + "…"
	if ev2.Excerpt != want {
		t.Fatalf("want clipped excerpt of len %d, got len %d", len([]rune(want)), len([]rune(ev2.Excerpt)))
	}
}

func TestRun_RecoversPanic(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	Register(testDetector{id: "INT-1", fn: func(c *Context) model.Signal { panic("boom") }})

	c := NewContext(nil, nil, nil, nil, nil)
	sigs := Run(c)
	if len(sigs) != 1 {
		t.Fatalf("want 1 signal, got %d", len(sigs))
	}
	sig := sigs[0]
	if sig.State != model.StateUnknown {
		t.Fatalf("want state unknown, got %s", sig.State)
	}
	if sig.Data["error"] == nil {
		t.Fatalf("want Data[\"error\"] set")
	}
	if !strings.Contains(sig.Summary, "boom") {
		t.Fatalf("want summary to mention panic value, got %q", sig.Summary)
	}
}

func TestRun_ForcesMetadata(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	Register(testDetector{id: "INT-2", fn: func(c *Context) model.Signal {
		sig := NewSignal("INT-2")
		sig.Title = "wrong title"
		sig.Priority = model.P2
		sig.Question = model.QFriction
		sig.Provenance = model.Inferred
		return sig
	}})

	c := NewContext(nil, nil, nil, nil, nil)
	sigs := Run(c)
	sig := sigs[0]

	var want Meta
	for _, m := range catalog {
		if m.ID == "INT-2" {
			want = m
		}
	}
	if sig.Title != want.Title || sig.Priority != want.Priority || sig.Question != want.Question || sig.Provenance != want.Provenance {
		t.Fatalf("metadata not forced back to catalog: %+v, want %+v", sig, want)
	}
}

func TestRun_Support(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	Register(testDetector{id: "AUTH-2"})

	cc := &model.Session{Harness: model.HarnessClaudeCode, ID: "a", Capture: model.CaptureHooked}
	cc.Finalize()
	cx := &model.Session{Harness: model.HarnessCodex, ID: "b", Capture: model.CaptureReconstructed}
	cx.Finalize()

	c := NewContext(nil, []*model.Session{cc, cx}, nil, nil, nil)
	sigs := Run(c)
	sig := sigs[0]

	want := map[string]string{"claude-code": "full", "codex": "partial"}
	if len(sig.Support) != len(want) {
		t.Fatalf("support = %v, want %v", sig.Support, want)
	}
	for k, v := range want {
		if sig.Support[k] != v {
			t.Fatalf("support[%s] = %s, want %s", k, sig.Support[k], v)
		}
	}
}

func TestRegister_DuplicatePanics(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	Register(testDetector{id: "INT-1"})

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("want panic registering a duplicate id")
		}
	}()
	Register(testDetector{id: "INT-1"})
}

func TestRegister_UnknownPanics(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("want panic registering an unknown id")
		}
	}()
	Register(testDetector{id: "NOT-A-REAL-ID"})
}

func TestRunIDs_Subset(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	Register(testDetector{id: "INT-1"})
	Register(testDetector{id: "INT-2"})
	Register(testDetector{id: "AUTH-1"})

	c := NewContext(nil, nil, nil, nil, nil)
	sigs := RunIDs(c, "AUTH-1", "INT-1")
	if len(sigs) != 2 {
		t.Fatalf("want 2 signals, got %d", len(sigs))
	}
	if sigs[0].ID != "INT-1" || sigs[1].ID != "AUTH-1" {
		t.Fatalf("want catalog order [INT-1 AUTH-1], got [%s %s]", sigs[0].ID, sigs[1].ID)
	}
}

func TestFormatHelpers(t *testing.T) {
	t1 := time.Date(2026, 9, 27, 19, 30, 0, 0, time.FixedZone("+0530", 5*3600+30*60))
	if got := Clock(t1); got != "14:00 UTC" {
		t.Fatalf("Clock = %q, want %q", got, "14:00 UTC")
	}

	if got := Plural(1, "edit", "edits"); got != "1 edit" {
		t.Fatalf("Plural(1,...) = %q", got)
	}
	if got := Plural(3, "edit", "edits"); got != "3 edits" {
		t.Fatalf("Plural(3,...) = %q", got)
	}

	if got := Pct(1, 3); got != "33%" {
		t.Fatalf("Pct(1,3) = %q, want 33%%", got)
	}
	if got := Pct(0, 0); got != "0%" {
		t.Fatalf("Pct(0,0) = %q, want 0%%", got)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available")
	}
	p := filepath.Join(home, "x", "y")
	if got := Home(p); got != filepath.Join("~", "x", "y") {
		t.Fatalf("Home(%q) = %q", p, got)
	}
	if got := Home("/elsewhere/x"); got != "/elsewhere/x" {
		t.Fatalf("Home should not touch unrelated paths, got %q", got)
	}
}
