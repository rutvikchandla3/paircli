package classify

import (
	"sort"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/config"
)

func TestCommand(t *testing.T) {
	tests := []struct {
		name  string
		cmd   string
		extra []config.CheckPattern
		want  []CmdClass
	}{
		{
			name: "npm test",
			cmd:  "npm test",
			want: []CmdClass{Test},
		},
		{
			name: "bash wrapped pnpm vitest",
			cmd:  "bash -lc 'cd app && pnpm vitest run'",
			want: []CmdClass{Test},
		},
		{
			name: "npx tsc typecheck",
			cmd:  "npx tsc --noEmit",
			want: []CmdClass{Typecheck},
		},
		{
			name: "ruff check lint",
			cmd:  "ruff check .",
			want: []CmdClass{Lint},
		},
		{
			name: "make build",
			cmd:  "make",
			want: []CmdClass{Build},
		},
		{
			name: "make test",
			cmd:  "make test",
			want: []CmdClass{Test},
		},
		{
			name: "git commit with no-verify short flag",
			cmd:  "git commit -nm 'fix'",
			want: []CmdClass{GitCommit, NoVerify},
		},
		{
			name: "git commit message flag",
			cmd:  "git commit -m '-n is fine'",
			want: []CmdClass{GitCommit},
		},
		{
			name: "git commit with HUSKY env",
			cmd:  "HUSKY=0 git commit -m x",
			want: []CmdClass{GitCommit, HookSkipEnv},
		},
		{
			name: "git force push",
			cmd:  "git push --force-with-lease origin HEAD",
			want: []CmdClass{GitPush, GitForcePush},
		},
		{
			name: "git push with refspec plus",
			cmd:  "git push origin +main",
			want: []CmdClass{GitPush, GitForcePush},
		},
		{
			name: "git reset hard",
			cmd:  "git reset --hard HEAD~1",
			want: []CmdClass{GitResetHard},
		},
		{
			name: "git checkout discard",
			cmd:  "git checkout -- src/a.ts",
			want: []CmdClass{GitDiscard},
		},
		{
			name: "git stash list no-op",
			cmd:  "git stash list",
			want: []CmdClass{},
		},
		{
			name: "rm rf",
			cmd:  "rm -rf dist node_modules",
			want: []CmdClass{RmRF},
		},
		{
			name: "prisma migrate",
			cmd:  "prisma migrate deploy",
			want: []CmdClass{Migration},
		},
		{
			name: "curl POST",
			cmd:  "curl -X POST https://api.example.com/x",
			want: []CmdClass{NetworkWrite},
		},
		{
			name: "curl localhost",
			cmd:  "curl http://localhost:3000/health",
			want: []CmdClass{LocalHTTP},
		},
		{
			name: "curl GET",
			cmd:  "curl -s https://example.com",
			want: []CmdClass{NetworkRead},
		},
		{
			name: "gh read",
			cmd:  "gh pr view 12",
			want: []CmdClass{GHRead},
		},
		{
			name: "gh write",
			cmd:  "gh api repos/o/r/issues -f title=x",
			want: []CmdClass{GHWrite},
		},
		{
			name: "npm publish",
			cmd:  "npm publish",
			want: []CmdClass{Publish},
		},
		{
			name: "kubectl apply",
			cmd:  "kubectl apply -f k.yaml",
			want: []CmdClass{Infra},
		},
		{
			name: "jest snapshot update",
			cmd:  "jest -u",
			want: []CmdClass{Test, SnapshotUpdate},
		},
		{
			name: "npm run dev",
			cmd:  "npm run dev",
			want: []CmdClass{DevServer},
		},
		{
			name: "cat read",
			cmd:  "cat src/a.ts | head -20",
			want: []CmdClass{ReadFile},
		},
		{
			name: "pip install",
			cmd:  "pip install requests==2.32.0",
			want: []CmdClass{Install},
		},
		{
			name: "echo nothing",
			cmd:  "echo hi",
			want: []CmdClass{},
		},
		{
			name: "extra pattern",
			cmd:  "just test",
			extra: []config.CheckPattern{
				{Class: "test", Regex: "^just test"},
			},
			want: []CmdClass{Test},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Command(tt.cmd, tt.extra)
			sortClasses(got)
			sortClasses(tt.want)
			if !classesEqual(got, tt.want) {
				t.Errorf("Command(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestCheckClass(t *testing.T) {
	tests := []struct {
		name string
		in   []CmdClass
		want CmdClass
		ok   bool
	}{
		{
			name: "test present",
			in:   []CmdClass{Test, Build},
			want: Test,
			ok:   true,
		},
		{
			name: "typecheck present",
			in:   []CmdClass{Typecheck, Build},
			want: Typecheck,
			ok:   true,
		},
		{
			name: "lint present",
			in:   []CmdClass{Lint, Build},
			want: Lint,
			ok:   true,
		},
		{
			name: "build only",
			in:   []CmdClass{Build},
			want: Build,
			ok:   true,
		},
		{
			name: "no check class",
			in:   []CmdClass{Install},
			want: "",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CheckClass(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("CheckClass(%v) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func sortClasses(classes []CmdClass) {
	sort.Slice(classes, func(i, j int) bool {
		return classes[i] < classes[j]
	})
}

func classesEqual(a, b []CmdClass) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
