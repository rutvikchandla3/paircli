package prtrigger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// resolveTimeout bounds `gh pr view`: the hook runs inside the agent's tool
// loop, so it must not wait on a slow network.
var resolveTimeout = 3 * time.Second

// ResolveCurrentBranchPR returns the open pull request for the branch checked
// out in dir, using `gh pr view`. It fails when gh is missing, dir is not a
// directory, the branch has no PR, or gh takes longer than three seconds; the
// error carries the first line gh wrote to stderr.
func ResolveCurrentBranchPR(dir string) (PR, error) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		return PR{}, errors.New("gh not found")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return PR{}, fmt.Errorf("%s is not a directory", dir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "pr", "view", "--json", "url", "--jq", ".url")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second // stdin is nil, i.e. /dev/null
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return PR{}, fmt.Errorf("gh pr view: no answer within %s", resolveTimeout)
		case errors.As(err, &exit) && firstLine(exit.Stderr) != "":
			// gh says why: `no pull requests found for branch "x"`, `not a git repository` …
			return PR{}, fmt.Errorf("gh pr view: %s", firstLine(exit.Stderr))
		}
		return PR{}, fmt.Errorf("gh pr view: %w", err)
	}
	pr, ok := singlePR(string(out))
	if !ok {
		return PR{}, errors.New("gh pr view did not print exactly one pull request URL")
	}
	return pr, nil
}

// firstLine is the first non-blank line of out, trimmed and clipped to one log
// line's worth.
func firstLine(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return model.Clip(line, 200)
		}
	}
	return ""
}
