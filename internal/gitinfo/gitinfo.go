// Package gitinfo parses .git/HEAD and .git/config directly (no git binary
// dependency) to resolve the current branch and remote owner/repo, the same
// technique agent-beacon's git.go uses. It is worktree-aware via
// commondir. A separate shellout helper (sha.go) captures exact commit SHAs
// at hook time only — that is a deliberate, narrow use of the git binary.
package gitinfo

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Info is the repo identity resolved from local git metadata.
type Info struct {
	Branch     string // current branch name, "" if detached HEAD
	RemoteURL  string // raw remote "origin" URL, "" if none configured
	OwnerRepo  string // "owner/repo" parsed from RemoteURL, "" if unparseable
	GitDir     string // resolved git dir (commondir-resolved for worktrees)
	WorkingDir string // the .git (or worktree) dir we started from
}

// Resolve walks up from startDir to find a .git entry, resolves worktrees
// via commondir, and parses HEAD + config.
func Resolve(startDir string) (*Info, error) {
	gitDir, err := findGitDir(startDir)
	if err != nil {
		return nil, err
	}

	commonDir, err := resolveCommonDir(gitDir)
	if err != nil {
		return nil, err
	}

	info := &Info{GitDir: commonDir, WorkingDir: gitDir}

	branch, err := parseHEAD(filepath.Join(gitDir, "HEAD"))
	if err == nil {
		info.Branch = branch
	}

	remoteURL, err := parseOriginURL(filepath.Join(commonDir, "config"))
	if err == nil {
		info.RemoteURL = remoteURL
		info.OwnerRepo = ownerRepoFromURL(remoteURL)
	}

	return info, nil
}

// findGitDir walks up from dir looking for a .git file or directory.
func findGitDir(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, ".git")
		if fi, err := os.Stat(candidate); err == nil {
			if fi.IsDir() {
				return candidate, nil
			}
			// .git file: worktree or submodule, contains "gitdir: <path>".
			return resolveGitFile(candidate, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("gitinfo: no .git found")
		}
		dir = parent
	}
}

func resolveGitFile(gitFile, baseDir string) (string, error) {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", errors.New("gitinfo: unrecognized .git file format")
	}
	path := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	return filepath.Clean(path), nil
}

// resolveCommonDir handles worktrees: a worktree's gitdir (e.g.
// .git/worktrees/<name>) contains a "commondir" file pointing back at the
// main repo's .git dir, which is where config/refs actually live.
func resolveCommonDir(gitDir string) (string, error) {
	commonDirFile := filepath.Join(gitDir, "commondir")
	data, err := os.ReadFile(commonDirFile)
	if err != nil {
		if os.IsNotExist(err) {
			return gitDir, nil
		}
		return "", err
	}
	rel := strings.TrimSpace(string(data))
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel), nil
	}
	return filepath.Clean(filepath.Join(gitDir, rel)), nil
}

// parseHEAD reads .git/HEAD, which is either "ref: refs/heads/<branch>" or
// a raw SHA (detached HEAD).
func parseHEAD(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(data))
	const prefix = "ref: refs/heads/"
	if strings.HasPrefix(line, prefix) {
		return strings.TrimPrefix(line, prefix), nil
	}
	// Detached HEAD: no branch name available.
	return "", nil
}

var originURLRe = regexp.MustCompile(`^\s*url\s*=\s*(.+)$`)

// parseOriginURL scans .git/config for the [remote "origin"] section's url.
func parseOriginURL(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inOrigin := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if inOrigin {
			if m := originURLRe.FindStringSubmatch(line); m != nil {
				return strings.TrimSpace(m[1]), nil
			}
		}
	}
	return "", errors.New("gitinfo: no origin url found")
}

// ownerRepoFromURL parses "owner/repo" out of common git remote URL forms:
// git@github.com:owner/repo.git, https://github.com/owner/repo.git, etc.
func ownerRepoFromURL(url string) string {
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	if idx := strings.Index(url, "github.com:"); idx != -1 {
		return url[idx+len("github.com:"):]
	}
	if idx := strings.Index(url, "github.com/"); idx != -1 {
		return url[idx+len("github.com/"):]
	}
	return ""
}
