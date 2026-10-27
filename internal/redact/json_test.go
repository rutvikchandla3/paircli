package redact

import (
	"bytes"
	"encoding/json"
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

// Go writes < > & as < > &, so in the file a token after one of
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
			out := r.JSON(in)
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
		got := string(r.JSON([]byte(tt.in)))
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
	if got := string(New0().JSON([]byte(in))); got != in {
		t.Errorf("got %s, want it unchanged", got)
	}
}

// Only the strings that change are rewritten: key order, numbers, indentation
// and the spelling of every other string are the input's.
func TestJSONRewritesOnlyTheStringsItRedacts(t *testing.T) {
	in := "{\n  \"z\": 9007199254740993,\n  \"a\": 1.5e300,\n  \"m\": 0.10,\n  \"s\": \"caf\\u00e9 \\u003cb\\u003e\",\n" +
		"  \"cmd\": \"export X=1 \\u0026\\u0026 echo " + ghToken + "\",\n  \"n\": [null, true, -0]\n}\n"
	want := strings.Replace(in, "\\u0026\\u0026 echo "+ghToken, "\\u0026\\u0026 echo [REDACTED:github_token]", 1)
	if got := string(New0().JSON([]byte(in))); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestJSONWithNothingToRedactIsByteIdentical(t *testing.T) {
	in := marshalIndent(t, map[string]any{"cmd": "a && b <c>", "lines": []string{"x\ty", "models.Job{ExecutionToken: &token}"}, "n": 3})
	r := New0()
	out := r.JSON(in)
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
	if err := json.Unmarshal(New0().JSON(in), &got); err != nil {
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
		got, want := r.JSON([]byte(in)), ref.Bytes([]byte(in))
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
	once := r.JSON(in)
	want := map[string]int{KindGitHubToken: 1, KindSecretAssignment: 1, KindAPIKey: 1, KindBearer: 1}
	if got := r.Counts(); len(got) != len(want) || got[KindGitHubToken] != 1 || got[KindSecretAssignment] != 1 ||
		got[KindAPIKey] != 1 || got[KindBearer] != 1 {
		t.Errorf("counts = %v, want %v", got, want)
	}
	if twice := New0().JSON(once); !bytes.Equal(twice, once) {
		t.Errorf("not idempotent:\n%s\n%s", once, twice)
	}
}
