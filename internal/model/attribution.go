package model

import (
	"sort"
	"strconv"
)

// LineLabel says who wrote an added PR line.
type LineLabel string

const (
	LabelAgent      LineLabel = "agent"            // an agent edit produced this exact line
	LabelMixed      LineLabel = "agent_then_human" // close to an agent line, but changed afterwards
	LabelHuman      LineLabel = "human_in_session" // appeared between agent actions without an agent edit producing it
	LabelUncaptured LineLabel = "uncaptured"       // no captured session explains it
	LabelTrivial    LineLabel = "trivial"          // blank/punctuation-only line, or a generated file
)

// LineAttribution labels one added line of a PR file.
type LineAttribution struct {
	Line        int       `json:"line"` // new-side line number
	Label       LineLabel `json:"label"`
	Reformatted bool      `json:"reformatted,omitempty"` // matched only after loose normalization
	Source      *EventRef `json:"source,omitempty"`      // the event that produced the line
	Model       string    `json:"model,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
}

// FileAttribution is the attribution for one PR file.
type FileAttribution struct {
	Path   string            `json:"path"`
	Lines  []LineAttribution `json:"lines"`
	Counts map[LineLabel]int `json:"counts"`
}

// Attribution covers every added line in the PR.
type Attribution struct {
	Files     []FileAttribution `json:"files"`
	Total     int               `json:"total"`     // added lines excluding trivial ones
	Explained int               `json:"explained"` // agent + agent_then_human + human_in_session
}

// Ratio is Explained/Total, or 0 when Total is 0.
func (a *Attribution) Ratio() float64 {
	if a == nil || a.Total == 0 {
		return 0
	}
	return float64(a.Explained) / float64(a.Total)
}

// AnchorsFor returns the PR line ranges produced by any of the given events,
// one Anchor per contiguous run of lines, ordered by file then line.
func (a *Attribution) AnchorsFor(refs map[EventRef]bool) []Anchor {
	if a == nil || len(refs) == 0 {
		return nil
	}
	var out []Anchor
	for _, f := range a.Files {
		var nums []int
		for _, l := range f.Lines {
			if l.Source != nil && refs[*l.Source] {
				nums = append(nums, l.Line)
			}
		}
		out = append(out, rangesToAnchors(f.Path, nums)...)
	}
	return out
}

// LinesFor returns the PR line ranges in one file with any of the given labels.
func (a *Attribution) LinesFor(path string, labels ...LineLabel) []Anchor {
	if a == nil {
		return nil
	}
	want := map[LineLabel]bool{}
	for _, l := range labels {
		want[l] = true
	}
	for _, f := range a.Files {
		if f.Path != path {
			continue
		}
		var nums []int
		for _, l := range f.Lines {
			if want[l.Label] {
				nums = append(nums, l.Line)
			}
		}
		return rangesToAnchors(path, nums)
	}
	return nil
}

func rangesToAnchors(path string, nums []int) []Anchor {
	if len(nums) == 0 {
		return nil
	}
	sort.Ints(nums)
	var out []Anchor
	start, prev := nums[0], nums[0]
	flush := func() {
		lines := strconv.Itoa(start)
		if prev != start {
			lines += "-" + strconv.Itoa(prev)
		}
		out = append(out, Anchor{File: path, Lines: lines})
	}
	for _, n := range nums[1:] {
		if n == prev || n == prev+1 {
			prev = n
			continue
		}
		flush()
		start, prev = n, n
	}
	flush()
	return out
}
