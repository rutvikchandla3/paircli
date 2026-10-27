package filecontent

import (
	"bytes"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// sides of a diff, as the manifest names them.
const (
	SideNew = "new"
	SideOld = "old"
)

// windows returns the line ranges to keep for one side of a file: every line
// the change touched on that side, widened by margin lines either way, clamped
// to the file and merged. Ranges are 1-based, inclusive, sorted and in the
// original file's numbering, which is what the diff's own line numbers index.
//
// A nil result does not mean "nothing to keep": it means the side has no hunk
// to anchor a window on at all — a large file whose patch GitHub omitted, a
// 100%-similarity rename, a mode-only change. The caller must treat that as all
// of the file or none of it, never as a side whose content is empty.
func windows(f *model.DiffFile, side string, fullLines, margin int) [][2]int {
	if f == nil || fullLines <= 0 || len(f.Hunks) == 0 {
		return nil
	}
	var out [][2]int
	for _, h := range f.Hunks {
		lo, hi := 0, 0
		for _, l := range h.Lines {
			if !changedOn(l, side) {
				continue
			}
			n := lineNo(l, side)
			if n <= 0 {
				continue
			}
			if lo == 0 || n < lo {
				lo = n
			}
			if n > hi {
				hi = n
			}
		}
		if lo == 0 {
			// This hunk changes nothing on this side: a pure deletion has no
			// new line to anchor on, a pure addition no old one. Anchor where
			// the hunk starts on this side, which is the line the change lands
			// between. Without this a file whose only hunks are deletions
			// would crop its new side away entirely.
			lo = hunkStart(h, side)
			hi = lo
		}
		lo, hi = clampRange(lo-margin, hi+margin, fullLines)
		if lo > 0 {
			out = append(out, [2]int{lo, hi})
		}
	}
	return mergeRanges(out)
}

// changedOn reports whether a hunk line is a change on this side rather than
// context: added lines are new-side changes, deleted lines old-side ones.
func changedOn(l model.DiffLine, side string) bool {
	if side == SideNew {
		return l.Kind == model.LineAdd
	}
	return l.Kind == model.LineDel
}

// lineNo is a hunk line's number on this side, 0 when it has none there.
func lineNo(l model.DiffLine, side string) int {
	if side == SideNew {
		return l.NewNo
	}
	return l.OldNo
}

// hunkStart is where a hunk begins on this side. For a hunk that carries no
// line on this side (`@@ -a,0 +c,d @@` on the old side) it is the line the
// change sits after, which the parser keeps.
func hunkStart(h model.DiffHunk, side string) int {
	if side == SideNew {
		return h.NewStart
	}
	return h.OldStart
}

// clampRange clips a range to [1, fullLines], returning 0,0 when nothing of it
// survives.
func clampRange(lo, hi, fullLines int) (int, int) {
	if lo < 1 {
		lo = 1
	}
	if hi > fullLines {
		hi = fullLines
	}
	if lo > hi {
		return 0, 0
	}
	return lo, hi
}

// mergeRanges sorts ranges and merges the ones that overlap or touch, so the
// manifest lists the fewest ranges that describe the kept lines.
func mergeRanges(in [][2]int) [][2]int {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool {
		if in[i][0] != in[j][0] {
			return in[i][0] < in[j][0]
		}
		return in[i][1] < in[j][1]
	})
	out := [][2]int{in[0]}
	for _, r := range in[1:] {
		last := &out[len(out)-1]
		if r[0] <= last[1]+1 {
			if r[1] > last[1] {
				last[1] = r[1]
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// splitLines splits text the way the manifest counts lines: a trailing newline
// ends the last line rather than starting an empty one, so the count equals the
// diff's own numbering for a file that ends in a newline and for one that does
// not.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// countLines is the manifest's line count for a block of text.
func countLines(text string) int { return len(splitLines(text)) }

// keptLines is how many lines a set of ranges selects.
func keptLines(ranges [][2]int) int {
	n := 0
	for _, r := range ranges {
		n += r[1] - r[0] + 1
	}
	return n
}

// sliceLines joins the selected lines, each terminated by a newline, so walking
// the ranges in order reconstructs the bytes exactly.
func sliceLines(lines []string, ranges [][2]int) []byte {
	var out bytes.Buffer
	for _, r := range ranges {
		for i := r[0]; i <= r[1]; i++ {
			out.WriteString(lines[i-1])
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}

// longestKeptLine is the byte length of the longest line a set of ranges
// selects, which is what the per-line cap tests.
func longestKeptLine(lines []string, ranges [][2]int) int {
	longest := 0
	for _, r := range ranges {
		for i := r[0]; i <= r[1]; i++ {
			if n := len(lines[i-1]); n > longest {
				longest = n
			}
		}
	}
	return longest
}

// longestLine is the longest line in a block, which is what the per-line cap
// tests for a side written whole.
func longestLine(lines []string) int {
	longest := 0
	for _, l := range lines {
		if len(l) > longest {
			longest = len(l)
		}
	}
	return longest
}

// clampRanges clips ranges to a text of n lines, dropping the ones that fall
// entirely outside it. It keeps the manifest's promise that walking the ranges
// reconstructs the bytes written, whatever the text's length turned out to be.
func clampRanges(ranges [][2]int, n int) [][2]int {
	out := make([][2]int, 0, len(ranges))
	for _, r := range ranges {
		lo, hi := r[0], r[1]
		if hi > n {
			hi = n
		}
		if lo < 1 || lo > hi {
			continue
		}
		out = append(out, [2]int{lo, hi})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
