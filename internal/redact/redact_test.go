package redact

import (
	"strings"
	"testing"
)

// Test secrets are assembled from parts so no scanner mistakes this file for a leak.
var (
	ghToken  = "ghp_" + strings.Repeat("aB3", 12)
	ghPAT    = "github_pat_" + strings.Repeat("A1b2", 8)
	skKey    = "sk-" + "proj-" + strings.Repeat("Q7z", 10)
	awsKeyID = "AKIA" + "IOSFODNN7EXAMPLE"[:16]
	awsSec   = strings.Repeat("wJalrXUtnFEMI/K7MDENG", 2)[:40]
	slackTok = "xoxb-" + "123456789012-" + "abcdefABCDEF"
	jwt      = "eyJhbGciOiJIUzI1NiJ9." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0." + "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	pem      = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA7\nabcDEF==\n-----END RSA PRIVATE KEY-----"
)

func TestRulesGolden(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		kind string
	}{
		{"github token", "token is " + ghToken + " ok", "token is [REDACTED:github_token] ok", KindGitHubToken},
		{"github fine-grained", "x " + ghPAT, "x [REDACTED:github_token]", KindGitHubToken},
		{"openai style key", "key " + skKey + ".", "key [REDACTED:api_key].", KindAPIKey},
		{"aws access key id", "id " + awsKeyID + " end", "id [REDACTED:aws_access_key] end", KindAWSAccessKey},
		{"aws secret assignment", "aws_secret_access_key = " + awsSec, "aws_secret_access_key = [REDACTED:aws_secret]", KindAWSSecret},
		{"slack token", "tok " + slackTok, "tok [REDACTED:slack_token]", KindSlackToken},
		{"jwt", "jwt " + jwt + " end", "jwt [REDACTED:jwt] end", KindJWT},
		{"bearer header", "Authorization: Bearer abcdef1234567890", "Authorization: Bearer [REDACTED:bearer]", KindBearer},
		{"bearer json", `{"Authorization": "Bearer abcdef1234567890"}`, `{"Authorization": "Bearer [REDACTED:bearer]"}`, KindBearer},
		{"pem block", "before\n" + pem + "\nafter", "before\n[REDACTED:private_key]\nafter", KindPrivateKey},
		{"pem with escaped newlines", `{"text":"-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----"}`, `{"text":"[REDACTED:private_key]"}`, KindPrivateKey},
		{"api_key assignment", "API_KEY=abc123def456", "API_KEY=[REDACTED:secret_assignment]", KindSecretAssignment},
		{"password colon quoted", `password: "hunter2hunter2"`, `password: "[REDACTED:secret_assignment]"`, KindSecretAssignment},
		{"go short assignment", `clientSecret := "s3cr3t-value-9"`, `clientSecret := "[REDACTED:secret_assignment]"`, KindSecretAssignment},
		{"json escaped assignment", `{"out":"{\"access_token\": \"Zk3v9Pq27Lm\"}"}`, `{"out":"{\"access_token\": \"[REDACTED:secret_assignment]\"}"}`, KindSecretAssignment},
		{"token in url query", "https://x.example/cb?token=Zk3v9Pq27Lm&next=/", "https://x.example/cb?token=[REDACTED:secret_assignment]&next=/", KindSecretAssignment},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewWith(nil, "")
			got := r.Text(tc.in)
			if got != tc.want {
				t.Fatalf("Text(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
			if r.Counts()[tc.kind] != 1 {
				t.Errorf("Counts()[%s] = %d, want 1 (all: %v)", tc.kind, r.Counts()[tc.kind], r.Counts())
			}
			if again := NewWith(nil, "").Text(got); again != got {
				t.Errorf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestFalsePositivesStayUntouched(t *testing.T) {
	tests := []string{
		"",
		"hello world",
		"multi\nline\nstring",
		"special chars: !@#$%^&*()",
		"password = false",
		"token: true",
		"secret = null",
		"password = None",
		"token = process.env.GITHUB_TOKEN",
		"token = self.refresh_token",
		"password = getpass()",
		"secret = os.environ[\"SECRET_NAME\"]",
		"password = ${DB_PASSWORD}",
		"password=$DB_PASSWORD",
		"api_key = <your api key>",
		"API_KEY=YOUR_API_KEY_HERE",
		"token = xxxxxxxxxxxxxxxx",
		"password: ********",
		"password = [REDACTED:secret_assignment]",
		"max_tokens = 16384000",
		"token_type: bearer",
		"secret_path = /var/run/secrets/kubernetes.io/serviceaccount/token",
		"token_file = ./config/token.json",
		"token_url = https://oauth.example.com/token?x=1",
		"password_field = usernameOrEmail",
		"Authorization: Bearer $TOKEN",
		"Authorization: Bearer <token>",
		"curl -H \"Authorization: Bearer ${GITHUB_TOKEN}\" https://api.github.com",
		"see /Users/dev/projects/ghp_tokens/readme.md",
		"git push https://github.com/acme/repo.git",
		"risk-assessment-for-the-new-feature-rollout",
		"sk-learn-is-short",
		"BEGIN PRIVATE KEY is documented here",
		"AKIA is a prefix, AKIAshort too",
		"ghp_short",
	}
	for _, in := range tests {
		r := NewWith(nil, "")
		if got := r.Text(in); got != in {
			t.Errorf("Text(%q) = %q, want unchanged", in, got)
		}
		if len(r.Counts()) != 0 {
			t.Errorf("Text(%q) counted %v", in, r.Counts())
		}
	}
}

func TestEnvValuesAreRedacted(t *testing.T) {
	environ := []string{
		"MY_SERVICE_TOKEN=tok_live_9f8e7d6c5b4a",
		"DB_PASSWORD=correct horse battery staple",
		"SESSION_COOKIE=abc.def.ghi.jkl.mno",
		"SHORT_SECRET=tiny",                                          // below the length floor
		"SSH_AUTH_SOCK=/private/tmp/com.apple.launchd.xyz/Listeners", // a path
		"CALLBACK_AUTH_URL=https://example.com/oauth/callback",       // a URL
		"FEATURE_AUTH_ENABLED=enabled",                               // boolean-ish and short
		"GIT_AUTHOR_NAME=Kousik Sanagavarapu",                        // AUTHOR is not AUTH
		"HOME=/Users/dev",
		"MONKEY=banana-banana-banana",
	}
	r := NewWith(environ, "")
	in := "export X=tok_live_9f8e7d6c5b4a; echo correct horse battery staple; ssh /private/tmp/com.apple.launchd.xyz/Listeners; Kousik Sanagavarapu; tiny; abc.def.ghi.jkl.mno"
	want := "export X=[REDACTED:env_secret]; echo [REDACTED:env_secret]; ssh /private/tmp/com.apple.launchd.xyz/Listeners; Kousik Sanagavarapu; tiny; [REDACTED:env_secret]"
	// MONKEY contains KEY, so banana-banana-banana is a secret too, but is absent from the input.
	if got := r.Text(in); got != want {
		t.Fatalf("Text:\n got %q\nwant %q", got, want)
	}
	if got := r.Counts()[KindEnvSecret]; got != 3 {
		t.Errorf("env_secret count = %d, want 3", got)
	}
}

func TestEnvValueWithQuotesIsRedactedInsideJSONLines(t *testing.T) {
	r := NewWith([]string{`APP_SECRET=pa"ss\word-123456`}, "")
	in := `{"text":"APP_SECRET is pa\"ss\\word-123456 here"}`
	want := `{"text":"APP_SECRET is [REDACTED:env_secret] here"}`
	if got := r.Text(in); got != want {
		t.Fatalf("Text:\n got %q\nwant %q", got, want)
	}
}

func TestNPMRCAuthTokens(t *testing.T) {
	npmrc := "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken=npm_" + strings.Repeat("Ab1", 12) +
		"\n//other.example/:_authToken=${NPM_TOKEN}\n"
	r := NewWith(nil, npmrc)
	tok := "npm_" + strings.Repeat("Ab1", 12)
	if got := r.Text("use " + tok + " and ${NPM_TOKEN}"); got != "use [REDACTED:npm_token] and ${NPM_TOKEN}" {
		t.Fatalf("Text = %q", got)
	}
	if r.Counts()[KindNPMToken] != 1 {
		t.Errorf("counts = %v", r.Counts())
	}
}

func TestBytesKeepsLinesAndJSON(t *testing.T) {
	in := `{"a":"` + ghToken + `"}` + "\n" +
		`{"b":"-----BEGIN PRIVATE KEY-----"}` + "\n" + // BEGIN without END on its own line is left alone
		`{"c":"-----END PRIVATE KEY-----"}` + "\n" +
		`{"d":"clean"}`
	r := NewWith(nil, "")
	got := string(r.Bytes([]byte(in)))
	want := `{"a":"[REDACTED:github_token]"}` + "\n" +
		`{"b":"-----BEGIN PRIVATE KEY-----"}` + "\n" +
		`{"c":"-----END PRIVATE KEY-----"}` + "\n" +
		`{"d":"clean"}`
	if got != want {
		t.Fatalf("Bytes:\n got %q\nwant %q", got, want)
	}
	if len(r.Bytes(nil)) != 0 {
		t.Error("Bytes(nil) should be empty")
	}
}

func TestCountsAccumulateAndMerge(t *testing.T) {
	r := NewWith(nil, "")
	r.Text(ghToken + " " + ghToken)
	r.Text("Authorization: Bearer abcdef1234567890")
	counts := r.Counts()
	if counts[KindGitHubToken] != 2 || counts[KindBearer] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	merged := Merge(counts, map[string]int{KindGitHubToken: 3, KindJWT: 1})
	if merged[KindGitHubToken] != 5 || merged[KindJWT] != 1 || merged[KindBearer] != 1 {
		t.Errorf("merged = %v", merged)
	}
	if counts[KindGitHubToken] != 2 {
		t.Error("Merge mutated its input")
	}
	r.ResetCounts()
	if len(r.Counts()) != 0 {
		t.Errorf("ResetCounts left %v", r.Counts())
	}
}

func TestTextUsesDefaultRedactor(t *testing.T) {
	Default().ResetCounts()
	if got := Text("t " + ghToken); got != "t [REDACTED:github_token]" {
		t.Fatalf("Text = %q", got)
	}
	if Default().Counts()[KindGitHubToken] != 1 {
		t.Errorf("default counts = %v", Default().Counts())
	}
}
