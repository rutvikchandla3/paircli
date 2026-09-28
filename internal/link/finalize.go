package link

import (
	"sort"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// Result is Finalize's output.
type Result struct {
	Sessions     []*model.Session            // linked, in start order
	Links        []model.CommitLink          // one per PR commit, PR order
	SessionLinks map[string]model.LinkMethod // Session.Ref() → method
	Dropped      int                         // Select drops + Finalize drops
	Unattributed []string
}

// linkTriggers are the GitHead.Trigger values that place a session at an
// exact commit (as opposed to merely starting on top of one).
var linkTriggers = map[string]bool{
	"commit": true, "prompt": true, "stop": true, "session_end": true,
}

// ancestorTriggers are the GitHead.Trigger values that record the commit a
// session started from.
var ancestorTriggers = map[string]bool{
	"session_start": true, "transcript_meta": true,
}

// heuristicWindow is the +/- window around a commit's time that a session's
// [Start, End] must overlap for the branch+time heuristic to apply.
const heuristicWindow = 2 * time.Hour

// commitSHASet returns the set of pr's commit SHAs.
func commitSHASet(pr *model.PR) map[string]bool {
	set := make(map[string]bool, len(pr.Commits))
	for _, c := range pr.Commits {
		set[c.SHA] = true
	}
	return set
}

// heuristicOverlap reports whether s's [Start, End] overlaps
// [commitTime-heuristicWindow, commitTime+heuristicWindow].
func heuristicOverlap(s *model.Session, commitTime time.Time) bool {
	from := commitTime.Add(-heuristicWindow)
	to := commitTime.Add(heuristicWindow)
	return !s.End.Before(from) && !s.Start.After(to)
}

// hasExactGitHead reports whether s has a git_head event at one of triggers
// whose SHA is sha.
func hasGitHead(s *model.Session, triggers map[string]bool, sha string) bool {
	for _, e := range s.Events {
		if e.Kind == model.KindGitHead && e.GitHead != nil &&
			triggers[e.GitHead.Trigger] && e.GitHead.SHA == sha {
			return true
		}
	}
	return false
}

// hasExactGitHeadAny reports whether s has a git_head event at one of
// triggers whose SHA is in shaSet.
func hasGitHeadAny(s *model.Session, triggers map[string]bool, shaSet map[string]bool) bool {
	for _, e := range s.Events {
		if e.Kind == model.KindGitHead && e.GitHead != nil &&
			triggers[e.GitHead.Trigger] && shaSet[e.GitHead.SHA] {
			return true
		}
	}
	return false
}

// hasContentAttribution reports whether attr has at least one line sourced
// from s.
func hasContentAttribution(attr *model.Attribution, ref string) bool {
	if attr == nil {
		return false
	}
	for _, f := range attr.Files {
		for _, l := range f.Lines {
			if l.Source != nil && l.Source.Session == ref {
				return true
			}
		}
	}
	return false
}

// matchesHeuristic reports whether s's branch matches pr's head ref and its
// window overlaps any PR commit's time.
func matchesHeuristic(pr *model.PR, s *model.Session) bool {
	if s.Branch == "" || s.Branch != pr.HeadRef {
		return false
	}
	for _, c := range pr.Commits {
		if heuristicOverlap(s, c.Time) {
			return true
		}
	}
	return false
}

// sessionMethod applies the Finalize precedence rules to one candidate
// session, returning its LinkMethod and whether any method applied.
func sessionMethod(pr *model.PR, s *model.Session, shaSet map[string]bool, attr *model.Attribution) (model.LinkMethod, bool) {
	if hasGitHeadAny(s, linkTriggers, shaSet) {
		return model.LinkSHAExact, true
	}
	if (s.StartSHA != "" && shaSet[s.StartSHA]) || hasGitHeadAny(s, ancestorTriggers, shaSet) {
		return model.LinkSHAAncestor, true
	}
	if hasContentAttribution(attr, s.Ref()) {
		return model.LinkContent, true
	}
	if matchesHeuristic(pr, s) {
		return model.LinkHeuristic, true
	}
	return model.LinkNone, false
}

// Finalize decides, from the candidates Select produced, which sessions are
// actually linked to pr (by SHA, content attribution, or the branch+time
// heuristic, in that precedence order) and how each PR commit ties to
// sessions.
func Finalize(pr *model.PR, cands []*model.Session, attr *model.Attribution,
	commitSessions map[string][]string, droppedEarlier int) Result {

	shaSet := commitSHASet(pr)
	res := Result{SessionLinks: map[string]model.LinkMethod{}}
	linkedSet := map[string]bool{}
	byRef := map[string]*model.Session{}
	dropped := droppedEarlier

	for _, s := range cands {
		byRef[s.Ref()] = s
		method, ok := sessionMethod(pr, s, shaSet, attr)
		if !ok {
			dropped++
			continue
		}
		res.Sessions = append(res.Sessions, s)
		res.SessionLinks[s.Ref()] = method
		linkedSet[s.Ref()] = true
	}
	sort.Slice(res.Sessions, func(i, j int) bool {
		if !res.Sessions[i].Start.Equal(res.Sessions[j].Start) {
			return res.Sessions[i].Start.Before(res.Sessions[j].Start)
		}
		return res.Sessions[i].Ref() < res.Sessions[j].Ref()
	})
	res.Dropped = dropped

	for _, c := range pr.Commits {
		link := model.CommitLink{SHA: c.SHA}

		var exactRefs []string
		for _, s := range cands {
			if hasGitHead(s, linkTriggers, c.SHA) {
				exactRefs = append(exactRefs, s.Ref())
			}
		}
		if len(exactRefs) > 0 {
			sort.Strings(exactRefs)
			link.Sessions = exactRefs
			link.Method = model.LinkSHAExact
			link.Confidence = "exact"
			res.Links = append(res.Links, link)
			continue
		}

		if refs, ok := commitSessions[c.SHA]; ok {
			var filtered []string
			for _, r := range refs {
				if linkedSet[r] {
					filtered = append(filtered, r)
				}
			}
			if len(filtered) > 0 {
				sort.Strings(filtered)
				link.Sessions = filtered
				link.Method = model.LinkContent
				link.Confidence = "inferred"
				res.Links = append(res.Links, link)
				continue
			}
		}

		var heurRefs []string
		for ref := range linkedSet {
			s := byRef[ref]
			if s.Branch != "" && s.Branch == pr.HeadRef && heuristicOverlap(s, c.Time) {
				heurRefs = append(heurRefs, ref)
			}
		}
		if len(heurRefs) > 0 {
			sort.Strings(heurRefs)
			link.Sessions = heurRefs
			link.Method = model.LinkHeuristic
			link.Confidence = "inferred"
			res.Links = append(res.Links, link)
			continue
		}

		link.Method = model.LinkNone
		link.Confidence = "unknown"
		res.Links = append(res.Links, link)
		res.Unattributed = append(res.Unattributed, c.SHA)
	}

	return res
}
