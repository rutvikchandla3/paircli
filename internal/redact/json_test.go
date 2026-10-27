package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// marshalIndent writes v the way the scan writes its JSON files.
func marshalIndent(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// Go writes < > & as \u003c \u003e \u0026, so in the file a token after one of
// them follows a word character and no word boundary. The decoded text has one.
func TestJSONRedactsTokensAfterHTMLEscapes(t *testing.T) {
	secrets := []struct{ name, secret, kind string }{
		{"github token", ghToken, KindGitHubToken},
		{"openai style key", skKey, KindAPIKey},
		{"jwt", jwt, KindJWT},
		{"slack token", slackTok, KindSlackToken},
		{"aws access key id", awsKeyID, KindAWSAccessKey},
	}
	for _, s := range secrets {
		for _, text := range []string{"see <" + s.secret + ">", "a&" + s.secret, ">" + s.secret + "<"} {
			in := marshalIndent(t, map[string]string{"v": text})
			r := New0()
			out := mustJSON(t, r, in)
			var got map[string]string
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("%s in %q: invalid JSON %s: %v", s.name, text, out, err)
			}
			want := strings.Replace(text, s.secret, "[REDACTED:"+s.kind+"]", 1)
			if got["v"] != want {
				t.Errorf("%s in %q: got %q, want %q", s.name, text, got["v"], want)
			}
			if r.Counts()[s.kind] != 1 {
				t.Errorf("%s in %q: counts = %v", s.name, text, r.Counts())
			}
		}
	}
}

func TestJSONRedactsKeysAndKeepsAMemberItsKey(t *testing.T) {
	tests := []struct{ name, in, want, kind string }{
		{"password value", `{"password": "hunter2hunter2"}`, `{"password": "[REDACTED:secret_assignment]"}`, KindSecretAssignment},
		{"authorization header", `{"Authorization":"Bearer abcdef1234567890"}`, `{"Authorization":"Bearer [REDACTED:bearer]"}`, KindBearer},
		{"aws secret", `{"aws_secret_access_key": "` + awsSec + `"}`, `{"aws_secret_access_key": "[REDACTED:aws_secret]"}`, KindAWSSecret},
		{"token as a key", `{"` + ghToken + `": "x"}`, `{"[REDACTED:github_token]": "x"}`, KindGitHubToken},
		{"token as a key and a value", `{"a&` + ghToken + `": [1, "` + ghToken + `"]}`, `{"a` + "\\u0026" + `[REDACTED:github_token]": [1, "[REDACTED:github_token]"]}`, KindGitHubToken},
	}
	for _, tt := range tests {
		r := New0()
		got := string(mustJSON(t, r, []byte(tt.in)))
		if got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
		n := strings.Count(tt.want, "[REDACTED:")
		if c := r.Counts(); c[tt.kind] != n || len(c) != 1 {
			t.Errorf("%s: counts = %v, want %s %d", tt.name, c, tt.kind, n)
		}
	}

	// A key that names a secret does not reach past its own value.
	in := `{"password": "x", "next": "plainvalue1234"}`
	if got := string(mustJSON(t, New0(), []byte(in))); got != in {
		t.Errorf("got %s, want it unchanged", got)
	}
}

// Only the strings that change are rewritten: key order, numbers, indentation
// and the spelling of every other string are the input's.
func TestJSONRewritesOnlyTheStringsItRedacts(t *testing.T) {
	in := "{\n  \"z\": 9007199254740993,\n  \"a\": 1.5e300,\n  \"m\": 0.10,\n  \"s\": \"caf\\u00e9 \\u003cb\\u003e\",\n" +
		"  \"cmd\": \"export X=1 \\u0026\\u0026 echo " + ghToken + "\",\n  \"n\": [null, true, -0]\n}\n"
	want := strings.Replace(in, "\\u0026\\u0026 echo "+ghToken, "\\u0026\\u0026 echo [REDACTED:github_token]", 1)
	if got := string(mustJSON(t, New0(), []byte(in))); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestJSONWithNothingToRedactIsByteIdentical(t *testing.T) {
	in := marshalIndent(t, map[string]any{"cmd": "a && b <c>", "lines": []string{"x\ty", "models.Job{ExecutionToken: &token}"}, "n": 3})
	r := New0()
	out := mustJSON(t, r, in)
	if !bytes.Equal(out, in) {
		t.Errorf("got %s, want the input", out)
	}
	if len(r.Counts()) != 0 {
		t.Errorf("counted %v", r.Counts())
	}
}

// A PEM block in a JSON string is matched across its decoded lines, and the
// marker leaves no newlines behind inside the string.
func TestJSONRedactsAPEMBlockInAString(t *testing.T) {
	in := marshalIndent(t, map[string]string{"output": "key:\n" + pem + "\ndone"})
	var got map[string]string
	if err := json.Unmarshal(mustJSON(t, New0(), in), &got); err != nil {
		t.Fatal(err)
	}
	if got["output"] != "key:\n[REDACTED:private_key]\ndone" {
		t.Errorf("got %q", got["output"])
	}
}

func TestJSONFallsBackToBytesForInvalidJSON(t *testing.T) {
	for _, in := range []string{
		`{"v":"see ` + ghToken + `"`,
		`{"a":1}{"b":"` + ghToken + `"}`,
		"{\"v\":\"" + ghToken + "\"}\n{\"v\":\"x\"}\n",
		"not json " + ghToken,
	} {
		r, ref := New0(), New0()
		got, want := mustJSON(t, r, []byte(in)), ref.Bytes([]byte(in))
		if !bytes.Equal(got, want) {
			t.Errorf("%q: got %q, want Bytes' %q", in, got, want)
		}
		if r.Counts()[KindGitHubToken] != ref.Counts()[KindGitHubToken] || r.Counts()[KindGitHubToken] != 1 {
			t.Errorf("%q: counts %v, Bytes counted %v", in, r.Counts(), ref.Counts())
		}
	}
}

func TestJSONIsIdempotentAndCountsOnce(t *testing.T) {
	in := marshalIndent(t, map[string]any{
		"a": "x <" + ghToken + ">", "password": "hunter2hunter2",
		"list": []string{"&" + skKey, "Authorization: Bearer abcdef1234567890"},
	})
	r := New0()
	once := mustJSON(t, r, in)
	want := map[string]int{KindGitHubToken: 1, KindSecretAssignment: 1, KindAPIKey: 1, KindBearer: 1}
	if got := r.Counts(); len(got) != len(want) || got[KindGitHubToken] != 1 || got[KindSecretAssignment] != 1 ||
		got[KindAPIKey] != 1 || got[KindBearer] != 1 {
		t.Errorf("counts = %v, want %v", got, want)
	}
	if twice := mustJSON(t, New0(), once); !bytes.Equal(twice, once) {
		t.Errorf("not idempotent:\n%s\n%s", once, twice)
	}
}

// mustJSON is r.JSON for input that holds no secret number.
func mustJSON(t *testing.T, r *Redactor, b []byte) []byte {
	t.Helper()
	out, err := r.JSON(b)
	if err != nil {
		t.Fatalf("JSON(%q): %v", b, err)
	}
	return out
}

// A key's own secret is redacted before the key gives its value context, so
// the value is still read as the assignment it is.
func TestJSONValueKeepsItsContextWhenTheKeyHeldASecret(t *testing.T) {
	env := []string{"MY_SERVICE_TOKEN=Zq9LiteralSecretValue"}
	tests := []struct {
		name, in, want string
		counts         map[string]int
	}{
		{"token in the key", `{"` + ghToken + ` password": "Hunter2abc9xyz"}`,
			`{"[REDACTED:github_token] password": "[REDACTED:secret_assignment]"}`,
			map[string]int{KindGitHubToken: 1, KindSecretAssignment: 1}},
		{"assignment in the key", `{"export GITHUB_TOKEN=` + ghToken + ` DB_PASSWORD": "Hunter2abc9xyz"}`,
			`{"export GITHUB_TOKEN=[REDACTED:github_token] DB_PASSWORD": "[REDACTED:secret_assignment]"}`,
			map[string]int{KindGitHubToken: 1, KindSecretAssignment: 1}},
		{"env literal in the key", `{"Zq9LiteralSecretValue password": "Hunter2abc9xyz"}`,
			`{"[REDACTED:env_secret] password": "[REDACTED:secret_assignment]"}`,
			map[string]int{KindEnvSecret: 1, KindSecretAssignment: 1}},
	}
	for _, tt := range tests {
		r := NewWith(env, "")
		if got := string(mustJSON(t, r, []byte(tt.in))); got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
		if got := r.Counts(); len(got) != len(tt.counts) || got[KindSecretAssignment] != 1 {
			t.Errorf("%s: counts = %v, want %v", tt.name, got, tt.counts)
		}
		for k, n := range tt.counts {
			if r.Counts()[k] != n {
				t.Errorf("%s: counts = %v, want %v", tt.name, r.Counts(), tt.counts)
			}
		}
	}
}

// A match that starts at the key and is rejected must not hide an assignment
// further inside the value.
func TestJSONFindsAnAssignmentInsideAValueItsKeyNames(t *testing.T) {
	in := `{"password_reset_url": "https://app.local/reset?token=Hunter2abc9xyz"}`
	want := `{"password_reset_url": "https://app.local/reset?token=[REDACTED:secret_assignment]"}`
	r := New0()
	if got := string(mustJSON(t, r, []byte(in))); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if r.Counts()[KindSecretAssignment] != 1 {
		t.Errorf("counts = %v", r.Counts())
	}
}

// Only strings are redacted: a number that holds a secret literal is refused,
// never uploaded.
func TestJSONRefusesASecretNumber(t *testing.T) {
	r := NewWith([]string{"NUM_SECRET_KEY=987654321012345"}, "")
	for _, in := range []string{`{"id": 987654321012345}`, `[1, -987654321012345e3]`} {
		if out, err := r.JSON([]byte(in)); !errors.Is(err, ErrSecretNumber) || out != nil {
			t.Errorf("%s: out %s err %v, want ErrSecretNumber", in, out, err)
		}
	}
	in := `{"id": 98765432101234, "s": "987654321012345"}`
	if got := string(mustJSON(t, r, []byte(in))); got != `{"id": 98765432101234, "s": "[REDACTED:env_secret]"}` {
		t.Errorf("got %s", got)
	}
}

// A token behind an escape the decoded text still holds: JSON nested in a JSON
// string, an ANSI colour code, and files that take the Bytes fallback.
func TestTokensAfterEscapesTheDecodeLeaves(t *testing.T) {
	nested := `{"out": "{\"x\":\"\\u003c` + ghToken + `\\u003e\"}"}`
	if got := string(mustJSON(t, New0(), []byte(nested))); got != `{"out": "{\"x\":\"\\u003c[REDACTED:github_token]\\u003e\"}"}` {
		t.Errorf("nested <: got %s", got)
	}
	if got := New0().Text("\x1b[0m" + ghToken + "\x1b[1;32m" + skKey); got != "\x1b[0m[REDACTED:github_token]\x1b[1;32m[REDACTED:api_key]" {
		t.Errorf("ESC[0m: got %q", got)
	}
	escaped := `{"v":"\u001b[0m` + ghToken + `\u001B[0m` + jwt + `"}`
	if got := string(New0().Bytes([]byte(escaped))); got != `{"v":"\u001b[0m[REDACTED:github_token]\u001B[0m[REDACTED:jwt]"}` {
		t.Errorf("escaped ESC in Bytes: got %s", got)
	}
	for name, in := range map[string]string{
		"bom":       "\xef\xbb\xbf{\"v\": \"see \\u003c" + ghToken + "\\u003e a\\u0026" + skKey + "\"}",
		"truncated": "{\"v\": \"see \\u003c" + ghToken + "\\u003e a\\u0026" + skKey + "\"",
	} {
		r := New0()
		got := string(mustJSON(t, r, []byte(in)))
		if strings.Contains(got, ghToken) || strings.Contains(got, skKey) || r.Counts()[KindGitHubToken] != 1 || r.Counts()[KindAPIKey] != 1 {
			t.Errorf("%s: got %q, counts %v", name, got, r.Counts())
		}
	}
}
