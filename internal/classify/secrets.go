package classify

import (
	"fmt"
	"regexp"
)

// SecretHit represents a detected secret.
type SecretHit struct {
	Kind   string
	Masked string
}

// Secrets detects secret-shaped strings in text.
// Runs patterns in order; an earlier match prevents later re-reporting.
// Deduplicates by (Kind, Masked).
func Secrets(text string) []SecretHit {
	type secretPattern struct {
		kind    string
		pattern *regexp.Regexp
	}

	patterns := []secretPattern{
		{"private_key", regexp.MustCompile(`-----BEGIN\s+(?:RSA|EC|DSA|OPENSSH|PGP)?\s+PRIVATE\s+KEY(?:\s+BLOCK)?-----`)},
		{"aws_access_key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
		{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
		{"github_token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}\b`)},
		{"anthropic_key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
		{"openai_key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}`)},
		{"stripe_key", regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{16,}\b`)},
		{"slack_token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`)},
		{"google_api_key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
		{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
		{"db_url_password", regexp.MustCompile(`\b(?:postgres|postgresql|mysql|mongodb(?:\+srv)?|redis|amqp)://[^:\s/]+:[^@\s]+@`)},
		{"generic_secret", regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|token|password|passwd)\b\s*[:=]\s*['"]?[A-Za-z0-9/+_\-]{16,}`)},
	}

	var results []SecretHit
	coveredRanges := make([]bool, len(text))

	// Track which parts of the text we've already matched
	for _, sp := range patterns {
		matches := sp.pattern.FindAllStringIndex(text, -1)
		for _, matchRange := range matches {
			start, end := matchRange[0], matchRange[1]

			// Check if this range is already covered
			isCovered := false
			for i := start; i < end; i++ {
				if coveredRanges[i] {
					isCovered = true
					break
				}
			}
			if isCovered {
				continue
			}

			// Mark this range as covered
			for i := start; i < end; i++ {
				coveredRanges[i] = true
			}

			// Extract and mask the secret
			secret := text[start:end]
			masked := maskSecret(secret)

			results = append(results, SecretHit{
				Kind:   sp.kind,
				Masked: masked,
			})
		}
	}

	// Deduplicate by (Kind, Masked)
	seen := make(map[string]bool)
	var dedupResults []SecretHit
	for _, hit := range results {
		key := hit.Kind + "|" + hit.Masked
		if !seen[key] {
			seen[key] = true
			dedupResults = append(dedupResults, hit)
		}
	}

	return dedupResults
}

func maskSecret(secret string) string {
	if len(secret) <= 4 {
		return fmt.Sprintf("%s…(0 chars)", secret)
	}

	// First 4 chars + … + (n chars)
	return fmt.Sprintf("%s…(%d chars)", secret[:4], len(secret)-4)
}
