// Package pr fetches a GitHub pull request's metadata, combined diff and
// (optionally) per-commit patches via the `gh` CLI.
package pr

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/diff"
	"github.com/rutvikchandla3/paircli/internal/gitinfo"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// Runner runs `gh` with the given arguments and returns its stdout.
type Runner interface {
	Run(args ...string) ([]byte, error)
}

// GH is the real Runner, shelling out to the gh binary.
type GH struct{}

// Run executes `gh <args...>`, including stderr in any error.
func (GH) Run(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// maxCommitPatches is the most per-commit patches Fetch will retrieve.
const maxCommitPatches = 100

type ghCommitEntry struct {
	OID             string `json:"oid"`
	MessageHeadline string `json:"messageHeadline"`
	MessageBody     string `json:"messageBody"`
	CommittedDate   string `json:"committedDate"`
}

type ghPRView struct {
	Number      int             `json:"number"`
	URL         string          `json:"url"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	HeadRefName string          `json:"headRefName"`
	BaseRefName string          `json:"baseRefName"`
	CreatedAt   string          `json:"createdAt"`
	Commits     []ghCommitEntry `json:"commits"`
}

type ghCommitFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Patch            string `json:"patch"`
}

type ghCommitResp struct {
	Files []ghCommitFile `json:"files"`
}

var apiStatusMap = map[string]model.FileStatus{
	"added":    model.StatusAdded,
	"removed":  model.StatusDeleted,
	"modified": model.StatusModified,
	"renamed":  model.StatusRenamed,
}

// Fetch fetches PR metadata and its combined diff via gh, and, when
// withCommitPatches is true, each commit's own patch (up to
// maxCommitPatches commits).
func Fetch(r Runner, repo string, number int, withCommitPatches bool) (*model.PR, error) {
	out, err := r.Run("pr", "view", strconv.Itoa(number), "-R", repo,
		"--json", "number,url,title,body,headRefName,baseRefName,createdAt,commits")
	if err != nil {
		return nil, fmt.Errorf("pr: gh pr view: %w", err)
	}
	var raw ghPRView
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("pr: parsing gh pr view output: %w", err)
	}

	prv := &model.PR{
		Repo:    repo,
		Number:  raw.Number,
		URL:     raw.URL,
		Title:   raw.Title,
		Body:    raw.Body,
		HeadRef: raw.HeadRefName,
		BaseRef: raw.BaseRefName,
	}
	if t, err := time.Parse(time.RFC3339, raw.CreatedAt); err == nil {
		prv.CreatedAt = t.UTC()
	}
	for _, c := range raw.Commits {
		msg := c.MessageHeadline
		if body := strings.TrimSpace(c.MessageBody); body != "" {
			msg = msg + "\n\n" + body
		}
		msg = strings.TrimSpace(msg)
		var ct time.Time
		if t, err := time.Parse(time.RFC3339, c.CommittedDate); err == nil {
			ct = t.UTC()
		}
		prv.Commits = append(prv.Commits, model.Commit{SHA: c.OID, Time: ct, Message: msg})
	}

	diffOut, err := r.Run("pr", "diff", strconv.Itoa(number), "-R", repo)
	if err != nil {
		return nil, fmt.Errorf("pr: gh pr diff: %w", err)
	}
	files, err := diff.Parse(string(diffOut))
	if err != nil {
		return nil, fmt.Errorf("pr: parsing pr diff: %w", err)
	}
	prv.Files = files

	if withCommitPatches {
		n := len(prv.Commits)
		if n > maxCommitPatches {
			n = maxCommitPatches
		}
		for i := 0; i < n; i++ {
			sha := prv.Commits[i].SHA
			apiOut, err := r.Run("api", fmt.Sprintf("repos/%s/commits/%s", repo, sha))
			if err != nil {
				return nil, fmt.Errorf("pr: gh api commit %s: %w", sha, err)
			}
			var cr ghCommitResp
			if err := json.Unmarshal(apiOut, &cr); err != nil {
				return nil, fmt.Errorf("pr: parsing gh api commit %s: %w", sha, err)
			}
			var cfiles []model.DiffFile
			for _, f := range cr.Files {
				cfiles = append(cfiles, commitFileToDiffFile(f))
			}
			prv.Commits[i].Files = cfiles
		}
	}

	return prv, nil
}

// commitFileToDiffFile converts one gh api commit file entry into a
// model.DiffFile, parsing its patch (if any) for hunks.
func commitFileToDiffFile(f ghCommitFile) model.DiffFile {
	status, ok := apiStatusMap[f.Status]
	if !ok {
		status = model.StatusModified
	}

	df := model.DiffFile{Path: f.Filename, Status: status}
	if status == model.StatusRenamed {
		df.OldPath = f.PreviousFilename
	}

	if f.Patch == "" {
		df.Binary = true
		return df
	}

	oldPath := f.Filename
	if f.PreviousFilename != "" {
		oldPath = f.PreviousFilename
	}
	wrapped := wrapPatch(oldPath, f.Filename, f.Patch)
	parsed, err := diff.Parse(wrapped)
	if err == nil && len(parsed) > 0 {
		df.Hunks = parsed[0].Hunks
	}
	return df
}

// wrapPatch wraps a gh api commit file's raw patch (hunks only) in a
// minimal unified-diff header so diff.Parse can extract hunks.
func wrapPatch(oldPath, newPath, patch string) string {
	patch = strings.TrimRight(patch, "\n")
	return fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n%s\n", oldPath, newPath, oldPath, newPath, patch)
}

var prURLRe = regexp.MustCompile(`github\.com/([^/]+/[^/]+)/pull/(\d+)`)

// ResolveArg accepts either a bare PR number (uses the current repo,
// resolved via gitinfo) or a full GitHub PR URL.
func ResolveArg(cwd, arg string) (repo string, number int, err error) {
	if m := prURLRe.FindStringSubmatch(arg); m != nil {
		n, convErr := strconv.Atoi(m[2])
		if convErr != nil {
			return "", 0, convErr
		}
		return m[1], n, nil
	}

	n, convErr := strconv.Atoi(arg)
	if convErr != nil {
		return "", 0, fmt.Errorf("pr: could not parse %q as a PR number or GitHub PR URL", arg)
	}

	info, err := gitinfo.Resolve(cwd)
	if err != nil {
		return "", 0, fmt.Errorf("pr: resolving current repo: %w", err)
	}
	if info.OwnerRepo == "" {
		return "", 0, fmt.Errorf("pr: could not determine owner/repo from git remote; pass a full PR URL instead")
	}
	return info.OwnerRepo, n, nil
}
