package gitinfo

import (
	"os/exec"
	"strings"
)

// CurrentSHA shells out to `git rev-parse HEAD` in dir. This is a
// deliberate, narrow use of the git binary: it's only invoked at hook time
// (session-end / post-commit) to capture an exact SHA, never during normal
// branch/remote resolution (see Resolve, which avoids the git binary
// entirely).
func CurrentSHA(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
