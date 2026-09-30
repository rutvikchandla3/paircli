// Package scan runs paircli's full PR signal pipeline as a library: fetch the
// PR, discover and link agent sessions, attribute PR lines, run the detectors,
// write the output folder and optionally upsert the PR comment. The CLI
// (cmd/paircli) and the end-to-end tests both call Run.
package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/agenttrace"
	"github.com/rutvikchandla3/paircli/internal/attrib"
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/enrich"
	"github.com/rutvikchandla3/paircli/internal/gitinfo"
	"github.com/rutvikchandla3/paircli/internal/judge"
	"github.com/rutvikchandla3/paircli/internal/link"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/pr"
	"github.com/rutvikchandla3/paircli/internal/render"
	"github.com/rutvikchandla3/paircli/internal/snapshot"
)

// CommentMarker is the HTML comment every paircli PR comment starts with. A
// comment carrying it is updated in place instead of a new one being posted.
const CommentMarker = "<!-- paircli -->"

// Options configures one scan.
type Options struct {
	Repo            string         // "owner/repo"
	Number          int            // PR number
	CWD             string         // where scan was invoked
	OutDir          string         // default <repo root or CWD>/.paircli/pr-<n>
	Runner          pr.Runner      // gh runner; pr.GH{} in the CLI
	Config          *config.Config // nil → config.Load(repo root)
	NoHooks         bool           // skip hook-log records
	NoCommitPatches bool           // skip per-commit patches
	NoAgentTrace    bool           // skip agent-trace.json
	Snapshot        bool           // also write snapshot.json (a full replay record)
	Post            bool           // upsert the PR comment
	IncludePrompts  bool           // let comment.md carry prompt text
	LLM             llm.Provider   // nil = LLM pass off (T26 uses it)
	Now             time.Time      // zero → time.Now()
	Version         string         // written into the report
}

// Result is what one scan produced.
type Result struct {
	Report *model.Report
	OutDir string
	Alerts int // signals whose State is alert
}

// Run runs the whole pipeline for one PR. See docs/plan/README.md
// "Architecture" for the step list this implements.
func Run(ctx context.Context, o Options) (*Result, error) {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}

	// 1. Fetch the PR (metadata, combined diff, per-commit patches).
	p, err := pr.Fetch(o.Runner, o.Repo, o.Number, !o.NoCommitPatches)
	if err != nil {
		return nil, err
	}

	// 2. Repo root and configuration.
	root := RepoRoot(o.CWD, o.Repo)
	cfg := o.Config
	if cfg == nil {
		loaded, err := config.Load(root)
		if err != nil {
			return nil, err
		}
		cfg = loaded
	}
	includePrompts := cfg.Comment.IncludePrompts || o.IncludePrompts
	outDir := o.OutDir
	if outDir == "" {
		base := root
		if base == "" {
			base = o.CWD
		}
		outDir = filepath.Join(base, ".paircli", fmt.Sprintf("pr-%d", o.Number))
	}
	// Create it now rather than leaving it to render.Write: the snapshot is
	// written into outDir earlier in the pipeline, and on a first scan the
	// folder does not exist yet.
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("scan: create output folder %s: %w", outDir, err)
	}

	// 3. Discover sessions inside the PR's time window.
	w := link.WindowFor(p, cfg)
	all, err := link.Discover(cfg, w, !o.NoHooks)
	if err != nil {
		return nil, err
	}

	// 4. Keep the plausible candidates.
	cands, dropped := link.Select(p, all, w)

	// 5. Attribute PR lines to those candidates, and map commits to sessions.
	attr := attrib.Attribute(p, cands, cfg)
	cs := attrib.CommitSessions(p, cands)

	// 6. Decide which candidates are actually linked to this PR.
	res := link.Finalize(p, cands, attr, cs, dropped)

	// 7. Attribution over the linked sessions only.
	attr = attrib.Attribute(p, res.Sessions, cfg)

	// 8. Detector context.
	ec := engine.NewContext(p, res.Sessions, attr, res.Links, cfg)
	ec.SessionLinks = res.SessionLinks

	// 9. Deterministic detectors.
	sigs := engine.Run(ec)

	// The snapshot records everything the deterministic pass used. It is taken
	// here, before the LLM pass, so it is a complete replay input: judging can
	// be redone from it later, or on another machine. Judgments is therefore
	// nil in a snapshot a scan writes.
	if o.Snapshot {
		if err := snapshot.Write(outDir, snapshot.Build(snapshot.Input{
			PR:           p,
			Sessions:     res.Sessions,
			Attribution:  attr,
			Links:        res.Links,
			SessionLinks: res.SessionLinks,
			Dropped:      res.Dropped,
			Unattributed: res.Unattributed,
			Config:       cfg,
			Tool:         "paircli " + o.Version,
		})); err != nil {
			return nil, err
		}
	}

	// T26: LLM pass. Without a provider the three inferred signals (DEC-3,
	// CON-1, CON-3) stay unknown, and the report lists them under "Not
	// available" with the hint to run with --llm. With one, the judge turns the
	// deterministic signals and the session facts into model.Judgments, the
	// inferred signals are recomputed from it, and the judgments are folded
	// back into the other signals.
	if o.LLM != nil {
		j, stats, err := judge.RunFull(ctx, o.LLM, ec, sigs, cfg)
		if err != nil {
			// Keep the deterministic output and note the failure on AUTH-2.
			sigs = noteLLMError(sigs, []string{err.Error()})
			j, stats = nil, nil
		} else if j != nil && len(j.Errors) > 0 {
			// Some jobs failed while others answered. The signals those jobs
			// feed are marked unknown below; record the errors here too, so the
			// report footer says the pass was incomplete rather than clean.
			sigs = noteLLMError(sigs, j.Errors)
		}
		ec.Judgments = j
		if j != nil {
			sigs = replaceByID(sigs, engine.RunIDs(ec, inferredIDs...))
			sigs = markUnresolved(sigs, stats)
			sigs = enrich.Apply(ec, sigs)
		}
	}

	// 10. Report and files.
	rep := render.BuildReport(render.BuildInput{
		PR:           p,
		Sessions:     res.Sessions,
		Links:        res.Links,
		SessionLinks: res.SessionLinks,
		Attribution:  attr,
		Signals:      sigs,
		Dropped:      res.Dropped,
		Unattributed: res.Unattributed,
		Version:      o.Version,
		Now:          now,
	})
	if err := render.Write(render.Options{
		OutDir:          outDir,
		IncludePrompts:  includePrompts,
		MaxCommentLines: cfg.Comment.MaxLines,
	}, rep, attr, res.Sessions); err != nil {
		return nil, err
	}

	// 11. Agent Trace record (T27): the same attribution in the vendor-neutral
	// format other tools read (https://agent-trace.dev/).
	if !o.NoAgentTrace {
		if err := agenttrace.WriteWith(outDir, agenttrace.Options{GeneratedAt: now, Version: o.Version}, p, attr, res.Sessions); err != nil {
			return nil, err
		}
	}

	// 12. Optionally upsert the PR comment.
	if o.Post {
		if err := postComment(o.Runner, o.Repo, o.Number, filepath.Join(outDir, "comment.md")); err != nil {
			return nil, err
		}
	}

	return &Result{Report: rep, OutDir: outDir, Alerts: alertCount(sigs)}, nil
}

// inferredIDs are the three signals only the LLM pass produces (T26).
var inferredIDs = []string{"DEC-3", "CON-1", "CON-3"}

// replaceByID returns sigs with every signal in repl swapped in for the signal
// of the same id, in place; a signal sigs does not carry is appended.
func replaceByID(sigs []model.Signal, repl []model.Signal) []model.Signal {
	for _, r := range repl {
		done := false
		for i := range sigs {
			if sigs[i].ID == r.ID {
				sigs[i] = r
				done = true
				break
			}
		}
		if !done {
			sigs = append(sigs, r)
		}
	}
	return sigs
}

// noteLLMError records a failed LLM pass on AUTH-2's Data, the signal the
// report footer is built from, and returns sigs unchanged otherwise.
func noteLLMError(sigs []model.Signal, msgs []string) []model.Signal {
	if len(msgs) == 0 {
		return sigs
	}
	for i := range sigs {
		if sigs[i].ID != "AUTH-2" {
			continue
		}
		data := make(map[string]any, len(sigs[i].Data)+1)
		for k, v := range sigs[i].Data {
			data[k] = v
		}
		data["llm_errors"] = msgs
		sigs[i].Data = data
		break
	}
	return sigs
}

// markUnresolved replaces each inferred signal whose judgment input was
// unusable with an unknown signal. Without it, a job that failed or whose items
// were all dropped by validation leaves the signal reporting clear — which
// reads as "checked, nothing found" when the truth is "nothing was checked".
//
// Only the inferred signals are touched. Every other signal carries a
// deterministic measurement that an unusable judgment result must not
// overwrite, and marking those unknown would throw away real evidence.
func markUnresolved(sigs []model.Signal, stats *judge.Stats) []model.Signal {
	for _, u := range stats.Unresolved() {
		for i := range sigs {
			if sigs[i].ID != u.Signal {
				continue
			}
			sigs[i] = engine.Unknown(u.Signal, u.Reason)
			if u.Attempted > 0 {
				sigs[i].Data = map[string]any{"llm_dropped": u.Attempted}
			}
			break
		}
	}
	return sigs
}

// RepoRoot returns the git repository root containing cwd, but only when that
// repository's origin remote is repo (case-insensitive); otherwise "" — the
// caller then falls back to cwd and to the default configuration. repo may be
// "" to resolve the root without checking the remote.
func RepoRoot(cwd, repo string) string {
	if cwd == "" {
		return ""
	}
	root, err := gitinfo.TopLevel(cwd)
	if err != nil || root == "" {
		return ""
	}
	if repo == "" {
		return root
	}
	info, err := gitinfo.Resolve(cwd)
	if err != nil || info.OwnerRepo == "" {
		return ""
	}
	if !strings.EqualFold(info.OwnerRepo, repo) {
		return ""
	}
	return root
}

// alertCount counts signals a reviewer should look at.
func alertCount(sigs []model.Signal) int {
	n := 0
	for _, s := range sigs {
		if s.State == model.StateAlert {
			n++
		}
	}
	return n
}

// postComment upserts the paircli comment on a PR: it lists the issue's
// comments and, when one already carries CommentMarker, PATCHes that comment
// with the new body; otherwise it posts a new comment.
func postComment(r pr.Runner, repo string, number int, bodyPath string) error {
	if r == nil {
		return fmt.Errorf("scan: post: no gh runner configured")
	}
	if _, err := os.Stat(bodyPath); err != nil {
		return fmt.Errorf("scan: post: %w", err)
	}

	id, found, err := findMarkerComment(r, repo, number)
	if err != nil {
		return err
	}
	if found {
		_, err := r.Run("api", "-X", "PATCH",
			fmt.Sprintf("repos/%s/issues/comments/%d", repo, id), "-F", "body=@"+bodyPath)
		if err != nil {
			return fmt.Errorf("scan: post: updating comment %d: %w", id, err)
		}
		return nil
	}

	if _, err := r.Run("pr", "comment", strconv.Itoa(number), "-R", repo, "--body-file", bodyPath); err != nil {
		return fmt.Errorf("scan: post: creating comment: %w", err)
	}
	return nil
}

// findMarkerComment returns the id of the first issue comment whose body
// starts with CommentMarker. gh api --paginate concatenates one JSON array
// per page, so the response is decoded array by array.
func findMarkerComment(r pr.Runner, repo string, number int) (int64, bool, error) {
	out, err := r.Run("api", fmt.Sprintf("repos/%s/issues/%d/comments", repo, number), "--paginate")
	if err != nil {
		return 0, false, fmt.Errorf("scan: post: listing comments: %w", err)
	}
	type comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var page []comment
		if err := dec.Decode(&page); err != nil {
			if err == io.EOF {
				break
			}
			return 0, false, fmt.Errorf("scan: post: parsing comment list: %w", err)
		}
		for _, c := range page {
			if strings.HasPrefix(strings.TrimSpace(c.Body), CommentMarker) {
				return c.ID, true, nil
			}
		}
	}
	return 0, false, nil
}
