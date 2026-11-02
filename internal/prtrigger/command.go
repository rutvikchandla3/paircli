package prtrigger

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// Operation kinds CommandOperation reports.
const (
	OpCreate = "create" // gh pr create
	OpPush   = "push"   // git push that updates the current branch
)

// Operation is a command that may have opened or updated a pull request.
type Operation struct {
	Kind string
	// ExecIndex is the index in Tokens of the `gh` or `git` executable.
	ExecIndex int
	Tokens    []string
	words     []word // Tokens, with where each holds an expandable `$`
}

// Name is the command the operation runs, for messages.
func (op Operation) Name() string {
	if op.Kind == OpCreate {
		return "gh pr create"
	}
	return "git push"
}

// separators end a simple command.
var separators = map[string]bool{"&": true, "&&": true, "|": true, "||": true, ";": true, "(": true, ")": true}

// pushFlagsIgnored are `git push` arguments that do not update just the
// current branch, so the PR (if any) did not change.
var pushFlagsIgnored = map[string]bool{"--all": true, "--delete": true, "--mirror": true, "--tags": true, "-d": true}

// CommandOperation finds a `gh pr create` or a `git push` in a shell command.
// A push that deletes, mirrors, pushes all branches or tags, or whose refspec
// starts with ':' is not an operation. `git -C <dir>`, `git -c <key=value>`
// and other leading git options are skipped before the subcommand. The command
// only counts when it is an executable token, so `echo 'git push'` does not.
func CommandOperation(command string) (Operation, bool) {
	words := lex(command)
	tokens := texts(words)
	for i, token := range tokens {
		if filepath.Base(token) == "gh" && i+2 < len(tokens) && tokens[i+1] == "pr" && tokens[i+2] == "create" {
			return Operation{Kind: OpCreate, ExecIndex: i, Tokens: tokens, words: words}, true
		}
	}
	for i, token := range tokens {
		if filepath.Base(token) != "git" {
			continue
		}
		cursor := i + 1
		for cursor < len(tokens) && !separators[tokens[cursor]] {
			if (tokens[cursor] == "-C" || tokens[cursor] == "-c") && cursor+1 < len(tokens) {
				cursor += 2
				continue
			}
			if strings.HasPrefix(tokens[cursor], "-") {
				cursor++
				continue
			}
			break
		}
		if cursor >= len(tokens) || tokens[cursor] != "push" {
			continue
		}
		if pushIgnored(tokens[cursor+1:]) {
			continue
		}
		return Operation{Kind: OpPush, ExecIndex: i, Tokens: tokens, words: words}, true
	}
	return Operation{}, false
}

func pushIgnored(rest []string) bool {
	for _, argument := range rest {
		if separators[argument] {
			return false
		}
		if pushFlagsIgnored[argument] || strings.HasPrefix(argument, ":") {
			return true
		}
	}
	return false
}

// RepoDir is the directory the operation in command ran in: the target of
// `git -C <dir>`, else the directory of the nearest preceding `cd <dir>`, else
// cwd (the process's working directory when cwd is empty). Relative targets
// resolve against cwd. $NAME and ${NAME} in the target expand to what the
// command itself assigned before it (see assignments); any other variable is
// left as written, since the shell's own state is not visible here.
func RepoDir(cwd, command string) string {
	cwd = expandHome(cwd)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	op, ok := CommandOperation(command)
	if !ok {
		return cwd
	}
	tokens := op.Tokens
	if filepath.Base(tokens[op.ExecIndex]) == "git" {
		for i := op.ExecIndex + 1; i < len(tokens)-1; i++ {
			if tokens[i] == "-C" {
				return resolveDir(cwd, op.expanded(i+1))
			}
			if tokens[i] == "push" || separators[tokens[i]] {
				break
			}
		}
	}
	for i := op.ExecIndex - 1; i >= 0; i-- {
		if tokens[i] == "cd" && i+1 < op.ExecIndex {
			return resolveDir(cwd, op.expanded(i+1))
		}
	}
	return cwd
}

// expanded is Tokens[i] with the variables the command assigned before it
// expanded.
func (op Operation) expanded(i int) string {
	text, _ := expand(op.words[i], assignments(op.words, i))
	return text
}

// assignments returns the variables the command has surely set by the time
// words[end] runs. It follows each NAME=value of a simple command made only of
// assignments, with or without a leading `export`, that a `;`, `&&` or newline
// ends. One that may not have run in this shell (after `||` or `|`, in a list
// sent to the background with `&`, or after a `&&` whose chain does not reach
// words[end]), or whose value holds a `$` that cannot be expanded, unsets the
// name instead, and one inside parentheses is dropped at the closing one.
// NAME=value before a command word sets NAME for that command only, so it is
// not followed, nor are other ways a shell sets a variable (read, for,
// declare).
func assignments(words []word, end int) map[string]string {
	type scope struct {
		vars map[string]string
		list []string // names the open and-or list set
		cond []string // those of them set after a `&&`, so only if it succeeded
	}
	cur := scope{vars: map[string]string{}}
	var outer []scope // the scope outside each open `(`
	start, prev := 0, ";"
	for i := 0; i < end; i++ {
		op := words[i].text
		if !separators[op] {
			continue
		}
		sure := (prev == ";" || prev == "&&" || prev == "&" || prev == "(") && (op == ";" || op == "&&")
		set := assign(cur.vars, words[start:i], sure)
		cur.list = append(cur.list, set...)
		if prev == "&&" {
			cur.cond = append(cur.cond, set...)
		}
		switch op {
		case ";", "||":
			unset(cur.vars, cur.cond)
			cur.cond = nil
			if op == ";" {
				cur.list = nil
			}
		case "&":
			unset(cur.vars, cur.list)
			cur.list, cur.cond = nil, nil
		case "(":
			outer = append(outer, cur)
			cur = scope{vars: maps.Clone(cur.vars)}
		case ")":
			if n := len(outer); n > 0 {
				cur, outer = outer[n-1], outer[:n-1]
			}
		}
		start, prev = i+1, op
	}
	return cur.vars
}

// assign applies a simple command's assignments to vars and returns the names
// it set: each is set when sure is true and its value expands fully, and
// unset otherwise. A command that is not only assignments changes nothing.
func assign(vars map[string]string, command []word, sure bool) []string {
	exported := len(command) > 0 && command[0].text == "export"
	if exported {
		command = command[1:] // `export NAME` alone exports and assigns nothing
	}
	for _, w := range command {
		if _, ok := assignmentName(w.text); !ok && !exported {
			return nil
		}
	}
	var set []string
	for _, w := range command {
		name, ok := assignmentName(w.text)
		if !ok {
			continue
		}
		if value, complete := expand(w.from(len(name)+1), vars); sure && complete {
			vars[name] = value
			set = append(set, name)
		} else {
			delete(vars, name)
		}
	}
	return set
}

func unset(vars map[string]string, names []string) {
	for _, name := range names {
		delete(vars, name)
	}
}

// assignmentName is the NAME of a NAME=value word.
func assignmentName(text string) (string, bool) {
	name, _, ok := strings.Cut(text, "=")
	return name, ok && nameLen(name) == len(name) && name != ""
}

// expand replaces each $NAME and ${NAME} in w that vars holds with its value.
// Any other `$` — a name vars does not hold, $1, $(...), ${NAME:-x} — stays as
// written, and complete is false.
func expand(w word, vars map[string]string) (text string, complete bool) {
	var b strings.Builder
	complete, last := true, 0
	for _, at := range w.dollars {
		name, end := varRef(w.text, at)
		value, ok := vars[name]
		if !ok {
			complete = false
			continue
		}
		b.WriteString(w.text[last:at])
		b.WriteString(value)
		last = end
	}
	b.WriteString(w.text[last:])
	return b.String(), complete
}

// varRef reads the $NAME or ${NAME} at text[at], returning NAME and the offset
// just past the reference, or "" when there is none.
func varRef(text string, at int) (string, int) {
	from := at + 1
	braced := strings.HasPrefix(text[from:], "{")
	if braced {
		from++
	}
	to := from + nameLen(text[from:])
	if to == from {
		return "", 0
	}
	if !braced {
		return text[from:to], to
	}
	if !strings.HasPrefix(text[to:], "}") {
		return "", 0
	}
	return text[from:to], to + 1
}

// nameLen is the length of the shell variable name s starts with: a letter or
// underscore, then letters, digits and underscores.
func nameLen(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return i
		}
	}
	return len(s)
}

// from is w from byte offset at on.
func (w word) from(at int) word {
	out := word{text: w.text[at:]}
	for _, d := range w.dollars {
		if d >= at {
			out.dollars = append(out.dollars, d-at)
		}
	}
	return out
}

func resolveDir(cwd, target string) string {
	target = expandHome(target)
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(cwd, target)
}

// expandHome replaces a leading "~" or "~/" with the home directory.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}
