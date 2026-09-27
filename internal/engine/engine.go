// Package engine is the detector registry and shared context every signal
// detector runs against. internal/detect/* packages register detectors here
// via init(); internal/scan calls Run to produce the report's signals.
package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/redact"
)

// Item is one timeline event together with the session it belongs to.
type Item struct {
	S *model.Session
	E *model.Event
}

// Context is the read-only view of a linked PR every detector runs against.
// Detectors must never mutate it or anything reachable from it.
type Context struct {
	PR           *model.PR
	Sessions     []*model.Session   // linked sessions only; RelPath fields populated
	Timeline     []Item             // all events of Sessions, sorted by (TS, Session.Ref(), Seq)
	Attribution  *model.Attribution // never nil
	Links        []model.CommitLink
	SessionLinks map[string]model.LinkMethod // Session.Ref() -> how it was linked; nil in unit tests
	Judgments    *model.Judgments            // nil unless the LLM pass ran
	Config       *config.Config              // never nil
}

// NewContext builds a Context. It copies the slices it receives (the caller
// keeps ownership of its own backing arrays) and builds Timeline by merging
// every event of every session.
func NewContext(pr *model.PR, sessions []*model.Session, attr *model.Attribution,
	links []model.CommitLink, cfg *config.Config) *Context {
	c := &Context{
		PR:       pr,
		Sessions: append([]*model.Session(nil), sessions...),
		Links:    append([]model.CommitLink(nil), links...),
	}
	if attr != nil {
		c.Attribution = attr
	} else {
		c.Attribution = &model.Attribution{}
	}
	if cfg != nil {
		c.Config = cfg
	} else {
		c.Config = config.Default()
	}
	c.Timeline = buildTimeline(c.Sessions)
	return c
}

func buildTimeline(sessions []*model.Session) []Item {
	var items []Item
	for _, s := range sessions {
		for i := range s.Events {
			items = append(items, Item{S: s, E: &s.Events[i]})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if !a.E.TS.Equal(b.E.TS) {
			return a.E.TS.Before(b.E.TS)
		}
		if ra, rb := a.S.Ref(), b.S.Ref(); ra != rb {
			return ra < rb
		}
		return a.E.Seq < b.E.Seq
	})
	return items
}

// Ref identifies it across all sessions.
func (c *Context) Ref(it Item) model.EventRef {
	return model.EventRef{Session: it.S.Ref(), Event: it.E.ID}
}

// oneLine replaces newlines and tabs with spaces and collapses runs of spaces.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// Evidence builds an Evidence pointing at it, with excerpt flattened to one
// line, clipped to 200 runes, and passed through redact.Text.
func (c *Context) Evidence(it Item, excerpt string) model.Evidence {
	return model.Evidence{
		Session: it.S.Ref(),
		Event:   it.E.ID,
		TS:      it.E.TS,
		Excerpt: redact.Text(model.Clip(oneLine(excerpt), 200)),
	}
}

// AnchorsFor returns the PR line ranges produced by any of the given events.
func (c *Context) AnchorsFor(items ...Item) []model.Anchor {
	if len(items) == 0 {
		return nil
	}
	refs := make(map[model.EventRef]bool, len(items))
	for _, it := range items {
		refs[c.Ref(it)] = true
	}
	return c.Attribution.AnchorsFor(refs)
}

// Of returns timeline items whose kind is one of kinds, in timeline order.
func (c *Context) Of(kinds ...model.EventKind) []Item {
	want := make(map[model.EventKind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	var out []Item
	for _, it := range c.Timeline {
		if want[it.E.Kind] {
			out = append(out, it)
		}
	}
	return out
}

// IsPRFile reports whether rel is a file touched by the PR's diff.
func (c *Context) IsPRFile(rel string) bool {
	return c.PR != nil && c.PR.File(rel) != nil
}

// HasSessions reports whether any session is linked to the PR.
func (c *Context) HasSessions() bool { return len(c.Sessions) > 0 }

// Session returns the linked session with the given Session.Ref(), or nil.
func (c *Context) Session(ref string) *model.Session {
	for _, s := range c.Sessions {
		if s.Ref() == ref {
			return s
		}
	}
	return nil
}

// Detector is one signal's detection logic. Implementations register
// themselves from init() with Register.
type Detector interface {
	ID() string
	Detect(c *Context) model.Signal
}

var (
	catalogIdx   = buildCatalogIdx()
	catalogOrder = buildCatalogOrder()
	registry     []Detector
	registryIdx  = map[string]bool{}
)

func buildCatalogIdx() map[string]Meta {
	m := make(map[string]Meta, len(catalog))
	for _, c := range catalog {
		m[c.ID] = c
	}
	return m
}

func buildCatalogOrder() map[string]int {
	m := make(map[string]int, len(catalog))
	for i, c := range catalog {
		m[c.ID] = i
	}
	return m
}

// Register adds d to the registry. Call it from init(). It panics if d's ID
// is not in the catalog, or if that ID was already registered.
func Register(d Detector) {
	id := d.ID()
	if _, ok := catalogIdx[id]; !ok {
		panic("engine: Register: unknown signal id " + id)
	}
	if registryIdx[id] {
		panic("engine: Register: duplicate detector id " + id)
	}
	registryIdx[id] = true
	registry = append(registry, d)
}

// Detectors returns every registered detector, in catalog order.
func Detectors() []Detector {
	out := append([]Detector(nil), registry...)
	sort.SliceStable(out, func(i, j int) bool {
		return catalogOrder[out[i].ID()] < catalogOrder[out[j].ID()]
	})
	return out
}

// applyMeta forces sig's catalog-controlled fields to match the catalog
// entry for id, regardless of what the detector set.
func applyMeta(sig *model.Signal, id string) {
	m := catalogIdx[id]
	sig.ID = id
	sig.Question = m.Question
	sig.Title = m.Title
	sig.Priority = m.Priority
	sig.Provenance = m.Provenance
}

// runOne runs d.Detect, recovering any panic into an Unknown signal.
func runOne(c *Context, d Detector) (sig model.Signal) {
	id := d.ID()
	defer func() {
		if r := recover(); r != nil {
			sig = Unknown(id, fmt.Sprintf("Detector failed: %v", r))
			if sig.Data == nil {
				sig.Data = map[string]any{}
			}
			sig.Data["error"] = fmt.Sprintf("%v", r)
		}
		applyMeta(&sig, id)
	}()
	sig = d.Detect(c)
	return sig
}

// fillSupport sets Signal.Support for every harness present in c.Sessions.
func fillSupport(c *Context, sigs []model.Signal) {
	present := map[model.Harness]bool{}
	hooked := map[model.Harness]bool{}
	for _, s := range c.Sessions {
		present[s.Harness] = true
		if s.Capture == model.CaptureHooked {
			hooked[s.Harness] = true
		}
	}
	if len(present) == 0 {
		return
	}
	for i := range sigs {
		row, ok := support[sigs[i].ID]
		if !ok {
			continue
		}
		m := make(map[string]string, len(present))
		for h := range present {
			lv := row[h]
			idx := 0
			if hooked[h] {
				idx = 1
			}
			m[string(h)] = lv[idx]
		}
		sigs[i].Support = m
	}
}

// Run runs every registered detector in catalog order, recovers panics, and
// fills Support. Catalog IDs without a registered detector are omitted.
func Run(c *Context) []model.Signal {
	dets := Detectors()
	sigs := make([]model.Signal, 0, len(dets))
	for _, d := range dets {
		sigs = append(sigs, runOne(c, d))
	}
	fillSupport(c, sigs)
	return sigs
}

// RunIDs is Run restricted to the given catalog IDs, in catalog order.
func RunIDs(c *Context, ids ...string) []model.Signal {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var dets []Detector
	for _, d := range Detectors() {
		if want[d.ID()] {
			dets = append(dets, d)
		}
	}
	sigs := make([]model.Signal, 0, len(dets))
	for _, d := range dets {
		sigs = append(sigs, runOne(c, d))
	}
	fillSupport(c, sigs)
	return sigs
}

// NewSignal returns a Signal pre-filled with id's catalog metadata and
// State: model.StateClear. It panics if id is not in the catalog.
func NewSignal(id string) model.Signal {
	m, ok := catalogIdx[id]
	if !ok {
		panic("engine: NewSignal: unknown signal id " + id)
	}
	return model.Signal{
		ID:         m.ID,
		Question:   m.Question,
		Title:      m.Title,
		Priority:   m.Priority,
		Provenance: m.Provenance,
		State:      model.StateClear,
	}
}

// Unknown returns a Signal with State: model.StateUnknown and Summary: reason.
func Unknown(id, reason string) model.Signal {
	sig := NewSignal(id)
	sig.State = model.StateUnknown
	sig.Summary = reason
	return sig
}

// NoSessions is Unknown(id, "No captured sessions for this PR.").
func NoSessions(id string) model.Signal {
	return Unknown(id, "No captured sessions for this PR.")
}
