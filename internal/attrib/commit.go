package attrib

import (
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// minCommitLineLen is the shortest (trimmed) added line CommitSessions will
// use to link a commit to a session; short lines (braces, closing tags) are
// too common to be reliable evidence.
const minCommitLineLen = 8

// CommitSessions maps each PR commit's SHA to the sessions whose agent edits
// produced its added lines. For every added, non-trivial line at least
// minCommitLineLen characters after trimming, it looks up strict agent
// matches in the same file and collects their sessions. Commits with no
// matches are absent from the result.
func CommitSessions(pr *model.PR, sessions []*model.Session) map[string][]string {
	out := map[string][]string{}
	if pr == nil {
		return out
	}
	idx := buildIndexes(sessions)

	for _, c := range pr.Commits {
		if len(c.Files) == 0 {
			continue
		}
		refs := map[string]bool{}
		for _, f := range c.Files {
			if f.Binary {
				continue
			}
			for _, dl := range f.AddedLines() {
				if model.IsTrivialLine(dl.Text) {
					continue
				}
				if len(strings.TrimSpace(dl.Text)) < minCommitLineLen {
					continue
				}
				norm := model.NormalizeLine(dl.Text)
				for _, it := range idx.agentStrict[f.Path][norm] {
					refs[it.sess.Ref()] = true
				}
			}
		}
		if len(refs) == 0 {
			continue
		}
		list := make([]string, 0, len(refs))
		for r := range refs {
			list = append(list, r)
		}
		sort.Strings(list)
		out[c.SHA] = list
	}
	return out
}
