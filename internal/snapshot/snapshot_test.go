package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// fixture builds a small but complete context: a PR whose one added line a
// session also edited, so attribution has something to resolve.
func fixture(t *testing.T) (*engine.Context, *model.PR, *model.Session) {
	t.Helper()
	pr := testkit.PR("acme/shop", 7).
		Body("Fixes the retry bug. Tests pass.").
		Add("internal/api/client.go", 10, "func retry() {}", "\treturn nil").
		Commit("abc1234", "14:00").
		Build()
	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("add a retry to the api client").
		Run("go test ./...", 0, "ok").
		Edit("internal/api/client.go", "func retry() {}", "\treturn nil").
		Build()
	return testkit.Ctx(pr, s), pr, s
}

// TestRoundTrip_ReproducesSignals is the property the whole artifact exists
// for: a snapshot read back from disk rebuilds a context whose deterministic
// report is byte-identical to the original run's.
func TestRoundTrip_ReproducesSignals(t *testing.T) {
	ec, pr, _ := fixture(t)
	want := engine.Run(ec)

	snap := Build(Input{
		PR:          pr,
		Sessions:    ec.Sessions,
		Attribution: ec.Attribution,
		Links:       ec.Links,
		Config:      ec.Config,
		Tool:        "paircli test",
	})

	dir := t.TempDir()
	if err := Write(dir, snap); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	restored, err := read.Context()
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	got := engine.Run(restored)
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("restored signals differ from the original run:\n--- want ---\n%s\n--- got ---\n%s",
			wantJSON, gotJSON)
	}
}

// TestRoundTrip_ComputedFieldsSurvive covers the fields the pipeline computes
// rather than parses. A naive unmarshal would leave them zeroed and the report
// would quietly change.
func TestRoundTrip_ComputedFieldsSurvive(t *testing.T) {
	ec, pr, orig := fixture(t)

	dir := t.TempDir()
	snap := Build(Input{PR: pr, Sessions: ec.Sessions, Attribution: ec.Attribution, Config: ec.Config})
	if err := Write(dir, snap); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(read.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(read.Sessions))
	}
	got := read.Sessions[0]

	// Session.Finalize sets Start/End; Event.Finalize sets Seq and Turn.
	if got.Start.IsZero() || got.End.IsZero() {
		t.Errorf("session window lost: start=%v end=%v", got.Start, got.End)
	}
	if !got.Start.Equal(orig.Start) || !got.End.Equal(orig.End) {
		t.Errorf("session window changed: got %v..%v, want %v..%v",
			got.Start, got.End, orig.Start, orig.End)
	}
	if len(got.Events) != len(orig.Events) {
		t.Fatalf("events = %d, want %d", len(got.Events), len(orig.Events))
	}
	for i := range got.Events {
		if got.Events[i].Seq != orig.Events[i].Seq {
			t.Errorf("event %d: Seq = %d, want %d", i, got.Events[i].Seq, orig.Events[i].Seq)
		}
		if got.Events[i].Turn != orig.Events[i].Turn {
			t.Errorf("event %d: Turn = %d, want %d", i, got.Events[i].Turn, orig.Events[i].Turn)
		}
	}

	// Attribution is what every anchor resolves through; it must survive whole.
	if got, want := len(read.Attribution.Files), len(ec.Attribution.Files); got != want {
		t.Errorf("attributed files = %d, want %d", got, want)
	}
	if read.Attribution.Total != ec.Attribution.Total {
		t.Errorf("attributed lines = %d, want %d", read.Attribution.Total, ec.Attribution.Total)
	}
}

// TestWrite_IsDeterministic checks the file is stable across writes: the same
// snapshot marshals byte for byte, which is what makes a stored hash meaningful.
func TestWrite_IsDeterministic(t *testing.T) {
	ec, pr, _ := fixture(t)
	snap := Build(Input{PR: pr, Sessions: ec.Sessions, Attribution: ec.Attribution, Config: ec.Config})

	first, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Error("marshalling the same snapshot twice produced different bytes")
	}
	if snap.Hash == "" {
		t.Error("Build did not stamp a hash")
	}
}

// TestHash_TracksContent checks the hash actually depends on the record, so it
// can tie a set of judgments to the input that produced them.
func TestHash_TracksContent(t *testing.T) {
	ec, pr, _ := fixture(t)
	base := Build(Input{PR: pr, Sessions: ec.Sessions, Attribution: ec.Attribution, Config: ec.Config})

	other := Build(Input{PR: pr, Sessions: ec.Sessions, Attribution: ec.Attribution,
		Config: ec.Config, Tool: "paircli other"})

	if base.Hash == other.Hash {
		t.Error("two different snapshots share a hash")
	}
	// Recomputing must be stable, and clearing Hash must not change the digest.
	if again := base.computeHash(); again != base.Hash {
		t.Errorf("hash is not stable: %q then %q", base.Hash, again)
	}
}

// TestConfigRoundTrips covers the reason the merged config is stored instead of
// .paircli.json: config.Load appends list fields to the defaults, so the file
// alone cannot reproduce what a scan ran with.
func TestConfigRoundTrips(t *testing.T) {
	cfg := config.Default()
	cfg.TestPathPatterns = append(cfg.TestPathPatterns, `_spec\.rb$`)
	cfg.ExtraChecks = append(cfg.ExtraChecks, config.CheckPattern{Class: "test", Regex: `^make check$`})
	cfg.WindowBefore = config.Duration{Duration: 12 * time.Hour}
	cfg.LLM.Provider = "anthropic"

	dir := t.TempDir()
	if err := Write(dir, Build(Input{Config: cfg, Tool: "paircli test"})); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	wantJSON, _ := json.Marshal(cfg)
	gotJSON, _ := json.Marshal(read.Config)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("config changed across the round trip:\n--- want ---\n%s\n--- got ---\n%s",
			wantJSON, gotJSON)
	}
	// The merged defaults must survive too, not just the user's additions.
	if len(read.Config.SensitivePaths) != len(config.Default().SensitivePaths) {
		t.Errorf("default sensitive paths lost: %d, want %d",
			len(read.Config.SensitivePaths), len(config.Default().SensitivePaths))
	}
}

// TestRead_RejectsWrongVersion checks a future schema is refused rather than
// silently misread.
func TestRead_RejectsWrongVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":"99","config":{}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Read(path); err == nil {
		t.Error("Read accepted a snapshot from another schema version")
	}
}

// TestContext_RejectsWrongVersion checks the same for an in-memory snapshot.
func TestContext_RejectsWrongVersion(t *testing.T) {
	s := &Snapshot{SchemaVersion: "99"}
	if _, err := s.Context(); err == nil {
		t.Error("Context accepted a snapshot from another schema version")
	}
}
