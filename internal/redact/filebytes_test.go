package redact

import (
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
