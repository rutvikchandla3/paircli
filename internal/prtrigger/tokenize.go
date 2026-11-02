package prtrigger

import "strings"

// word is one token of a shell command and the byte offsets in text of each
// `$` a shell would expand: one outside single quotes and not escaped by a
// backslash.
type word struct {
	text    string
	dollars []int
}

// tokenize splits a shell command into words the way a POSIX shell would for
// the purposes of spotting `git push` and `gh pr create`: whitespace separates
// words, single quotes are literal, double quotes honour \" \\ and \$, a bare
// backslash escapes the next character, and the control operators
// `; & | ( )` (plus `&&` and `||`) are tokens of their own even when glued to
// a word. A newline outside quotes is a `;`, and a backslash-newline is
// dropped. It returns nil for a command with an unterminated quote or a
// trailing backslash, which no shell would run either.
func tokenize(command string) []string { return texts(lex(command)) }

// texts is the text of each word.
func texts(words []word) []string {
	if words == nil {
		return nil
	}
	tokens := make([]string, len(words))
	for i, w := range words {
		tokens[i] = w.text
	}
	return tokens
}

// lex is tokenize keeping where each word holds an expandable `$`.
func lex(command string) []word {
	var (
		words   []word
		text    strings.Builder
		dollars []int
		started bool // a word is open, so an empty "" still yields a token
	)
	flush := func() {
		if started {
			words = append(words, word{text: text.String(), dollars: dollars})
			text.Reset()
			dollars = nil
			started = false
		}
	}
	operator := func(op string) { words = append(words, word{text: op}) }
	// write adds r to the open word; a `$` there is one a shell would expand.
	write := func(r rune) {
		if r == '$' {
			dollars = append(dollars, text.Len())
		}
		text.WriteRune(r)
		started = true
	}

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t' || r == '\r':
			flush()
		case r == '\n':
			flush()
			operator(";")
		case r == '\\':
			if i+1 >= len(runes) {
				return nil
			}
			i++
			if runes[i] != '\n' {
				text.WriteRune(runes[i])
				started = true
			}
		case r == '\'':
			end := indexRune(runes, i+1, '\'')
			if end < 0 {
				return nil
			}
			text.WriteString(string(runes[i+1 : end]))
			started = true
			i = end
		case r == '"':
			i++
			for ; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' && i+1 < len(runes) && strings.ContainsRune(`"\$`, runes[i+1]) {
					i++
					text.WriteRune(runes[i])
					continue
				}
				write(runes[i])
			}
			if i >= len(runes) {
				return nil
			}
			started = true
		case strings.ContainsRune(";&|()", r):
			flush()
			if (r == '&' || r == '|') && i+1 < len(runes) && runes[i+1] == r {
				operator(string([]rune{r, r}))
				i++
			} else {
				operator(string(r))
			}
		default:
			write(r)
		}
	}
	flush()
	return words
}

func indexRune(runes []rune, from int, target rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}
