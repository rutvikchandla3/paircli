package classify

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// Unwrap removes shell wrappers from a command string.
// If cmd matches ^(/bin/|/usr/bin/)?(ba|z)?sh -l?c '(<x>)' or "(<x>)" $ (dot matches newline),
// it returns the inner text with '\” → '. Repeat until no wrapper remains.
// Otherwise returns cmd trimmed.
func Unwrap(cmd string) string {
	// Regex patterns to match shell wrappers with single and double quotes
	wrapperSingleRE := regexp.MustCompile(`(?s)^(?:/bin/|/usr/bin/)?(?:ba|z)?sh\s+-l?c\s+'(.*)'\s*$`)
	wrapperDoubleRE := regexp.MustCompile(`(?s)^(?:/bin/|/usr/bin/)?(?:ba|z)?sh\s+-l?c\s+"(.*)"\s*$`)

	for {
		// Try single quote pattern first
		matches := wrapperSingleRE.FindStringSubmatch(cmd)
		if len(matches) < 2 {
			// Try double quote pattern
			matches = wrapperDoubleRE.FindStringSubmatch(cmd)
			if len(matches) < 2 {
				// No wrapper match, return trimmed
				return strings.TrimSpace(cmd)
			}
		}
		// Extract the inner content
		inner := matches[1]
		// Replace '\'' with ' (escaped single quote in shell)
		inner = strings.ReplaceAll(inner, `\'`, `'`)
		cmd = inner
	}
}

// Segments splits cmd (after Unwrap) on &&, ||, ;, |, and newlines
// that are outside single quotes, double quotes, $(...), and not backslash-escaped.
// Returns trimmed non-empty segments.
func Segments(cmd string) []string {
	cmd = Unwrap(cmd)

	var result []string
	var current strings.Builder
	var inSingle bool
	var inDouble bool
	var parenDepth int

	i := 0
	for i < len(cmd) {
		ch := cmd[i]

		// Handle escape sequences
		if i > 0 && cmd[i-1] == '\\' {
			// Check if the backslash itself is escaped
			isEscaped := false
			backslashCount := 0
			for j := i - 1; j >= 0 && cmd[j] == '\\'; j-- {
				backslashCount++
			}
			// If odd number of backslashes, this character is escaped
			if backslashCount%2 == 1 {
				isEscaped = true
			}
			if isEscaped {
				current.WriteByte(ch)
				i++
				continue
			}
		}

		// Track quote state
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteByte(ch)
			i++
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteByte(ch)
			i++
			continue
		}

		// Track $(...) nesting
		if !inSingle && !inDouble {
			if ch == '$' && i+1 < len(cmd) && cmd[i+1] == '(' {
				parenDepth++
				current.WriteByte(ch)
				i++
				current.WriteByte(cmd[i])
				i++
				continue
			}
			if ch == ')' && parenDepth > 0 {
				parenDepth--
				current.WriteByte(ch)
				i++
				continue
			}
		}

		// Check for delimiters
		if !inSingle && !inDouble && parenDepth == 0 {
			if ch == '&' && i+1 < len(cmd) && cmd[i+1] == '&' {
				seg := strings.TrimSpace(current.String())
				if seg != "" {
					result = append(result, seg)
				}
				current.Reset()
				i += 2 // skip both &
				continue
			}
			if ch == '|' {
				if i+1 < len(cmd) && cmd[i+1] == '|' {
					seg := strings.TrimSpace(current.String())
					if seg != "" {
						result = append(result, seg)
					}
					current.Reset()
					i += 2 // skip both |
					continue
				}
				// Single pipe is also a delimiter
				seg := strings.TrimSpace(current.String())
				if seg != "" {
					result = append(result, seg)
				}
				current.Reset()
				i++
				continue
			}
			if ch == ';' {
				seg := strings.TrimSpace(current.String())
				if seg != "" {
					result = append(result, seg)
				}
				current.Reset()
				i++
				continue
			}
			if ch == '\n' {
				seg := strings.TrimSpace(current.String())
				if seg != "" {
					result = append(result, seg)
				}
				current.Reset()
				i++
				continue
			}
		}

		current.WriteByte(ch)
		i++
	}

	// Add final segment if any
	seg := strings.TrimSpace(current.String())
	if seg != "" {
		result = append(result, seg)
	}

	return result
}

// Tokens performs a shell-like split of a segment on unquoted whitespace.
// Removes surrounding quotes and keeps $VAR literally.
func Tokens(segment string) []string {
	var result []string
	var current strings.Builder
	var inSingle bool
	var inDouble bool

	i := 0
	for i < len(segment) {
		ch := segment[i]

		// Handle escape
		if i > 0 && segment[i-1] == '\\' {
			isEscaped := false
			backslashCount := 0
			for j := i - 1; j >= 0 && segment[j] == '\\'; j-- {
				backslashCount++
			}
			if backslashCount%2 == 1 {
				isEscaped = true
			}
			if isEscaped {
				current.WriteByte(ch)
				i++
				continue
			}
		}

		// Track quotes
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			// Don't include the quote in the token
			i++
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			// Don't include the quote in the token
			i++
			continue
		}

		// Handle whitespace
		if unicode.IsSpace(rune(ch)) && !inSingle && !inDouble {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
			i++
			continue
		}

		current.WriteByte(ch)
		i++
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// ShortCmd returns Unwrap + collapsed whitespace + clipped to 80 runes.
func ShortCmd(cmd string) string {
	unwrapped := Unwrap(cmd)
	// Collapse whitespace
	fields := strings.Fields(unwrapped)
	collapsed := strings.Join(fields, " ")
	// Clip to 80 runes
	return model.Clip(collapsed, 80)
}
