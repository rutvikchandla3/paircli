package classify

import (
	"strings"
)

// Glob matches a glob pattern against a path.
// Supports:
// - ** matches zero or more whole segments
// - * matches any run within a segment
// - ? matches one char
// - [...] matches a class
// Patterns starting with **/ also match at the root.
// Patterns ending with /** match everything below that directory.
func Glob(pattern, path string) bool {
	return globMatch(pattern, path, 0, 0)
}

func globMatch(pattern, path string, pi, pai int) bool {
	// Base case: both exhausted
	if pi == len(pattern) && pai == len(path) {
		return true
	}

	// Pattern exhausted
	if pi == len(pattern) {
		return false
	}

	// Check for ** (matches zero or more whole segments)
	if pi+1 < len(pattern) && pattern[pi:pi+2] == "**" {
		nextSlash := strings.IndexByte(pattern[pi+2:], '/')
		if nextSlash == -1 {
			// ** at the end matches everything
			return true
		}

		// ** followed by / matches zero or more path segments
		// Try matching after consuming 0 or more path segments
		nextPatternSegmentStart := pi + 2 + nextSlash + 1

		// Try matching without consuming any path segments
		if globMatch(pattern, path, nextPatternSegmentStart, pai) {
			return true
		}

		// Try matching after consuming path segments up to next /
		for i := pai; i <= len(path); i++ {
			if i == len(path) || path[i] == '/' {
				if i+1 < len(path) || i == len(path) {
					if globMatch(pattern, path, nextPatternSegmentStart, i) {
						return true
					}
				}
				if i < len(path) && globMatch(pattern, path, nextPatternSegmentStart, i+1) {
					return true
				}
			}
		}
		return false
	}

	// Match current character/segment
	if pattern[pi] == '*' {
		// * matches any characters in a segment (not across /)
		// Try matching 0 or more chars until we hit a / or end
		for i := pai; i <= len(path); i++ {
			if i < len(path) && path[i] == '/' {
				break
			}
			if globMatch(pattern, path, pi+1, i) {
				return true
			}
		}
		return false
	}

	if pattern[pi] == '?' {
		// ? matches exactly one character
		if pai >= len(path) || path[pai] == '/' {
			return false
		}
		return globMatch(pattern, path, pi+1, pai+1)
	}

	if pattern[pi] == '[' {
		// Character class
		endBracket := strings.IndexByte(pattern[pi+1:], ']')
		if endBracket == -1 {
			// Malformed bracket, treat as literal
			if pai >= len(path) || path[pai] != '[' {
				return false
			}
			return globMatch(pattern, path, pi+1, pai+1)
		}

		charClass := pattern[pi+1 : pi+1+endBracket]
		if pai >= len(path) {
			return false
		}

		// Check if current character matches the class
		if matchesCharClass(charClass, path[pai]) {
			return globMatch(pattern, path, pi+1+endBracket+1, pai+1)
		}
		return false
	}

	// Literal character
	if pai >= len(path) || pattern[pi] != path[pai] {
		return false
	}

	return globMatch(pattern, path, pi+1, pai+1)
}

func matchesCharClass(class string, ch byte) bool {
	// Simple character class matching
	// TODO: Support ranges like a-z
	for _, c := range class {
		if byte(c) == ch {
			return true
		}
	}
	return false
}
