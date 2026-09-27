package codex

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/model"
)

var rolloutHunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))?\s\+(\d+)(?:,(\d+))?\s@@`)

// parseUnifiedHunks parses a unified-diff body (no file headers, just
// "@@ -a,b +c,d @@" hunk headers and ' '/'+'/'-' lines) into hunks plus the
// flattened added/removed line text (without the +/- prefix). Lines reading
// "\ No newline at end of file" are dropped.
func parseUnifiedHunks(diff string) (hunks []model.Hunk, added, removed []string) {
	if diff == "" {
		return nil, nil, nil
	}
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	var cur *model.Hunk
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "@@"):
			h := model.Hunk{}
			if m := rolloutHunkHeaderRe.FindStringSubmatch(line); m != nil {
				h.OldStart = rolloutAtoiDefault(m[1], 0)
				h.OldLines = rolloutAtoiDefault(m[2], 1)
				h.NewStart = rolloutAtoiDefault(m[3], 0)
				h.NewLines = rolloutAtoiDefault(m[4], 1)
			}
			hunks = append(hunks, h)
			cur = &hunks[len(hunks)-1]
		case strings.HasPrefix(line, `\ No newline at end of file`):
			// ignored
		case cur == nil:
			// content before any hunk header: ignore
		default:
			cur.Lines = append(cur.Lines, line)
			if len(line) == 0 {
				continue
			}
			switch line[0] {
			case '+':
				added = append(added, line[1:])
			case '-':
				removed = append(removed, line[1:])
			}
		}
	}
	return hunks, added, removed
}

func rolloutAtoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// fileChange is one file change parsed out of an apply_patch "*** Begin
// Patch" block, used only as the intermediate result of the apply_patch
// fallback in this package.
type fileChange struct {
	Path     string
	Op       model.EditOp
	MovePath string
	Added    []string
	Removed  []string
	Hunks    []model.Hunk
}

// parseApplyPatch parses the body of a custom_tool_call apply_patch
// invocation: a "*** Begin Patch" … "*** End Patch" block with
// "*** Add File: <path>", "*** Update File: <path>" (+ optional
// "*** Move to: <path>") and "*** Delete File: <path>" sections, whose body
// lines are prefixed ' ', '+' or '-' (optionally inside "@@" chunks).
func parseApplyPatch(input string) []fileChange {
	var out []fileChange
	var cur *fileChange
	var curHunk *model.Hunk

	flushHunk := func() {
		if cur != nil && curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
		}
		curHunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			out = append(out, *cur)
		}
		cur = nil
	}

	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "*** Begin Patch"):
			continue
		case strings.HasPrefix(line, "*** End Patch"):
			flushFile()
		case strings.HasPrefix(line, "*** Add File: "):
			flushFile()
			cur = &fileChange{Path: strings.TrimPrefix(line, "*** Add File: "), Op: model.OpCreate}
		case strings.HasPrefix(line, "*** Update File: "):
			flushFile()
			cur = &fileChange{Path: strings.TrimPrefix(line, "*** Update File: "), Op: model.OpUpdate}
		case strings.HasPrefix(line, "*** Delete File: "):
			flushFile()
			cur = &fileChange{Path: strings.TrimPrefix(line, "*** Delete File: "), Op: model.OpDelete}
		case strings.HasPrefix(line, "*** Move to: "):
			if cur != nil {
				cur.Op = model.OpMove
				cur.MovePath = strings.TrimPrefix(line, "*** Move to: ")
			}
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			curHunk = &model.Hunk{}
		case cur == nil:
			continue
		case line == "":
			continue
		default:
			switch line[0] {
			case '+':
				cur.Added = append(cur.Added, line[1:])
				if curHunk != nil {
					curHunk.Lines = append(curHunk.Lines, line)
				}
			case '-':
				cur.Removed = append(cur.Removed, line[1:])
				if curHunk != nil {
					curHunk.Lines = append(curHunk.Lines, line)
				}
			case ' ':
				if curHunk != nil {
					curHunk.Lines = append(curHunk.Lines, line)
				}
			}
		}
	}
	flushFile()
	return out
}
