// Package diff parses unified diff text, as produced by `git diff` or
// `gh pr diff`, into model.DiffFile records.
package diff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// fileBuilder accumulates the extended-header state and hunks for one file
// section while it is being parsed.
type fileBuilder struct {
	headerOld, headerNew         string
	newFileMode, deletedFileMode bool
	renameFrom, renameTo         string
	sawMinus, sawPlus            bool
	minusPath, plusPath          string
	minusDevNull, plusDevNull    bool
	binary                       bool
	hunks                        []model.DiffHunk
}

// finalize turns the accumulated state into a model.DiffFile.
func (fb *fileBuilder) finalize() model.DiffFile {
	df := model.DiffFile{Hunks: fb.hunks}

	switch {
	case fb.renameFrom != "" || fb.renameTo != "":
		df.Status = model.StatusRenamed
		if fb.sawPlus && !fb.plusDevNull {
			df.Path = fb.plusPath
		} else if fb.renameTo != "" {
			df.Path = fb.renameTo
		} else {
			df.Path = fb.headerNew
		}
		if fb.sawMinus && !fb.minusDevNull {
			df.OldPath = fb.minusPath
		} else if fb.renameFrom != "" {
			df.OldPath = fb.renameFrom
		} else {
			df.OldPath = fb.headerOld
		}
	case fb.newFileMode:
		df.Status = model.StatusAdded
		if fb.sawPlus && !fb.plusDevNull {
			df.Path = fb.plusPath
		} else {
			df.Path = fb.headerNew
		}
	case fb.deletedFileMode:
		df.Status = model.StatusDeleted
		if fb.sawMinus && !fb.minusDevNull {
			df.Path = fb.minusPath
		} else {
			df.Path = fb.headerOld
		}
	default:
		df.Status = model.StatusModified
		if fb.sawPlus && !fb.plusDevNull {
			df.Path = fb.plusPath
		} else {
			df.Path = fb.headerNew
		}
	}

	if fb.binary {
		df.Binary = true
		df.Hunks = nil
	}
	return df
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)

// parseHunkHeader parses "@@ -a[,b] +c[,d] @@ section". Missing counts mean 1.
func parseHunkHeader(line string) (oldStart, oldLines, newStart, newLines int, section string, ok bool) {
	m := hunkHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, 0, 0, "", false
	}
	oldStart, _ = strconv.Atoi(m[1])
	oldLines = 1
	if m[2] != "" {
		oldLines, _ = strconv.Atoi(m[2])
	}
	newStart, _ = strconv.Atoi(m[3])
	newLines = 1
	if m[4] != "" {
		newLines, _ = strconv.Atoi(m[4])
	}
	section = strings.TrimPrefix(m[5], " ")
	return oldStart, oldLines, newStart, newLines, section, true
}

// unquoteGitPath strips surrounding double quotes and unescapes \" and \\.
func unquoteGitPath(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	inner := s[1 : len(s)-1]
	inner = strings.ReplaceAll(inner, `\"`, `"`)
	inner = strings.ReplaceAll(inner, `\\`, `\`)
	return inner
}

func stripPrefix(s, prefix string) string {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

// parsePathLine parses the path portion of a "--- " / "+++ " line (with the
// prefix already stripped). Returns ("", true) for /dev/null.
func parsePathLine(rest string) (path string, isDevNull bool) {
	if idx := strings.IndexByte(rest, '\t'); idx >= 0 {
		rest = rest[:idx]
	}
	rest = strings.TrimSpace(rest)
	if rest == "/dev/null" {
		return "", true
	}
	rest = unquoteGitPath(rest)
	rest = stripPrefix(rest, "a/")
	rest = stripPrefix(rest, "b/")
	return rest, false
}

// findQuoteEnd returns the index of the closing, unescaped quote in s
// starting the search at start, or -1 if none is found.
func findQuoteEnd(s string, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return i
		}
	}
	return -1
}

// parseDiffGitHeader parses "diff --git a/<old> b/<new>" into fallback
// old/new paths, handling quoted paths.
func parseDiffGitHeader(line string) (oldPath, newPath string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	if strings.HasPrefix(rest, `"`) {
		if end := findQuoteEnd(rest, 1); end > 0 {
			firstTok := rest[:end+1]
			remainder := strings.TrimSpace(rest[end+1:])
			oldPath = stripPrefix(unquoteGitPath(firstTok), "a/")
			newPath = stripPrefix(unquoteGitPath(remainder), "b/")
			return oldPath, newPath
		}
	}
	idx := strings.Index(rest, " b/")
	if idx < 0 {
		return rest, rest
	}
	oldPart := rest[:idx]
	newPart := rest[idx+len(" b/"):]
	oldPath = stripPrefix(oldPart, "a/")
	newPath = newPart
	return oldPath, newPath
}

// appendContentLine adds one hunk content line, advancing the old/new line
// counters. Unrecognized line prefixes are ignored.
func appendContentLine(h *model.DiffHunk, line string, oldNo, newNo *int) {
	if line == "" {
		h.Lines = append(h.Lines, model.DiffLine{Kind: model.LineCtx, OldNo: *oldNo, NewNo: *newNo})
		*oldNo++
		*newNo++
		return
	}
	switch line[0] {
	case '+':
		h.Lines = append(h.Lines, model.DiffLine{Kind: model.LineAdd, Text: line[1:], NewNo: *newNo})
		*newNo++
	case '-':
		h.Lines = append(h.Lines, model.DiffLine{Kind: model.LineDel, Text: line[1:], OldNo: *oldNo})
		*oldNo++
	case ' ':
		h.Lines = append(h.Lines, model.DiffLine{Kind: model.LineCtx, Text: line[1:], OldNo: *oldNo, NewNo: *newNo})
		*oldNo++
		*newNo++
	default:
		// Unexpected prefix; ignore rather than corrupt the numbering.
	}
}

// Parse parses unified diff output (as produced by `git diff` or
// `gh pr diff`) into per-file records. Malformed input returns the files
// parsed so far and a non-nil error only when nothing could be parsed from
// non-empty input.
func Parse(unified string) ([]model.DiffFile, error) {
	if strings.TrimSpace(unified) == "" {
		return nil, nil
	}

	lines := strings.Split(unified, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var files []model.DiffFile
	var cur *fileBuilder
	var curHunk *model.DiffHunk
	var oldNo, newNo int

	flushHunk := func() {
		if curHunk != nil {
			cur.hunks = append(cur.hunks, *curHunk)
			curHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, cur.finalize())
			cur = nil
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			ho, hn := parseDiffGitHeader(line)
			cur = &fileBuilder{headerOld: ho, headerNew: hn}

		case cur == nil:
			// Stray line before any "diff --git" header; ignore.
			continue

		case strings.HasPrefix(line, "old mode "), strings.HasPrefix(line, "new mode "):
			// Informational only.

		case strings.HasPrefix(line, "new file mode"):
			cur.newFileMode = true

		case strings.HasPrefix(line, "deleted file mode"):
			cur.deletedFileMode = true

		case strings.HasPrefix(line, "rename from "):
			cur.renameFrom = strings.TrimPrefix(line, "rename from ")

		case strings.HasPrefix(line, "rename to "):
			cur.renameTo = strings.TrimPrefix(line, "rename to ")

		case strings.HasPrefix(line, "similarity index"), strings.HasPrefix(line, "dissimilarity index"):
			// Informational only.

		case strings.HasPrefix(line, "index "):
			// Informational only.

		case strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ"):
			cur.binary = true

		case strings.HasPrefix(line, "GIT binary patch"):
			cur.binary = true
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(lines[j], "diff --git ") {
				j++
			}
			i = j - 1

		case strings.HasPrefix(line, "--- "):
			p, dn := parsePathLine(strings.TrimPrefix(line, "--- "))
			cur.sawMinus, cur.minusPath, cur.minusDevNull = true, p, dn

		case strings.HasPrefix(line, "+++ "):
			p, dn := parsePathLine(strings.TrimPrefix(line, "+++ "))
			cur.sawPlus, cur.plusPath, cur.plusDevNull = true, p, dn

		case strings.HasPrefix(line, "@@ "):
			flushHunk()
			if os_, ol, ns, nl, sec, ok := parseHunkHeader(line); ok {
				curHunk = &model.DiffHunk{OldStart: os_, OldLines: ol, NewStart: ns, NewLines: nl, Section: sec}
				oldNo, newNo = os_, ns
			}

		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file"; ignore.

		default:
			if curHunk != nil {
				appendContentLine(curHunk, line, &oldNo, &newNo)
			}
		}
	}
	flushFile()

	if len(files) == 0 {
		return nil, fmt.Errorf("diff: no files parsed from input")
	}
	return files, nil
}
