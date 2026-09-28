package verification

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() {
	engine.Register(ver3{})
}

type ver3 struct{}

func (ver3) ID() string { return "VER-3" }

// ver3CodeExts are the extensions that make a test-run argument a targeted file.
var ver3CodeExts = []string{
	".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py",
	".rb", ".rs", ".java", ".kt", ".cs", ".php", ".swift", ".ex", ".exs",
}

// ver3NonSourceClasses are the path classes that keep a changed file out of
// VER-3's "changed source files" set.
func ver3NonSourceClasses() []classify.PathClass {
	return []classify.PathClass{
		classify.TestFile, classify.Docs, classify.Generated,
		classify.Lockfile, classify.Manifest, classify.CIConfig,
		classify.SnapshotFile,
	}
}

// ver3ChangedSourceFiles returns the PR files that count as changed source
// files: at least one added line, and none of the non-source path classes.
func ver3ChangedSourceFiles(c *engine.Context) []string {
	var out []string
	if c.PR == nil {
		return out
	}
	skip := ver3NonSourceClasses()
	for i := range c.PR.Files {
		f := &c.PR.Files[i]
		if len(f.AddedLines()) == 0 {
			continue
		}
		classes := classify.Path(f.Path, c.Config)
		excluded := false
		for _, cl := range skip {
			if classify.Has(classes, cl) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		out = append(out, f.Path)
	}
	return out
}

// ver3TargetedArg normalizes one token of a test command into a targeted
// argument, or reports ok=false when the token is not one. It strips pytest
// node ids ("::…") and a leading "./".
func ver3TargetedArg(tok string) (string, bool) {
	if strings.HasPrefix(tok, "-") {
		return "", false
	}
	raw := tok
	if i := strings.Index(raw, "::"); i >= 0 {
		raw = raw[:i]
	}
	if raw == "" {
		return "", false
	}
	// Go package patterns (./x/... , ./x) are targeted no matter their shape.
	goPkg := strings.HasPrefix(raw, "./")
	s := strings.TrimPrefix(raw, "./")
	if s == "" {
		return "", false
	}
	if goPkg || strings.Contains(s, "/") || ver3HasCodeExt(s) {
		return s, true
	}
	return "", false
}

func ver3HasCodeExt(s string) bool {
	for _, e := range ver3CodeExts {
		if strings.HasSuffix(s, e) {
			return true
		}
	}
	return false
}

// ver3IsPrefixToken reports whether tok belongs to the command's leading
// wrapper (env assignments, sudo, npx-style launchers' prefix words) rather
// than being the runner itself.
func ver3IsPrefixToken(tok string) bool {
	switch tok {
	case "sudo", "time", "command", "exec", "nohup", "env":
		return true
	}
	return strings.Contains(tok, "=")
}

// ver3TargetedArgs returns the targeted arguments of a test command: the
// tokens after the runner that name files or Go packages. An empty result
// means the run is a full-suite run.
func ver3TargetedArgs(cmd string) []string {
	var out []string
	for _, seg := range classify.Segments(cmd) {
		toks := classify.Tokens(seg)
		i := 0
		for i < len(toks) && ver3IsPrefixToken(toks[i]) {
			i++
		}
		if i >= len(toks) {
			continue
		}
		// toks[i] is the runner; every later token is a candidate argument.
		for _, t := range toks[i+1:] {
			if a, ok := ver3TargetedArg(t); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

// ver3BaseNoExt returns the file's base name without its extension.
func ver3BaseNoExt(p string) string {
	b := path.Base(p)
	ext := path.Ext(b)
	return strings.TrimSuffix(b, ext)
}

// ver3ArgCoversFile reports whether targeted argument a plausibly exercises
// file f, by any of VER-3's three matching rules.
func ver3ArgCoversFile(f, a string) bool {
	// (a) f starts with a treated as a directory (Go ./pkg/... -> pkg/).
	dir := strings.TrimPrefix(a, "./")
	dir = strings.TrimSuffix(dir, "/...")
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || dir == "." || dir == "..." {
		// The argument names the repo root (e.g. `go test ./...`).
		return true
	}
	if strings.HasPrefix(f, dir+"/") {
		return true
	}

	// (b) f and a share their first two path segments.
	fs := strings.Split(f, "/")
	as := strings.Split(a, "/")
	if len(fs) >= 2 && len(as) >= 2 && fs[0] == as[0] && fs[1] == as[1] {
		return true
	}

	// (c) f's base name without extension appears in a's base name.
	fb := ver3BaseNoExt(f)
	if fb != "" && strings.Contains(path.Base(a), fb) {
		return true
	}
	return false
}

func ver3Covers(f string, args []string) bool {
	for _, a := range args {
		if ver3ArgCoversFile(f, a) {
			return true
		}
	}
	return false
}

func (ver3) Detect(c *engine.Context) model.Signal {
	sig := engine.NewSignal("VER-3")
	if !c.HasSessions() {
		return engine.NoSessions("VER-3")
	}

	sourceFiles := ver3ChangedSourceFiles(c)

	var testRuns []checkRun
	for _, r := range checkRuns(c) {
		if r.Class == classify.Test {
			testRuns = append(testRuns, r)
		}
	}
	if len(testRuns) == 0 {
		return engine.Unknown("VER-3", "No test runs to compare against.")
	}

	// A full-suite run exercises everything (VER-3 is a heuristic).
	fullSuite := ""
	targetedRuns := 0
	covered := map[string]bool{}
	for _, r := range testRuns {
		args := ver3TargetedArgs(r.It.E.Command.Cmd)
		if len(args) == 0 {
			if fullSuite == "" {
				fullSuite = r.Cmd
			}
			continue
		}
		targetedRuns++
		for _, f := range sourceFiles {
			if covered[f] {
				continue
			}
			if ver3Covers(f, args) {
				covered[f] = true
			}
		}
	}

	sig.Data = map[string]any{
		"uncovered":     []string{},
		"targeted_runs": targetedRuns,
		"full_suite":    fullSuite != "",
	}

	if fullSuite != "" {
		sig.State = model.StateClear
		sig.Summary = fmt.Sprintf("A full test run (`%s`) ran; every changed file was likely exercised.", fullSuite)
		return sig
	}

	var uncovered []string
	for _, f := range sourceFiles {
		if !covered[f] {
			uncovered = append(uncovered, f)
		}
	}
	sort.Strings(uncovered)

	if len(uncovered) == 0 {
		sig.State = model.StateClear
		sig.Summary = "Every changed source file matched a targeted test run."
		return sig
	}

	sig.State = model.StateInfo
	sig.Summary = fmt.Sprintf("Likely not exercised by any test run: %d changed files (e.g. `%s`).",
		len(uncovered), uncovered[0])
	sig.Data["uncovered"] = uncovered

	show := uncovered
	if len(show) > 20 {
		show = show[:20]
	}
	for _, f := range show {
		sig.Findings = append(sig.Findings, model.Finding{
			Summary:  fmt.Sprintf("`%s` — no targeted test run matched it (likely).", f),
			Severity: model.StateInfo,
			Anchors:  []model.Anchor{{File: f}},
		})
	}
	return sig
}
