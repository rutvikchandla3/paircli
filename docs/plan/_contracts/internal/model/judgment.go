package model

// Judgments is the validated output of the grounded LLM pass (internal/judge).
// Every item carries Cites: ids from the fact bundle ("ev:<session-ref>/<event-id>",
// "sig:<signal-id>", "file:<path>:<lines>"). The judge drops any item whose
// cites are empty or unknown before it reaches this struct.
type Judgments struct {
	Model           string           `json:"model"`
	AskSummary      *AskSummary      `json:"ask_summary,omitempty"`      // INT-1
	PlanDrift       []DriftItem      `json:"plan_drift,omitempty"`       // INT-3
	RuleViolations  []RuleViolation  `json:"rule_violations,omitempty"`  // INT-4
	Corrections     []CorrectionItem `json:"corrections,omitempty"`      // DEC-1
	Abandoned       []AbandonedItem  `json:"abandoned,omitempty"`        // DEC-2
	Decisions       []DecisionItem   `json:"decisions,omitempty"`        // DEC-3
	LostConstraints []LostConstraint `json:"lost_constraints,omitempty"` // FRI-3
	Claims          []ClaimVerdict   `json:"claims,omitempty"`           // CON-1
	Caveats         []CaveatItem     `json:"caveats,omitempty"`          // CON-2
	Scope           []ScopeItem      `json:"scope,omitempty"`            // CON-3
	Errors          []string         `json:"errors,omitempty"`           // jobs that failed, for the report footer
}

// AskSummary condenses the ask ledger.
type AskSummary struct {
	Ask         string   `json:"ask"`
	Refinements []string `json:"refinements,omitempty"`
	FinalScope  string   `json:"final_scope,omitempty"`
	Cites       []string `json:"cites"`
}

// DriftItem is a planned step without a matching change, or a change that was never planned.
type DriftItem struct {
	Kind  string   `json:"kind"` // "missing_step" | "unplanned_change"
	Text  string   `json:"text"`
	File  string   `json:"file,omitempty"`
	Lines string   `json:"lines,omitempty"`
	Cites []string `json:"cites"`
}

// RuleViolation is a diff hunk that appears to break a loaded rule.
type RuleViolation struct {
	Rule  string   `json:"rule"`
	File  string   `json:"file"`
	Lines string   `json:"lines,omitempty"`
	Cites []string `json:"cites"`
}

// CorrectionItem confirms or rejects a candidate correction prompt.
type CorrectionItem struct {
	Event        EventRef `json:"event"`
	IsCorrection bool     `json:"is_correction"`
	About        string   `json:"about,omitempty"`
	Cites        []string `json:"cites"`
}

// AbandonedItem summarizes an approach that was tried and dropped.
type AbandonedItem struct {
	Events  []EventRef `json:"events"`
	Summary string     `json:"summary"`
	Cites   []string   `json:"cites"`
}

// DecisionItem is one choice that shaped the diff.
type DecisionItem struct {
	Choice string   `json:"choice"`
	Reason string   `json:"reason"`
	By     string   `json:"by,omitempty"` // "human" | "agent"
	Cites  []string `json:"cites"`
}

// LostConstraint is an early instruction missing from a later compaction summary.
type LostConstraint struct {
	Constraint string   `json:"constraint"`
	Cites      []string `json:"cites"`
}

// ClaimVerdict checks one claim from the PR body or the agent's final message.
type ClaimVerdict struct {
	Claim   string   `json:"claim"`
	Source  string   `json:"source"`  // "pr_body" | "final_message"
	Verdict string   `json:"verdict"` // "supported" | "contradicted" | "no_evidence"
	Reason  string   `json:"reason"`
	Cites   []string `json:"cites"`
}

// CaveatItem is a limitation or assumption the agent stated.
type CaveatItem struct {
	Text  string   `json:"text"`
	Cites []string `json:"cites"`
}

// ScopeItem says whether a hunk traces back to the ask, plan or a decision.
type ScopeItem struct {
	File    string   `json:"file"`
	Lines   string   `json:"lines,omitempty"`
	Traced  bool     `json:"traced"`
	TraceTo string   `json:"trace_to,omitempty"`
	Cites   []string `json:"cites"`
}
