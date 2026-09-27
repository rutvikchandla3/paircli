package redact

import (
	"testing"
)

func TestTextNoop(t *testing.T) {
	tests := []string{
		"",
		"hello world",
		"secret password 123",
		"API_KEY=abc123def456",
		"multi\nline\nstring",
		"special chars: !@#$%^&*()",
	}

	for _, input := range tests {
		result := Text(input)
		if result != input {
			t.Errorf("Text(%q) = %q, want unchanged", input, result)
		}
	}
}
