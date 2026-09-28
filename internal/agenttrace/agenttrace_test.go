package agenttrace

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// update rewrites the golden file in testdata/golden. Update with
// `go test ./internal/agenttrace/... -update`.
var update = flag.Bool("update", false, "rewrite golden files")

var fixedNow = time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC)

// specRequired is the required-member list of the Agent Trace specification,
// version 0.1.0 (https://agent-trace.dev/ §6.1 "Trace Record Schema"), read
// off the published JSON Schema. Each key names a schema node; the value is the
// exact list of members that node declares "required". Nodes with no required
// members are listed too, so a future read of the spec has a place to record
// them.
var specRequired = map[string][]string{
	"record":       {"version", "id", "timestamp", "files"},
	"file":         {"path", "conversations"},
	"conversation": {"ranges"},
	"contributor":  {"type"},
	"range":        {"start_line", "end_line"},
	"vcs":          {"type", "revision"},
	"tool":         {},
}

// specContributorTypes is the spec's contributor enum (§4, §6.1).
var specContributorTypes = []string{"human", "ai", "mixed", "unknown"}

// specVCSTypes is the spec's version-control-system enum (§6.4).
var specVCSTypes = []string{"git", "jj", "hg", "svn"}

// goldenInput builds the fixture the golden file pins: one PR, three harness
// sessions and a hand-built attribution carrying every line label, runs of
// consecutive lines, two sessions in one file, an uncaptured line, a trivial
// line and a file with nothing but trivial lines.
func goldenInput() (*model.PR, *model.Attribution, []*model.Session) {
	pr := testkit.PR("acme/shop", 482).
		Add("src/webhooks/retry.ts", 40,
			"const max = 5",    // 40 agent
			"const base = 100", // 41 agent (run with 40)
			"  }",              // 42 trivial
			"function retry()", // 43 agent_then_human
			"  return backoff", // 44 human_in_session
			"// note",          // 45 uncaptured
			"sleep(ms)",        // 46 agent, other session
		).
		Add("src/webhooks/sender.ts", 10,
			"post()",   // 10 human
			"await it", // 11 human (run with 10)
			"drain()",  // 12 agent
		).
		Add("src/webhooks/generated.ts", 1, "{", "}").
		Commit("aaaaaaa1111111111111111111111111111111", "14:00").
		Commit("bbbbbbb2222222222222222222222222222222", "14:20").
		Build()

	main := testkit.Session(model.HarnessClaudeCode, "ab12cd34ef567890").
		Model("claude-opus-4-5-20251101").
		Prompt("Add retry with backoff").
		Edit("src/webhooks/retry.ts", "const max = 5", "const base = 100").
		Build()

	cx := testkit.Session(model.HarnessCodex, "019f0000aabb").
		Model("gpt-5-codex").
		Prompt("run tests").
		Build()

	pi := testkit.Session(model.HarnessPi, "pi-session-1").
		Model("some-model").
		Prompt("why is retry slow").
		Build()

	ref := func(s *model.Session, ev string) *model.EventRef {
		return &model.EventRef{Session: s.Ref(), Event: ev}
	}
	attr := &model.Attribution{
		Files: []model.FileAttribution{
			{
				Path: "src/webhooks/retry.ts",
				Lines: []model.LineAttribution{
					{Line: 40, Label: model.LabelAgent, Source: ref(main, "e5"), Model: "claude-opus-4-5-20251101"},
					{Line: 41, Label: model.LabelAgent, Source: ref(main, "e5"), Model: "claude-opus-4-5-20251101"},
					{Line: 42, Label: model.LabelTrivial},
					{Line: 43, Label: model.LabelMixed, Source: ref(main, "e9"), Model: "claude-opus-4-5-20251101"},
					{Line: 44, Label: model.LabelHuman, Source: ref(main, "e11")},
					{Line: 45, Label: model.LabelUncaptured},
					{Line: 46, Label: model.LabelAgent, Source: ref(cx, "e2"), Model: "gpt-5-codex"},
				},
				Counts: map[model.LineLabel]int{
					model.LabelAgent: 3, model.LabelTrivial: 1, model.LabelMixed: 1,
					model.LabelHuman: 1, model.LabelUncaptured: 1,
				},
			},
			{
				Path: "src/webhooks/sender.ts",
				Lines: []model.LineAttribution{
					{Line: 10, Label: model.LabelHuman, Source: ref(pi, "e3"), Reformatted: true},
					{Line: 11, Label: model.LabelHuman, Source: ref(pi, "e4")},
					{Line: 12, Label: model.LabelAgent, Source: ref(cx, "e7"), Model: "gpt-5-codex"},
				},
				Counts: map[model.LineLabel]int{model.LabelHuman: 2, model.LabelAgent: 1},
			},
			{
				Path:   "src/webhooks/generated.ts",
				Lines:  []model.LineAttribution{{Line: 1, Label: model.LabelTrivial}, {Line: 2, Label: model.LabelTrivial}},
				Counts: map[model.LineLabel]int{model.LabelTrivial: 2},
			},
		},
		Total:     8,
		Explained: 6,
	}
	return pr, attr, []*model.Session{main, cx, pi}
}

func TestWrite_Golden(t *testing.T) {
	pr, attr, sessions := goldenInput()
	dir := t.TempDir()
	if err := WriteWith(dir, Options{GeneratedAt: fixedNow, Version: "0.2.0"}, pr, attr, sessions); err != nil {
		t.Fatalf("WriteWith: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	checkGolden(t, FileName, string(got))
}

// TestWrite_ValidatesRequiredFields walks the written record and insists on
// every member the spec marks required, on every node kind the record can
// contain, plus the spec's contributor and VCS enums.
func TestWrite_ValidatesRequiredFields(t *testing.T) {
	pr, attr, sessions := goldenInput()
	dir := t.TempDir()
	if err := WriteWith(dir, Options{GeneratedAt: fixedNow, Version: "0.2.0"}, pr, attr, sessions); err != nil {
		t.Fatalf("WriteWith: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("decode record: %v", err)
	}

	requireMembers(t, "record", rec, specRequired["record"])

	tool, ok := rec["tool"]
	if !ok {
		t.Error("record.tool is absent; the spec's tool node is optional but this package always writes it")
	}
	requireMembers(t, "tool", obj(t, "record.tool", tool), specRequired["tool"])

	vcs, ok := rec["vcs"]
	if !ok {
		t.Fatal("record.vcs is absent; a PR with a head commit must carry VCS info")
	}
	vcsObj := obj(t, "record.vcs", vcs)
	requireMembers(t, "vcs", vcsObj, specRequired["vcs"])
	requireEnum(t, "record.vcs.type", str(t, "record.vcs.type", vcsObj["type"]), specVCSTypes)

	files := arr(t, "record.files", rec["files"])
	if len(files) == 0 {
		t.Fatal("record.files is empty")
	}
	kinds := map[string]int{}
	for i, fv := range files {
		path := "record.files[" + itoa(i) + "]"
		f := obj(t, path, fv)
		requireMembers(t, "file", f, specRequired["file"])
		str(t, path+".path", f["path"])
		cons := arr(t, path+".conversations", f["conversations"])
		if len(cons) == 0 {
			t.Errorf("%s.conversations is empty; a file entry must carry at least one", path)
		}
		for j, cv := range cons {
			cpath := path + ".conversations[" + itoa(j) + "]"
			c := obj(t, cpath, cv)
			requireMembers(t, "conversation", c, specRequired["conversation"])
			contrib, ok := c["contributor"]
			if !ok {
				t.Errorf("%s.contributor is absent", cpath)
				continue
			}
			co := obj(t, cpath+".contributor", contrib)
			requireMembers(t, "contributor", co, specRequired["contributor"])
			kind := str(t, cpath+".contributor.type", co["type"])
			requireEnum(t, cpath+".contributor.type", kind, specContributorTypes)
			kinds[kind]++
			ranges := arr(t, cpath+".ranges", c["ranges"])
			if len(ranges) == 0 {
				t.Errorf("%s.ranges is empty", cpath)
			}
			for k, rv := range ranges {
				rpath := cpath + ".ranges[" + itoa(k) + "]"
				r := obj(t, rpath, rv)
				requireMembers(t, "range", r, specRequired["range"])
				start := num(t, rpath+".start_line", r["start_line"])
				end := num(t, rpath+".end_line", r["end_line"])
				if start < 1 || end < start {
					t.Errorf("%s = %d-%d, want a 1-indexed range with end >= start", rpath, start, end)
				}
			}
		}
	}
	// The fixture's labels, so a regression that silently drops a mapping
	// (or leaks a trivial one) fails here as well as in the golden.
	for _, want := range []string{ContributorAI, ContributorMixed, ContributorHuman, ContributorUnknown} {
		if kinds[want] == 0 {
			t.Errorf("no conversation with contributor.type %q", want)
		}
	}
}

// TestWrite_NoContentLeak checks the record carries no source text: neither the
// PR's added lines nor anything a session recorded may appear — only paths,
// refs, harness names and line numbers. The transcript path itself is expected,
// because it is what makes the attribution traceable without embedding the
// transcript.
func TestWrite_NoContentLeak(t *testing.T) {
	pr, attr, sessions := goldenInput()
	// Plant distinctive strings in every place content could be copied from.
	const lineText = "sensitiveLineText"
	const promptText = "sensitivePromptText"
	const messageText = "sensitiveMessageText"
	const titleText = "sensitiveTitleText"
	sessions[0].Events[0].Prompt.Text = promptText
	sessions[0].Title = titleText
	for i := range sessions[0].Events {
		if sessions[0].Events[i].Message != nil {
			sessions[0].Events[i].Message.Text = messageText
		}
	}
	sessions[0].Events[1].Edit.Added = []string{lineText}
	pr.Files[0].Hunks[0].Lines[0].Text = lineText

	dir := t.TempDir()
	if err := WriteWith(dir, Options{GeneratedAt: fixedNow, Version: "0.2.0"}, pr, attr, sessions); err != nil {
		t.Fatalf("WriteWith: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	out := string(raw)
	for _, secret := range []string{lineText, promptText, messageText, titleText} {
		if strings.Contains(out, secret) {
			t.Errorf("agent-trace.json contains %q:\n%s", secret, out)
		}
	}
	// The transcript path itself is expected — it is a reference, not content.
	if !strings.Contains(out, sessions[0].SourcePath) {
		t.Errorf("agent-trace.json does not reference the transcript path %q:\n%s", sessions[0].SourcePath, out)
	}
}

// TestWrite_TranscriptPathIsHomeRelative checks the transcript reference goes
// through engine.Home, so no "/Users/<name>/" prefix reaches the record.
func TestWrite_TranscriptPathIsHomeRelative(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skipf("no home directory: %v", err)
	}
	pr, attr, sessions := goldenInput()
	sessions[0].SourcePath = filepath.Join(home, ".claude", "projects", "p", sessions[0].ID+".jsonl")

	dir := t.TempDir()
	if err := WriteWith(dir, Options{GeneratedAt: fixedNow}, pr, attr, sessions); err != nil {
		t.Fatalf("WriteWith: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	out := string(raw)
	if !strings.Contains(out, "~/.claude/projects/p/"+sessions[0].ID+".jsonl") {
		t.Errorf("transcript path is not home-relative:\n%s", out)
	}
	if strings.Contains(out, home) {
		t.Errorf("agent-trace.json leaks the home directory %q:\n%s", home, out)
	}
}

// TestWrite_Deterministic runs the same scan state twice and insists on
// identical bytes, and checks an injected timestamp changes only the fields
// that should depend on it.
func TestWrite_Deterministic(t *testing.T) {
	pr, attr, sessions := goldenInput()
	write := func(dir string, at time.Time) []byte {
		t.Helper()
		if err := WriteWith(dir, Options{GeneratedAt: at, Version: "0.2.0"}, pr, attr, sessions); err != nil {
			t.Fatalf("WriteWith: %v", err)
		}
		b, err := os.ReadFile(filepath.Join(dir, FileName))
		if err != nil {
			t.Fatalf("read output: %v", err)
		}
		return b
	}
	a := write(t.TempDir(), fixedNow)
	b := write(t.TempDir(), fixedNow)
	if string(a) != string(b) {
		t.Error("two writes of the same state differ")
	}
	var ra, rc Record
	if err := json.Unmarshal(a, &ra); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(write(t.TempDir(), fixedNow.Add(time.Hour)), &rc); err != nil {
		t.Fatal(err)
	}
	if ra.Timestamp != fixedNow.UTC().Format(time.RFC3339) {
		t.Errorf("timestamp = %q, want the injected generated-at", ra.Timestamp)
	}
	if ra.ID == rc.ID {
		t.Error("record id does not depend on the record's content and timestamp")
	}
	if ra.ID == "" || len(strings.Split(ra.ID, "-")) != 5 {
		t.Errorf("id = %q, want a UUID", ra.ID)
	}
}

// TestWrite_DerivedTimestamp checks the contract entry point with no injected
// clock: the timestamp comes from the PR and sessions, never the wall clock.
func TestWrite_DerivedTimestamp(t *testing.T) {
	pr, attr, sessions := goldenInput()
	dir := t.TempDir()
	if err := Write(dir, pr, attr, sessions); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	want := pr.Commits[len(pr.Commits)-1].Time.UTC().Format(time.RFC3339)
	if rec.Timestamp != want {
		t.Errorf("timestamp = %q, want the last PR commit time %q", rec.Timestamp, want)
	}
}

// TestBuild_MappingAndRanges pins the contributor mapping and the range rules
// without going through the golden file.
func TestBuild_MappingAndRanges(t *testing.T) {
	pr, attr, sessions := goldenInput()
	rec := Build(pr, attr, sessions, fixedNow, "0.2.0")

	if rec.Version != SpecVersion {
		t.Errorf("version = %q, want %q", rec.Version, SpecVersion)
	}
	if rec.VCS == nil || rec.VCS.Type != VCSTypeGit ||
		rec.VCS.Revision != "bbbbbbb2222222222222222222222222222222" {
		t.Errorf("vcs = %+v, want the PR head commit as git", rec.VCS)
	}
	if rec.Metadata == nil {
		t.Error("metadata is nil; the repo has no other place in the spec")
	}
	if len(rec.Files) != 2 {
		t.Fatalf("files = %d, want 2 (the all-trivial file is omitted): %+v", len(rec.Files), rec.Files)
	}
	if rec.Files[0].Path != "src/webhooks/retry.ts" {
		t.Errorf("files[0] = %q, want sorted by path", rec.Files[0].Path)
	}

	cons := rec.Files[0].Conversations
	want := []struct {
		start, end int
		kind       string
		url        string
		model      string
	}{
		{40, 41, ContributorAI, "claude-code:ab12cd34ef567890", "anthropic/claude-opus-4-5-20251101"},
		{43, 43, ContributorMixed, "claude-code:ab12cd34ef567890", "anthropic/claude-opus-4-5-20251101"},
		{44, 44, ContributorHuman, "claude-code:ab12cd34ef567890", ""},
		{45, 45, ContributorUnknown, "", ""},
		{46, 46, ContributorAI, "codex:019f0000aabb", "openai/gpt-5-codex"},
	}
	if len(cons) != len(want) {
		t.Fatalf("conversations = %d, want %d: %+v", len(cons), len(want), cons)
	}
	for i, w := range want {
		c := cons[i]
		if len(c.Ranges) != 1 || c.Ranges[0].StartLine != w.start || c.Ranges[0].EndLine != w.end {
			t.Errorf("conversations[%d].ranges = %+v, want %d-%d", i, c.Ranges, w.start, w.end)
		}
		if c.URL != w.url {
			t.Errorf("conversations[%d].url = %q, want %q", i, c.URL, w.url)
		}
		if c.Contributor == nil || c.Contributor.Type != w.kind || c.Contributor.ModelID != w.model {
			t.Errorf("conversations[%d].contributor = %+v, want type %q model %q", i, c.Contributor, w.kind, w.model)
		}
		if w.url == "" {
			if len(c.Related) != 0 {
				t.Errorf("conversations[%d].related = %+v, want none without a session", i, c.Related)
			}
			continue
		}
		if len(c.Related) != 2 || c.Related[0].Type != RelatedHarness || c.Related[1].Type != RelatedTranscript {
			t.Errorf("conversations[%d].related = %+v, want harness and transcript", i, c.Related)
		}
	}

	// The trivial line 42 is in no range, and the pi session's two human lines
	// are one contiguous range in the second file.
	for _, c := range cons {
		for _, r := range c.Ranges {
			if r.StartLine <= 42 && 42 <= r.EndLine {
				t.Errorf("trivial line 42 appears in range %+v; trivial lines are omitted", r)
			}
		}
	}
	sender := rec.Files[1].Conversations
	if len(sender) != 2 || sender[0].Ranges[0].StartLine != 10 || sender[0].Ranges[0].EndLine != 11 {
		t.Fatalf("sender.ts conversations = %+v, want a 10-11 human run then line 12", sender)
	}
	if sender[1].Ranges[0].StartLine != 12 {
		t.Errorf("sender.ts second conversation = %+v, want line 12", sender[1])
	}
}

// checkGolden compares got against testdata/golden/<name>, rewriting it with -update.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run go test ./internal/agenttrace/... -update)", err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// requireMembers fails when obj is missing any member the spec marks required
// for that node kind.
func requireMembers(t *testing.T, kind string, obj map[string]any, required []string) {
	t.Helper()
	for _, m := range required {
		if _, ok := obj[m]; !ok {
			t.Errorf("%s is missing required member %q", kind, m)
		}
	}
	// Members the spec does not define for this node would mean the record no
	// longer matches the schema's shape.
	for m := range obj {
		if !specDefines(kind, m) {
			t.Errorf("%s carries member %q, which the spec does not define for it", kind, m)
		}
	}
}

// specDefines reports whether the spec's JSON Schema lists member m under the
// named node kind (required and optional members alike).
func specDefines(kind, m string) bool {
	switch kind {
	case "record":
		return inList(m, "version", "id", "timestamp", "vcs", "tool", "files", "metadata")
	case "file":
		return inList(m, "path", "conversations")
	case "conversation":
		return inList(m, "url", "contributor", "ranges", "related")
	case "contributor":
		return inList(m, "type", "model_id")
	case "range":
		return inList(m, "start_line", "end_line", "content_hash", "contributor")
	case "vcs":
		return inList(m, "type", "revision")
	case "tool":
		return inList(m, "name", "version")
	}
	return false
}

func inList(s string, list ...string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func requireEnum(t *testing.T, path, got string, allowed []string) {
	t.Helper()
	if !inList(got, allowed...) {
		t.Errorf("%s = %q, want one of %v", path, got, allowed)
	}
}

func obj(t *testing.T, path string, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want an object", path, v)
	}
	return m
}

func arr(t *testing.T, path string, v any) []any {
	t.Helper()
	a, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is %T, want an array", path, v)
	}
	return a
}

func str(t *testing.T, path string, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s is %T, want a string", path, v)
	}
	if s == "" {
		t.Errorf("%s is empty", path)
	}
	return s
}

func num(t *testing.T, path string, v any) int {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%s is %T, want a number", path, v)
	}
	return int(f)
}

func itoa(i int) string { return strconv.Itoa(i) }
