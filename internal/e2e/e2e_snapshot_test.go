package e2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/scan"
	"github.com/rutvikchandla3/paircli/internal/snapshot"
)

// TestE2E_SnapshotRoundTrip proves the replay artifact against the full
// scenario: a scan that writes snapshot.json, read back from disk and rebuilt
// into a context, reproduces the very signals the scan reported. That is the
// property the whole artifact exists for — it is what lets the grounded LLM
// pass run later, or on another machine, instead of during the scan.
func TestE2E_SnapshotRoundTrip(t *testing.T) {
	w := newWorld(t)
	w.build()
	t.Setenv("PAIRCLI_EVENTS_DIR", w.hookDir)

	res, err := scan.Run(context.Background(), scan.Options{
		Repo:     e2eRepo,
		Number:   e2eNumber,
		CWD:      w.repoDir,
		OutDir:   w.outDir,
		Runner:   w.runner,
		Config:   w.cfg,
		Snapshot: true,
		Now:      e2eAt(e2eNowHHMM),
		Version:  e2eVersion,
	})
	if err != nil {
		t.Fatalf("e2e: scan.Run with --snapshot: %v", err)
	}

	read, err := snapshot.Read(filepath.Join(w.outDir, snapshot.FileName))
	if err != nil {
		t.Fatalf("e2e: reading the snapshot: %v", err)
	}
	if got, want := read.Tool, "paircli "+e2eVersion; got != want {
		t.Errorf("snapshot tool = %q, want %q", got, want)
	}
	if read.Hash == "" {
		t.Error("snapshot carries no hash")
	}
	if got, want := len(read.Sessions), len(res.Report.Sessions); got != want {
		t.Errorf("snapshot sessions = %d, want %d", got, want)
	}
	if read.Config == nil {
		t.Fatal("snapshot carries no config")
	}

	ec, err := read.Context()
	if err != nil {
		t.Fatalf("e2e: rebuilding the context: %v", err)
	}
	restored := engine.Run(ec)

	wantJSON, err := json.Marshal(res.Report.Signals)
	if err != nil {
		t.Fatalf("e2e: marshalling the reported signals: %v", err)
	}
	gotJSON, err := json.Marshal(restored)
	if err != nil {
		t.Fatalf("e2e: marshalling the replayed signals: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("replayed signals differ from the scan's own report:\n--- want ---\n%s\n--- got ---\n%s",
			wantJSON, gotJSON)
	}
}
