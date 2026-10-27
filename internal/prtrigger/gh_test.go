package prtrigger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH puts a `gh` script with the given body first on PATH.
func fakeGH(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestResolveCurrentBranchPR(t *testing.T) {
	fakeGH(t, `echo `+testPRURL)
	got, err := ResolveCurrentBranchPR(t.TempDir())
	if err != nil || got != wantPR {
		t.Errorf("ResolveCurrentBranchPR = %+v, %v, want %+v", got, err, wantPR)
	}
}

func TestResolveCurrentBranchPR_AsksGHForTheBranchURLInTheDirectory(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(t.TempDir(), "call")
	fakeGH(t, `echo "$PWD|$*" > `+record+"\necho "+testPRURL)
	if _, err := ResolveCurrentBranchPR(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, _ := filepath.EvalSymlinks(dir)
	gotDir, args, _ := strings.Cut(strings.TrimSpace(string(data)), "|")
	if gotEval, _ := filepath.EvalSymlinks(gotDir); gotEval != wantDir {
		t.Errorf("gh ran in %q, want %q", gotDir, wantDir)
	}
	if want := "pr view --json url --jq .url"; args != want {
		t.Errorf("gh args = %q, want %q", args, want)
	}
}

func TestResolveCurrentBranchPR_Failures(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no pull request", "exit 1"},
		{"no output", "true"},
		{"two urls", "echo " + testPRURL + "\necho https://github.com/Five-SH/awt/pull/13"},
		{"not a github url", "echo https://gitlab.com/a/b/pull/1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeGH(t, tt.body)
			if pr, err := ResolveCurrentBranchPR(t.TempDir()); err == nil {
				t.Errorf("ResolveCurrentBranchPR = %+v, want an error", pr)
			}
		})
	}
}

func TestResolveCurrentBranchPR_MissingDirectory(t *testing.T) {
	fakeGH(t, "echo "+testPRURL)
	if _, err := ResolveCurrentBranchPR(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("want an error for a missing directory")
	}
}

func TestResolveCurrentBranchPR_NoGH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveCurrentBranchPR(t.TempDir()); err == nil {
		t.Error("want an error without gh on PATH")
	}
}
