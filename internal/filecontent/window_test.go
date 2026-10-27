package filecontent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// hunk builds a hunk from a compact spec: each entry is a line, "a5" an added
// line 5, "d3" a deleted line 3, " 7" a context line 7.
func hunk(oldStart, newStart int, spec ...string) model.DiffHunk {
	h := model.DiffHunk{OldStart: oldStart, NewStart: newStart}
	for _, s := range spec {
		kind := model.LineCtx
		switch s[0] {
		case 'a':
			kind = model.LineAdd
		case 'd':
			kind = model.LineDel
		}
		n := 0
		fmt.Sscanf(s[1:], "%d", &n)
		l := model.DiffLine{Kind: kind, Text: "line"}
		switch kind {
		case model.LineAdd:
			l.NewNo = n
		case model.LineDel:
			l.OldNo = n
		default:
			l.NewNo, l.OldNo = n, n
		}
		h.Lines = append(h.Lines, l)
	}
	return h
}

func TestWindowsSeedsAndMargins(t *testing.T) {
	f := &model.DiffFile{Hunks: []model.DiffHunk{hunk(40, 40, "a40", "a41")}}
	got := windows(f, SideNew, 100, 3)
	want := [][2]int{{37, 44}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("windows = %v, want %v", got, want)
	}
}

func TestWindowsClampToTheFile(t *testing.T) {
	tests := []struct {
		name      string
		fullLines int
		spec      model.DiffHunk
		want      [][2]int
	}{
		{"at the start", 100, hunk(1, 1, "a1"), [][2]int{{1, 4}}},
		{"at the end", 10, hunk(10, 10, "a10"), [][2]int{{7, 10}}},
		{"a one-line file", 1, hunk(1, 1, "a1"), [][2]int{{1, 1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &model.DiffFile{Hunks: []model.DiffHunk{tc.spec}}
			if got := windows(f, SideNew, tc.fullLines, 3); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("windows = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWindowsMergeOverlappingAndTouching(t *testing.T) {
	tests := []struct {
		name  string
		hunks []model.DiffHunk
		want  [][2]int
	}{
		{
			"overlapping",
			[]model.DiffHunk{hunk(40, 40, "a40"), hunk(44, 44, "a44")},
			[][2]int{{37, 47}},
		},
		{
			// 40±3 ends at 43 and 47±3 starts at 44, so the two touch and merge.
			"touching after the margin",
			[]model.DiffHunk{hunk(40, 40, "a40"), hunk(47, 47, "a47")},
			[][2]int{{37, 50}},
		},
		{
			"far apart stays apart",
			[]model.DiffHunk{hunk(40, 40, "a40"), hunk(400, 400, "a400")},
			[][2]int{{37, 43}, {397, 403}},
		},
		{
			"unsorted input comes back sorted",
			[]model.DiffHunk{hunk(400, 400, "a400"), hunk(40, 40, "a40")},
			[][2]int{{37, 43}, {397, 403}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &model.DiffFile{Hunks: tc.hunks}
			if got := windows(f, SideNew, 1000, 3); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("windows = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWindowsUseTheSidesOwnNumbering(t *testing.T) {
	// A hunk that deletes line 10 and adds line 42 of the new file: the two
	// sides are nowhere near each other.
	f := &model.DiffFile{Hunks: []model.DiffHunk{hunk(10, 42, "d10", "a42")}}

	if got, want := windows(f, SideOld, 100, 2), [][2]int{{8, 12}}; !reflect.DeepEqual(got, want) {
		t.Errorf("old windows = %v, want %v", got, want)
	}
	if got, want := windows(f, SideNew, 100, 2), [][2]int{{40, 44}}; !reflect.DeepEqual(got, want) {
		t.Errorf("new windows = %v, want %v", got, want)
	}
}

func TestWindowsAnchorASideThatHasNoChangedLine(t *testing.T) {
	// A pure deletion: the new side has no added line to anchor on, so it
	// anchors where the hunk starts in the new file. Without that the new side
	// of a file whose only hunks are deletions would be cropped away.
	pureDel := model.DiffFile{Hunks: []model.DiffHunk{{OldStart: 20, NewStart: 19,
		Lines: []model.DiffLine{{Kind: model.LineDel, OldNo: 20, Text: "gone"}}}}}
	if got, want := windows(&pureDel, SideNew, 100, 3), [][2]int{{16, 22}}; !reflect.DeepEqual(got, want) {
		t.Errorf("new windows = %v, want %v", got, want)
	}

	// A pure addition: the old side anchors on OldStart.
	pureAdd := model.DiffFile{Hunks: []model.DiffHunk{{OldStart: 7, NewStart: 8,
		Lines: []model.DiffLine{{Kind: model.LineAdd, NewNo: 8, Text: "new"}}}}}
	if got, want := windows(&pureAdd, SideOld, 100, 3), [][2]int{{4, 10}}; !reflect.DeepEqual(got, want) {
		t.Errorf("old windows = %v, want %v", got, want)
	}
}

func TestWindowsClampAZeroStart(t *testing.T) {
	// `@@ -0,0 +1,2 @@` on the old side: OldStart is 0, and line 0 does not
	// exist, so the window clamps to the first line.
	f := &model.DiffFile{Hunks: []model.DiffHunk{{OldStart: 0, NewStart: 1,
		Lines: []model.DiffLine{{Kind: model.LineAdd, NewNo: 1, Text: "first"}}}}}
	if got, want := windows(f, SideOld, 50, 3), [][2]int{{1, 3}}; !reflect.DeepEqual(got, want) {
		t.Errorf("old windows = %v, want %v", got, want)
	}
}

func TestWindowsOfAnAddedOrDeletedFileCoverEverything(t *testing.T) {
	// The hunk of an added file is the whole file, so every line is a changed
	// line and the single window is the file. This is why cropping cannot
	// rescue one that is over the cap: there is nothing outside the window to
	// drop.
	var lines []model.DiffLine
	for i := 1; i <= 200; i++ {
		lines = append(lines, model.DiffLine{Kind: model.LineAdd, NewNo: i, Text: "x"})
	}
	f := &model.DiffFile{Hunks: []model.DiffHunk{{NewStart: 1, Lines: lines}}}

	got := windows(f, SideNew, 200, 300)
	want := [][2]int{{1, 200}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("windows = %v, want %v", got, want)
	}
}

func TestWindowsWithoutHunksOrLines(t *testing.T) {
	empty := &model.DiffFile{}
	if got := windows(empty, SideNew, 100, 3); got != nil {
		t.Errorf("windows with no hunks = %v, want nil", got)
	}
	withHunk := &model.DiffFile{Hunks: []model.DiffHunk{hunk(10, 10, "a10")}}
	if got := windows(withHunk, SideNew, 0, 3); got != nil {
		t.Errorf("windows of an empty file = %v, want nil", got)
	}
}

// TestRangesReconstructTheKeptLines is the invariant the reading guide depends
// on: walking the ranges in order and taking end-start+1 lines each reproduces
// the bytes written, and every written line maps back to its original number.
func TestRangesReconstructTheKeptLines(t *testing.T) {
	text := ""
	for i := 1; i <= 20; i++ {
		text += fmt.Sprintf("line %d\n", i)
	}
	lines := splitLines(text)
	ranges := [][2]int{{3, 5}, {9, 9}, {15, 20}}

	got := string(sliceLines(lines, ranges))
	var rebuilt []string
	for _, r := range ranges {
		if r[1] > len(lines) || r[0] < 1 {
			t.Fatalf("range %v is outside a %d-line file", r, len(lines))
		}
		for i := r[0]; i <= r[1]; i++ {
			rebuilt = append(rebuilt, lines[i-1])
		}
	}
	want := strings.Join(rebuilt, "\n") + "\n"
	if got != want {
		t.Errorf("kept lines:\n got %q\nwant %q", got, want)
	}
	if n := countLines(got); n != keptLines(ranges) {
		t.Errorf("countLines = %d, want %d", n, keptLines(ranges))
	}
	// Original numbers survive the walk: the third written line came from the
	// file's fifth.
	written := splitLines(got)
	original := map[int]int{}
	k := 0
	for _, r := range ranges {
		for i := r[0]; i <= r[1]; i++ {
			k++
			original[k] = i
		}
	}
	if got, want := original[3], 5; got != want {
		t.Errorf("written line 3 maps to original %d, want %d", got, want)
	}
	if strings.Contains(written[0], "line 3") == false {
		t.Errorf("the first kept line = %q, want the file's line 3", written[0])
	}
}

func TestCountLinesMatchesTheManifestsRule(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"\n", 1},
	}
	for _, tc := range tests {
		if got := countLines(tc.text); got != tc.want {
			t.Errorf("countLines(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestLongestKeptLine(t *testing.T) {
	lines := []string{"a", strings.Repeat("x", 50), "ccc"}
	if got := longestKeptLine(lines, [][2]int{{1, 3}}); got != 50 {
		t.Errorf("longestKeptLine = %d, want 50", got)
	}
	if got := longestKeptLine(lines, [][2]int{{1, 1}, {3, 3}}); got != 3 {
		t.Errorf("longestKeptLine without the long line = %d, want 3", got)
	}
}
