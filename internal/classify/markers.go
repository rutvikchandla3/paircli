package classify

import (
	"regexp"
)

// Suppression detects lint/type/coverage suppression comments.
// Returns the kind (matched text) and ok=true if found.
func Suppression(line string) (kind string, ok bool) {
	patterns := []string{
		`eslint-disable`,
		`biome-ignore`,
		`@ts-ignore`,
		`@ts-expect-error`,
		`@ts-nocheck`,
		`#\s+type:\s+ignore`,
		`#\s+noqa`,
		`pylint:\s+disable`,
		`nolint`,
		`rubocop:disable`,
		`@SuppressWarnings`,
		`#\[allow\(`,
		`istanbul\s+ignore`,
		`c8\s+ignore`,
		`pragma:\s+no\s+cover`,
		`NOSONAR`,
		`#pragma\s+warning\s+disable`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindString(line); match != "" {
			return match, true
		}
	}

	return "", false
}

// SkipMarker detects test skip/only/xfail markers.
// Returns "only", "skip", "xfail", "todo", or "" if not found.
func SkipMarker(line string) (kind string, ok bool) {
	// Test only markers
	onlyPatterns := []string{
		`\b(?:it|test|describe|context)\.only\(`,
		`\b(?:fit|fdescribe)\(`,
	}

	for _, pattern := range onlyPatterns {
		if regexp.MustCompile(pattern).MatchString(line) {
			return "only", true
		}
	}

	// Test skip markers
	skipPatterns := []string{
		`\b(?:it|test|describe|context)\.skip\(`,
		`\b(?:xit|xtest|xdescribe)\(`,
		`@pytest\.mark\.skip(?:if)?\b`,
		`pytest\.skip\(`,
		`unittest\.skip`,
		`\bt\.Skip(?:Now|f)?\(`,
		`@(?:Ignore|Disabled)\b`,
		`#\[ignore\]`,
		`test\.fixme\(`,
	}

	for _, pattern := range skipPatterns {
		if regexp.MustCompile(pattern).MatchString(line) {
			return "skip", true
		}
	}

	// Test xfail markers
	xfailPatterns := []string{
		`@pytest\.mark\.xfail\b`,
	}

	for _, pattern := range xfailPatterns {
		if regexp.MustCompile(pattern).MatchString(line) {
			return "xfail", true
		}
	}

	// Test todo markers
	todoPatterns := []string{
		`\b(?:it|test)\.todo\(`,
	}

	for _, pattern := range todoPatterns {
		if regexp.MustCompile(pattern).MatchString(line) {
			return "todo", true
		}
	}

	return "", false
}

// AssertionLike checks if a line contains assertion-like code.
func AssertionLike(line string) bool {
	pattern := `\b(?:expect|assert\w*|should|require\.\w+|t\.(?:Error|Errorf|Fatal|Fatalf|Fail|FailNow)|Assert\.\w+|XCTAssert\w*)\b`
	return regexp.MustCompile(pattern).MatchString(line)
}
