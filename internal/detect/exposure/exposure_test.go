package exposure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
	"github.com/rutvikchandla3/paircli/internal/testkit"
)

// expSig runs one exposure detector and returns its signal, checking the
// 160-character summary limit every time.
func expSig(t *testing.T, c *engine.Context, id string) model.Signal {
	t.Helper()
	sig := testkit.Find(engine.RunIDs(c, id), id)
	if sig == nil {
		t.Fatalf("%s: no signal produced", id)
	}
	if n := utf8.RuneCountInString(sig.Summary); n > 160 {
		t.Errorf("%s: summary is %d characters: %q", id, n, sig.Summary)
	}
	return *sig
}

// expCtxLine builds a context line of a diff hunk.
func expCtxLine(text string) model.DiffLine {
	return model.DiffLine{Kind: model.LineCtx, Text: text}
}

// expAddLine builds an added line of a diff hunk, numbered in the new file.
func expAddLine(text string, no int) model.DiffLine {
	return model.DiffLine{Kind: model.LineAdd, Text: text, NewNo: no}
}

// expPRFile builds a PR diff file with a single hunk of the given lines.
func expPRFile(path string, lines ...model.DiffLine) model.DiffFile {
	return model.DiffFile{
		Path:   path,
		Status: model.StatusModified,
		Hunks:  []model.DiffHunk{{Lines: lines}},
	}
}

// expPR builds a PR with the given files.
func expPR(files ...model.DiffFile) *model.PR {
	return &model.PR{Repo: "acme/shop", Number: 1, Files: files}
}

func TestEXP1_CommandClasses(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		want    string
		wantSev model.State
	}{
		{"force push", "git push --force origin main",
			"Force-pushed with `git push --force origin main` at 14:00 UTC.", model.StateAlert},
		{"hard reset", "git reset --hard HEAD~1",
			"Hard reset with `git reset --hard HEAD~1` at 14:00 UTC.", model.StateAlert},
		{"migration", "prisma migrate deploy",
			"Ran a database migration: `prisma migrate deploy` at 14:00 UTC.", model.StateAlert},
		{"publish", "npm publish",
			"Published a package or release: `npm publish` at 14:00 UTC.", model.StateAlert},
		{"infra", "terraform apply",
			"Changed infrastructure or cloud resources: `terraform apply` at 14:00 UTC.", model.StateAlert},
		{"cloud", "aws s3 rm s3://bucket/key",
			"Changed infrastructure or cloud resources: `aws s3 rm s3://bucket/key` at 14:00 UTC.", model.StateAlert},
		{"network write", "curl -X POST https://api.example.com -d x=1",
			"Sent data to an external host: `curl -X POST https://api.example.com -d x=1` at 14:00 UTC.", model.StateAlert},
		{"gh write", "gh pr create --title x",
			"Changed GitHub state: `gh pr create --title x` at 14:00 UTC.", model.StateAlert},
		{"rm rf", "rm -rf src",
			"Deleted files with `rm -rf src` at 14:00 UTC.", model.StateAlert},
		{"container", "docker build .",
			"Ran a container command: `docker build .` at 14:00 UTC.", model.StateInfo},
		{"git push", "git push origin main",
			"Pushed with `git push origin main` at 14:00 UTC.", model.StateInfo},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := testkit.Session(model.HarnessClaudeCode, "s1")
			sb.Run(tc.cmd, 0, "")
			c := testkit.Ctx(expPR(), sb.Build())

			sig := expSig(t, c, "EXP-1")
			if len(sig.Findings) != 1 {
				t.Fatalf("want 1 finding, got %d: %+v", len(sig.Findings), sig.Findings)
			}
			if got := sig.Findings[0].Summary; got != tc.want {
				t.Errorf("summary = %q, want %q", got, tc.want)
			}
			if sig.Findings[0].Severity != tc.wantSev {
				t.Errorf("severity = %q, want %q", sig.Findings[0].Severity, tc.wantSev)
			}
			if sig.State != tc.wantSev {
				t.Errorf("state = %q, want %q", sig.State, tc.wantSev)
			}
			if len(sig.Findings[0].Evidence) == 0 {
				t.Error("finding has no evidence")
			}
		})
	}
}

func TestEXP1_CleanupRmIsInfo(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("rm -rf node_modules dist", 0, "")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-1")
	if len(sig.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(sig.Findings))
	}
	want := "Deleted files with `rm -rf node_modules dist` at 14:00 UTC."
	if sig.Findings[0].Summary != want {
		t.Errorf("summary = %q, want %q", sig.Findings[0].Summary, want)
	}
	if sig.Findings[0].Severity != model.StateInfo {
		t.Errorf("severity = %q, want info", sig.Findings[0].Severity)
	}
	if sig.State != model.StateInfo {
		t.Errorf("state = %q, want info", sig.State)
	}
	if got := sig.Summary; got != "Effects outside the diff: 1 deletion." {
		t.Errorf("signal summary = %q", got)
	}
}

func TestEXP1_WriteOutsideRepo(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}

	sbHome := testkit.Session(model.HarnessClaudeCode, "s1")
	sbHome.Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{
		Path: filepath.Join(home, "notes.txt"), Op: model.OpCreate, Via: "write",
	}})
	sbTmp := testkit.Session(model.HarnessClaudeCode, "s2")
	sbTmp.Add(model.Event{Kind: model.KindEdit, Edit: &model.FileEdit{
		Path: filepath.Join(os.TempDir(), "scratch.txt"), Op: model.OpCreate, Via: "write",
	}})
	c := testkit.Ctx(expPR(), sbHome.Build(), sbTmp.Build())

	sig := expSig(t, c, "EXP-1")
	if len(sig.Findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(sig.Findings), sig.Findings)
	}
	if !strings.Contains(sig.Findings[0].Summary, "`~/notes.txt`") {
		t.Errorf("home write summary = %q, want a `~/notes.txt` path", sig.Findings[0].Summary)
	}
	if sig.Findings[0].Severity != model.StateAlert {
		t.Errorf("home write severity = %q, want alert", sig.Findings[0].Severity)
	}
	if !strings.Contains(sig.Findings[1].Summary, "scratch.txt") {
		t.Errorf("tmp write summary = %q", sig.Findings[1].Summary)
	}
	if sig.Findings[1].Severity != model.StateInfo {
		t.Errorf("tmp write severity = %q, want info", sig.Findings[1].Severity)
	}
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if got := sig.Data["outside_repo_writes"]; got != 2 {
		t.Errorf("outside_repo_writes = %v, want 2", got)
	}
}

func TestEXP1_Clear(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("go test ./...", 0, "ok")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-1")
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if sig.Summary != "No commands with effects outside the diff." {
		t.Errorf("summary = %q", sig.Summary)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("want no findings, got %d", len(sig.Findings))
	}
}

func TestEXP1_UserRunMarked(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.UserRun("npm publish", 0)
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-1")
	if len(sig.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(sig.Findings))
	}
	want := "Published a package or release: `npm publish` (run by the author) at 14:00 UTC."
	if sig.Findings[0].Summary != want {
		t.Errorf("summary = %q, want %q", sig.Findings[0].Summary, want)
	}
}

func TestEXP2_NonexistentPackage(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("npm install left-pad-v2", 1,
		"npm ERR! code E404\nnpm ERR! 404 Not Found - GET https://registry.npmjs.org/left-pad-v2")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-2")
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	want := "Tried to install `left-pad-v2`: the registry says it does not exist."
	if len(sig.Findings) != 1 || sig.Findings[0].Summary != want {
		t.Fatalf("findings = %+v, want one %q", sig.Findings, want)
	}
	if sig.Summary != "1 install attempt for a package that does not exist." {
		t.Errorf("summary = %q", sig.Summary)
	}
	if got := sig.Data["failed_installs"]; len(got.([]string)) != 1 || got.([]string)[0] != "left-pad-v2" {
		t.Errorf("failed_installs = %v", got)
	}
}

func TestEXP2_PackageJSONBlockTracking(t *testing.T) {
	// The scripts entry carries a version-shaped value, so only block
	// tracking keeps it out of the dependency list.
	f := expPRFile("package.json",
		expCtxLine(`  "scripts": {`),
		expAddLine(`    "build": "^1.0.0",`, 3),
		expCtxLine(`  },`),
		expCtxLine(`  "dependencies": {`),
		expAddLine(`    "lodash": "^4.17.21",`, 6),
		expCtxLine(`  },`),
	)
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Say("adding a dependency")
	c := testkit.Ctx(expPR(f), sb.Build())

	sig := expSig(t, c, "EXP-2")
	if len(sig.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(sig.Findings), sig.Findings)
	}
	want := "Added `lodash@^4.17.21`: the agent chose it; no prompt mentions it."
	if sig.Findings[0].Summary != want {
		t.Errorf("summary = %q, want %q", sig.Findings[0].Summary, want)
	}
	if a := sig.Findings[0].Anchors; len(a) != 1 || a[0].File != "package.json" || a[0].Lines != "6" {
		t.Errorf("anchors = %+v, want package.json:6", a)
	}
	added := sig.Data["added"].([]map[string]any)
	if len(added) != 1 || added[0]["name"] != "lodash" {
		t.Errorf("data added = %+v, want only lodash", added)
	}
}

func TestEXP2_VersionHeuristicWithoutBlock(t *testing.T) {
	f := expPRFile("package.json",
		expAddLine(`"chalk": "^5.3.0",`, 2),
		expAddLine(`"notes": "hello",`, 3),
	)
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Say("adding a dependency")
	c := testkit.Ctx(expPR(f), sb.Build())

	sig := expSig(t, c, "EXP-2")
	added := sig.Data["added"].([]map[string]any)
	if len(added) != 1 {
		t.Fatalf("added = %+v, want only chalk", added)
	}
	if added[0]["name"] != "chalk" || added[0]["version"] != "^5.3.0" {
		t.Errorf("added[0] = %+v", added[0])
	}
}

func TestEXP2_GoModRequireBlock(t *testing.T) {
	f := expPRFile("go.mod",
		expCtxLine(`require (`),
		expAddLine(`	github.com/stretchr/testify v1.9.0`, 5),
		expAddLine(`	golang.org/x/text v0.14.0 // indirect`, 6),
		expCtxLine(`)`),
	)
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Say("adding a dependency")
	c := testkit.Ctx(expPR(f), sb.Build())

	sig := expSig(t, c, "EXP-2")
	added := sig.Data["added"].([]map[string]any)
	if len(added) != 2 {
		t.Fatalf("added = %+v, want 2 entries", added)
	}
	if added[0]["name"] != "github.com/stretchr/testify" || added[0]["indirect"] != false {
		t.Errorf("added[0] = %+v", added[0])
	}
	if added[1]["name"] != "golang.org/x/text" || added[1]["indirect"] != true {
		t.Errorf("added[1] = %+v", added[1])
	}
}

func TestEXP2_Requirements(t *testing.T) {
	f := expPRFile("requirements-dev.txt",
		expAddLine(`requests==2.31.0`, 1),
		expAddLine(`# a comment`, 2),
		expAddLine(`flask>=3.0`, 3),
		expAddLine(`-r base.txt`, 4),
	)
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Say("adding a dependency")
	c := testkit.Ctx(expPR(f), sb.Build())

	sig := expSig(t, c, "EXP-2")
	added := sig.Data["added"].([]map[string]any)
	if len(added) != 2 {
		t.Fatalf("added = %+v, want 2 entries", added)
	}
	if added[0]["name"] != "requests" || added[0]["version"] != "==2.31.0" {
		t.Errorf("added[0] = %+v", added[0])
	}
	if added[1]["name"] != "flask" || added[1]["version"] != ">=3.0" {
		t.Errorf("added[1] = %+v", added[1])
	}
}

func TestEXP2_Requested(t *testing.T) {
	f := expPRFile("package.json",
		expAddLine(`"lodash": "^4.17.21",`, 2),
		expAddLine(`"express": "^4.18.2",`, 3),
	)
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("please add lodash for the debounce helpers")
	sb.Run("npm install lodash express", 0, "")
	c := testkit.Ctx(expPR(f), sb.Build())

	sig := expSig(t, c, "EXP-2")
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if got := sig.Summary; got != "2 dependencies added (1 agent choice)." {
		t.Errorf("summary = %q", got)
	}
	if len(sig.Findings) != 2 {
		t.Fatalf("want 2 findings, got %+v", sig.Findings)
	}
	if want := "Added `lodash@^4.17.21` (mentioned by the author)."; sig.Findings[0].Summary != want {
		t.Errorf("lodash summary = %q, want %q", sig.Findings[0].Summary, want)
	}
	if sig.Findings[0].Severity != model.StateInfo {
		t.Errorf("lodash severity = %q, want info", sig.Findings[0].Severity)
	}
	if len(sig.Findings[0].Evidence) == 0 {
		t.Error("lodash finding has no install-command evidence")
	}
	if want := "Added `express@^4.18.2`: the agent chose it; no prompt mentions it."; sig.Findings[1].Summary != want {
		t.Errorf("express summary = %q, want %q", sig.Findings[1].Summary, want)
	}
	if sig.Findings[1].Severity != model.StateAlert {
		t.Errorf("express severity = %q, want alert", sig.Findings[1].Severity)
	}
}

func TestEXP2_Clear(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("npm ci", 0, "")
	c := testkit.Ctx(expPR(expPRFile("package.json", expCtxLine(`"name": "shop",`))), sb.Build())

	sig := expSig(t, c, "EXP-2")
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if sig.Summary != "No dependencies added or installed." {
		t.Errorf("summary = %q", sig.Summary)
	}
}

func TestEXP3_IssueThenWorkflowEdit(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("gh issue view 12", 0, "title: CI is red")
	sb.Edit(".github/workflows/ci.yml", "run: make test")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-3")
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	want := "14:00 UTC read external content (`gh issue view 12`) → 14:01 UTC edited `.github/workflows/ci.yml`."
	if len(sig.Findings) != 1 || sig.Findings[0].Summary != want {
		t.Fatalf("findings = %+v, want one %q", sig.Findings, want)
	}
	if len(sig.Findings[0].Evidence) != 2 {
		t.Errorf("want read and edit evidence, got %+v", sig.Findings[0].Evidence)
	}
	if a := sig.Findings[0].Anchors; len(a) != 1 || a[0].File != ".github/workflows/ci.yml" {
		t.Errorf("anchors = %+v, want a fallback anchor on the file", a)
	}
	if sig.Summary != "1 sensitive file was edited after reading external content in the same session." {
		t.Errorf("summary = %q", sig.Summary)
	}
}

func TestEXP3_LookupSearch(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Add(model.Event{Kind: model.KindLookup, Lookup: &model.Lookup{
		Kind: "search", Query: "terraform aws provider breaking change",
	}})
	sb.Edit("infra/main.tf", "resource \"aws_s3_bucket\" \"b\" {}")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-3")
	if len(sig.Findings) != 1 {
		t.Fatalf("want 1 finding, got %+v", sig.Findings)
	}
	if !strings.Contains(sig.Findings[0].Summary, "search “terraform aws provider breaking change”") {
		t.Errorf("summary = %q", sig.Findings[0].Summary)
	}
}

func TestEXP3_ReadAfterEditNotLinked(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Edit(".github/workflows/ci.yml", "run: make test")
	sb.Run("gh issue view 12", 0, "title: CI is red")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-3")
	if sig.State != model.StateClear || len(sig.Findings) != 0 {
		t.Errorf("want no chains, got state %q and %+v", sig.State, sig.Findings)
	}
	if sig.Summary != "No sensitive files were edited after reading external content." {
		t.Errorf("summary = %q", sig.Summary)
	}
}

func TestEXP3_OtherSessionNotLinked(t *testing.T) {
	sbA := testkit.Session(model.HarnessClaudeCode, "s1")
	sbA.Run("gh issue view 12", 0, "title: CI is red")
	sbB := testkit.Session(model.HarnessClaudeCode, "s2")
	sbB.At("16:00").Edit(".github/workflows/ci.yml", "run: make test")
	c := testkit.Ctx(expPR(), sbA.Build(), sbB.Build())

	sig := expSig(t, c, "EXP-3")
	if len(sig.Findings) != 0 {
		t.Fatalf("want no chains across sessions, got %+v", sig.Findings)
	}
	if got := sig.Data["external_reads"]; got != 1 {
		t.Errorf("external_reads = %v, want 1", got)
	}
}

func TestEXP3_Clear(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Edit("src/main.go", "fmt.Println(\"hi\")")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-3")
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if got := sig.Data["chains"]; got != 0 {
		t.Errorf("chains = %v, want 0", got)
	}
}

func TestEXP4_EnvReadViaCat(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("cat .env", 0, "TOKEN=abc")
	sb.Read(".env.local")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-4")
	if sig.State != model.StateInfo {
		t.Errorf("state = %q, want info", sig.State)
	}
	if len(sig.Findings) != 2 {
		t.Fatalf("want one finding per secret file, got %+v", sig.Findings)
	}
	if want := "Read `.env` at 14:00 UTC."; sig.Findings[0].Summary != want {
		t.Errorf("summary = %q, want %q", sig.Findings[0].Summary, want)
	}
	if got := sig.Findings[0].Data["count"]; got != 1 {
		t.Errorf("count = %v, want 1", got)
	}
	if sig.Summary != "2 secret files read." {
		t.Errorf("summary = %q", sig.Summary)
	}
}

func TestEXP4_SecretInDiffMasked(t *testing.T) {
	const raw = "AKIAIOSFODNN7EXAMPLE"
	f := expPRFile("config/settings.py", expAddLine(`AWS_KEY = "`+raw+`"`, 3))
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Say("adding settings")
	c := testkit.Ctx(expPR(f), sb.Build())

	sig := expSig(t, c, "EXP-4")
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if len(sig.Findings) != 1 {
		t.Fatalf("want 1 finding, got %+v", sig.Findings)
	}
	want := "Possible aws_access_key in `config/settings.py:3` (AKIA…(16 chars))."
	if sig.Findings[0].Summary != want {
		t.Errorf("summary = %q, want %q", sig.Findings[0].Summary, want)
	}
	if a := sig.Findings[0].Anchors; len(a) != 1 || a[0].Lines != "3" {
		t.Errorf("anchors = %+v, want config/settings.py:3", a)
	}
	blob, err := json.Marshal(sig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), raw) {
		t.Errorf("raw secret leaked into the signal: %s", blob)
	}
	if !strings.Contains(string(blob), "AKIA…(16 chars)") {
		t.Errorf("masked value missing from the signal: %s", blob)
	}
}

func TestEXP4_SecretInOutput(t *testing.T) {
	const raw = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("echo $GITHUB_TOKEN", 0, "token: "+raw)
	sb.Prompt("here is the key AKIAIOSFODNN7EXAMPLE, use it")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-4")
	if sig.State != model.StateAlert {
		t.Errorf("state = %q, want alert", sig.State)
	}
	if len(sig.Findings) != 2 {
		t.Fatalf("want 2 findings, got %+v", sig.Findings)
	}
	if want := "A github_token appeared in command output at 14:00 UTC (ghp_…(36 chars))."; sig.Findings[0].Summary != want {
		t.Errorf("output summary = %q, want %q", sig.Findings[0].Summary, want)
	}
	if want := "A aws_access_key appeared in a prompt at 14:01 UTC (AKIA…(16 chars))."; sig.Findings[1].Summary != want {
		t.Errorf("prompt summary = %q, want %q", sig.Findings[1].Summary, want)
	}
	if got := sig.Findings[1].Evidence[0].Excerpt; got != "AKIA…(16 chars)" {
		t.Errorf("prompt evidence excerpt = %q, want the masked value only", got)
	}
	if sig.Summary != "2 in prompts or output." {
		t.Errorf("summary = %q", sig.Summary)
	}
	blob, err := json.Marshal(sig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), raw) || strings.Contains(string(blob), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("raw secret leaked into the signal: %s", blob)
	}
}

func TestEXP4_ExampleEnvNotSecret(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Run("cat .env.example", 0, "TOKEN=replace-me")
	sb.Read(".env.sample")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-4")
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if len(sig.Findings) != 0 {
		t.Errorf("want no findings, got %+v", sig.Findings)
	}
}

func TestEXP4_Clear(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("please refactor the parser")
	sb.Run("go test ./...", 0, "ok")
	c := testkit.Ctx(expPR(), sb.Build())

	sig := expSig(t, c, "EXP-4")
	if sig.State != model.StateClear {
		t.Errorf("state = %q, want clear", sig.State)
	}
	if sig.Summary != "No secret files read and no secret-shaped strings found." {
		t.Errorf("summary = %q", sig.Summary)
	}
	if kinds := sig.Data["kinds"].([]string); len(kinds) != 0 {
		t.Errorf("kinds = %v, want none", kinds)
	}
}

func TestEXP_NilPRSafe(t *testing.T) {
	sb := testkit.Session(model.HarnessClaudeCode, "s1")
	sb.Prompt("add a dependency")
	sb.Run("npm install lodash", 0, "")
	sb.Edit("src/main.go", "x")

	c := testkit.Ctx(nil, sb.Build())
	for _, sig := range engine.RunIDs(c, "EXP-1", "EXP-2", "EXP-3", "EXP-4") {
		if sig.State == model.StateUnknown {
			t.Errorf("%s: unexpected unknown state: %s", sig.ID, sig.Summary)
		}
		if sig.ID == "" || sig.Question != model.QExposure {
			t.Errorf("%s: metadata not applied: %+v", sig.ID, sig)
		}
	}
}
