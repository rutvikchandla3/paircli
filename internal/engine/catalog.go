package engine

import "github.com/rutvikchandla3/paircli/internal/model"

// Meta is the fixed catalog metadata for one signal (docs/SIGNALS.md).
type Meta struct {
	ID         string
	Question   model.ReviewQuestion
	Title      string
	Priority   model.Priority
	Provenance model.Provenance
}

var catalog = []Meta{
	{"INT-1", model.QIntent, "Ask ledger", model.P0, model.Derived},
	{"INT-2", model.QIntent, "Human-answered decisions", model.P0, model.Recorded},
	{"INT-3", model.QIntent, "Approved plan and plan drift", model.P0, model.Recorded},
	{"INT-4", model.QIntent, "Rules in effect", model.P1, model.Recorded},
	{"AUTH-1", model.QAuthorship, "Line-level authorship", model.P0, model.Derived},
	{"AUTH-2", model.QAuthorship, "Session-commit map and coverage", model.P0, model.Derived},
	{"AUTH-3", model.QAuthorship, "Model mix and disclosure trailer", model.P1, model.Recorded},
	{"VER-1", model.QVerification, "Verification log", model.P0, model.Recorded},
	{"VER-2", model.QVerification, "Verification freshness", model.P0, model.Derived},
	{"VER-3", model.QVerification, "Check-to-change coverage", model.P1, model.Derived},
	{"VER-4", model.QVerification, "Test integrity", model.P0, model.Derived},
	{"VER-5", model.QVerification, "Quality-gate bypasses", model.P0, model.Derived},
	{"VER-6", model.QVerification, "Runtime and visual evidence", model.P1, model.Recorded},
	{"DEC-1", model.QDecisions, "Human corrections", model.P0, model.Recorded},
	{"DEC-2", model.QDecisions, "Abandoned approaches", model.P1, model.Derived},
	{"DEC-3", model.QDecisions, "Decision trail", model.P1, model.Inferred},
	{"FRI-1", model.QFriction, "Churn hotspots", model.P1, model.Derived},
	{"FRI-2", model.QFriction, "Loops and unresolved errors", model.P1, model.Derived},
	{"FRI-3", model.QFriction, "Context resets", model.P1, model.Recorded},
	{"FRI-4", model.QFriction, "Knowledge lookups", model.P2, model.Recorded},
	{"EXP-1", model.QExposure, "Side-effect ledger", model.P0, model.Derived},
	{"EXP-2", model.QExposure, "Dependency provenance", model.P0, model.Derived},
	{"EXP-3", model.QExposure, "Untrusted-input chain", model.P0, model.Derived},
	{"EXP-4", model.QExposure, "Secret contact", model.P1, model.Derived},
	{"OVS-1", model.QOversight, "Autonomy envelope", model.P0, model.Recorded},
	{"OVS-2", model.QOversight, "Human touchpoints", model.P1, model.Derived},
	{"OVS-3", model.QOversight, "Delegated work", model.P2, model.Recorded},
	{"CON-1", model.QConsistency, "Claim check", model.P0, model.Inferred},
	{"CON-2", model.QConsistency, "Open loops", model.P1, model.Recorded},
	{"CON-3", model.QConsistency, "Scope match", model.P1, model.Inferred},
}

// support[id][harness] = {transcript-only level, hooked level}; see docs/SIGNALS.md "Harness support".
type level [2]string

const (
	full    = "full"
	partial = "partial"
	none    = "none"
)

var (
	ff = level{full, full}
	pf = level{partial, full}
	pp = level{partial, partial}
	np = level{none, partial}
)

func row(cc, cx, pi level) map[model.Harness]level {
	return map[model.Harness]level{model.HarnessClaudeCode: cc, model.HarnessCodex: cx, model.HarnessPi: pi}
}

var support = map[string]map[model.Harness]level{
	"INT-1": row(ff, ff, ff), "INT-2": row(ff, ff, pf), "INT-3": row(ff, pp, np), "INT-4": row(pf, ff, pf),
	"AUTH-1": row(ff, pf, pf), "AUTH-2": row(pf, pf, pf), "AUTH-3": row(ff, ff, ff),
	"VER-1": row(ff, ff, pf), "VER-2": row(ff, ff, pf), "VER-3": row(pp, pp, pp),
	"VER-4": row(ff, ff, ff), "VER-5": row(ff, ff, ff), "VER-6": row(ff, pp, pp),
	"DEC-1": row(ff, ff, pf), "DEC-2": row(pf, pp, ff), "DEC-3": row(ff, ff, pp),
	"FRI-1": row(ff, ff, ff), "FRI-2": row(ff, ff, ff), "FRI-3": row(ff, ff, ff), "FRI-4": row(ff, ff, ff),
	"EXP-1": row(ff, ff, ff), "EXP-2": row(ff, ff, ff), "EXP-3": row(ff, ff, ff), "EXP-4": row(ff, ff, ff),
	"OVS-1": row(ff, ff, pf), "OVS-2": row(ff, ff, ff), "OVS-3": row(ff, pf, pp),
	"CON-1": row(ff, ff, pp), "CON-2": row(ff, ff, pp), "CON-3": row(ff, ff, pp),
}

// Catalog returns the 30 signals in catalog order.
func Catalog() []Meta { return append([]Meta(nil), catalog...) }
