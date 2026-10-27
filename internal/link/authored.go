package link

import (
	"regexp"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// minSubjectLen keeps a subject like "fix" or "wip" from matching text that
// merely contains the word.
const minSubjectLen = 12

// gitCommit matches a `git commit`, tolerating `git -C dir commit` and
// `git -c k=v commit`.
var gitCommit = regexp.MustCompile(`\bgit(?:\s+(?:-C|-c)\s+\S+)*\s+commit\b`)

var spaces = regexp.MustCompile(`\s+`)

// authoredCommits returns the SHAs of the PR commits that s itself created:
// the session ran a `git commit`, and a PR commit's subject appears in text
// the session typed (a command, which carries -m and heredoc messages, or a
// file it wrote, which is where `git commit -F file` reads one).
//
// It needs no hook, repo, branch or working directory, which is what makes it
// work for a PR whose commits were made before hooks were installed, and for a
// session that ran in one repo while committing to another. Reading a commit
// log is not enough: only text the session wrote counts, and only in a session
// that ran `git commit`.
func authoredCommits(pr *model.PR, s *model.Session) map[string]bool {
	if len(pr.Commits) == 0 {
		return nil
	}
	var typed strings.Builder
	committed := false
	for i := range s.Events {
		e := &s.Events[i]
		switch {
		case e.Kind == model.KindCommand && e.Command != nil:
			committed = committed || gitCommit.MatchString(e.Command.Cmd)
			typed.WriteString(e.Command.Cmd)
			typed.WriteByte('\n')
		case e.Kind == model.KindEdit && e.Edit != nil && !e.Edit.Failed:
			for _, line := range e.Edit.Added {
				typed.WriteString(line)
				typed.WriteByte('\n')
			}
		}
	}
	if !committed {
		return nil
	}
	text := spaces.ReplaceAllString(typed.String(), " ")

	var out map[string]bool
	for _, c := range pr.Commits {
		subject, _, _ := strings.Cut(c.Message, "\n")
		// GitHub cuts a long headline short and ends it with an ellipsis, moving
		// the rest into the body; what the session typed is the text before it.
		subject = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(subject), "…"), "...")
		subject = spaces.ReplaceAllString(strings.TrimSpace(subject), " ")
		if len(subject) < minSubjectLen || !strings.Contains(text, subject) {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[c.SHA] = true
	}
	return out
}
