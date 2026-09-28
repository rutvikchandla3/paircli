package exposure

import (
	"regexp"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// exp2Dep is one dependency added by a PR diff.
type exp2Dep struct {
	File     string
	Name     string
	Version  string
	Line     int  // new-file line number
	Indirect bool // go.mod "// indirect" modules
}

// exp2ManifestDeps parses the added dependency entries of one manifest file.
// Files that are not dependency manifests return nil.
func exp2ManifestDeps(path string, hunks []model.DiffHunk) []exp2Dep {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	switch {
	case base == "package.json":
		return exp2PackageJSON(path, hunks)
	case base == "go.mod":
		return exp2GoMod(path, hunks)
	case base == "pyproject.toml":
		return exp2Pyproject(path, hunks)
	case base == "Cargo.toml":
		return exp2CargoToml(path, hunks)
	case base == "Gemfile":
		return exp2Gemfile(path, hunks)
	case strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"):
		return exp2Requirements(path, hunks)
	}
	return nil
}

// exp2DepBlocks are the package.json objects whose entries are dependencies.
var exp2DepBlocks = map[string]bool{
	"dependencies":         true,
	"devDependencies":      true,
	"peerDependencies":     true,
	"optionalDependencies": true,
}

var (
	exp2JSONBlockRE   = regexp.MustCompile(`^"([A-Za-z]+)"\s*:\s*\{$`)
	exp2JSONDepRE     = regexp.MustCompile(`^"([^"]+)"\s*:\s*"([^"]*)"\s*,?$`)
	exp2VersionLikeRE = regexp.MustCompile(`^[\^~<>=]*\d|^workspace:|^npm:|^git|^file:|^latest$`)
)

// exp2PackageJSON walks each hunk's context and added lines, tracking whether
// the current object key is a dependency block. Lines whose block cannot be
// determined from the hunk fall back to a version-shape heuristic.
func exp2PackageJSON(path string, hunks []model.DiffHunk) []exp2Dep {
	var out []exp2Dep
	for _, h := range hunks {
		known, isDep := false, false
		for _, l := range h.Lines {
			if l.Kind == model.LineDel {
				continue
			}
			trimmed := strings.TrimSpace(l.Text)
			if m := exp2JSONBlockRE.FindStringSubmatch(trimmed); m != nil {
				known, isDep = true, exp2DepBlocks[m[1]]
				continue
			}
			if strings.HasPrefix(trimmed, "}") {
				known, isDep = false, false
				continue
			}
			if l.Kind != model.LineAdd {
				continue
			}
			m := exp2JSONDepRE.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			switch {
			case known && !isDep:
				continue
			case !known && !exp2VersionLikeRE.MatchString(m[2]):
				continue
			}
			out = append(out, exp2Dep{File: path, Name: m[1], Version: m[2], Line: l.NewNo})
		}
	}
	return out
}

// exp2GoMod parses added require lines, both single-line and inside a
// require ( ... ) block.
func exp2GoMod(path string, hunks []model.DiffHunk) []exp2Dep {
	var out []exp2Dep
	for _, h := range hunks {
		inBlock := false
		for _, l := range h.Lines {
			if l.Kind == model.LineDel {
				continue
			}
			trimmed := strings.TrimSpace(l.Text)
			if strings.HasPrefix(trimmed, "require (") {
				inBlock = true
				continue
			}
			if trimmed == ")" {
				inBlock = false
				continue
			}
			if l.Kind != model.LineAdd {
				continue
			}
			body := trimmed
			switch {
			case strings.HasPrefix(body, "require "):
				body = strings.TrimSpace(strings.TrimPrefix(body, "require "))
			case !inBlock:
				continue
			}
			indirect := strings.Contains(body, "// indirect")
			if i := strings.Index(body, "//"); i >= 0 {
				body = strings.TrimSpace(body[:i])
			}
			fields := strings.Fields(body)
			if len(fields) < 2 {
				continue
			}
			out = append(out, exp2Dep{
				File: path, Name: fields[0], Version: fields[1],
				Line: l.NewNo, Indirect: indirect,
			})
		}
	}
	return out
}

// exp2Requirements parses added non-comment lines of a requirements file.
func exp2Requirements(path string, hunks []model.DiffHunk) []exp2Dep {
	var out []exp2Dep
	for _, h := range hunks {
		for _, l := range h.Lines {
			if l.Kind != model.LineAdd {
				continue
			}
			line := strings.TrimSpace(l.Text)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, ver := exp2PEPName(line)
			if name == "" {
				continue
			}
			out = append(out, exp2Dep{File: path, Name: name, Version: ver, Line: l.NewNo})
		}
	}
	return out
}

var (
	exp2PoetryDepsRE   = regexp.MustCompile(`^\[tool\.poetry\.[^\]]*dependencies\]$`)
	exp2TOMLAssignRE   = regexp.MustCompile(`^([A-Za-z0-9_.\-]+)\s*=\s*"(.*)"\s*$`)
	exp2TOMLTableRE    = regexp.MustCompile(`^([A-Za-z0-9_\-]+)\s*=\s*\{`)
	exp2TOMLTableVerRE = regexp.MustCompile(`version\s*=\s*"([^"]*)"`)
)

// exp2Pyproject parses added array items and poetry dependency assignments.
func exp2Pyproject(path string, hunks []model.DiffHunk) []exp2Dep {
	var out []exp2Dep
	for _, h := range hunks {
		inPoetryDeps := false
		for _, l := range h.Lines {
			if l.Kind == model.LineDel {
				continue
			}
			trimmed := strings.TrimSpace(l.Text)
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				inPoetryDeps = exp2PoetryDepsRE.MatchString(trimmed)
				continue
			}
			if l.Kind != model.LineAdd {
				continue
			}
			if inPoetryDeps {
				if m := exp2TOMLAssignRE.FindStringSubmatch(trimmed); m != nil {
					out = append(out, exp2Dep{File: path, Name: m[1], Version: m[2], Line: l.NewNo})
					continue
				}
			}
			if m := exp2TOMLArrayItemRE.FindStringSubmatch(trimmed); m != nil {
				name, ver := exp2PEPName(m[1])
				if name == "" {
					continue
				}
				out = append(out, exp2Dep{File: path, Name: name, Version: ver, Line: l.NewNo})
			}
		}
	}
	return out
}

// exp2CargoSections are the Cargo.toml tables whose entries are dependencies.
var exp2CargoSections = map[string]bool{
	"[dependencies]":       true,
	"[dev-dependencies]":   true,
	"[build-dependencies]": true,
}

// exp2CargoToml parses added dependency entries under Cargo dependency tables.
func exp2CargoToml(path string, hunks []model.DiffHunk) []exp2Dep {
	var out []exp2Dep
	for _, h := range hunks {
		inDeps := false
		for _, l := range h.Lines {
			if l.Kind == model.LineDel {
				continue
			}
			trimmed := strings.TrimSpace(l.Text)
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				inDeps = exp2CargoSections[trimmed]
				continue
			}
			if l.Kind != model.LineAdd || !inDeps {
				continue
			}
			if m := exp2TOMLAssignRE.FindStringSubmatch(trimmed); m != nil {
				out = append(out, exp2Dep{File: path, Name: m[1], Version: m[2], Line: l.NewNo})
				continue
			}
			if m := exp2TOMLTableRE.FindStringSubmatch(trimmed); m != nil {
				ver := ""
				if vm := exp2TOMLTableVerRE.FindStringSubmatch(trimmed); vm != nil {
					ver = vm[1]
				}
				out = append(out, exp2Dep{File: path, Name: m[1], Version: ver, Line: l.NewNo})
			}
		}
	}
	return out
}

var exp2GemRE = regexp.MustCompile(`^gem\s+["']([^"']+)["'](?:\s*,\s*["']([^"']+)["'])?`)

// exp2Gemfile parses added gem lines.
func exp2Gemfile(path string, hunks []model.DiffHunk) []exp2Dep {
	var out []exp2Dep
	for _, h := range hunks {
		for _, l := range h.Lines {
			if l.Kind != model.LineAdd {
				continue
			}
			m := exp2GemRE.FindStringSubmatch(strings.TrimSpace(l.Text))
			if m == nil {
				continue
			}
			out = append(out, exp2Dep{File: path, Name: m[1], Version: m[2], Line: l.NewNo})
		}
	}
	return out
}

var exp2TOMLArrayItemRE = regexp.MustCompile(`^"([^"]+)"\s*,?\s*$`)

// exp2PEPName splits a PEP 508 requirement such as "name[extras]>=1.0" into
// its project name and the version specifier that follows it. Environment
// markers and trailing comments are dropped, and option lines ("-r x.txt")
// yield an empty name.
func exp2PEPName(s string) (string, string) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ";"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if i := strings.IndexAny(s, " \t#"); i >= 0 {
		s = s[:i]
	}
	name, ver := s, ""
	if i := strings.IndexAny(s, "=<>!~"); i >= 0 {
		name, ver = s[:i], s[i:]
	}
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") {
		return "", ""
	}
	return name, strings.TrimSpace(ver)
}
