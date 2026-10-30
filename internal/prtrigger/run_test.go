package prtrigger

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type startCall struct {
	pr  PR
	dir string
}

// fakeRunner resolves every branch to pr (none when pr is nil) and records
// each start.
func fakeRunner(pr *PR, startErr error, starts *[]startCall) Runner {
	return Runner{
		Resolve: func(string) (PR, error) {
			if pr == nil {
				return PR{}, errors.New("no pull request")
			}
			return *pr, nil
		},
		Start: func(p PR, dir string) error {
			*starts = append(*starts, startCall{p, dir})
			return startErr
		},
	}
}

func TestRunnerRun_CreateStartsPublishAndPrintsOneObject(t *testing.T) {
	var starts []startCall
	var out bytes.Buffer
	ev := bashEvent("cd /repo && gh pr create --fill", testPRURL+"\n", nil)
	ev.CWD = "/work"
	if err := fakeRunner(nil, nil, &starts).Run(ev, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(starts) != 1 || starts[0].pr != wantPR || starts[0].dir != "/repo" {
		t.Fatalf("starts = %+v, want one start of %+v in /repo", starts, wantPR)
	}

	var got Output
	dec := json.NewDecoder(&out)
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, out.String())
	}
	if dec.More() {
		t.Errorf("output holds more than one JSON object: %q", out.String())
	}
	const review = "https://pair.sh/five-sh/awt/pull/12"
	if !strings.Contains(got.SystemMessage, "Pair review: "+review) {
		t.Errorf("systemMessage = %q", got.SystemMessage)
	}
	if got.HookSpecificOutput.HookEventName != "PostToolUse" || !strings.Contains(got.HookSpecificOutput.AdditionalContext, review) {
		t.Errorf("hookSpecificOutput = %+v", got.HookSpecificOutput)
	}
}

func TestRunnerRun_PushResolvesInTheTargetDirectory(t *testing.T) {
	pr := wantPR
	var starts []startCall
	var asked string
	r := fakeRunner(&pr, nil, &starts)
	resolve := r.Resolve
	r.Resolve = func(dir string) (PR, error) { asked = dir; return resolve(dir) }

	ev := bashEvent("git -C sub push", "branch updated\n", nil)
	ev.CWD = "/work"
	var out bytes.Buffer
	if err := r.Run(ev, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if asked != "/work/sub" || len(starts) != 1 || starts[0].dir != "/work/sub" {
		t.Errorf("resolved %q, starts %+v, want /work/sub", asked, starts)
	}
	if out.Len() == 0 {
		t.Error("nothing printed for a push to a PR branch")
	}
}

func TestRunnerRun_RelativeDirectoryBecomesAbsolute(t *testing.T) {
	pr := wantPR
	var starts []startCall
	ev := bashEvent("git push", "", nil) // no cwd: the process's working directory
	if err := fakeRunner(&pr, nil, &starts).Run(ev, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(starts) != 1 || !filepath.IsAbs(starts[0].dir) {
		t.Errorf("starts = %+v, want an absolute directory", starts)
	}
}

func TestRunnerRun_PrintsAndStartsNothingForOtherEvents(t *testing.T) {
	pr := wantPR
	tests := []struct {
		name string
		ev   Event
		pr   *PR
	}{
		{"unrelated command", bashEvent("ls -la", "", nil), &pr},
		{"git commit", bashEvent("git commit -am wip", "", nil), &pr},
		{"push without an open PR", bashEvent("git push", "", nil), nil},
		{"failed push", bashEvent("git push", "", map[string]any{"exit_code": 1}), &pr},
		{"failed create", bashEvent("gh pr create", "", map[string]any{"exit_code": 1}), &pr},
		{"delete push", bashEvent("git push --delete origin x", "", nil), &pr},
		{"other tool", Event{ToolName: "Write", Command: "git push"}, &pr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var starts []startCall
			var out bytes.Buffer
			if err := fakeRunner(tt.pr, nil, &starts).Run(tt.ev, &out); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(starts) != 0 || out.Len() != 0 {
				t.Errorf("starts = %+v, output = %q, want neither", starts, out.String())
			}
		})
	}
}

func TestRunnerRun_StartFailureIsReturnedAndNothingIsPrinted(t *testing.T) {
	var starts []startCall
	var out bytes.Buffer
	ev := bashEvent("gh pr create --fill", testPRURL+"\n", nil)
	err := fakeRunner(nil, errors.New("exec format error"), &starts).Run(ev, &out)
	if err == nil || !strings.Contains(err.Error(), "exec format error") {
		t.Errorf("err = %v, want the start failure", err)
	}
	if out.Len() != 0 {
		t.Errorf("printed %q after a failed start", out.String())
	}
}

// startDetached is given /bin/true-like binary so the test never re-executes
// itself; the publish log is what it is expected to leave behind.
func TestStartDetached_WritesALogUnderTheLogDir(t *testing.T) {
	exe, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	if err := startDetached(exe, wantPR, root); err != nil {
		t.Fatalf("startDetached: %v", err)
	}
	logs, err := filepath.Glob(filepath.Join(root, ".paircli", "logs", "publish-five-sh__awt__12-*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs = %v, %v, want one publish log", logs, err)
	}
	if info, err := os.Stat(logs[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("log stat = %v, %v, want mode 0600", info, err)
	}
}

// The detached publish follows the review to the end (--wait, default
// timeout), since that is the process that sends the notification saying the
// review is ready.
func TestPublishArgs_FollowTheReviewWithTheDefaultTimeout(t *testing.T) {
	got := strings.Join(PublishArgs(wantPR, "/repo"), " ")
	want := "publish " + wantPR.URL() + " --repo-dir /repo --wait"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// startDetached passes those arguments to the binary it starts.
func TestStartDetached_PassesWait(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	out := filepath.Join(t.TempDir(), "args")
	script := filepath.Join(t.TempDir(), "paircli")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_OUT\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARGS_OUT", out)
	if err := startDetached(script, wantPR, "/repo"); err != nil {
		t.Fatalf("startDetached: %v", err)
	}
	var got []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got, _ = os.ReadFile(out); len(got) > 0 {
			break
		}
	}
	want := strings.Join(PublishArgs(wantPR, "/repo"), "\n") + "\n"
	if string(got) != want {
		t.Errorf("the started process got %q, want %q", got, want)
	}
}
