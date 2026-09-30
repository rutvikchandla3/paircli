package judge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rutvikchandla3/paircli/internal/config"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/llm"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// fixture is a small but complete context: one session with an ask, a steering
// prompt, an edit, a question, an approved plan, a final message, a compaction
// and a rule, plus a PR with one file of two added lines.
type fixture struct {
	ec   *engine.Context
	sigs []model.Signal
	s    *model.Session

	askID   string // main-agent prompt
	steerID string // steering prompt
	editID  string
	qaID    string
	planID  string
	finalID string
	compID  string
	ruleID  string
	hunkID  string
}

func newFixture() *fixture {
	f := &fixture{}

	pr := testkit.PR("acme/shop", 7).
		Body("Fixes the retry bug. Tests pass.").
		Add("internal/api/client.go", 10, "func retry() {}", "\treturn nil").
		Build()

	s := testkit.Session(model.HarnessClaudeCode, "s1").
		Prompt("add a retry to the api client").
		Steer("keep the timeout at 5s").
		Edit("internal/api/client.go", "func retry() {}", "\treturn nil").
		Add(model.Event{Kind: model.KindQuestion, Question: &model.Question{
			Tool:  "ask_user",
			Items: []model.QA{{Header: "Retries", Question: "retry three times?", Answer: "yes"}},
		}}).
		Add(model.Event{Kind: model.KindPlan, Plan: &model.Plan{
			Text:     "1. add retry\n2. add backoff",
			Approved: model.BoolPtr(true),
			Source:   "exit_plan_mode",
		}}).
		Say("I added the retry but could not test the timeout path.").
		Add(model.Event{Kind: model.KindCompaction, Compaction: &model.Compaction{
			Trigger: "auto",
			Summary: "Added a retry to the api client.",
		}}).
		Add(model.Event{Kind: model.KindInstructions, Instructions: &model.Instructions{
			Path:    "/repo/CLAUDE.md",
			RelPath: "CLAUDE.md",
			Scope:   "project",
			Content: "Never log secrets. Keep the timeout at 5s.",
		}}).
		Build()

	f.s = s
	ref := s.Ref()
	f.askID = "ev:" + ref + "/e1"
	f.steerID = "ev:" + ref + "/e2"
	f.editID = "ev:" + ref + "/e3"
	f.qaID = "ev:" + ref + "/e4"
	f.planID = "ev:" + ref + "/e5"
	f.finalID = "ev:" + ref + "/e6"
	f.compID = "ev:" + ref + "/e7"
	f.ruleID = "ev:" + ref + "/e8"
	f.hunkID = "file:internal/api/client.go:10-11"

	f.ec = testkit.Ctx(pr, s)
	f.sigs = []model.Signal{
		{
			ID: "DEC-1", Question: model.QDecisions, State: model.StateAlert,
			Summary: "2 human corrections; 1 touched PR line.",
			Findings: []model.Finding{
				{
					Summary:  "Correction at 14:02: “no, use backoff”",
					Severity: model.StateAlert,
					Data:     map[string]any{"kind": "correction_prompt"},
					Evidence: []model.Evidence{{Session: ref, Event: "e2"}},
				},
				{
					Summary:  "Interrupted at 14:03.",
					Severity: model.StateInfo,
					Data:     map[string]any{"kind": "interrupt"},
					Evidence: []model.Evidence{{Session: ref, Event: "e3"}},
				},
				{
					Summary:  "Rejected a `bash` call at 14:04.",
					Severity: model.StateInfo,
					Data:     map[string]any{"kind": "rejection"},
					Evidence: []model.Evidence{{Session: ref, Event: "e4"}},
				},
			},
		},
		{
			ID: "DEC-2", Question: model.QDecisions, State: model.StateInfo,
			Summary: "1 abandoned attempt (reverted edits).",
			Findings: []model.Finding{{
				Summary:  "Code written in `internal/api/client.go` was later removed.",
				Severity: model.StateInfo,
				Data:     map[string]any{"kind": "reverted_edit"},
				Evidence: []model.Evidence{{Session: ref, Event: "e3"}},
			}},
		},
		{
			ID: "VER-2", Question: model.QVerification, State: model.StateClear,
			Summary: "Checks ran after the last change.",
			Findings: []model.Finding{{
				Summary:  "`go test ./...` passed at 14:03.",
				Severity: model.StateInfo,
				Evidence: []model.Evidence{{Session: ref, Event: "e3"}},
			}},
		},
		// Unknown signals must stay out of the bundle.
		{ID: "INT-1", Question: model.QIntent, State: model.StateUnknown, Summary: "No captured sessions for this PR."},
	}
	return f
}

// bundle builds the fact bundle for the fixture at the default budget.
func (f *fixture) bundle(t *testing.T) (*Bundle, map[string]bool, []byte) {
	t.Helper()
	b, ids := BuildBundle(f.ec, f.sigs, DefaultMaxChars)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return b, ids, raw
}

// spec returns the jobSpec with the given name.
func spec(t *testing.T, name string) jobSpec {
	t.Helper()
	for _, jb := range jobSpecs() {
		if jb.name == name {
			return jb
		}
	}
	t.Fatalf("no job named %q", name)
	return jobSpec{}
}

// runJob runs one job against the fixture bundle with the given fake replies.
func runJob(t *testing.T, name string, replies []string) (*model.Judgments, *llm.Fake, bool) {
	t.Helper()
	f := newFixture()
	_, ids, raw := f.bundle(t)
	j := &model.Judgments{Model: config.Default().LLM.Model}
	fake := &llm.Fake{Replies: replies}
	ok := spec(t, name).run(context.Background(), fake, raw, config.Default(), j, &validator{ids: ids, ec: f.ec})
	return j, fake, ok
}

func TestBuildBundle_IDsAndContent(t *testing.T) {
	f := newFixture()
	b, ids, _ := f.bundle(t)

	for _, want := range []string{f.askID, f.steerID, f.qaID, f.planID, f.finalID, f.compID, f.ruleID, f.hunkID, "sig:DEC-1", "sig:VER-2"} {
		if !ids[want] {
			t.Errorf("id %q missing from the bundle id set", want)
		}
	}
	if ids["sig:INT-1"] {
		t.Error("unknown signal INT-1 must not be in the bundle")
	}

	if b.PR.Title != "Test PR" || !strings.Contains(b.PR.Body, "retry bug") {
		t.Errorf("PR header = %+v", b.PR)
	}
	if len(b.PR.Files) != 1 || b.PR.Files[0].Added != 2 || b.PR.Files[0].Path != "internal/api/client.go" {
		t.Errorf("PR files = %+v", b.PR.Files)
	}

	if len(b.Asks) != 2 || b.Asks[0].ID != f.askID || b.Asks[0].Steering {
		t.Fatalf("asks = %+v", b.Asks)
	}
	if !b.Asks[1].Steering || b.Asks[1].ID != f.steerID {
		t.Errorf("steering ask = %+v", b.Asks[1])
	}

	if len(b.Decisions) != 1 {
		t.Fatalf("decisions = %+v", b.Decisions)
	}
	if b.Decisions[0].ID != f.qaID || b.Decisions[0].Answer != "yes" || b.Decisions[0].Question != "retry three times?" {
		t.Errorf("decision = %+v", b.Decisions[0])
	}

	if len(b.Plans) != 1 || !b.Plans[0].Approved || b.Plans[0].ID != f.planID {
		t.Errorf("plans = %+v", b.Plans)
	}

	if len(b.FinalMessages) != 1 || b.FinalMessages[0].ID != f.finalID || !strings.Contains(b.FinalMessages[0].Text, "timeout path") {
		t.Errorf("final messages = %+v", b.FinalMessages)
	}

	// corrections: DEC-1 correction prompts and interrupts only.
	if len(b.Corrections) != 2 {
		t.Fatalf("corrections = %+v", b.Corrections)
	}
	if b.Corrections[0].ID != f.steerID || b.Corrections[1].ID != f.editID {
		t.Errorf("correction ids = %q, %q", b.Corrections[0].ID, b.Corrections[1].ID)
	}

	// abandoned: DEC-2, keyed by its first evidence event.
	if len(b.Abandoned) != 1 || b.Abandoned[0].ID != f.editID {
		t.Errorf("abandoned = %+v", b.Abandoned)
	}

	if len(b.Compactions) != 1 || b.Compactions[0].ID != f.compID || !strings.Contains(b.Compactions[0].Text, "retry") {
		t.Errorf("compactions = %+v", b.Compactions)
	}
	if len(b.Rules) != 1 || b.Rules[0].ID != f.ruleID || !strings.Contains(b.Rules[0].Text, "Never log secrets") {
		t.Errorf("rules = %+v", b.Rules)
	}

	if len(b.Signals) != 3 {
		t.Fatalf("signals = %+v", b.Signals)
	}
	if b.Signals[0].ID != "sig:DEC-1" || b.Signals[0].State != "alert" || len(b.Signals[0].Findings) != 3 {
		t.Errorf("signal = %+v", b.Signals[0])
	}

	if len(b.Diff) != 1 || len(b.Diff[0].Hunks) != 1 {
		t.Fatalf("diff = %+v", b.Diff)
	}
	h := b.Diff[0].Hunks[0]
	if h.ID != f.hunkID {
		t.Errorf("hunk id = %q, want %q", h.ID, f.hunkID)
	}
	if h.Header != "@@ -0,0 +10,2 @@" {
		t.Errorf("hunk header = %q", h.Header)
	}
	if len(h.Added) != 2 || h.Added[0] != "func retry() {}" {
		t.Errorf("hunk added = %v", h.Added)
	}
}

// budgetFixture is a context with enough content that a small budget forces
// real trimming.
func budgetFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	pr := testkit.PR("acme/shop", 9).Body("Big change.")
	pb := pr
	for i := 0; i < 20; i++ {
		lines := make([]string, 0, 40)
		for j := 0; j < 40; j++ {
			lines = append(lines, "some added line of source code number "+strings.Repeat("x", 10))
		}
		pb = pb.Add("dir/file"+string(rune('a'+i))+".go", 1, lines...)
	}
	built := pb.Build()

	s := testkit.Session(model.HarnessClaudeCode, "s1")
	for i := 0; i < 3; i++ {
		s = s.Prompt(strings.Repeat("ask ", 300))
	}
	s = s.Say(strings.Repeat("final ", 500)).
		Add(model.Event{Kind: model.KindCompaction, Compaction: &model.Compaction{Summary: strings.Repeat("summary ", 400)}}).
		Add(model.Event{Kind: model.KindInstructions, Instructions: &model.Instructions{Path: "/repo/CLAUDE.md", Content: strings.Repeat("rule ", 600)}})

	f.ec = testkit.Ctx(built, s.Build())
	f.sigs = nil
	return f
}

func TestBuildBundle_Budget(t *testing.T) {
	f := budgetFixture(t)
	full, _ := BuildBundle(f.ec, f.sigs, DefaultMaxChars)
	fullRaw, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(fullRaw) <= 1500 {
		t.Fatalf("fixture too small to force trimming: %d chars", len(fullRaw))
	}

	// full - 1 byte: only the first documented step (hunk lines to 10) is needed.
	b, _ := BuildBundle(f.ec, f.sigs, len(fullRaw)-1)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) > len(fullRaw)-1 {
		t.Fatalf("bundle did not fit: %d > %d", len(raw), len(fullRaw)-1)
	}
	for _, df := range b.Diff {
		for _, h := range df.Hunks {
			if len(h.Added) != 10 {
				t.Fatalf("hunk %s kept %d added lines, want the documented trim to 10", h.ID, len(h.Added))
			}
		}
	}
	// Later steps must not have run yet: asks are still at their 1000-rune limit.
	if got := utf8.RuneCountInString(b.Asks[0].Text); got != 1001 {
		t.Errorf("ask trimmed to %d runes, want the untrimmed 1000+ellipsis", got)
	}

	// A much smaller budget trims further, still in the documented order.
	small := 2000
	b2, ids2 := BuildBundle(f.ec, f.sigs, small)
	raw2, err := json.Marshal(b2)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw2) > small {
		t.Fatalf("small bundle did not fit: %d > %d", len(raw2), small)
	}
	if len(ids2) == 0 {
		t.Error("small bundle returned no ids")
	}
	for _, df := range b2.Diff {
		for _, h := range df.Hunks {
			if len(h.Added) > 10 {
				t.Errorf("hunk %s kept %d added lines", h.ID, len(h.Added))
			}
		}
	}
	if len(b2.Diff) > 15 {
		t.Errorf("kept %d diff files, want at most 15", len(b2.Diff))
	}
	for _, a := range b2.Asks {
		if utf8.RuneCountInString(a.Text) > 301 {
			t.Errorf("ask kept %d runes, want at most 300 plus ellipsis", utf8.RuneCountInString(a.Text))
		}
	}
	for _, r := range b2.Rules {
		if utf8.RuneCountInString(r.Text) > 501 {
			t.Errorf("rule kept %d runes", utf8.RuneCountInString(r.Text))
		}
	}
	for _, c := range b2.Compactions {
		if utf8.RuneCountInString(c.Text) > 1001 {
			t.Errorf("compaction kept %d runes", utf8.RuneCountInString(c.Text))
		}
	}
}

func TestJob_Claims_Valid(t *testing.T) {
	f := newFixture()
	_, ids, raw := f.bundle(t)
	reply := `{"claims":[{"claim":"the client retries","source":"pr_body","verdict":"supported","reason":"the diff adds retry()","cites":["` + f.hunkID + `","sig:VER-2"]}]}`

	j := &model.Judgments{}
	fake := &llm.Fake{Replies: []string{reply}}
	if !spec(t, "claims").run(context.Background(), fake, raw, config.Default(), j, &validator{ids: ids, ec: f.ec}) {
		t.Fatalf("job failed: %v", j.Errors)
	}
	if len(j.Claims) != 1 {
		t.Fatalf("claims = %+v", j.Claims)
	}
	c := j.Claims[0]
	if c.Claim != "the client retries" || c.Source != "pr_body" || c.Verdict != "supported" {
		t.Errorf("claim = %+v", c)
	}
	if len(c.Cites) != 2 || c.Cites[0] != f.hunkID {
		t.Errorf("cites = %v", c.Cites)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("made %d provider calls, want 1", len(fake.Calls))
	}
	req := fake.Calls[0]
	if req.System != systemPrompt {
		t.Error("request did not use the shared system prompt")
	}
	if req.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", req.MaxTokens)
	}
	if req.Model != config.Default().LLM.Model {
		t.Errorf("Model = %q, want %q", req.Model, config.Default().LLM.Model)
	}
	if !strings.Contains(req.Prompt, "\n\nFACT BUNDLE:\n") {
		t.Error("prompt does not embed the fact bundle header")
	}
	if !strings.Contains(req.Prompt, f.askID) {
		t.Error("prompt does not embed the bundle JSON")
	}
}

func TestJob_DropsUncited(t *testing.T) {
	f := newFixture()
	_, ids, raw := f.bundle(t)
	reply := `{"claims":[
		{"claim":"uncited","source":"pr_body","verdict":"supported","reason":"r","cites":[]},
		{"claim":"cited","source":"final_message","verdict":"no_evidence","reason":"r","cites":["` + f.finalID + `"]}
	]}`
	j := &model.Judgments{}
	if !spec(t, "claims").run(context.Background(), &llm.Fake{Replies: []string{reply}}, raw, config.Default(), j, &validator{ids: ids, ec: f.ec}) {
		t.Fatalf("job failed: %v", j.Errors)
	}
	if len(j.Claims) != 1 || j.Claims[0].Claim != "cited" {
		t.Errorf("claims = %+v", j.Claims)
	}
}

func TestJob_DropsUnknownCite(t *testing.T) {
	f := newFixture()
	_, ids, raw := f.bundle(t)
	reply := `{"caveats":[
		{"text":"ghost event","cites":["ev:ghost/nope"]},
		{"text":"ghost signal","cites":["sig:CON-9"]},
		{"text":"real","cites":["` + f.finalID + `"]}
	]}`
	j := &model.Judgments{}
	if !spec(t, "caveats").run(context.Background(), &llm.Fake{Replies: []string{reply}}, raw, config.Default(), j, &validator{ids: ids, ec: f.ec}) {
		t.Fatalf("job failed: %v", j.Errors)
	}
	if len(j.Caveats) != 1 || j.Caveats[0].Text != "real" {
		t.Errorf("caveats = %+v", j.Caveats)
	}
}

func TestJob_BadEnumDropped(t *testing.T) {
	f := newFixture()
	_, ids, raw := f.bundle(t)
	// Bad verdict, bad source, bad "by", bad drift kind, and a non-PR file.
	reply := `{
		"claims":[
			{"claim":"bad verdict","source":"pr_body","verdict":"maybe","reason":"r","cites":["` + f.askID + `"]},
			{"claim":"bad source","source":"commit","verdict":"supported","reason":"r","cites":["` + f.askID + `"]},
			{"claim":"good","source":"pr_body","verdict":"contradicted","reason":"r","cites":["` + f.askID + `"]}
		]}`
	j := &model.Judgments{}
	if !spec(t, "claims").run(context.Background(), &llm.Fake{Replies: []string{reply}}, raw, config.Default(), j, &validator{ids: ids, ec: f.ec}) {
		t.Fatalf("job failed: %v", j.Errors)
	}
	if len(j.Claims) != 1 || j.Claims[0].Claim != "good" {
		t.Errorf("claims = %+v", j.Claims)
	}

	// story: bad "by" is dropped, bad event id is dropped.
	story := `{"decisions":[{"choice":"c","reason":"r","by":"robot","cites":["` + f.planID + `"]},{"choice":"ok","reason":"r","by":"agent","cites":["` + f.planID + `"]}],
		"corrections":[{"event":"ev:ghost/x","is_correction":true,"about":"a","cites":["` + f.steerID + `"]},{"event":"` + f.steerID + `","is_correction":true,"about":"real","cites":["` + f.steerID + `"]}]}`
	j2 := &model.Judgments{}
	if !spec(t, "story").run(context.Background(), &llm.Fake{Replies: []string{story}}, raw, config.Default(), j2, &validator{ids: ids, ec: f.ec}) {
		t.Fatalf("job failed: %v", j2.Errors)
	}
	if len(j2.Decisions) != 1 || j2.Decisions[0].By != "agent" {
		t.Errorf("decisions = %+v", j2.Decisions)
	}
	if len(j2.Corrections) != 1 || !j2.Corrections[0].IsCorrection {
		t.Fatalf("corrections = %+v", j2.Corrections)
	}
	if j2.Corrections[0].Event.Session != "claude-code:s1" || j2.Corrections[0].Event.Event != "e2" {
		t.Errorf("correction ref = %+v", j2.Corrections[0].Event)
	}

	// scope: bad drift kind and a file outside the PR are dropped.
	scope := `{"plan_drift":[{"kind":"sideways","text":"t","file":"","lines":"","cites":["` + f.planID + `"]},
		{"kind":"missing_step","text":"t","file":"other.go","lines":"","cites":["` + f.planID + `"]},
		{"kind":"unplanned_change","text":"t","file":"internal/api/client.go","lines":"10-11","cites":["` + f.hunkID + `"]}],
		"scope":[{"file":"internal/api/client.go","lines":"10x","traced":true,"trace_to":"a","cites":["` + f.hunkID + `"]}]}`
	j3 := &model.Judgments{}
	if !spec(t, "scope").run(context.Background(), &llm.Fake{Replies: []string{scope}}, raw, config.Default(), j3, &validator{ids: ids, ec: f.ec}) {
		t.Fatalf("job failed: %v", j3.Errors)
	}
	if len(j3.PlanDrift) != 1 || j3.PlanDrift[0].Kind != "unplanned_change" {
		t.Errorf("plan drift = %+v", j3.PlanDrift)
	}
	if len(j3.Scope) != 0 {
		t.Errorf("scope = %+v, want the bad lines value dropped", j3.Scope)
	}
}

func TestJob_FencedJSON(t *testing.T) {
	f := newFixture()
	_, ids, raw := f.bundle(t)
	reply := "```json\n" + `{"caveats":[{"text":"timeout path untested","cites":["` + f.finalID + `"]}]}` + "\n```"
	j := &model.Judgments{}
	if !spec(t, "caveats").run(context.Background(), &llm.Fake{Replies: []string{reply}}, raw, config.Default(), j, &validator{ids: ids, ec: f.ec}) {
		t.Fatalf("job failed: %v", j.Errors)
	}
	if len(j.Caveats) != 1 || j.Caveats[0].Text != "timeout path untested" {
		t.Errorf("caveats = %+v", j.Caveats)
	}
}

func TestJob_RetryOnInvalidJSON(t *testing.T) {
	f := newFixture()
	_, ids, raw := f.bundle(t)
	good := `{"lost_constraints":[{"constraint":"keep the timeout at 5s","cites":["` + f.askID + `","` + f.steerID + `"]}]}`
	fake := &llm.Fake{Replies: []string{"I think the answer is ...", good}}
	j := &model.Judgments{}
	if !spec(t, "caveats").run(context.Background(), fake, raw, config.Default(), j, &validator{ids: ids, ec: f.ec}) {
		t.Fatalf("job failed: %v", j.Errors)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("made %d calls, want one retry", len(fake.Calls))
	}
	if !strings.HasSuffix(fake.Calls[1].Prompt, retryPrompt) {
		t.Errorf("retry prompt = %q", fake.Calls[1].Prompt)
	}
	if !strings.HasPrefix(fake.Calls[1].Prompt, fake.Calls[0].Prompt) {
		t.Error("retry prompt must append to the original prompt")
	}
	if len(j.LostConstraints) != 1 || j.LostConstraints[0].Constraint != "keep the timeout at 5s" {
		t.Errorf("lost constraints = %+v", j.LostConstraints)
	}
}

func TestJob_InvalidTwiceRecordsError(t *testing.T) {
	f := newFixture()
	_, ids, raw := f.bundle(t)
	fake := &llm.Fake{Replies: []string{"not json", "still not json"}}
	j := &model.Judgments{}
	if ok := spec(t, "claims").run(context.Background(), fake, raw, config.Default(), j, &validator{ids: ids, ec: f.ec}); ok {
		t.Fatal("job reported success on two invalid replies")
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("made %d calls, want one retry", len(fake.Calls))
	}
	if len(j.Errors) != 1 || j.Errors[0] != "claims: invalid JSON" {
		t.Errorf("errors = %v", j.Errors)
	}
	if len(j.Claims) != 0 {
		t.Errorf("claims = %+v, want none", j.Claims)
	}
}

func TestRun_ProviderErrorContinues(t *testing.T) {
	f := newFixture()
	cfg := config.Default()
	claims := `{"claims":[{"claim":"retry added","source":"pr_body","verdict":"supported","reason":"diff","cites":["` + f.hunkID + `"]}]}`
	story := `{"ask_summary":{"ask":"add a retry","refinements":["keep the timeout"],"final_scope":"retry","cites":["` + f.askID + `"]}}`
	scope := `{"scope":[{"file":"internal/api/client.go","lines":"10-11","traced":true,"trace_to":"add a retry","cites":["` + f.hunkID + `"]}]}`

	// Three replies for four jobs: the last job (caveats) gets a provider error.
	fake := &llm.Fake{Replies: []string{claims, story, scope}}
	j, err := Run(context.Background(), fake, f.ec, f.sigs, cfg)
	if err != nil {
		t.Fatalf("Run returned an error although only one job failed: %v", err)
	}
	if j == nil {
		t.Fatal("Run returned no judgments")
	}
	if j.Model != cfg.LLM.Model {
		t.Errorf("Model = %q, want %q", j.Model, cfg.LLM.Model)
	}
	if len(j.Errors) != 1 || !strings.HasPrefix(j.Errors[0], "caveats: ") {
		t.Errorf("errors = %v", j.Errors)
	}
	if len(j.Claims) != 1 || j.AskSummary == nil || len(j.Scope) != 1 {
		t.Errorf("other jobs' results missing: %+v", j)
	}
}

func TestRun_NilProvider(t *testing.T) {
	f := newFixture()
	j, err := Run(context.Background(), nil, f.ec, f.sigs, config.Default())
	if err != nil {
		t.Fatalf("Run(nil provider) = %v", err)
	}
	if j != nil {
		t.Errorf("Run(nil provider) = %+v, want nil", j)
	}
}

func TestPrompts_ContainSystemRules(t *testing.T) {
	const want = "You review evidence about how a pull request was produced with AI coding agents.\n" +
		"Use only the facts in the JSON fact bundle. Every item you output must cite one\n" +
		"or more ids that appear in the bundle (\"id\" fields). If the bundle does not\n" +
		"support a statement, leave it out. Do not judge the author's skill, speed or\n" +
		"intent. Reply with a single JSON object that matches the requested schema and\n" +
		"nothing else."
	if systemPrompt != want {
		t.Errorf("system prompt drifted from the spec:\n%q\nwant\n%q", systemPrompt, want)
	}

	f := newFixture()
	cfg := config.Default()
	_, _, raw := f.bundle(t)
	for _, jb := range jobSpecs() {
		if jb.prompt == "" || !strings.Contains(jb.prompt, "Job: "+jb.name) {
			t.Errorf("job %q has no instructions", jb.name)
		}
		req := jb.request(cfg, raw)
		if req.System != want {
			t.Errorf("job %q does not use the shared system prompt", jb.name)
		}
		if req.MaxTokens != 4096 || req.Model != cfg.LLM.Model {
			t.Errorf("job %q request = %+v", jb.name, req)
		}
		if !strings.Contains(req.Prompt, jb.prompt) || !strings.Contains(req.Prompt, "FACT BUNDLE:") {
			t.Errorf("job %q prompt is not instructions + bundle", jb.name)
		}
	}
}

func TestValidator_EventRefs(t *testing.T) {
	f := newFixture()
	_, ids, _ := f.bundle(t)
	v := &validator{ids: ids, ec: f.ec}

	if _, ok := v.eventRef(f.steerID); !ok {
		t.Error("valid event id rejected")
	}
	if _, ok := v.eventRef("ev:ghost/x"); ok {
		t.Error("unknown event id accepted")
	}
	if _, ok := v.eventRefs(nil); !ok {
		t.Error("empty event list must be accepted")
	}
	if _, ok := v.eventRefs([]string{"ev:ghost/x"}); ok {
		t.Error("event list with no valid id must be rejected")
	}
	refs, ok := v.eventRefs([]string{"ev:ghost/x", f.steerID})
	if !ok || len(refs) != 1 || refs[0].Event != "e2" {
		t.Errorf("eventRefs = %+v, %v", refs, ok)
	}
}

// TestHunkLinesOK pins the range check: a range must lie inside a real hunk of
// the PR diff, not merely look like one.
func TestHunkLinesOK(t *testing.T) {
	f := newFixture()
	v := &validator{ec: f.ec}

	// The fixture's only file adds two lines at line 10, so its hunk spans 10-11.
	cases := []struct {
		name  string
		file  string
		lines string
		want  bool
	}{
		{"exact hunk range", "internal/api/client.go", "10-11", true},
		{"single line inside hunk", "internal/api/client.go", "10", true},
		{"last line of hunk", "internal/api/client.go", "11", true},
		{"range starts before hunk", "internal/api/client.go", "9-10", false},
		{"range extends past hunk", "internal/api/client.go", "10-12", false},
		{"well-formed range matching no hunk", "internal/api/client.go", "99-100", false},
		{"unknown file", "internal/api/ghost.go", "10-11", false},
		{"backwards range", "internal/api/client.go", "11-10", false},
		{"empty file and lines is not checked", "", "", true},
		{"empty lines is not checked", "internal/api/client.go", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := v.hunkLinesOK(tc.file, tc.lines); got != tc.want {
				t.Errorf("hunkLinesOK(%q, %q) = %v, want %v", tc.file, tc.lines, got, tc.want)
			}
		})
	}
}

// TestApplyScope_DropsFakeRange checks the check is wired into the scope job and
// that the item counters record what was offered versus what survived.
func TestApplyScope_DropsFakeRange(t *testing.T) {
	f := newFixture()
	_, ids, _ := f.bundle(t)
	stats := newStats()
	v := &validator{ids: ids, ec: f.ec, stats: stats}

	reply := &scopeReply{Scope: []scopeItemReply{
		{File: "internal/api/client.go", Lines: "10-11", Traced: true, Cites: []string{f.hunkID}},
		{File: "internal/api/client.go", Lines: "10", Traced: true, Cites: []string{f.hunkID}},
		{File: "internal/api/client.go", Lines: "99-100", Traced: true, Cites: []string{f.hunkID}},
	}}
	j := &model.Judgments{}
	applyScope(j, reply, v)

	if len(j.Scope) != 2 {
		t.Fatalf("kept %d scope items, want 2 (exact range, sub-range): %+v", len(j.Scope), j.Scope)
	}
	if got, want := stats.Items[kindScope], [2]int{3, 2}; got != want {
		t.Errorf("scope stats = %v, want %v", got, want)
	}
}

// TestApplyScope_EmptyFileDriftKept checks a drift item with no file and no
// lines is still accepted: a missing plan step legitimately has neither.
func TestApplyScope_EmptyFileDriftKept(t *testing.T) {
	f := newFixture()
	_, ids, _ := f.bundle(t)
	stats := newStats()
	v := &validator{ids: ids, ec: f.ec, stats: stats}

	reply := &scopeReply{PlanDrift: []driftReply{
		{Kind: "missing_step", Text: "add backoff", Cites: []string{f.planID}},
	}}
	j := &model.Judgments{}
	applyScope(j, reply, v)

	if len(j.PlanDrift) != 1 {
		t.Fatalf("kept %d drift items, want 1: %+v", len(j.PlanDrift), j.PlanDrift)
	}
}

// TestStatsUnresolved pins which signals an unusable judgment input marks
// unknown: only the three inferred ones, and only when the input was genuinely
// unusable rather than legitimately empty.
func TestStatsUnresolved(t *testing.T) {
	cases := []struct {
		name     string
		stats    *Stats
		want     []string // signal IDs, in inferredSources order
		attempts []int
	}{
		{
			name:  "nothing wrong",
			stats: newStats(),
		},
		{
			name: "claims job failed marks CON-1",
			stats: &Stats{
				Jobs:  map[string]bool{"claims": true, "story": true, "scope": true, "caveats": true},
				Items: map[string][2]int{},
			},
			want: []string{"CON-1", "CON-3", "DEC-3"},
		},
		{
			name: "all scope items dropped marks CON-3",
			stats: &Stats{
				Jobs:  map[string]bool{},
				Items: map[string][2]int{kindScope: {3, 0}},
			},
			want:     []string{"CON-3"},
			attempts: []int{3},
		},
		{
			name: "a legitimately empty reply stays clear",
			stats: &Stats{
				Jobs: map[string]bool{},
				Items: map[string][2]int{
					kindClaims:    {0, 0},
					kindScope:     {0, 0},
					kindDecisions: {0, 0},
				},
			},
		},
		{
			name: "a partly usable reply stays clear",
			stats: &Stats{
				Jobs:  map[string]bool{},
				Items: map[string][2]int{kindScope: {4, 1}},
			},
		},
		{
			name: "a failed job that feeds no inferred signal marks nothing",
			stats: &Stats{
				Jobs:  map[string]bool{"caveats": true},
				Items: map[string][2]int{},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.stats.Unresolved()
			if len(got) != len(tc.want) {
				t.Fatalf("unresolved = %+v, want %v", got, tc.want)
			}
			for i, u := range got {
				if u.Signal != tc.want[i] {
					t.Errorf("unresolved[%d].Signal = %q, want %q", i, u.Signal, tc.want[i])
				}
				if u.Reason == "" {
					t.Errorf("unresolved[%d] has no reason", i)
				}
				if len(tc.attempts) > i && u.Attempted != tc.attempts[i] {
					t.Errorf("unresolved[%d].Attempted = %d, want %d", i, u.Attempted, tc.attempts[i])
				}
			}
		})
	}
}

// TestRunFull_DroppedScopeIsUnresolved checks the wiring: a scope reply whose
// only item names a range no hunk covers leaves CON-3 unresolved, while the
// empty claims and story replies leave CON-1 and DEC-3 alone.
func TestRunFull_DroppedScopeIsUnresolved(t *testing.T) {
	f := newFixture()
	cfg := config.Default()

	claims := `{"claims":[]}`
	story := `{}`
	scope := `{"scope":[{"file":"internal/api/client.go","lines":"99-100","traced":true,"trace_to":"x","cites":["` + f.hunkID + `"]}]}`
	caveats := `{}`
	fake := &llm.Fake{Replies: []string{claims, story, scope, caveats}}

	j, stats, err := RunFull(context.Background(), fake, f.ec, f.sigs, cfg)
	if err != nil {
		t.Fatalf("RunFull: %v", err)
	}
	if j == nil {
		t.Fatal("no judgments")
	}
	if len(j.Scope) != 0 {
		t.Fatalf("scope = %+v, want the fake range dropped", j.Scope)
	}

	got := stats.Unresolved()
	if len(got) != 1 || got[0].Signal != "CON-3" {
		t.Fatalf("unresolved = %+v, want only CON-3", got)
	}
	if got[0].Attempted != 1 {
		t.Errorf("Attempted = %d, want 1", got[0].Attempted)
	}
	if stats.Failed() != 0 {
		t.Errorf("Failed() = %d, want 0: every job answered", stats.Failed())
	}
}

// TestRunFull_AllJobsFailedNamesTheReason checks the error a caller records on
// the report. "4 jobs failed" on its own is not a diagnosis: the reason (an
// unauthenticated provider, an unreachable gateway) is the only thing that says
// what to do about it.
func TestRunFull_AllJobsFailedNamesTheReason(t *testing.T) {
	f := newFixture()
	// An exhausted provider fails every job, and every retry with it.
	fake := &llm.Fake{}

	j, stats, err := RunFull(context.Background(), fake, f.ec, f.sigs, config.Default())
	if err == nil {
		t.Fatalf("RunFull: expected an error when every job fails (j=%v, stats=%v)", j, stats)
	}
	if !strings.Contains(err.Error(), "no more replies") {
		t.Errorf("err = %v, want it to carry the provider's own error", err)
	}
	for _, name := range []string{"claims", "story", "scope", "caveats"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("err = %v, want it to name the %s job", err, name)
		}
	}
}
