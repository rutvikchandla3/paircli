// Package link finds the sessions that belong to a PR: it discovers
// transcripts from every harness inside a time window, merges in hook
// records, normalizes paths against the repo, keeps only sessions that plausibly
// touch this PR's repo, and — once internal/attrib has computed line
// attribution — decides which sessions are actually linked and how each PR
// commit ties to sessions.
package link

import (
	"sort"
	"time"

	"github.com/rutvikchandla3/paircli/internal/claudecode"
	"github.com/rutvikchandla3/paircli/internal/codex"
	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/hooklog"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/pi"
)

// Window is the time range Discover searches for sessions in.
type Window struct{ From, To time.Time }

// WindowFor computes the session search window for pr: from the earliest PR
// commit minus cfg.WindowBefore, to the latest PR commit plus
// cfg.WindowAfter. When pr has no commits, it uses
// [pr.CreatedAt-WindowBefore, now).
func WindowFor(pr *model.PR, cfg *config.Config) Window {
	if len(pr.Commits) == 0 {
		return Window{From: pr.CreatedAt.Add(-cfg.WindowBefore.Duration), To: time.Now()}
	}
	earliest, latest := pr.Commits[0].Time, pr.Commits[0].Time
	for _, c := range pr.Commits[1:] {
		if c.Time.Before(earliest) {
			earliest = c.Time
		}
		if c.Time.After(latest) {
			latest = c.Time
		}
	}
	return Window{From: earliest.Add(-cfg.WindowBefore.Duration), To: latest.Add(cfg.WindowAfter.Duration)}
}

// discoverFn matches the Discover signature every harness package exposes.
type discoverFn func(root string, since time.Time) ([]string, error)

// parseFn matches the ParseFile signature every harness package exposes.
type parseFn func(path string) ([]*model.Session, error)

// sessionOverlaps reports whether s's [Start, End] overlaps w.
func sessionOverlaps(s *model.Session, w Window) bool {
	return !s.End.Before(w.From) && !s.Start.After(w.To)
}

// discoverHarness runs one harness's Discover+ParseFile pair over root,
// keeping only sessions that overlap w. Unparsable files are skipped, not
// fatal: parsers already tolerate malformed lines, so a ParseFile error here
// means the file could not be read at all.
func discoverHarness(root string, w Window, discover discoverFn, parse parseFn) ([]*model.Session, error) {
	files, err := discover(root, w.From)
	if err != nil {
		return nil, err
	}
	var out []*model.Session
	for _, f := range files {
		sessions, err := parse(f)
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if sessionOverlaps(s, w) {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// mergeKey identifies a session across files (resumed Claude Code sessions
// can span several).
type mergeKey struct {
	h  model.Harness
	id string
}

// mergeDuplicates merges sessions sharing the same Harness and ID:
// concatenates their events (dropping events with a duplicate ID, keeping
// the first), keeps the earliest SourcePath, and re-finalizes.
func mergeDuplicates(sessions []*model.Session) []*model.Session {
	var order []mergeKey
	merged := map[mergeKey]*model.Session{}
	for _, s := range sessions {
		k := mergeKey{s.Harness, s.ID}
		if existing, ok := merged[k]; ok {
			existing.Events = append(existing.Events, s.Events...)
			continue
		}
		merged[k] = s
		order = append(order, k)
	}
	out := make([]*model.Session, 0, len(order))
	for _, k := range order {
		s := merged[k]
		dedupeEventIDs(s)
		s.Finalize()
		out = append(out, s)
	}
	return out
}

// dedupeEventIDs drops events whose ID duplicates one already seen, keeping
// the first occurrence in slice order.
func dedupeEventIDs(s *model.Session) {
	seen := map[string]bool{}
	out := s.Events[:0]
	for _, e := range s.Events {
		if e.ID != "" {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
		}
		out = append(out, e)
	}
	s.Events = out
}

// Discover reads every harness's transcripts under cfg's configured (or
// default) roots, keeps sessions overlapping w, merges resumed sessions
// spanning several files, and — when withHooks is true — merges in hook log
// records (Claude Code, Codex) or Pi extension-written custom entries.
// Discovery never reads files older than w.From. Returns sessions sorted by
// Start, then Ref().
func Discover(cfg *config.Config, w Window, withHooks bool) ([]*model.Session, error) {
	var all []*model.Session

	ccRoot := cfg.Roots.ClaudeCode
	if ccRoot == "" {
		ccRoot = claudecode.DefaultRoot()
	}
	ccSessions, err := discoverHarness(ccRoot, w, claudecode.Discover, claudecode.ParseFile)
	if err != nil {
		return nil, err
	}
	all = append(all, ccSessions...)

	codexRoot := cfg.Roots.Codex
	if codexRoot == "" {
		codexRoot = codex.DefaultRoot()
	}
	codexSessions, err := discoverHarness(codexRoot, w, codex.Discover, codex.ParseFile)
	if err != nil {
		return nil, err
	}
	all = append(all, codexSessions...)

	piRoot := cfg.Roots.Pi
	if piRoot == "" {
		piRoot = pi.DefaultRoot()
	}
	piSessions, err := discoverHarness(piRoot, w, pi.Discover, pi.ParseFile)
	if err != nil {
		return nil, err
	}
	all = append(all, piSessions...)

	all = mergeDuplicates(all)

	if withHooks {
		hookDir := cfg.Roots.HookLog
		if hookDir == "" {
			hookDir = hooklog.DefaultDir()
		}
		since := w.From.Add(-24 * time.Hour)
		ccRecs, err := hooklog.Read(hookDir, model.HarnessClaudeCode, since)
		if err != nil {
			return nil, err
		}
		codexRecs, err := hooklog.Read(hookDir, model.HarnessCodex, since)
		if err != nil {
			return nil, err
		}
		for _, s := range all {
			switch s.Harness {
			case model.HarnessClaudeCode:
				if recs := ccRecs[s.ID]; len(recs) > 0 {
					hooklog.Merge(s, recs)
				}
			case model.HarnessCodex:
				if recs := codexRecs[s.ID]; len(recs) > 0 {
					hooklog.Merge(s, recs)
				}
			case model.HarnessPi:
				recs, err := pi.HookRecords(s.SourcePath)
				if err == nil && len(recs) > 0 {
					hooklog.Merge(s, recs)
				}
			}
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if !all[i].Start.Equal(all[j].Start) {
			return all[i].Start.Before(all[j].Start)
		}
		return all[i].Ref() < all[j].Ref()
	})
	return all, nil
}
