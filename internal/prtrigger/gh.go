package prtrigger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// resolveTimeout bounds `gh pr view`: the hook runs inside the agent's tool
// loop, so it must not wait on a slow network.
var resolveTimeout = 3 * time.Second

// ResolveCurrentBranchPR returns the open pull request for the branch checked
// out in dir, using `gh pr view`. It fails when gh is missing, dir is not a
// directory, the branch has no PR, or gh takes longer than three seconds.
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
		return PR{}, fmt.Errorf("gh pr view: %w", err)
	}
	pr, ok := singlePR(string(out))
	if !ok {
		return PR{}, errors.New("gh pr view did not print exactly one pull request URL")
	}
	return pr, nil
}
