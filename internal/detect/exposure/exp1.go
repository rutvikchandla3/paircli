package exposure

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/classify"
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/model"
)

func init() { engine.Register(exp1{}) }

type exp1 struct{}

func (exp1) ID() string { return "EXP-1" }

// exp1Rule is one row of the EXP-1 side-effect table. Rules are ordered most
// severe first: the first class a command matches decides the wording.
type exp1Rule struct {
	class  classify.CmdClass
	one    string // summary count label, singular
	many   string // summary count label, plural
	alert  bool
	format string // %s = command, %s = clock
}

var exp1Rules = []exp1Rule{
	{classify.GitForcePush, "force push", "force pushes", true, "Force-pushed with %s at %s."},
	{classify.GitResetHard, "hard reset", "hard resets", true, "Hard reset with %s at %s."},
	{classify.Migration, "migration", "migrations", true, "Ran a database migration: %s at %s."},
	{classify.Publish, "publish", "publishes", true, "Published a package or release: %s at %s."},
	{classify.Infra, "infra change", "infra changes", true, "Changed infrastructure or cloud resources: %s at %s."},
	{classify.Cloud, "infra change", "infra changes", true, "Changed infrastructure or cloud resources: %s at %s."},
	{classify.NetworkWrite, "network write", "network writes", true, "Sent data to an external host: %s at %s."},
	{classify.GHWrite, "GitHub write", "GitHub writes", true, "Changed GitHub state: %s at %s."},
	{classify.RmRF, "deletion", "deletions", true, "Deleted files with %s at %s."},
	{classify.Container, "container command", "container commands", false, "Ran a container command: %s at %s."},
	{classify.GitPush, "push", "pushes", false, "Pushed with %s at %s."},
}

// exp1CleanupNames are directory base names whose wholesale deletion is a
// build-artifact cleanup rather than a meaningful side effect.
var exp1CleanupNames = map[string]bool{
	"dist": true, "build": true, "out": true, "target": true,
	"node_modules": true, ".next": true, ".nuxt": true, ".cache": true,
	"coverage": true, "__pycache__": true, ".pytest_cache": true,
	"tmp": true, ".turbo": true,
}

// exp1Tally counts findings by summary label.
type exp1Tally struct {
	one, many string
	n         int
}

func (d exp1) Detect(c *engine.Context) model.Signal {
	if !c.HasSessions() {
		return engine.NoSessions(d.ID())
	}
	sig := engine.NewSignal(d.ID())

	var findings []model.Finding
	tallies := map[string]*exp1Tally{}
	var tallyOrder []string
	bump := func(one, many string) {
		t, ok := tallies[one]
		if !ok {
			t = &exp1Tally{one: one, many: many}
			tallies[one] = t
			tallyOrder = append(tallyOrder, one)
		}
		t.n++
	}

	byClass := map[string]int{}
	outsideWrites := 0
	alert := false

	for _, it := range c.Timeline {
		e := it.E
		switch e.Kind {
		case model.KindCommand:
			cmd := e.Command
			if cmd == nil {
				continue
			}
			classes := classify.Command(cmd.Cmd, c.Config.ExtraChecks)
			for _, r := range exp1Rules {
				if !expHasClass(classes, r.class) {
					continue
				}
				sev := model.StateInfo
				if r.alert {
					sev = model.StateAlert
				}
				if r.class == classify.RmRF && exp1CleanupOnly(cmd.Cmd) {
					sev = model.StateInfo
				}
				// The command phrase is wrapped in backticks; human-run
				// commands carry the "(run by the author)" marker after it.
				phrase := "`" + classify.ShortCmd(cmd.Cmd) + "`"
				if cmd.ByUser {
					phrase += " (run by the author)"
				}
				findings = append(findings, model.Finding{
					Summary:  fmt.Sprintf(r.format, phrase, engine.Clock(e.TS)),
					Severity: sev,
					Evidence: []model.Evidence{c.Evidence(it, cmd.Cmd)},
					Data: map[string]any{
						"class":   string(r.class),
						"session": it.S.Ref(),
						"event":   e.ID,
						"by_user": cmd.ByUser,
					},
				})
				if sev == model.StateAlert {
					alert = true
				}
				byClass[string(r.class)]++
				bump(r.one, r.many)
				break // the most severe matching class decides the wording
			}

		case model.KindEdit:
			ed := e.Edit
			if ed == nil || ed.Failed || ed.RelPath != "" || it.S.RepoRoot == "" {
				continue
			}
			sev := model.StateAlert
			if exp1TempPath(ed.Path) {
				sev = model.StateInfo
			}
			findings = append(findings, model.Finding{
				Summary:  fmt.Sprintf("Wrote outside the repo: `%s` at %s.", engine.Home(ed.Path), engine.Clock(e.TS)),
				Severity: sev,
				Evidence: []model.Evidence{c.Evidence(it, ed.Path)},
				Data: map[string]any{
					"session": it.S.Ref(),
					"event":   e.ID,
					"path":    engine.Home(ed.Path),
				},
			})
			if sev == model.StateAlert {
				alert = true
			}
			outsideWrites++
			bump("write outside the repo", "writes outside the repo")

		case model.KindCwdChange:
			cc := e.CwdChange
			if cc == nil || cc.To == "" || it.S.RepoRoot == "" {
				continue
			}
			if exp1Under(cc.To, it.S.RepoRoot) {
				continue
			}
			findings = append(findings, model.Finding{
				Summary:  fmt.Sprintf("Moved the working directory outside the repo to `%s`.", engine.Home(cc.To)),
				Severity: model.StateInfo,
				Evidence: []model.Evidence{c.Evidence(it, cc.To)},
				Data: map[string]any{
					"session": it.S.Ref(),
					"event":   e.ID,
					"to":      engine.Home(cc.To),
				},
			})
			bump("directory change", "directory changes")
		}
	}

	sig.Findings = findings
	switch {
	case alert:
		sig.State = model.StateAlert
	case len(findings) > 0:
		sig.State = model.StateInfo
	default:
		sig.State = model.StateClear
	}

	if len(findings) == 0 {
		sig.Summary = "No commands with effects outside the diff."
	} else {
		parts := make([]string, 0, len(tallyOrder))
		for _, one := range tallyOrder {
			t := tallies[one]
			parts = append(parts, engine.Plural(t.n, t.one, t.many))
		}
		sig.Summary = model.Clip("Effects outside the diff: "+strings.Join(parts, ", ")+".", 160)
	}

	sig.Data = map[string]any{
		"by_class":            byClass,
		"outside_repo_writes": outsideWrites,
	}
	return sig
}

// expHasClass reports whether want is one of the command's classes.
func expHasClass(classes []classify.CmdClass, want classify.CmdClass) bool {
	for _, cl := range classes {
		if cl == want {
			return true
		}
	}
	return false
}

// exp1CleanupOnly reports whether every rm target's base name is a build
// artifact directory.
func exp1CleanupOnly(cmd string) bool {
	targets := classify.RmTargets(cmd)
	if len(targets) == 0 {
		return false
	}
	for _, t := range targets {
		base := t
		if i := strings.LastIndexAny(t, "/\\"); i >= 0 {
			base = t[i+1:]
		}
		if base == "" || !exp1CleanupNames[base] {
			return false
		}
	}
	return true
}

// exp1TempPath reports whether path lives under a system temporary directory.
func exp1TempPath(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	prefixes := []string{"/tmp", "/private/tmp", "/var/folders"}
	if td := os.TempDir(); td != "" {
		prefixes = append(prefixes, filepath.Clean(td))
	}
	for _, pre := range prefixes {
		if clean == pre || strings.HasPrefix(clean, pre+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// exp1Under reports whether path is root itself or lives under it.
func exp1Under(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	p := filepath.Clean(path)
	r := filepath.Clean(root)
	return p == r || strings.HasPrefix(p, r+string(os.PathSeparator))
}
