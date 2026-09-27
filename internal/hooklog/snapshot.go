package hooklog

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// snapshotGitTimeout bounds each git shellout Snapshot makes.
const snapshotGitTimeout = 2 * time.Second

// snapshotMaxFileBytes skips any dirty file larger than this.
const snapshotMaxFileBytes = 1 << 20 // 1 MB

// snapshotMaxFiles caps how many dirty files (sorted by path) Snapshot considers.
const snapshotMaxFiles = 200

// snapshotSniffBytes is how much of a file's head Snapshot checks for a NUL
// byte to decide whether it is binary.
const snapshotSniffBytes = 8192

// statusEntry is one parsed `git status --porcelain=v1 -z` record.
type statusEntry struct {
	Status string // two-character XY status
	Path   string // new path for renames/copies
}

// Snapshot returns line hashes of the working tree's dirty files in the repo
// containing repoDir: modified, added, untracked, renamed (new path),
// copied, and unmerged files, skipping deletions, files over 1 MB, and files
// with a NUL byte in their first 8 KB. Considers at most 200 files, sorted
// by repo-relative path.
func Snapshot(repoDir string) ([]model.FileLines, error) {
	top, err := runGit(repoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}

	out, err := runGitRaw(top, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}

	paths := dirtyPaths(out)
	sort.Strings(paths)
	if len(paths) > snapshotMaxFiles {
		paths = paths[:snapshotMaxFiles]
	}

	var result []model.FileLines
	for _, p := range paths {
		fl, ok := snapshotFile(top, p)
		if !ok {
			continue
		}
		result = append(result, fl)
	}
	return result, nil
}

// dirtyPaths parses `git status --porcelain=v1 -z` output into the set of
// repo-relative paths worth snapshotting (skips pure deletions).
func dirtyPaths(out []byte) []string {
	tokens := strings.Split(string(out), "\x00")
	seen := map[string]bool{}
	var paths []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if len(tok) < 4 {
			continue
		}
		status := tok[:2]
		path := tok[3:]
		if strings.ContainsAny(status, "RC") {
			// The next token is the rename/copy "from" path; skip it.
			i++
		}
		if !strings.ContainsAny(status, "MARCU?") {
			continue // pure deletion or unrecognized status
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

// snapshotFile reads top/path, skipping it (ok=false) when it is missing,
// over the size limit, or looks binary.
func snapshotFile(top, relPath string) (fl model.FileLines, ok bool) {
	full := filepath.Join(top, filepath.FromSlash(relPath))
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > snapshotMaxFileBytes {
		return model.FileLines{}, false
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return model.FileLines{}, false
	}
	head := data
	if len(head) > snapshotSniffBytes {
		head = head[:snapshotSniffBytes]
	}
	if bytes.IndexByte(head, 0) != -1 {
		return model.FileLines{}, false
	}

	lines := model.SplitLines(string(data))
	hashes := make([]string, len(lines))
	for i, l := range lines {
		hashes[i] = model.LineHash(l)
	}
	return model.FileLines{Path: relPath, Hashes: hashes}, true
}

// runGit runs `git -C dir <args>` with a timeout and returns trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	out, err := runGitRaw(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runGitRaw runs `git -C dir <args>` with a timeout and returns raw stdout.
func runGitRaw(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), snapshotGitTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	return exec.CommandContext(ctx, "git", full...).Output()
}
