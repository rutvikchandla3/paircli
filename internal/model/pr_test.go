package model

import (
	"testing"
)

func TestAddedLines(t *testing.T) {
	file := &DiffFile{
		Path: "test.go",
		Hunks: []DiffHunk{
			{
				NewStart: 1,
				NewLines: 3,
				Lines: []DiffLine{
					{Kind: LineAdd, Text: "line1", NewNo: 1},
					{Kind: LineCtx, Text: "ctx", OldNo: 1, NewNo: 2},
					{Kind: LineAdd, Text: "line2", NewNo: 3},
				},
			},
			{
				NewStart: 10,
				NewLines: 2,
				Lines: []DiffLine{
					{Kind: LineCtx, Text: "ctx2", OldNo: 5, NewNo: 10},
					{Kind: LineAdd, Text: "line3", NewNo: 11},
				},
			},
		},
	}

	added := file.AddedLines()

	expectedCount := 3
	if len(added) != expectedCount {
		t.Errorf("AddedLines() returned %d lines, want %d", len(added), expectedCount)
	}

	expected := []DiffLine{
		{Kind: LineAdd, Text: "line1", NewNo: 1},
		{Kind: LineAdd, Text: "line2", NewNo: 3},
		{Kind: LineAdd, Text: "line3", NewNo: 11},
	}

	for i, line := range added {
		if i >= len(expected) {
			break
		}
		if line.Kind != expected[i].Kind || line.Text != expected[i].Text || line.NewNo != expected[i].NewNo {
			t.Errorf("AddedLines[%d] = {%s %s %d}, want {%s %s %d}",
				i, line.Kind, line.Text, line.NewNo,
				expected[i].Kind, expected[i].Text, expected[i].NewNo)
		}
	}
}

func TestRemovedLines(t *testing.T) {
	file := &DiffFile{
		Path: "test.go",
		Hunks: []DiffHunk{
			{
				OldStart: 1,
				OldLines: 3,
				Lines: []DiffLine{
					{Kind: LineDel, Text: "oldline1", OldNo: 1},
					{Kind: LineCtx, Text: "ctx", OldNo: 2, NewNo: 1},
					{Kind: LineDel, Text: "oldline2", OldNo: 3},
				},
			},
			{
				OldStart: 10,
				OldLines: 2,
				Lines: []DiffLine{
					{Kind: LineCtx, Text: "ctx2", OldNo: 10, NewNo: 5},
					{Kind: LineDel, Text: "oldline3", OldNo: 11},
				},
			},
		},
	}

	removed := file.RemovedLines()

	expectedCount := 3
	if len(removed) != expectedCount {
		t.Errorf("RemovedLines() returned %d lines, want %d", len(removed), expectedCount)
	}

	expected := []DiffLine{
		{Kind: LineDel, Text: "oldline1", OldNo: 1},
		{Kind: LineDel, Text: "oldline2", OldNo: 3},
		{Kind: LineDel, Text: "oldline3", OldNo: 11},
	}

	for i, line := range removed {
		if i >= len(expected) {
			break
		}
		if line.Kind != expected[i].Kind || line.Text != expected[i].Text || line.OldNo != expected[i].OldNo {
			t.Errorf("RemovedLines[%d] = {%s %s %d}, want {%s %s %d}",
				i, line.Kind, line.Text, line.OldNo,
				expected[i].Kind, expected[i].Text, expected[i].OldNo)
		}
	}
}

func TestPRFile(t *testing.T) {
	pr := &PR{
		Repo:   "acme/shop",
		Number: 42,
		Files: []DiffFile{
			{Path: "main.go", Status: StatusModified},
			{Path: "config.json", Status: StatusAdded},
			{Path: "old.go", Status: StatusDeleted, OldPath: "very_old.go"},
		},
	}

	// Test found
	file := pr.File("config.json")
	if file == nil || file.Path != "config.json" {
		t.Errorf("File(\"config.json\") = nil or wrong file, want {Path: \"config.json\"}")
	}

	// Test not found
	file = pr.File("missing.go")
	if file != nil {
		t.Errorf("File(\"missing.go\") = %v, want nil", file)
	}

	// Test first file
	file = pr.File("main.go")
	if file == nil || file.Path != "main.go" {
		t.Errorf("File(\"main.go\") = nil or wrong file")
	}
}
