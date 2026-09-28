package gitinfo

import (
	"errors"
	"os"
	"path/filepath"
)

// TopLevel walks up from dir to the nearest ancestor directory containing a
// .git entry (file or directory, as with a worktree) and returns that
// ancestor directory. It returns an error if no such ancestor exists.
func TopLevel(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("gitinfo: no .git found above " + dir)
		}
		dir = parent
	}
}

// OwnerRepoFromURL parses "owner/repo" out of common git remote URL forms
// (git@github.com:owner/repo.git, https://github.com/owner/repo.git, etc).
// It is an exported wrapper around ownerRepoFromURL for use outside this
// package.
func OwnerRepoFromURL(url string) string { return ownerRepoFromURL(url) }
