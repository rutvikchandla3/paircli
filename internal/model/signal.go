package model

import "time"

// ReviewQuestion is the reviewer question a signal answers.
type ReviewQuestion string

const (
	QIntent       ReviewQuestion = "intent"
	QAuthorship   ReviewQuestion = "authorship"
	QVerification ReviewQuestion = "verification"
	QDecisions    ReviewQuestion = "decisions"
	QFriction     ReviewQuestion = "friction"
	QExposure     ReviewQuestion = "exposure"
	QOversight    ReviewQuestion = "oversight"
	QConsistency  ReviewQuestion = "consistency"
)

// ReviewQuestions lists the questions in report order.
var ReviewQuestions = []ReviewQuestion{QIntent, QAuthorship, QVerification, QDecisions, QFriction, QExposure, QOversight, QConsistency}

// Priority is the build/display priority from the catalog.
type Priority string

const (
	P0 Priority = "P0"
	P1 Priority = "P1"
	P2 Priority = "P2"
)

// Provenance says how a signal or finding was produced.
type Provenance string

const (
	Recorded Provenance = "recorded" // the harness wrote it down
	Derived  Provenance = "derived"  // deterministic code over recorded events
	Inferred Provenance = "inferred" // grounded LLM judgment, always cited
)

// State is the signal's outcome.
type State string

const (
	StateAlert   State = "alert"   // a reviewer should look at this
	StateInfo    State = "info"    // useful context, nothing wrong
	StateClear   State = "clear"   // checked, nothing found
	StateUnknown State = "unknown" // no data (no sessions, harness can't provide it, LLM off)
)

// Signal is one catalog signal's result for a PR. Every detector returns
// exactly one Signal whose ID equals the detector's ID.
type Signal struct {
	ID         string            `json:"id"` // e.g. "VER-2"
	Question   ReviewQuestion    `json:"question"`
	Title      string            `json:"title"`
	Priority   Priority          `json:"priority"`
	Provenance Provenance        `json:"provenance"`
	State      State             `json:"state"`
	Summary    string            `json:"summary"` // one line, at most 160 characters
	Findings   []Finding         `json:"findings,omitempty"`
	Support    map[string]string `json:"support,omitempty"` // harness -> "full" | "partial" | "none"; filled by the engine
	Data       map[string]any    `json:"data,omitempty"`    // signal-specific payload, documented per signal
}

// Finding is one specific thing a signal found.
type Finding struct {
	Summary    string         `json:"summary"`
	Severity   State          `json:"severity"`             // StateAlert or StateInfo
	Provenance Provenance     `json:"provenance,omitempty"` // set only when it differs from the signal's
	Anchors    []Anchor       `json:"anchors,omitempty"`
	Evidence   []Evidence     `json:"evidence,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

// Anchor points at PR lines. Lines is "40" or "40-45"; empty means the whole file.
type Anchor struct {
	File  string `json:"file"`
	Lines string `json:"lines,omitempty"`
}

// Evidence points at the session event a finding is based on.
type Evidence struct {
	Session string    `json:"session"` // Session.Ref()
	Event   string    `json:"event"`   // Event.ID
	TS      time.Time `json:"ts"`
	Excerpt string    `json:"excerpt,omitempty"` // at most 200 characters
}
