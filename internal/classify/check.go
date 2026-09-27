package classify

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/model"
)

// CheckResult represents the result of a test run.
type CheckResult struct {
	Status                  string // "pass" | "fail" | "unknown"
	Passed, Failed, Skipped int    // -1 when not parsed
}

// Check parses test output from a command.
func Check(c *model.Command) CheckResult {
	result := CheckResult{
		Status:  "unknown",
		Passed:  -1,
		Failed:  -1,
		Skipped: -1,
	}

	// Step 1: Determine status from exit code or explicit status
	if c.ExitCode != nil && *c.ExitCode == 0 {
		result.Status = "pass"
	} else if c.ExitCode != nil && *c.ExitCode > 0 {
		result.Status = "fail"
	} else if c.Status == model.CmdOK || c.Status == "ok" {
		result.Status = "pass"
	} else if c.Status == model.CmdFailed || c.Status == "failed" || c.Status == model.CmdTimeout || c.Status == "timeout" {
		result.Status = "fail"
	}

	// Step 2: Parse counts from output
	parseTestCounts(c.Output, &result)

	// Step 3: If status is unknown and counts were found, infer status
	if result.Status == "unknown" {
		if result.Failed >= 0 && result.Failed > 0 {
			result.Status = "fail"
		} else if result.Passed >= 0 || result.Skipped >= 0 {
			result.Status = "pass"
		}
	}

	return result
}

func parseTestCounts(output string, result *CheckResult) {
	// Try rspec pattern first (most specific)
	rspecMatch := findRspecCounts(output)
	if rspecMatch.examples >= 0 {
		result.Passed = rspecMatch.examples - rspecMatch.failures - rspecMatch.pending
		result.Failed = rspecMatch.failures
		result.Skipped = rspecMatch.pending
		return
	}

	// Try the first pattern set (jest, vitest, pytest, cargo)
	passed := findLastMatch(output, `(\d+)\s+passed`)
	failed := findLastMatch(output, `(\d+)\s+failed`)
	skipped := findLastMatch(output, `(\d+)\s+(skipped|ignored)`)

	if passed >= 0 || failed >= 0 || skipped >= 0 {
		if passed >= 0 {
			result.Passed = passed
		}
		if failed >= 0 {
			result.Failed = failed
		}
		if skipped >= 0 {
			result.Skipped = skipped
		}
		return
	}

	// Try mocha patterns
	passing := findLastMatch(output, `(\d+)\s+passing`)
	failing := findLastMatch(output, `(\d+)\s+failing`)
	pending := findLastMatch(output, `(\d+)\s+pending`)

	if passing >= 0 || failing >= 0 || pending >= 0 {
		if passing >= 0 {
			result.Passed = passing
		}
		if failing >= 0 {
			result.Failed = failing
		}
		if pending >= 0 {
			result.Skipped = pending
		}
		return
	}

	// Try node --test patterns
	passNode := findLastMatch(output, `#\s+pass\s+(\d+)`)
	failNode := findLastMatch(output, `#\s+fail\s+(\d+)`)
	skipNode := findLastMatch(output, `#\s+skipped\s+(\d+)`)

	if passNode >= 0 || failNode >= 0 || skipNode >= 0 {
		if passNode >= 0 {
			result.Passed = passNode
		}
		if failNode >= 0 {
			result.Failed = failNode
		}
		if skipNode >= 0 {
			result.Skipped = skipNode
		}
		return
	}

	// Try go test pattern
	goMatch := parseGoTest(output)
	if goMatch.passed >= 0 || goMatch.failed >= 0 {
		result.Passed = goMatch.passed
		result.Failed = goMatch.failed
		return
	}
}

func findLastMatch(text string, pattern string) int {
	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return -1
	}

	lastMatch := matches[len(matches)-1]
	if len(lastMatch) >= 2 {
		val, err := strconv.Atoi(lastMatch[1])
		if err == nil {
			return val
		}
	}
	return -1
}

type rspecResult struct {
	examples, failures, pending int
}

func findRspecCounts(text string) rspecResult {
	result := rspecResult{examples: -1, failures: -1, pending: -1}

	// Pattern: "X examples, Y failures(, Z pending)?"
	re := regexp.MustCompile(`(\d+)\s+examples?,\s+(\d+)\s+failures?(?:,\s+(\d+)\s+pending)?`)
	matches := re.FindStringSubmatch(text)

	if len(matches) < 3 {
		return result
	}

	ex, _ := strconv.Atoi(matches[1])
	fail, _ := strconv.Atoi(matches[2])

	result.examples = ex
	result.failures = fail
	result.pending = 0

	if len(matches) > 3 && matches[3] != "" {
		pend, _ := strconv.Atoi(matches[3])
		result.pending = pend
	}

	return result
}

type goTestResult struct {
	passed, failed int
}

func parseGoTest(text string) goTestResult {
	result := goTestResult{passed: -1, failed: -1}

	// Count "--- FAIL:" and "--- PASS:" lines
	failRe := regexp.MustCompile(`^---\s+FAIL:`)
	passRe := regexp.MustCompile(`^---\s+PASS:`)

	lines := strings.Split(text, "\n")
	failCount := 0
	passCount := 0

	for _, line := range lines {
		if failRe.MatchString(line) {
			failCount++
		}
		if passRe.MatchString(line) {
			passCount++
		}
	}

	// Only set if we found any matches
	if failCount > 0 || passCount > 0 {
		if failCount > 0 {
			result.failed = failCount
		}
		if passCount > 0 {
			result.passed = passCount
		}
	}

	return result
}
