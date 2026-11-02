package prtrigger

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/state"
)

// prURLRe matches a GitHub pull request URL in command output.
var prURLRe = regexp.MustCompile(`https://github\.com/([A-Za-z0-9-]+)/([\w.-]+)/pull/(\d+)`)

// PR is a GitHub pull request, owner and repo lower-cased.
type PR struct {
	Owner  string
	Repo   string
	Number int
}

// URL is the pull request's GitHub URL, the argument `paircli publish` takes.
func (p PR) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", p.Owner, p.Repo, p.Number)
}

// ReviewURL is where the PR's review lives on pair.sh.
func (p PR) ReviewURL() string { return state.ReviewURL(p.Owner, p.Repo, p.Number) }

// Event is the part of a PostToolUse hook payload the trigger reads.
type Event struct {
	ToolName string
	Command  string
	CWD      string
	// Response is the raw tool_response: an object with stdout and an exit
	// status (Claude Code), a bare string (Codex), or empty when the harness
	// does not send one.
	Response json.RawMessage
}

// response is a decoded tool_response.
type response struct {
	present     bool
	stdout      string
	interrupted bool
	failed      bool
}

func parseResponse(raw json.RawMessage) response {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return response{}
	}
	switch v := value.(type) {
	case string:
		return response{present: true, stdout: v}
	case map[string]any:
		r := response{present: true}
		r.stdout, _ = v["stdout"].(string)
		r.interrupted, _ = v["interrupted"].(bool)
		for _, key := range []string{"exit_code", "exitCode", "returncode"} {
			if code, ok := v[key].(float64); ok && code != 0 {
				r.failed = true
			}
		}
		return r
	}
	return response{present: true}
}

// PRFromEvent returns the pull request an event's command created or updated.
// A failed or interrupted command, or one that is neither `gh pr create` nor
// a branch `git push`, yields none and no error. A push, and any command whose
// response the harness did not send, resolves the PR from the branch checked
// out in dir; `gh pr create` otherwise reads the single PR URL it printed.
// When that finds no PR (dir is not a repository or its branch has no PR, or
// the output names zero or several PRs), the error says so.
func PRFromEvent(ev Event, dir string, resolve func(dir string) (PR, error)) (PR, bool, error) {
	if ev.ToolName != "Bash" {
		return PR{}, false, nil
	}
	op, ok := CommandOperation(ev.Command)
	if !ok {
		return PR{}, false, nil
	}
	resp := parseResponse(ev.Response)
	if resp.interrupted || resp.failed {
		return PR{}, false, nil
	}
	missed := func(reason error) (PR, bool, error) {
		return PR{}, false, fmt.Errorf("no pull request for %s in %s: %w", op.Name(), dir, reason)
	}
	if op.Kind == OpPush || !resp.present {
		pr, err := resolve(dir)
		if err != nil {
			return missed(err)
		}
		return pr, true, nil
	}
	if pr, ok := singlePR(resp.stdout); ok {
		return pr, true, nil
	}
	return missed(errors.New("its output does not name exactly one pull request URL"))
}

// singlePR parses the one PR URL in text.
func singlePR(text string) (PR, bool) {
	matches := prURLRe.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		return PR{}, false
	}
	number, err := strconv.Atoi(matches[0][3])
	if err != nil {
		return PR{}, false
	}
	return PR{
		Owner:  strings.ToLower(matches[0][1]),
		Repo:   strings.ToLower(matches[0][2]),
		Number: number,
	}, true
}
