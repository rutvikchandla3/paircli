package model

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateOutput_Short(t *testing.T) {
	short := strings.Repeat("x", 100)
	result := TruncateOutput(short)
	if result != short {
		t.Errorf("TruncateOutput(%d chars) = %d chars, want unchanged", len(short), len(result))
	}
}

func TestTruncateOutput_Long(t *testing.T) {
	long := strings.Repeat("x", 10000)
	result := TruncateOutput(long)

	// Should contain the truncation marker
	if !strings.Contains(result, "[2000 bytes truncated]") {
		t.Errorf("TruncateOutput(10000 chars) missing truncation marker")
	}

	// Should keep first OutputLimit bytes and last OutputLimit bytes
	if !strings.HasPrefix(result, strings.Repeat("x", OutputLimit)) {
		t.Errorf("TruncateOutput(10000 chars) missing first %d bytes", OutputLimit)
	}
	if !strings.HasSuffix(result, strings.Repeat("x", OutputLimit)) {
		t.Errorf("TruncateOutput(10000 chars) missing last %d bytes", OutputLimit)
	}
}

func TestTruncateOutput_UTF8(t *testing.T) {
	// Create a string that will be cut through multi-byte characters
	s := strings.Repeat("😀", 3000) // 😀 is 4 bytes in UTF-8
	result := TruncateOutput(s)

	if !utf8.ValidString(result) {
		t.Errorf("TruncateOutput produced invalid UTF-8")
	}
}

func TestClip(t *testing.T) {
	tests := []struct {
		input    string
		limit    int
		expected string
	}{
		{"hi", 3, "hi"},      // No truncation needed
		{"héllo", 3, "hél…"}, // 3 runes: 'h', 'é', 'l'
		{"hello", 3, "hel…"}, // ASCII truncation
		{"", 3, ""},          // Empty string
		{"a", 1, "a"},        // Single character, no truncation
		{"ab", 1, "a…"},      // Single character with truncation
	}

	for _, tt := range tests {
		result := Clip(tt.input, tt.limit)
		if result != tt.expected {
			t.Errorf("Clip(%q, %d) = %q, want %q", tt.input, tt.limit, result, tt.expected)
		}
	}
}

func TestLooseLine(t *testing.T) {
	// Same line with different formatting should produce the same loose form
	line1 := "  const a = 'x';"
	line2 := "const a = \"x\""
	result1 := LooseLine(line1)
	result2 := LooseLine(line2)
	if result1 != result2 {
		t.Errorf("LooseLine(%q) = %q, LooseLine(%q) = %q, want equal", line1, result1, line2, result2)
	}
}

func TestIsTrivialLine(t *testing.T) {
	trivialCases := []string{
		"",
		"  }",
		"});",
		"   ",
		"{}[]();,",
	}
	for _, s := range trivialCases {
		if !IsTrivialLine(s) {
			t.Errorf("IsTrivialLine(%q) = false, want true", s)
		}
	}

	nonTrivialCases := []string{
		"return x;",
		"const x = 1;",
		"a",
		" a ",
	}
	for _, s := range nonTrivialCases {
		if IsTrivialLine(s) {
			t.Errorf("IsTrivialLine(%q) = true, want false", s)
		}
	}
}

func TestLineHash(t *testing.T) {
	// Test that hash is 16 hex characters
	hash := LineHash("test")
	if len(hash) != 16 {
		t.Errorf("LineHash length = %d, want 16", len(hash))
	}

	// Test that all characters are hex
	for _, c := range hash {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("LineHash contains non-hex character: %c", c)
		}
	}

	// Test that trailing whitespace is ignored
	hash1 := LineHash("a")
	hash2 := LineHash("a  ")
	if hash1 != hash2 {
		t.Errorf("LineHash(\"a\") != LineHash(\"a  \"), but trailing whitespace should be ignored")
	}

	// Test that content differences matter
	hash3 := LineHash("a")
	hash4 := LineHash("b")
	if hash3 == hash4 {
		t.Errorf("LineHash(\"a\") == LineHash(\"b\"), but should differ")
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"a\n", []string{"a"}},
		{"a\r\nb", []string{"a", "b"}},
		{"a\n\nb\n", []string{"a", "", "b"}},
		{"line1\nline2\nline3", []string{"line1", "line2", "line3"}},
		{"single", []string{"single"}},
		{"\n", []string{""}},
		{"\r\n", []string{""}},
	}

	for _, tt := range tests {
		result := SplitLines(tt.input)
		if !stringsEqual(result, tt.expected) {
			t.Errorf("SplitLines(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
