package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// JSON returns the JSON document b with secrets removed from the decoded text
// of every string in it, object keys included.
//
// Bytes reads a JSON file as its escaped spelling, and that hides secrets: Go
// writes < > & as \u003c \u003e \u0026, so a token right after one of them
// follows a word character and its rule's word boundary never holds. Here the
// rules see the text itself, the way Text sees a rendered string.
//
// Only a string whose text changes is rewritten, spliced back in place encoded
// the way encoding/json writes every scan file, so the result is valid JSON by
// construction and object key order, numbers and indentation are untouched. A
// document in which nothing changes comes back as b itself, byte for byte.
//
// A value keeps the context its object key gave it in the file: "password":
// "hunter2..." is redacted as the assignment it reads as, the way Bytes sees
// it. A string is redacted the way FileBytes redacts a file, so a PEM block
// held in one is matched across its lines and no other rule spans a line, but
// a match leaves no newlines behind it.
//
// Only strings are redacted. A number is not, so one that holds a literal this
// process knows is secret is an error, ErrSecretNumber: it cannot be replaced
// without changing the file's shape, and uploading it would ship the secret.
//
// b that is not one valid JSON value is redacted by Bytes instead, so an
// unexpected shape degrades and never fails.
func (r *Redactor) JSON(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return b, nil
	}
	out, ok, err := r.jsonStrings(b)
	if err != nil {
		return nil, err
	}
	if ok {
		return out, nil
	}
	return r.Bytes(b), nil
}

// ErrSecretNumber is JSON's error for a number that holds a secret literal.
var ErrSecretNumber = errors.New("a JSON number holds a secret value, and only strings can be redacted")

// splice is one string literal of the input, b[start:end] including its
// quotes, and what replaces it.
type splice struct {
	start, end int
	with       []byte
}

// jsonFrame is one open container: an object, and whether its next string is
// a key and the last key it read, or an array.
type jsonFrame struct {
	object  bool
	wantKey bool
	key     string
}

// jsonStrings redacts every string of b in one streaming pass. ok is false when
// b does not parse; nothing is then counted.
func (r *Redactor) jsonStrings(b []byte) (out []byte, ok bool, err error) {
	if !json.Valid(b) {
		return nil, false, nil
	}
	// Counted apart and merged only on success, so a fallback to Bytes never
	// counts a match twice.
	w := &Redactor{literals: r.literals, counts: map[string]int{}}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var stack []jsonFrame
	var splices []splice
	pos := 0 // just past the last string literal read
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, nil
		}
		var top *jsonFrame
		if len(stack) > 0 {
			top = &stack[len(stack)-1]
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{', '[':
				if top != nil && top.object {
					top.wantKey = true
				}
				stack = append(stack, jsonFrame{object: t == '{', wantKey: t == '{'})
			default:
				stack = stack[:len(stack)-1]
			}
		case string:
			// Between two tokens there is only whitespace, ',' and ':', and no
			// other token holds a quote, so the next quote opens this string.
			i := bytes.IndexByte(b[pos:], '"')
			if i < 0 {
				return nil, false, nil
			}
			start := pos + i
			end := literalEnd(b, start)
			if end < 0 {
				return nil, false, nil
			}
			pos = end

			var red string
			switch {
			case top != nil && top.object && top.wantKey:
				// The key's context for its value is its redacted text: a
				// secret in the key is gone from the prefix scrubMember builds,
				// so it cannot change there and push the value out of context.
				red = w.scrubString(t)
				top.key, top.wantKey = red, false
			case top != nil && top.object:
				top.wantKey = true
				red = w.scrubMember(top.key, t)
			default:
				red = w.scrubString(t)
			}
			if red != t {
				enc, err := json.Marshal(red)
				if err != nil {
					return nil, false, nil
				}
				splices = append(splices, splice{start, end, enc})
			}
		default:
			if top != nil && top.object {
				top.wantKey = true
			}
			if n, isNum := t.(json.Number); isNum && w.holdsLiteral(string(n)) {
				return nil, false, ErrSecretNumber
			}
		}
	}
	for kind, n := range w.Counts() {
		r.add(kind, n)
	}
	if len(splices) == 0 {
		return b, true, nil
	}
	var buf bytes.Buffer
	buf.Grow(len(b))
	last := 0
	for _, s := range splices {
		buf.Write(b[last:s.start])
		buf.Write(s.with)
		last = s.end
	}
	buf.Write(b[last:])
	return buf.Bytes(), true, nil
}

// literalEnd returns the index just past the closing quote of the JSON string
// literal that opens at b[start], or -1.
func literalEnd(b []byte, start int) int {
	for i := start + 1; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

// scrubString redacts one decoded JSON string.
func (r *Redactor) scrubString(s string) string { return r.scrubFile(s, false) }

// scrubMember redacts the value of an object member in the context of its key,
// as the text `"key": "value` that the file spells it as, so that a rule which
// pairs a name with a value (secret_assignment, aws_secret, bearer) still sees
// both. A match that reaches into the key is the key's own business, already
// redacted on its own: then the value is redacted alone.
//
// The value is then redacted once more on its own, because a match that
// starts at the key can cover an assignment inside the value and be rejected:
// "password_reset_url": "https://...?token=..." is a URL to the rule that
// begins at password_reset_url, which hides the token= after it.
func (r *Redactor) scrubMember(key, value string) string {
	prefix := `"` + key + `": "`
	if !r.mayMatch(prefix + value) {
		return value
	}
	try := &Redactor{literals: r.literals, counts: map[string]int{}}
	if out := try.scrubString(prefix + value); len(out) >= len(prefix) && out[:len(prefix)] == prefix {
		for kind, n := range try.Counts() {
			r.add(kind, n)
		}
		return r.scrubString(out[len(prefix):])
	}
	return r.scrubString(value)
}

// mayMatch reports whether any rule's hint or any literal occurs in s: when
// none does, nothing can be redacted and the work of trying is skipped.
func (r *Redactor) mayMatch(s string) bool {
	lower := strings.ToLower(s)
	for i := range rules {
		if hasAny(lower, rules[i].hints) {
			return true
		}
	}
	return r.holdsLiteral(s)
}

// holdsLiteral reports whether s contains a value this process knows is secret.
func (r *Redactor) holdsLiteral(s string) bool {
	for _, l := range r.literals {
		if strings.Contains(s, l.value) {
			return true
		}
	}
	return false
}
