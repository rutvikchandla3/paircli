package model

import (
	"testing"
)

func TestAnchorsFor_Ranges(t *testing.T) {
	ref1 := EventRef{Session: "cc:1", Event: "e1"}
	ref2 := EventRef{Session: "cc:1", Event: "e2"}

	attr := &Attribution{
		Files: []FileAttribution{
			{
				Path: "a.go",
				Lines: []LineAttribution{
					{Line: 1, Source: &ref2}, // from another ref
					{Line: 2, Source: &ref2},
					{Line: 3, Source: &ref1}, // from ref1
					{Line: 4, Source: &ref1}, // contiguous with 3
					{Line: 5, Source: &ref1}, // contiguous with 4
					{Line: 6, Source: &ref2},
					{Line: 7, Source: &ref2},
					{Line: 8, Source: &ref2},
					{Line: 9, Source: &ref1}, // gap from 5
					{Line: 10, Source: &ref2},
				},
			},
		},
	}

	refs := map[EventRef]bool{ref1: true}
	anchors := attr.AnchorsFor(refs)

	expected := []Anchor{
		{File: "a.go", Lines: "3-5"},
		{File: "a.go", Lines: "9"},
	}

	if len(anchors) != len(expected) {
		t.Errorf("AnchorsFor returned %d anchors, want %d", len(anchors), len(expected))
	}

	for i, anc := range anchors {
		if i >= len(expected) {
			break
		}
		if anc.File != expected[i].File || anc.Lines != expected[i].Lines {
			t.Errorf("AnchorsFor[%d] = {%s %s}, want {%s %s}", i, anc.File, anc.Lines, expected[i].File, expected[i].Lines)
		}
	}
}

func TestAnchorsFor_Empty(t *testing.T) {
	// Nil attribution
	var attr *Attribution
	anchors := attr.AnchorsFor(nil)
	if anchors != nil {
		t.Errorf("AnchorsFor(nil, nil) = %v, want nil", anchors)
	}

	// Empty refs
	attr = &Attribution{}
	anchors = attr.AnchorsFor(make(map[EventRef]bool))
	if anchors != nil {
		t.Errorf("AnchorsFor with empty refs = %v, want nil", anchors)
	}
}

func TestLinesFor(t *testing.T) {
	attr := &Attribution{
		Files: []FileAttribution{
			{
				Path: "a.go",
				Lines: []LineAttribution{
					{Line: 1, Label: LabelAgent},
					{Line: 2, Label: LabelMixed},
					{Line: 3, Label: LabelHuman},
					{Line: 4, Label: LabelTrivial},
					{Line: 5, Label: LabelAgent},
				},
			},
			{
				Path: "b.go",
				Lines: []LineAttribution{
					{Line: 1, Label: LabelAgent},
				},
			},
		},
	}

	// Test with LabelAgent
	anchors := attr.LinesFor("a.go", LabelAgent)
	expected := []Anchor{
		{File: "a.go", Lines: "1"},
		{File: "a.go", Lines: "5"},
	}
	if len(anchors) != len(expected) {
		t.Errorf("LinesFor(\"a.go\", LabelAgent) returned %d anchors, want %d", len(anchors), len(expected))
	}

	// Test with multiple labels
	anchors = attr.LinesFor("a.go", LabelAgent, LabelMixed)
	expectedMulti := []Anchor{
		{File: "a.go", Lines: "1-2"},
		{File: "a.go", Lines: "5"},
	}
	if len(anchors) != len(expectedMulti) {
		t.Errorf("LinesFor(\"a.go\", LabelAgent, LabelMixed) returned %d anchors, want %d", len(anchors), len(expectedMulti))
	}

	// Test with missing file
	anchors = attr.LinesFor("missing.go", LabelAgent)
	if anchors != nil {
		t.Errorf("LinesFor(\"missing.go\", LabelAgent) = %v, want nil", anchors)
	}
}

func TestRatio(t *testing.T) {
	tests := []struct {
		attr     *Attribution
		expected float64
		name     string
	}{
		{nil, 0, "nil attribution"},
		{&Attribution{Total: 0, Explained: 0}, 0, "zero total"},
		{&Attribution{Total: 10, Explained: 0}, 0, "zero explained"},
		{&Attribution{Total: 10, Explained: 4}, 0.4, "4 out of 10"},
		{&Attribution{Total: 10, Explained: 10}, 1.0, "all explained"},
		{&Attribution{Total: 100, Explained: 50}, 0.5, "half explained"},
	}

	for _, tt := range tests {
		result := tt.attr.Ratio()
		if result != tt.expected {
			t.Errorf("Ratio() for %s = %f, want %f", tt.name, result, tt.expected)
		}
	}
}
