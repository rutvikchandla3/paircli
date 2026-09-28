package link

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/gitinfo"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// Select fills RepoRoot and every event's RelPath for each session (in
// place), then keeps sessions that plausibly belong to pr: they must overlap
// w, and either their RepoRemote matches pr.Repo (case-insensitively) or
// (when RepoRemote is unknown) at least one non-failed edit's RelPath is a
// PR file. Returns the candidates sorted by Start and the number dropped.
func Select(pr *model.PR, sessions []*model.Session, w Window) (cands []*model.Session, dropped int) {
	for _, s := range sessions {
		fillRepoRoot(s)
		fillRelPaths(s, pr)
		if isCandidate(pr, s, w) {
			cands = append(cands, s)
		} else {
			dropped++
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Start.Before(cands[j].Start) })
	return cands, dropped
}

// fillRepoRoot sets s.RepoRoot (and s.RepoRemote, when the parser left it
// empty) from local git metadata, provided s.CWD still exists on disk. When
// CWD no longer exists (a deleted worktree), RepoRoot is left empty and any
// RepoRemote the parser already set (Codex records this from session_meta)
// is kept.
func fillRepoRoot(s *model.Session) {
	if s.CWD == "" {
		return
	}
	if _, err := os.Stat(s.CWD); err != nil {
		return
	}
	if root, err := gitinfo.TopLevel(s.CWD); err == nil {
		s.RepoRoot = root
	}
	if s.RepoRemote == "" {
		if info, err := gitinfo.Resolve(s.CWD); err == nil {
			s.RepoRemote = info.OwnerRepo
		}
	}
}

// fillRelPaths fills RelPath on every Edit, Read, External, Instructions and
// Diagnostics payload in s's events. Snapshot paths are already
// repo-relative; Codex's Edit.MovePath is left as recorded.
func fillRelPaths(s *model.Session, pr *model.PR) {
	rel := func(path string) string { return relPathFor(path, s.CWD, s.RepoRoot, pr) }
	for i := range s.Events {
		e := &s.Events[i]
		switch e.Kind {
		case model.KindEdit:
			if e.Edit != nil {
				e.Edit.RelPath = rel(e.Edit.Path)
			}
		case model.KindRead:
			if e.Read != nil {
				e.Read.RelPath = rel(e.Read.Path)
			}
		case model.KindExternalEdit:
			if e.External != nil {
				e.External.RelPath = rel(e.External.Path)
			}
		case model.KindInstructions:
			if e.Instructions != nil {
				e.Instructions.RelPath = rel(e.Instructions.Path)
			}
		case model.KindDiagnostics:
			if e.Diagnostics != nil {
				e.Diagnostics.RelPath = rel(e.Diagnostics.Path)
			}
		}
	}
}

// relPathFor resolves one recorded path to a repo-relative path:
//   - strip a "file://" prefix; join with cwd if not absolute; clean it;
//   - when repoRoot is known, the path must resolve inside it;
//   - when repoRoot is unknown, fall back to the longest PR file path that
//     is a path suffix of the resolved (absolute) path;
//   - otherwise "" (outside the repo, or nothing matched).
func relPathFor(path, cwd, repoRoot string, pr *model.PR) string {
	if path == "" {
		return ""
	}
	abs := strings.TrimPrefix(path, "file://")
	if !filepath.IsAbs(abs) {
		if cwd == "" {
			return ""
		}
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)

	if repoRoot != "" {
		rel, err := filepath.Rel(repoRoot, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		return filepath.ToSlash(rel)
	}

	absSlash := filepath.ToSlash(abs)
	best := ""
	for _, f := range pr.Files {
		if strings.HasSuffix(absSlash, "/"+f.Path) && len(f.Path) > len(best) {
			best = f.Path
		}
	}
	return best
}

// isCandidate applies the Select rule 3: overlap the window, and either the
// repo remote matches or (remote unknown) an edit landed on a PR file.
func isCandidate(pr *model.PR, s *model.Session, w Window) bool {
	if !sessionOverlaps(s, w) {
		return false
	}
	if strings.EqualFold(s.RepoRemote, pr.Repo) {
		return true
	}
	if s.RepoRemote != "" {
		return false
	}
	for _, e := range s.Events {
		if e.Kind == model.KindEdit && e.Edit != nil && !e.Edit.Failed &&
			e.Edit.RelPath != "" && pr.File(e.Edit.RelPath) != nil {
			return true
		}
	}
	return false
}
