package prtrigger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommandOperation(t *testing.T) {
	tests := []struct {
		command string
		want    string // OpCreate, OpPush or "" for no operation
	}{
		{"gh pr create --fill --base main", OpCreate},
		{"git push -u origin HEAD && gh pr create --fill --base main", OpCreate},
		{"/opt/homebrew/bin/gh pr create --draft", OpCreate},
		{"cd /repo && gh pr create --fill", OpCreate},
		{"git push", OpPush},
		{"git push origin HEAD", OpPush},
		{"git push -u origin feat", OpPush},
		{"git push --force-with-lease origin feat", OpPush},
		{"git -C /repo push", OpPush},
		{"git -c user.name=x push", OpPush},
		{"/usr/bin/git push origin HEAD", OpPush},
		{"cd /repo && git push origin HEAD", OpPush},
		{"git add . ; git commit -m x; git push", OpPush},
		{"(cd /repo && git push)", OpPush},
		{"git push origin HEAD 2>&1 | tail -3", OpPush},
		{"git push origin HEAD:refs/heads/feat", OpPush},
		{`git commit -m "fix: a && b" && git push`, OpPush},

		// pushes that do not update the current PR branch
		{"git push --delete origin old-branch", ""},
		{"git push origin --delete old-branch", ""},
		{"git push -d origin old-branch", ""},
		{"git push --tags", ""},
		{"git push --mirror", ""},
		{"git push --all", ""},
		{"git push origin :old-branch", ""},
		{"git -C /repo push --tags", ""},
		{"git push --tags && git push", OpPush},

		// not an executable token, or not the supported subcommand
		{"echo 'git push'", ""},
		{`echo "gh pr create"`, ""},
		{"gh pr view 12", ""},
		{"gh pr list", ""},
		{"gh pr", ""},
		{"git status", ""},
		{"git pull", ""},
		{"git commit -m 'git push'", ""},
		{"git", ""},
		{"git push; echo done", OpPush},
		{"", ""},
		{"echo 'unterminated", ""},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			op, ok := CommandOperation(tt.command)
			if tt.want == "" {
				if ok {
					t.Fatalf("CommandOperation(%q) = %+v, want none", tt.command, op)
				}
				return
			}
			if !ok || op.Kind != tt.want {
				t.Fatalf("CommandOperation(%q) = %+v, %v, want %s", tt.command, op, ok, tt.want)
			}
		})
	}
}

func TestRepoDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	tests := []struct {
		name    string
		cwd     string
		command string
		want    string
	}{
		{"cwd when the command names no directory", "/work", "git push", "/work"},
		{"cwd for a command that is not an operation", "/work", "cd /elsewhere && ls", "/work"},
		{"git -C absolute", "/work", "git -C /repo push", "/repo"},
		{"git -C relative joins cwd", "/work", "git -C sub/dir push", "/work/sub/dir"},
		{"git -C with a space in a quoted path", "/work", `git -C "/my repo" push`, "/my repo"},
		{"cd absolute", "/work", "cd /repo && git push origin HEAD", "/repo"},
		{"cd relative joins cwd", "/work", "cd repo && git push", "/work/repo"},
		{"cd before gh pr create", "/work", "cd /repo && gh pr create --fill", "/repo"},
		{"cd with tilde", "/work", "cd ~/code/repo && git push", filepath.Join(home, "code/repo")},
		{"nearest cd wins", "/work", "cd /a && cd /b && git push", "/b"},
		{"git -C wins over cd", "/work", "cd /a && git -C /b push", "/b"},
		{"cwd with tilde", "~/code", "git push", filepath.Join(home, "code")},
		{"git -C is not read past the subcommand", "/work", "git push origin -C /x", "/work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RepoDir(tt.cwd, tt.command); got != tt.want {
				t.Errorf("RepoDir(%q, %q) = %q, want %q", tt.cwd, tt.command, got, tt.want)
			}
		})
	}
}

func TestRepoDir_EmptyCWDUsesWorkingDirectory(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := RepoDir("", "git push"); got != want {
		t.Errorf("RepoDir(\"\", ...) = %q, want %q", got, want)
	}
}
