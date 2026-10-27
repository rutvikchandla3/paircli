package redact

import (
	"reflect"
	"strings"
	"testing"
)

// pemBlock is a whole private key, the shape a source file holds and a JSONL
// transcript does not.
var pemBlock = "-----BEGIN OPENSSH PRIVATE KEY-----\n" +
	"b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAAB\n" +
	"AAAAdwAAAAdzc2gtcnNhAAAAAwEAAQAAAYEAw1\n" +
	"-----END OPENSSH PRIVATE KEY-----"

// pemSource is a Go file with a key pasted into it.
const pemSource = `package main

import "os"

// key is the deploy key.
var key = ` + "`" + `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAAB
AAAAdwAAAAdzc2gtcnNhAAAAAwEAAQAAAYEAw1
-----END OPENSSH PRIVATE KEY-----` + "`" + `

func main() { os.Exit(0) }
`

func TestFileBytesRedactsMultiLineSecretThatBytesMisses(t *testing.T) {
	r := NewWith(nil, "")

	viaBytes := string(r.Bytes([]byte(pemSource)))
	if !strings.Contains(viaBytes, "BEGIN OPENSSH PRIVATE KEY") {
		t.Fatal("Bytes redacted the block, so this test no longer shows the difference")
	}

	viaFile := string(r.FileBytes([]byte(pemSource)))
	if strings.Contains(viaFile, "b3BlbnNzaC1rZXktdjEAAAAABG5vbmU") {
		t.Errorf("FileBytes left the key body in place:\n%s", viaFile)
	}
	if !strings.Contains(viaFile, "[REDACTED:private_key]") {
		t.Errorf("FileBytes did not mark the key:\n%s", viaFile)
	}
}

// TestFileBytesPreservesTheNewlineCount is the guarantee FileBytes makes: every
// line after a match keeps its number. The line count of a file that does not
// end in a newline is one more than its newline count, and a match running to
// that end re-emits its newlines, so the two can differ by one there — which is
// why the newline count is what is asserted, and why filecontent takes the
// file's own line count from the input.
func TestFileBytesPreservesTheNewlineCount(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"pem block", pemSource},
		{"pem alone", pemBlock},
		{"token", "package a\n\nvar t = \"" + ghToken + "\"\n"},
		{"assignment", "package a\n\n// token\nvar token = \"s3cr3t-value-9\"\n"},
		{"jwt", "a\nb = \"" + jwt + "\"\nc\n"},
		{"bare newline before a value", "password =\n\"hunter2hunter2\"\n"},
		{"crlf", "package a\r\n\r\nvar t = \"" + ghToken + "\"\r\n"},
		{"no trailing newline", "var t = \"" + ghToken + "\""},
		{"empty", ""},
		{"nothing to do", "package a\n\nfunc main() {}\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewWith(nil, "")
			out := string(r.FileBytes([]byte(tc.in)))
			if got, want := strings.Count(out, "\n"), strings.Count(tc.in, "\n"); got != want {
				t.Errorf("newline count = %d, want %d:\n%q", got, want, out)
			}
		})
	}
}

func TestFileBytesRedactsTheSameKindsAsText(t *testing.T) {
	// Every rule has to fire through FileBytes too: it is a different code path
	// over the same rule set, and the one a whole source file goes through.
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"github token", "token is " + ghToken + " ok", "token is [REDACTED:github_token] ok"},
		{"openai style key", "key " + skKey + ".", "key [REDACTED:api_key]."},
		{"aws access key id", "id " + awsKeyID + " end", "id [REDACTED:aws_access_key] end"},
		{"slack token", "tok " + slackTok, "tok [REDACTED:slack_token]"},
		{"jwt", "jwt " + jwt + " end", "jwt [REDACTED:jwt] end"},
		{"bearer header", "Authorization: Bearer abcdef1234567890", "Authorization: Bearer [REDACTED:bearer]"},
		{"assignment", "API_KEY=abc123def456", "API_KEY=[REDACTED:secret_assignment]"},
		{"go short assignment", `clientSecret := "s3cr3t-value-9"`, `clientSecret := "[REDACTED:secret_assignment]"`},
		{"token in url query", "https://x.example/cb?token=Zk3v9Pq27Lm&next=/", "https://x.example/cb?token=[REDACTED:secret_assignment]&next=/"},
		// The block's three newlines come back after the marker, so the lines
		// after it keep their numbers.
		{"pem block", "before\n" + pem + "\nafter", "before\n[REDACTED:private_key]\n\n\n\nafter"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewWith(nil, "")
			if got := string(r.FileBytes([]byte(tc.in))); got != tc.want {
				t.Errorf("FileBytes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFileBytesIsIdempotent(t *testing.T) {
	r := NewWith(nil, "")
	in := pemSource + "\nvar t = \"" + ghToken + "\"\n"
	once := r.FileBytes([]byte(in))
	twice := r.FileBytes(once)
	if string(once) != string(twice) {
		t.Errorf("redacting twice changed the text:\n%q\n%q", once, twice)
	}
}

func TestFileBytesCountsWhatItRemoved(t *testing.T) {
	r := NewWith(nil, "")
	r.FileBytes([]byte(pemSource))
	counts := r.Counts()
	if counts[KindPrivateKey] != 1 {
		t.Errorf("counts = %v, want one private_key", counts)
	}
}

func TestFileBytesNilIsEmpty(t *testing.T) {
	r := NewWith(nil, "")
	if got := r.FileBytes(nil); len(got) != 0 {
		t.Errorf("FileBytes(nil) = %q, want empty", got)
	}
}

// TestFileBytesKeepsEnvironmentValuesOffLine checks the literals pass, which
// bypasses the rules: a value with a real newline in it is not something a rule
// sees, and it must not shift the lines either.
func TestFileBytesKeepsEnvironmentValuesOffLine(t *testing.T) {
	r := NewWith([]string{"MULTILINE_SECRET=averyverylongsecret\nwithanewline"}, "")
	in := "package a\n\nvar s = `averyverylongsecret\nwithanewline`\n"
	out := string(r.FileBytes([]byte(in)))
	if strings.Contains(out, "averyverylongsecret") {
		t.Errorf("the literal survived:\n%s", out)
	}
	if got, want := strings.Count(out, "\n"), strings.Count(in, "\n"); got != want {
		t.Errorf("newline count = %d, want %d:\n%q", got, want, out)
	}
}

// composeSecrets is the shape that broke whole-buffer redaction: a key whose
// name ends in "secret", a blank line, then a bare "secrets:" key of its own.
// secret_assignment's separator allows whitespace before the colon, and in
// whole-buffer mode that whitespace was a newline, so the rule paired "secret"
// with the next line's text and redacted a line holding no secret.
const composeSecrets = `services:
  ade-jupyter-secret:

secrets:
  ade_ca_bundle:
    file: ${ADE_CA_BUNDLE_FILE:-/etc/ssl/cert.pem}
`

func TestFileBytesDoesNotRedactAcrossALineBreak(t *testing.T) {
	r := NewWith(nil, "")
	got := string(r.FileBytes([]byte(composeSecrets)))
	if got != composeSecrets {
		t.Errorf("whole-file redaction changed a file with no secret in it:\n got %q\nwant %q", got, composeSecrets)
	}
	if counts := r.Counts(); len(counts) != 0 {
		t.Errorf("counts = %v, want none", counts)
	}
}

// TestFileBytesEqualsBytesWithoutAMultiLineRule is the property the whole-file
// pass has to hold: apart from the rules written to span lines, it redacts
// exactly what the line-by-line pass redacts, and no more.
func TestFileBytesEqualsBytesWithoutAMultiLineRule(t *testing.T) {
	inputs := []string{
		composeSecrets,
		"",
		"\n",
		"a",
		"a\n",
		"secrets:\n",
		"secret:\nkey:\nvalue:\n",
		"password =\n\"hunter2hunter2\"\n",
		"api_key: token: secret:\n",
		"token is " + ghToken + " ok",
		"Authorization: Bearer abcdef1234567890\nnext line\n",
		"key " + skKey + "\n",
		"a\nb\nc\n" + composeSecrets + "d\n",
		"// no trailing newline\nsecrets:",
		"x\ny\n\ntoken:\napp:\n",
	}
	for _, in := range inputs {
		r := NewWith(nil, "")
		viaBytes := string(r.Bytes([]byte(in)))
		r2 := NewWith(nil, "")
		viaFile := string(r2.FileBytes([]byte(in)))
		if viaBytes != viaFile {
			t.Errorf("FileBytes differs from Bytes for %q:\n Bytes %q\n  File %q", in, viaBytes, viaFile)
		}
		if a, b := r.Counts(), r2.Counts(); !reflect.DeepEqual(a, b) {
			t.Errorf("counts differ for %q: Bytes %v, FileBytes %v", in, a, b)
		}
	}
}

// TestFileBytesStillSpansLinesForTheRuleThatNeedsIt keeps the property above
// from being satisfied by giving up on multi-line secrets: the private_key rule
// must still fire where Bytes cannot.
func TestFileBytesStillSpansLinesForTheRuleThatNeedsIt(t *testing.T) {
	in := pemSource
	r := NewWith(nil, "")
	if !strings.Contains(string(r.Bytes([]byte(in))), "b3BlbnNzaC1rZXktdjEAAAAABG5vbmU") {
		t.Fatal("Bytes redacted the block, so this test no longer shows the difference")
	}
	r2 := NewWith(nil, "")
	out := string(r2.FileBytes([]byte(in)))
	if strings.Contains(out, "b3BlbnNzaC1rZXktdjEAAAAABG5vbmU") {
		t.Errorf("FileBytes left the key body in place:\n%s", out)
	}
	if !strings.Contains(out, "[REDACTED:private_key]") {
		t.Errorf("FileBytes did not mark the key:\n%s", out)
	}
}

// TestFileBytesRedactsAMultiLineLiteral covers the one thing the whole-file
// pass does that Bytes cannot, beyond the multi-line rules: an exact value with
// a newline in it, such as an environment variable's.
func TestFileBytesRedactsAMultiLineLiteral(t *testing.T) {
	value := "averyverylongsecret\nwithanewline"
	in := "package a\n\nvar s = `" + value + "`\n"
	r := NewWith([]string{"MULTILINE_SECRET=" + value}, "")
	out := string(r.FileBytes([]byte(in)))
	if strings.Contains(out, "averyverylongsecret") {
		t.Errorf("the literal survived:\n%s", out)
	}
	if got, want := strings.Count(out, "\n"), strings.Count(in, "\n"); got != want {
		t.Errorf("newline count = %d, want %d", got, want)
	}
}
