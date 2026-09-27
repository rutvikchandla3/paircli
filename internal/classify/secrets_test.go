package classify

import (
	"testing"
)

func TestSecrets(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []SecretHit
	}{
		{
			name: "private key",
			text: "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----",
			want: []SecretHit{
				{Kind: "private_key", Masked: "----…(27 chars)"},
			},
		},
		{
			name: "aws key",
			text: "AKIAIOSFODNN7EXAMPLE",
			want: []SecretHit{
				{Kind: "aws_access_key", Masked: "AKIA…(16 chars)"},
			},
		},
		{
			name: "github token",
			text: "ghp_1234567890123456789012345678901234567890",
			want: []SecretHit{
				{Kind: "github_token", Masked: "ghp_…(40 chars)"},
			},
		},
		{
			name: "anthropic key",
			text: "sk-ant-abcdefghijklmnopqrst",
			want: []SecretHit{
				{Kind: "anthropic_key", Masked: "sk-a…(23 chars)"},
			},
		},
		{
			name: "openai key",
			text: "sk-proj-abcdefghijklmnopqrst",
			want: []SecretHit{
				{Kind: "openai_key", Masked: "sk-p…(24 chars)"},
			},
		},
		{
			name: "jwt",
			text: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			want: []SecretHit{
				{Kind: "jwt", Masked: "eyJh…(151 chars)"},
			},
		},
		{
			name: "no secrets",
			text: "This is just regular text",
			want: []SecretHit{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Secrets(tt.text)
			if !secretHitsEqual(got, tt.want) {
				t.Errorf("Secrets() = %v, want %v", got, tt.want)
			}
		})
	}
}

func secretHitsEqual(a, b []SecretHit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].Masked != b[i].Masked {
			return false
		}
	}
	return true
}
