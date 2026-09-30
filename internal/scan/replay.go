package scan

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rutvikchandla3/paircli/internal/agenttrace"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/judge"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/render"
	"github.com/rutvikchandla3/paircli/internal/snapshot"
)

// ReplayOptions configures one replay of a snapshot.
type ReplayOptions struct {
	// Snapshot is the record to replay, read with snapshot.Read.
	Snapshot *snapshot.Snapshot

	// OutDir receives the report files, and is created when missing. A scan's
	// own output folder is the usual value: the replay overwrites the
	// deterministic report with the judged one, in place.
	OutDir string

	// LLM is the grounded judge provider. A nil provider skips the LLM pass,
	// and the replay then reproduces the snapshot's deterministic report.
	LLM llm.Provider

	// NoAgentTrace skips agent-trace.json.
	NoAgentTrace bool

	// Force replays a snapshot a different paircli version wrote; see Version.
	Force bool

	// Now stamps the report. Zero means time.Now().
	Now time.Time

	// Version is the running binary's bare version, written into the report and
	// compared against the snapshot's Tool. The snapshot pins the detector code
	// that produced it, so a mismatch means the replayed signals may not be the
	// ones the scan reported.
	Version string
}

// Replay re-runs a scan's signal pipeline from its snapshot instead of from the
// PR: it rebuilds the context, re-runs the deterministic detectors (cheap, and
// a pure function of the snapshot), runs the grounded judge over them, merges
// the two exactly as Run does, and writes the same output folder.
//
// Nothing here reads the network, the repository or the local session stores,
// so a replay can run later, or on another machine, against the frozen input
// the snapshot is.
func Replay(ctx context.Context, o ReplayOptions) (*Result, error) {
	snap := o.Snapshot
	switch {
	case snap == nil:
		return nil, fmt.Errorf("replay: no snapshot")
	case o.OutDir == "":
		return nil, fmt.Errorf("replay: no output folder")
	}
	if err := checkTool(snap.Tool, o.Version, o.Force); err != nil {
		return nil, err
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}

	ec, err := snap.Context()
	if err != nil {
		return nil, err
	}
	cfg := ec.Config

	sigs := engine.Run(ec)
	var llmErrors []string
	switch {
	case o.LLM != nil:
		j, stats, err := judge.RunFull(ctx, o.LLM, ec, sigs, cfg)
		sigs = Merge(ec, sigs, j, stats, err)
		llmErrors = PassErrors(j, err)
	case snap.Judgments != nil:
		// The snapshot already carries a judgment result: fold it in as though
		// the pass had just produced it. A stored result carries no drop
		// accounting, so one whose items validation dropped cannot be
		// recognized as incomplete — it is merged on trust.
		sigs = Merge(ec, sigs, snap.Judgments, nil, nil)
	}

	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("replay: create output folder %s: %w", o.OutDir, err)
	}
	rep := render.BuildReport(render.BuildInput{
		PR:           ec.PR,
		Sessions:     ec.Sessions,
		Links:        ec.Links,
		SessionLinks: ec.SessionLinks,
		Attribution:  ec.Attribution,
		Signals:      sigs,
		Dropped:      snap.Dropped,
		Unattributed: snap.Unattributed,
		Version:      o.Version,
		Now:          now,
	})
	if err := render.Write(render.Options{
		OutDir:          o.OutDir,
		IncludePrompts:  cfg.Comment.IncludePrompts,
		MaxCommentLines: cfg.Comment.MaxLines,
	}, rep, ec.Attribution, ec.Sessions); err != nil {
		return nil, err
	}
	if !o.NoAgentTrace {
		if err := agenttrace.WriteWith(o.OutDir,
			agenttrace.Options{GeneratedAt: now, Version: o.Version},
			ec.PR, ec.Attribution, ec.Sessions); err != nil {
			return nil, err
		}
	}
	return &Result{Report: rep, OutDir: o.OutDir, Alerts: alertCount(sigs), LLMErrors: llmErrors}, nil
}

// checkTool refuses to replay a snapshot another paircli wrote. The snapshot
// records the PR, the sessions and the attribution, but not the signals: those
// are recomputed by the detector code in this binary, so a version mismatch can
// silently produce a different signal set from the one the scan reported.
func checkTool(tool, version string, force bool) error {
	want := "paircli " + version
	if force || tool == want {
		return nil
	}
	if tool == "" {
		tool = "(unrecorded)"
	}
	return fmt.Errorf("replay: snapshot was written by %s, this is %s: "+
		"signals are recomputed by the detectors in this binary, so they may not "+
		"match the ones that snapshot recorded — pass --force to replay it anyway",
		tool, want)
}
