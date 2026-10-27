// Package pr fetches a GitHub pull request's metadata, combined diff and
// (optionally) per-commit patches via the `gh` CLI.
package pr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	HeadRefOid  string          `json:"headRefOid"`
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
		"--json", "number,url,title,body,headRefName,headRefOid,baseRefName,createdAt,commits")
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
		HeadSHA: strings.ToLower(strings.TrimSpace(raw.HeadRefOid)),
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

	files, source, err := fetchFiles(r, repo, number)
	if err != nil {
		return nil, err
	}
	prv.Files, prv.FilesSource = files, source

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

// fetchFiles returns the PR's changed files from its combined diff, and which
// of the two endpoints they came from. GitHub refuses to render a combined diff
// over 20,000 lines (HTTP 406, too_large); for those PRs the files come from
// the paginated pulls files API instead, one patch per file.
func fetchFiles(r Runner, repo string, number int) ([]model.DiffFile, string, error) {
	diffOut, err := r.Run("pr", "diff", strconv.Itoa(number), "-R", repo)
	if err != nil {
		if !isDiffTooLarge(err) {
			return nil, "", fmt.Errorf("pr: gh pr diff: %w", err)
		}
		files, filesErr := fetchFilesFromAPI(r, repo, number)
		if filesErr != nil {
			return nil, "", fmt.Errorf("pr: gh pr diff: %w; falling back to the files API: %w", err, filesErr)
		}
		return files, model.SourceAPI, nil
	}
	files, err := diff.Parse(string(diffOut))
	if err != nil {
		return nil, "", fmt.Errorf("pr: parsing pr diff: %w", err)
	}
	return files, model.SourceDiff, nil
}

// isDiffTooLarge reports whether gh failed because GitHub would not render
// the combined diff.
func isDiffTooLarge(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "too_large") || strings.Contains(msg, "HTTP 406") ||
		strings.Contains(msg, "exceeded the maximum number of lines")
}

// fetchFilesFromAPI lists a PR's files through `gh api --paginate`. GitHub
// returns at most 3,000 files and omits the patch of a very large file; such
// a file is kept, without hunks, as commit patches keep a patchless file.
func fetchFilesFromAPI(r Runner, repo string, number int) ([]model.DiffFile, error) {
	out, err := r.Run("api", "--paginate", fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", repo, number))
	if err != nil {
		return nil, fmt.Errorf("gh api pull files: %w", err)
	}
	// --paginate writes each page's JSON array back to back.
	dec := json.NewDecoder(bytes.NewReader(out))
	var files []model.DiffFile
	for {
		var page []ghCommitFile
		if err := dec.Decode(&page); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("parsing gh api pull files: %w", err)
		}
		for _, f := range page {
			files = append(files, commitFileToDiffFile(f))
		}
	}
	return files, nil
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

var headSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// HeadSHA returns the PR's current head commit (headRefOid) with one cheap
// `gh` call, without fetching the diff or commits.
func HeadSHA(r Runner, repo string, number int) (string, error) {
	out, err := r.Run("pr", "view", strconv.Itoa(number), "-R", repo, "--json", "headRefOid", "--jq", ".headRefOid")
	if err != nil {
		return "", fmt.Errorf("pr: gh pr view: %w", err)
	}
	sha := strings.ToLower(strings.TrimSpace(string(out)))
	if !headSHARe.MatchString(sha) {
		return "", fmt.Errorf("pr: GitHub returned an invalid head %q", sha)
	}
	return sha, nil
}
