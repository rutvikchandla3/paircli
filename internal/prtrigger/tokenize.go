package prtrigger

import "strings"

// tokenize splits a shell command into words the way a POSIX shell would for
// the purposes of spotting `git push` and `gh pr create`: whitespace separates
// words, single quotes are literal, double quotes honour \" and \\, a bare
// backslash escapes the next character, and the control operators
// `; & | ( )` (plus `&&` and `||`) are tokens of their own even when glued to
// a word. A newline outside quotes is a `;`, and a backslash-newline is
// dropped. It returns nil for a command with an unterminated quote or a
// trailing backslash, which no shell would run either.
func tokenize(command string) []string {
	var (
		tokens  []string
		word    strings.Builder
		started bool // a word is open, so an empty "" still yields a token
	)
	flush := func() {
		if started {
			tokens = append(tokens, word.String())
			word.Reset()
			started = false
		}
	}

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t' || r == '\r':
			flush()
		case r == '\n':
			flush()
			tokens = append(tokens, ";")
		case r == '\\':
			if i+1 >= len(runes) {
				return nil
			}
			i++
			if runes[i] != '\n' {
				word.WriteRune(runes[i])
				started = true
			}
		case r == '\'':
			end := indexRune(runes, i+1, '\'')
			if end < 0 {
				return nil
			}
			word.WriteString(string(runes[i+1 : end]))
			started = true
			i = end
		case r == '"':
			i++
			for ; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
					i++
				}
				word.WriteRune(runes[i])
			}
			if i >= len(runes) {
				return nil
			}
			started = true
		case strings.ContainsRune(";&|()", r):
			flush()
			if (r == '&' || r == '|') && i+1 < len(runes) && runes[i+1] == r {
				tokens = append(tokens, string([]rune{r, r}))
				i++
			} else {
				tokens = append(tokens, string(r))
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	flush()
	return tokens
}

func indexRune(runes []rune, from int, target rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}
