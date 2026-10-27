// Package prtrigger turns a PostToolUse hook event into a background `paircli
// publish`: it recognises a successful `gh pr create` or `git push` in a shell
// command, finds the pull request it touched, starts the publish detached and
// builds the one JSON object the hook prints for the agent.
package prtrigger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rutvikchandla3/paircli/internal/state"
)

// Output is the JSON object a PostToolUse hook prints: a message for the user
// and the same fact as context for the agent.
type Output struct {
	SystemMessage      string             `json:"systemMessage"`
	HookSpecificOutput HookSpecificOutput `json:"hookSpecificOutput"`
}

// HookSpecificOutput is the PostToolUse part of Output.
type HookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// Runner is a PostToolUse trigger. Its fields are the only side effects, so
// tests can supply fakes.
type Runner struct {
	// Resolve returns the open PR for the branch checked out in a directory.
	Resolve func(dir string) (PR, error)
	// Start launches `paircli publish` for a PR detached from this process.
	Start func(pr PR, dir string) error
}

// Default is the Runner the installed hooks use.
var Default = Runner{Resolve: ResolveCurrentBranchPR, Start: StartPublish}

// Handle starts a publish when ev is a successful `gh pr create` or `git push`
// on a PR branch. It returns nil when there is nothing to do.
func (r Runner) Handle(ev Event) (*Output, error) {
	if _, ok := CommandOperation(ev.Command); !ok {
		return nil, nil
	}
	dir, err := filepath.Abs(RepoDir(ev.CWD, ev.Command))
	if err != nil {
		return nil, err
	}
	pr, ok := PRFromEvent(ev, dir, r.Resolve)
	if !ok {
		return nil, nil
	}
	if err := r.Start(pr, dir); err != nil {
		return nil, fmt.Errorf("start publish for %s: %w", pr.URL(), err)
	}
	url := pr.ReviewURL()
	return &Output{
		SystemMessage: "Pair is publishing this PR's review in the background. Pair review: " + url,
		HookSpecificOutput: HookSpecificOutput{
			HookEventName: "PostToolUse",
			AdditionalContext: "Pair is publishing the review for this pull request in the background. " +
				"Pair review: " + url + ". Share this link with the user.",
		},
	}, nil
}

// Run is Handle plus printing: the Output, if any, is written to w as one line
// of JSON. Nothing is written when there is nothing to do or Handle fails.
func (r Runner) Run(ev Event, w io.Writer) error {
	out, err := r.Handle(ev)
	if err != nil || out == nil {
		return err
	}
	return json.NewEncoder(w).Encode(out)
}

// StartPublish runs `paircli publish <pr-url> --repo-dir <dir>` in a new
// session so it outlives the hook, with stdout and stderr going to a
// timestamped log under state.LogDir().
func StartPublish(pr PR, dir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return startDetached(exe, pr, dir)
}

// startDetached is StartPublish for an explicit paircli binary.
func startDetached(exe string, pr PR, dir string) error {
	logs := state.LogDir()
	if logs == "" {
		return fmt.Errorf("no log directory")
	}
	if err := os.MkdirAll(logs, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("publish-%s-%s.log", state.Key(pr.Owner, pr.Repo, pr.Number), time.Now().Format("20060102-150405"))
	log, err := os.OpenFile(filepath.Join(logs, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()

	cmd := exec.Command(exe, "publish", pr.URL(), "--repo-dir", dir)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
