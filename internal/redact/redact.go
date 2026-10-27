// Package redact is the single seam for scrubbing secrets from text before it
// leaves the machine: every rendered report string, every uploaded scan file
// and every captured raw-session byte range goes through it.
//
// A Redactor applies a fixed set of pattern rules (RulesVersion names that
// set) plus the exact values of this process's own secret-looking environment
// variables and the tokens in ~/.npmrc. A match becomes [REDACTED:<kind>], and
// the number of matches per kind is kept so the bundle can say what was
// removed. Redaction is idempotent: redacting redacted text changes nothing.
//
// The rules favour precision over recall for generic assignments (a value
// must look like a secret, not a path, URL, boolean, placeholder or code
// expression), because a false positive silently damages evidence. Provider
// token formats are matched exactly.
package redact

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// RulesVersion identifies the rule set. It is written into bundle.json and
// bumped whenever a rule changes what is redacted.
const RulesVersion = "1"

// Redaction kinds, as they appear in [REDACTED:<kind>] and in the counts.
const (
	KindPrivateKey       = "private_key"
	KindGitHubToken      = "github_token"
	KindAPIKey           = "api_key"
	KindAWSAccessKey     = "aws_access_key"
	KindAWSSecret        = "aws_secret"
	KindSlackToken       = "slack_token"
	KindJWT              = "jwt"
	KindBearer           = "bearer"
	KindSecretAssignment = "secret_assignment"
	KindEnvSecret        = "env_secret"
	KindNPMToken         = "npm_token"
	KindURLCredentials   = "url_credentials"
)

// minEnvValue is the shortest environment value worth redacting by exact
// match: shorter values collide with ordinary text.
const minEnvValue = 12

// sep is what joins a name to its value, tolerating a JSON-escaped quote
// after the name and before the value (`\"password\": \"x\"`).
const sep = `\\?["']?\s*(?::=|=>|[:=])\s*\\?["']?`

// lead is the boundary in front of a token. Besides a word boundary it accepts
// a JSON-escaped newline, tab or carriage return, because in a raw transcript
// line a token that starts a line follows the two characters `\n`, which have
// no word boundary between them and the token.
const lead = `(?:\\[nrt]|\b)`

// rule is one pattern. The submatch group is replaced (0 = the whole match);
// keep, when set, decides whether a match is really a secret. hints are
// lowercase substrings, at least one of which must occur (case-insensitively)
// for the rule to run at all, which keeps large inputs fast.
type rule struct {
	kind  string
	re    *regexp.Regexp
	group int
	hints []string
	keep  func(value, rest string) bool
}

var rules = []rule{
	{
		kind:  KindPrivateKey,
		re:    regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----[\s\S]*?-----END (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`),
		hints: []string{"private key"},
	},
	{
		kind:  KindGitHubToken,
		re:    regexp.MustCompile(lead + `((?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{22,}))`),
		group: 1,
		hints: []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"},
	},
	{
		kind:  KindAPIKey,
		re:    regexp.MustCompile(lead + `(sk-[A-Za-z0-9_-]{20,})`),
		group: 1,
		hints: []string{"sk-"},
		keep:  func(v, _ string) bool { return hasDigitOrUpper(v) },
	},
	{
		kind:  KindAWSAccessKey,
		re:    regexp.MustCompile(lead + `((?:AKIA|ASIA)[0-9A-Z]{16})\b`),
		group: 1,
		hints: []string{"akia", "asia"},
	},
	{
		kind:  KindAWSSecret,
		re:    regexp.MustCompile(`(?i)` + lead + `aws_?secret_?(?:access_?)?key` + sep + `([A-Za-z0-9/+=]{40})`),
		group: 1,
		hints: []string{"secret"},
	},
	{
		kind:  KindSlackToken,
		re:    regexp.MustCompile(lead + `((?:xox[abposr]|xapp)-[A-Za-z0-9-]{10,})`),
		group: 1,
		hints: []string{"xox", "xapp-"},
	},
	{
		kind:  KindJWT,
		re:    regexp.MustCompile(lead + `(eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*)`),
		group: 1,
		hints: []string{"eyj"},
	},
	{
		kind:  KindBearer,
		re:    regexp.MustCompile(`(?i)` + lead + `authorization` + sep + `\s*(?:bearer|basic)\s+([A-Za-z0-9._~+/=-]{8,})`),
		group: 1,
		hints: []string{"authorization"},
		keep:  func(v, _ string) bool { return !isPlaceholder(v) },
	},
	{
		// scheme://user:password@host: the password of a connection string or
		// a credentialed remote. Provider tokens in that position are caught
		// by their own rules first, which leaves a placeholder behind.
		kind:  KindURLCredentials,
		re:    regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/:@"'\\]*:([^\s/@"'\\]{3,})@`),
		group: 1,
		hints: []string{"://"},
		keep:  func(v, _ string) bool { return !isPlaceholder(v) },
	},
	{
		kind: KindSecretAssignment,
		re: regexp.MustCompile(`(?i)[A-Za-z0-9_.-]*(?:api[_-]?key|token|secret|password|passwd)[A-Za-z0-9_.-]*` +
			sep + `([^\s"'` + "`" + `\\<>,;()\[\]{}&]{8,})`),
		group: 1,
		hints: []string{"api_key", "api-key", "apikey", "token", "secret", "password", "passwd"},
		keep:  keepAssigned,
	},
}

var (
	allLetters = regexp.MustCompile(`^[A-Za-z_-]+$`)
	allDigits  = regexp.MustCompile(`^[0-9]+$`)
	dotted     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+$`)
	upperSnake = regexp.MustCompile(`^[A-Z][A-Z_]*$`)
	repeated   = regexp.MustCompile(`^(?:x+|\*+|\.+|-+|_+|0+)$`)
	winPath    = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// keepAssigned decides whether the value of a `secret_name = value` match is a
// secret rather than a path, URL, boolean, placeholder, number, plain word or
// code expression.
func keepAssigned(value, rest string) bool {
	if isPlaceholder(value) || isPathOrURL(value) || isBooleanish(value) {
		return false
	}
	if rest != "" && (rest[0] == '(' || rest[0] == '[') {
		return false // a call or an index: code, not a literal
	}
	return !allLetters.MatchString(value) && !allDigits.MatchString(value) && !dotted.MatchString(value)
}

// isPlaceholder reports whether v is a stand-in someone wrote instead of a
// secret.
func isPlaceholder(v string) bool {
	lower := strings.ToLower(v)
	switch {
	case strings.Contains(lower, "redacted"):
		return true
	case strings.HasPrefix(v, "$") || strings.HasPrefix(v, "%") || strings.HasPrefix(v, "<") ||
		strings.HasPrefix(v, "{") || strings.HasPrefix(v, "*"):
		return true
	case repeated.MatchString(lower), upperSnake.MatchString(v):
		return true
	}
	for _, word := range []string{"your", "example", "placeholder", "changeme", "dummy", "xxxx"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func isPathOrURL(v string) bool {
	return strings.Contains(v, "://") || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "./") ||
		strings.HasPrefix(v, "../") || strings.HasPrefix(v, "~") || winPath.MatchString(v)
}

func isBooleanish(v string) bool {
	switch strings.ToLower(v) {
	case "true", "false", "yes", "no", "on", "off", "null", "none", "nil", "undefined", "enabled", "disabled":
		return true
	}
	return false
}

func hasDigitOrUpper(v string) bool {
	return strings.ContainsAny(v, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

// envKeywords are the name fragments that mark an environment variable as
// holding a secret. AUTHOR is blanked first so GIT_AUTHOR_NAME is not one.
var envKeywords = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "AUTH", "CREDENTIAL", "COOKIE"}

// literal is one exact value to redact, with the JSON-escaped spelling it
// takes inside a raw transcript line when that differs.
type literal struct {
	kind  string
	value string
}

// Redactor applies the rules and counts what it removed. It is safe for
// concurrent use.
type Redactor struct {
	literals []literal

	mu     sync.Mutex
	counts map[string]int
}

// New returns a Redactor for this process: its secret-looking environment
// variables and the tokens in ~/.npmrc are redacted by exact value.
func New() *Redactor {
	var npmrc string
	if home, err := os.UserHomeDir(); err == nil {
		if data, err := os.ReadFile(filepath.Join(home, ".npmrc")); err == nil {
			npmrc = string(data)
		}
	}
	return NewWith(os.Environ(), npmrc)
}

var npmToken = regexp.MustCompile(`(?m)_authToken\s*=\s*"?([^\s"]+)`)

// NewWith is New with the environment ("NAME=value" entries) and the content
// of an .npmrc given explicitly.
func NewWith(environ []string, npmrc string) *Redactor {
	r := &Redactor{counts: map[string]int{}}
	seen := map[string]bool{}
	add := func(kind, value string) {
		if len(value) < minEnvValue || seen[value] {
			return
		}
		seen[value] = true
		r.literals = append(r.literals, literal{kind, value})
		if escaped := jsonEscape(value); escaped != value {
			r.literals = append(r.literals, literal{kind, escaped})
		}
	}
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || !secretName(name) || isPathOrURL(value) || isBooleanish(value) || winPath.MatchString(value) {
			continue
		}
		add(KindEnvSecret, value)
	}
	for _, m := range npmToken.FindAllStringSubmatch(npmrc, -1) {
		if !strings.HasPrefix(m[1], "${") {
			add(KindNPMToken, m[1])
		}
	}
	sort.SliceStable(r.literals, func(i, j int) bool { return len(r.literals[i].value) > len(r.literals[j].value) })
	return r
}

func secretName(name string) bool {
	upper := strings.ReplaceAll(strings.ToUpper(name), "AUTHOR", "")
	for _, k := range envKeywords {
		if strings.Contains(upper, k) {
			return true
		}
	}
	return false
}

// jsonEscape returns v as it is spelled inside a JSON string.
func jsonEscape(v string) string {
	b, err := json.Marshal(v)
	if err != nil || len(b) < 2 {
		return v
	}
	return string(b[1 : len(b)-1])
}

var (
	defaultOnce sync.Once
	defaultR    *Redactor
)

// Default is the process-wide Redactor that Text uses. Its counts cover every
// Text call since the last ResetCounts.
func Default() *Redactor {
	defaultOnce.Do(func() { defaultR = New() })
	return defaultR
}

// Text returns s with secrets removed, using the process-wide Redactor. Every
// renderer and every Evidence excerpt calls it.
func Text(s string) string { return Default().Text(s) }

// Text returns s with secrets removed.
func (r *Redactor) Text(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	for i := range rules {
		s, lower = r.apply(&rules[i], s, lower)
	}
	for _, l := range r.literals {
		if n := strings.Count(s, l.value); n > 0 {
			s = strings.ReplaceAll(s, l.value, marker(l.kind))
			r.add(l.kind, n)
		}
	}
	return s
}

// Bytes returns b with secrets removed. It works line by line so a rule can
// never join two lines of a JSONL transcript, and the result has the same
// number of lines as the input.
func (r *Redactor) Bytes(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	var out bytes.Buffer
	out.Grow(len(b))
	for len(b) > 0 {
		line := b
		rest := []byte(nil)
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, rest = b[:i+1], b[i+1:]
		}
		out.WriteString(r.Text(string(line)))
		b = rest
	}
	return out.Bytes()
}

// apply runs one rule over s and returns the new text and its lowercase form.
func (r *Redactor) apply(ru *rule, s, lower string) (string, string) {
	if !hasAny(lower, ru.hints) {
		return s, lower
	}
	matches := ru.re.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s, lower
	}
	var out strings.Builder
	last, n := 0, 0
	for _, m := range matches {
		start, end := m[2*ru.group], m[2*ru.group+1]
		if start < 0 || start < last {
			continue
		}
		if ru.keep != nil && !ru.keep(s[start:end], s[end:]) {
			continue
		}
		out.WriteString(s[last:start])
		out.WriteString(marker(ru.kind))
		last = end
		n++
	}
	if n == 0 {
		return s, lower
	}
	out.WriteString(s[last:])
	r.add(ru.kind, n)
	s = out.String()
	return s, strings.ToLower(s)
}

func hasAny(lower string, hints []string) bool {
	for _, h := range hints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

func marker(kind string) string { return "[REDACTED:" + kind + "]" }

func (r *Redactor) add(kind string, n int) {
	r.mu.Lock()
	r.counts[kind] += n
	r.mu.Unlock()
}

// Counts returns how many matches each kind has had so far.
func (r *Redactor) Counts() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.counts))
	for k, v := range r.counts {
		out[k] = v
	}
	return out
}

// ResetCounts zeroes the counters.
func (r *Redactor) ResetCounts() {
	r.mu.Lock()
	r.counts = map[string]int{}
	r.mu.Unlock()
}

// Merge adds the counts of b into a copy of a.
func Merge(a, b map[string]int) map[string]int {
	out := make(map[string]int, len(a)+len(b))
	for k, v := range a {
		out[k] += v
	}
	for k, v := range b {
		out[k] += v
	}
	return out
}
