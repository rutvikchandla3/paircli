package prtrigger

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{"words", "git push origin main", []string{"git", "push", "origin", "main"}},
		{"extra whitespace", "  git \t push   origin ", []string{"git", "push", "origin"}},
		{"empty", "", nil},
		{"single quotes are literal", `echo 'a b \n $x'`, []string{"echo", `a b \n $x`}},
		{"double quotes keep spaces", `echo "a  b"`, []string{"echo", "a  b"}},
		{"double quote escapes", `echo "a\"b\\c\d"`, []string{"echo", `a"b\c\d`}},
		{"adjacent quoted parts join", `a'b'"c"d`, []string{"abcd"}},
		{"empty quotes make an empty word", `echo ""`, []string{"echo", ""}},
		{"backslash escapes a space", `cd my\ dir`, []string{"cd", "my dir"}},
		{"backslash newline is a continuation", "git push \\\n origin", []string{"git", "push", "origin"}},
		{"operators split glued words", "a&&b||c;d|e&f", []string{"a", "&&", "b", "||", "c", ";", "d", "|", "e", "&", "f"}},
		{"parentheses", "(cd x; git push)", []string{"(", "cd", "x", ";", "git", "push", ")"}},
		{"stacked parentheses", "((a))", []string{"(", "(", "a", ")", ")"}},
		{"operators inside quotes are literal", `echo 'a && b' "c;d"`, []string{"echo", "a && b", "c;d"}},
		{"escaped operator is literal", `echo a\;b`, []string{"echo", "a;b"}},
		{"newline separates commands", "git add .\ngit push", []string{"git", "add", ".", ";", "git", "push"}},
		{"hash is not a comment", "echo a #b", []string{"echo", "a", "#b"}},
		{"redirect stays in the word", "git push 2>&1", []string{"git", "push", "2>", "&", "1"}},
		{"unterminated single quote", `echo 'abc`, nil},
		{"unterminated double quote", `echo "abc`, nil},
		{"trailing backslash", `echo abc\`, nil},
		{"non-ascii", `echo "héllo wörld"`, []string{"echo", "héllo wörld"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tokenize(tt.command); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenize(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}
