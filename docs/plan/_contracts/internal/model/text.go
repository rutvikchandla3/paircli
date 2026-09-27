package model

import (
	"fmt"
	"hash/fnv"
	"strings"
	"unicode/utf8"
)

// OutputLimit is how many bytes of command output TruncateOutput keeps at
// each end.
const OutputLimit = 4000

// TruncateOutput keeps the first and last OutputLimit bytes of s and marks
// the cut. Every parser must pass Command.Output through it.
func TruncateOutput(s string) string {
	if len(s) <= 2*OutputLimit {
		return s
	}
	head := strings.ToValidUTF8(s[:OutputLimit], "")
	tail := strings.ToValidUTF8(s[len(s)-OutputLimit:], "")
	return fmt.Sprintf("%s\n…[%d bytes truncated]…\n%s", head, len(s)-2*OutputLimit, tail)
}

// Clip returns s cut to at most n runes, with "…" appended when cut.
func Clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// NormalizeLine is the strict line form used for attribution matching:
// trailing whitespace and carriage returns removed.
func NormalizeLine(s string) string {
	return strings.TrimRight(s, " \t\r")
}

// LooseLine is the lenient line form used to detect formatter rewrites:
// all whitespace removed, quotes unified to ", trailing ; and , dropped.
func LooseLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\r', '\n':
			continue
		case '\'', '`':
			b.WriteRune('"')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(b.String(), ";,")
}

// IsTrivialLine reports blank or punctuation-only lines, which are never
// counted in attribution totals.
func IsTrivialLine(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune(" \t\r{}[]();,", r) {
			return false
		}
	}
	return true
}

// LineHash is the hash stored in hook snapshots: FNV-1a 64 of NormalizeLine(s),
// as 16 lowercase hex characters.
func LineHash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(NormalizeLine(s)))
	return fmt.Sprintf("%016x", h.Sum64())
}

// SplitLines splits file content into lines without trailing newline
// characters. An empty string yields no lines; a trailing newline does not
// produce an extra empty line.
func SplitLines(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.TrimSuffix(content, "\n")
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// IntPtr returns a pointer to i.
func IntPtr(i int) *int { return &i }

// BoolPtr returns a pointer to b.
func BoolPtr(b bool) *bool { return &b }
