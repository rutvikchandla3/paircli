package prtrigger

import (
	"os"
	"path/filepath"
	"strings"
)

// Operation kinds CommandOperation reports.
const (
	OpCreate = "create" // gh pr create
	OpPush   = "push"   // git push that updates the current branch
)

// Operation is a command that may have opened or updated a pull request.
type Operation struct {
	Kind string
	// ExecIndex is the index in Tokens of the `gh` or `git` executable.
	ExecIndex int
	Tokens    []string
}

// separators end a simple command.
var separators = map[string]bool{"&": true, "&&": true, "|": true, "||": true, ";": true, "(": true, ")": true}

// pushFlagsIgnored are `git push` arguments that do not update just the
// current branch, so the PR (if any) did not change.
var pushFlagsIgnored = map[string]bool{"--all": true, "--delete": true, "--mirror": true, "--tags": true, "-d": true}

// CommandOperation finds a `gh pr create` or a `git push` in a shell command.
// A push that deletes, mirrors, pushes all branches or tags, or whose refspec
// starts with ':' is not an operation. `git -C <dir>`, `git -c <key=value>`
// and other leading git options are skipped before the subcommand. The command
// only counts when it is an executable token, so `echo 'git push'` does not.
func CommandOperation(command string) (Operation, bool) {
	tokens := tokenize(command)
	for i, token := range tokens {
		if filepath.Base(token) == "gh" && i+2 < len(tokens) && tokens[i+1] == "pr" && tokens[i+2] == "create" {
			return Operation{Kind: OpCreate, ExecIndex: i, Tokens: tokens}, true
		}
	}
	for i, token := range tokens {
		if filepath.Base(token) != "git" {
			continue
		}
		cursor := i + 1
		for cursor < len(tokens) && !separators[tokens[cursor]] {
			if (tokens[cursor] == "-C" || tokens[cursor] == "-c") && cursor+1 < len(tokens) {
				cursor += 2
				continue
			}
			if strings.HasPrefix(tokens[cursor], "-") {
				cursor++
				continue
			}
			break
		}
		if cursor >= len(tokens) || tokens[cursor] != "push" {
			continue
		}
		if pushIgnored(tokens[cursor+1:]) {
			continue
		}
		return Operation{Kind: OpPush, ExecIndex: i, Tokens: tokens}, true
	}
	return Operation{}, false
}

func pushIgnored(rest []string) bool {
	for _, argument := range rest {
		if separators[argument] {
			return false
		}
		if pushFlagsIgnored[argument] || strings.HasPrefix(argument, ":") {
			return true
		}
	}
	return false
}

// RepoDir is the directory the operation in command ran in: the target of
// `git -C <dir>`, else the directory of the nearest preceding `cd <dir>`, else
// cwd (the process's working directory when cwd is empty). Relative targets
// resolve against cwd.
func RepoDir(cwd, command string) string {
	cwd = expandHome(cwd)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	op, ok := CommandOperation(command)
	if !ok {
		return cwd
	}
	tokens := op.Tokens
	if filepath.Base(tokens[op.ExecIndex]) == "git" {
		for i := op.ExecIndex + 1; i < len(tokens)-1; i++ {
			if tokens[i] == "-C" {
				return resolveDir(cwd, tokens[i+1])
			}
			if tokens[i] == "push" || separators[tokens[i]] {
				break
			}
		}
	}
	for i := op.ExecIndex - 1; i >= 0; i-- {
		if tokens[i] == "cd" && i+1 < op.ExecIndex {
			return resolveDir(cwd, tokens[i+1])
		}
	}
	return cwd
}

func resolveDir(cwd, target string) string {
	target = expandHome(target)
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(cwd, target)
}

// expandHome replaces a leading "~" or "~/" with the home directory.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}
