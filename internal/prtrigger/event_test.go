package prtrigger

import (
	"encoding/json"
	"errors"
	"testing"
)

const testPRURL = "https://github.com/Five-SH/awt/pull/12"

var wantPR = PR{Owner: "five-sh", Repo: "awt", Number: 12}

// bashEvent is a Claude Code style Bash PostToolUse event with stdout.
func bashEvent(command, stdout string, extra map[string]any) Event {
	resp := map[string]any{"stdout": stdout, "stderr": "", "interrupted": false, "isImage": false}
	for k, v := range extra {
		resp[k] = v
	}
	raw, _ := json.Marshal(resp)
	return Event{ToolName: "Bash", Command: command, Response: raw}
}

// resolver returns a resolve func that reports pr (or a failure when pr is nil)
// and counts how often it was asked.
func resolver(pr *PR, calls *int) func(string) (PR, error) {
	return func(string) (PR, error) {
		*calls++
		if pr == nil {
			return PR{}, errors.New("no pull request")
		}
		return *pr, nil
	}
}

func TestPRFromEvent_Create(t *testing.T) {
	tests := []struct {
		name string
		ev   Event
		want bool
	}{
		{"pr create prints the url",
			bashEvent("git push -u origin HEAD && gh pr create --fill --base main",
				"Creating pull request for feat into main\n\n"+testPRURL+"\n", nil), true},
		{"codex string response",
			Event{ToolName: "Bash", Command: "gh pr create --fill", Response: json.RawMessage(`"` + testPRURL + `"`)}, true},
		{"gh pr view is not a creation", bashEvent("gh pr view 12", testPRURL+"\n", nil), false},
		{"echo of the command", bashEvent("echo 'gh pr create'", "gh pr create\n", nil), false},
		{"web mode prints no pr url",
			bashEvent("gh pr create --web", "Opening github.com/five-sh/awt/pull/new/feat in your browser.\n", nil), false},
		{"interrupted", bashEvent("gh pr create --fill", testPRURL+"\n", map[string]any{"interrupted": true}), false},
		{"exit_code", bashEvent("gh pr create --fill", testPRURL+"\n", map[string]any{"exit_code": 1}), false},
		{"exitCode", bashEvent("gh pr create --fill", testPRURL+"\n", map[string]any{"exitCode": 2}), false},
		{"returncode", bashEvent("gh pr create --fill", testPRURL+"\n", map[string]any{"returncode": 1}), false},
		{"zero exit_code is success", bashEvent("gh pr create --fill", testPRURL+"\n", map[string]any{"exit_code": 0}), true},
		{"other tool", Event{ToolName: "Read", Command: "gh pr create --fill", Response: json.RawMessage(`{"stdout":"` + testPRURL + `"}`)}, false},
		{"two different urls are ambiguous",
			bashEvent("gh pr create --fill", testPRURL+"\nhttps://github.com/Five-SH/awt/pull/13\n", nil), false},
		{"the same url twice is ambiguous", bashEvent("gh pr create --fill", testPRURL+"\n"+testPRURL+"\n", nil), false},
		{"non-github url", bashEvent("gh pr create --fill", "https://gitlab.com/five-sh/awt/pull/12\n", nil), false},
		{"github enterprise url", bashEvent("gh pr create --fill", "https://github.example.com/five-sh/awt/pull/12\n", nil), false},
		{"no output", bashEvent("gh pr create --fill", "", nil), false},
		{"response without stdout", Event{ToolName: "Bash", Command: "gh pr create --fill", Response: json.RawMessage(`{"interrupted":false}`)}, false},
		{"array response", Event{ToolName: "Bash", Command: "gh pr create --fill", Response: json.RawMessage(`[1]`)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got, ok := PRFromEvent(tt.ev, "/repo", resolver(nil, &calls))
			if ok != tt.want || (ok && got != wantPR) {
				t.Errorf("PRFromEvent = %+v, %v, want %+v, %v", got, ok, wantPR, tt.want)
			}
			if calls != 0 {
				t.Errorf("resolve called %d times for a create with a response, want 0", calls)
			}
		})
	}
}

func TestPRFromEvent_PushResolvesBranchPR(t *testing.T) {
	pr := wantPR
	calls := 0
	got, ok := PRFromEvent(bashEvent("cd /repo && git push origin HEAD", "branch updated\n", map[string]any{"exit_code": 0}),
		"/repo", resolver(&pr, &calls))
	if !ok || got != wantPR || calls != 1 {
		t.Errorf("PRFromEvent = %+v, %v after %d resolves, want %+v after 1", got, ok, calls, wantPR)
	}
}

func TestPRFromEvent_ResolveGetsTheDirectory(t *testing.T) {
	var asked string
	resolve := func(dir string) (PR, error) { asked = dir; return wantPR, nil }
	if _, ok := PRFromEvent(bashEvent("git push", "", nil), "/the/repo", resolve); !ok || asked != "/the/repo" {
		t.Errorf("resolve asked for %q, ok=%v", asked, ok)
	}
}

func TestPRFromEvent_PushWithoutOpenPR(t *testing.T) {
	calls := 0
	if pr, ok := PRFromEvent(bashEvent("git push origin HEAD", "branch updated\n", nil), "/repo", resolver(nil, &calls)); ok {
		t.Errorf("PRFromEvent = %+v, want none when the branch has no PR", pr)
	}
}

func TestPRFromEvent_IgnoredPushesAndFailures(t *testing.T) {
	pr := wantPR
	tests := []struct {
		name string
		ev   Event
	}{
		{"delete", bashEvent("git push --delete origin old-branch", "done\n", nil)},
		{"tags", bashEvent("git push --tags", "done\n", nil)},
		{"mirror", bashEvent("git push --mirror", "done\n", nil)},
		{"echo", bashEvent("echo 'git push'", "done\n", nil)},
		{"failed push", bashEvent("git push", "", map[string]any{"exit_code": 1})},
		{"interrupted push", bashEvent("git push", "", map[string]any{"interrupted": true})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			if got, ok := PRFromEvent(tt.ev, "/repo", resolver(&pr, &calls)); ok || calls != 0 {
				t.Errorf("PRFromEvent = %+v, %v after %d resolves, want none without resolving", got, ok, calls)
			}
		})
	}
}

// Codex sometimes omits tool_response; the PR is then read from the branch.
func TestPRFromEvent_MissingResponseResolvesBranchPR(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage("null")} {
		pr := wantPR
		calls := 0
		ev := Event{ToolName: "Bash", Command: "cd /repo && gh pr create --draft --fill", Response: raw}
		got, ok := PRFromEvent(ev, "/repo", resolver(&pr, &calls))
		if !ok || got != wantPR || calls != 1 {
			t.Errorf("response %q: PRFromEvent = %+v, %v after %d resolves", raw, got, ok, calls)
		}
	}
}

func TestPR_URLs(t *testing.T) {
	if got, want := wantPR.URL(), "https://github.com/five-sh/awt/pull/12"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if got, want := wantPR.ReviewURL(), "https://pair.sh/five-sh/awt/pull/12"; got != want {
		t.Errorf("ReviewURL = %q, want %q", got, want)
	}
}
