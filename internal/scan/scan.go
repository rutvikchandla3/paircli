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

	"github.com/rutvikchandla3/paircli/internal/attrib"
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/gitinfo"
	"github.com/rutvikchandla3/paircli/internal/link"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/pr"
	"github.com/rutvikchandla3/paircli/internal/render"
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

	// T26: LLM pass. When o.LLM is non-nil, T26 replaces this comment with
	// judge.Run(ctx, o.LLM, ec, sigs, cfg) → ec.Judgments,
	// engine.RunIDs(ec, "DEC-3", "CON-1", "CON-3") and enrich.Apply(ec, sigs).
	// Nothing here until then: the LLM pass is off unless configured.
	_ = ctx
	_ = o.LLM

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

	// 11. Optionally upsert the PR comment.
	if o.Post {
		if err := postComment(o.Runner, o.Repo, o.Number, filepath.Join(outDir, "comment.md")); err != nil {
			return nil, err
		}
	}

	return &Result{Report: rep, OutDir: outDir, Alerts: alertCount(sigs)}, nil
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
