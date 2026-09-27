package classify

import (
	"regexp"
	"sort"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// CmdClass is a classification of a shell command.
type CmdClass string

const (
	Test           CmdClass = "test"
	Lint           CmdClass = "lint"
	Typecheck      CmdClass = "typecheck"
	Build          CmdClass = "build"
	Format         CmdClass = "format"
	Install        CmdClass = "install"
	GitCommit      CmdClass = "git_commit"
	GitPush        CmdClass = "git_push"
	GitForcePush   CmdClass = "git_force_push"
	GitResetHard   CmdClass = "git_reset_hard"
	GitDiscard     CmdClass = "git_discard"
	GitHistory     CmdClass = "git_history"
	NoVerify       CmdClass = "no_verify"
	HookSkipEnv    CmdClass = "hook_skip_env"
	RmRF           CmdClass = "rm_rf"
	Migration      CmdClass = "migration"
	LocalHTTP      CmdClass = "local_http"
	NetworkWrite   CmdClass = "network_write"
	NetworkRead    CmdClass = "network_read"
	Container      CmdClass = "container"
	Infra          CmdClass = "infra"
	Cloud          CmdClass = "cloud"
	Publish        CmdClass = "publish"
	GHWrite        CmdClass = "gh_write"
	GHRead         CmdClass = "gh_read"
	DevServer      CmdClass = "dev_server"
	ReadFile       CmdClass = "read_file"
	SnapshotUpdate CmdClass = "snapshot_update"
)

// patternSet holds compiled regex patterns for a command class.
type patternSet struct {
	class    CmdClass
	patterns []*regexp.Regexp
}

var cmdPatterns []patternSet

func init() {
	// Initialize all command patterns
	cmdPatterns = []patternSet{
		{
			class: Test,
			patterns: compilePatterns([]string{
				`^(npm|pnpm|yarn|bun)\s+(run\s+)?test\b`,
				`^(npm|pnpm|yarn)\s+t\b`,
				`^(npx\s+|pnpm\s+exec\s+|yarn\s+|bunx\s+)?(jest|vitest|mocha|ava|playwright\s+test|cypress\s+run)\b`,
				`^(pnpm|yarn|bun|npm)\s+(vitest|jest|mocha|ava|cypress|playwright)\b`,
				`^(python3?\s+-m\s+|uv\s+run\s+|poetry\s+run\s+)?pytest\b`,
				`^py\.test\b`,
				`^python3?\s+-m\s+unittest\b`,
				`^go\s+test\b`,
				`^cargo\s+(test|nextest)\b`,
				`^(bundle\s+exec\s+)?(rspec|rake\s+test)\b`,
				`^(mvn|./mvnw)\b.*\btest\b`,
				`^(gradle|./gradlew)\b.*\btest\b`,
				`^dotnet\s+test\b`,
				`^node\s+--test\b`,
				`^deno\s+test\b`,
				`^make\s+(test|check)\b`,
				`^tox\b`,
				`^phpunit\b`,
				`^swift\s+test\b`,
				`^mix\s+test\b`,
			}),
		},
		{
			class: Typecheck,
			patterns: compilePatterns([]string{
				`^(npx\s+|pnpm\s+exec\s+|yarn\s+|bunx\s+)?tsc\b`,
				`^(npm|pnpm|yarn|bun)\s+(run\s+)?(typecheck|type-check|check-types|tsc)\b`,
				`^(uv\s+run\s+|poetry\s+run\s+|python3?\s+-m\s+)?mypy\b`,
				`^pyright\b`,
				`^go\s+vet\b`,
				`^cargo\s+check\b`,
			}),
		},
		{
			class: Lint,
			patterns: compilePatterns([]string{
				`^(npx\s+|pnpm\s+exec\s+|yarn\s+|bunx\s+)?(eslint|oxlint|stylelint|biome\s+(lint|check))\b`,
				`^(npx\s+)?prettier\s+(--check|-c)\b`,
				`^(npm|pnpm|yarn|bun)\s+(run\s+)?lint\b`,
				`^(uv\s+run\s+|poetry\s+run\s+)?(ruff(\s+check)?|flake8|pylint)\b`,
				`^black\s+--check\b`,
				`^golangci-lint\b`,
				`^staticcheck\b`,
				`^cargo\s+clippy\b`,
				`^(bundle\s+exec\s+)?rubocop\b`,
				`^shellcheck\b`,
				`^gofmt\s+-l\b`,
			}),
		},
		{
			class: Build,
			patterns: compilePatterns([]string{
				`^(npm|pnpm|yarn|bun)\s+(run\s+)?build\b`,
				`^go\s+build\b`,
				`^cargo\s+build\b`,
				`^make(\s|$)`,
				`^(mvn|./mvnw)\b.*\b(package|install|compile)\b`,
				`^(gradle|./gradlew)\b.*\b(build|assemble)\b`,
				`^dotnet\s+build\b`,
				`^(npx\s+)?(vite|next|webpack|rollup|esbuild|nuxt|astro)\s+build\b`,
			}),
		},
		{
			class: Format,
			patterns: compilePatterns([]string{
				`^(npx\s+)?prettier\s+(--write|-w)\b`,
				`^gofmt\s+-w\b`,
				`^go\s+fmt\b`,
				`^black\b`,
				`^ruff\s+format\b`,
				`^cargo\s+fmt\b`,
				`^(npm|pnpm|yarn|bun)\s+(run\s+)?format\b`,
			}),
		},
		{
			class: Install,
			patterns: compilePatterns([]string{
				`^npm\s+(i|install|add|ci)\b`,
				`^pnpm\s+(add|i|install)\b`,
				`^yarn\s+add\b`,
				`^yarn(\s+install)?$`,
				`^bun\s+(add|i|install)\b`,
				`^pip3?\s+install\b`,
				`^uv\s+(add|pip\s+install)\b`,
				`^poetry\s+add\b`,
				`^go\s+get\b`,
				`^go\s+install\s+\S+@`,
				`^cargo\s+(add|install)\b`,
				`^gem\s+install\b`,
				`^bundle\s+(add|install)\b`,
				`^brew\s+install\b`,
				`^apt(-get)?\s+install\b`,
			}),
		},
		{
			class: GitCommit,
			patterns: compilePatterns([]string{
				`^git\s+commit\b`,
			}),
		},
		{
			class: GitPush,
			patterns: compilePatterns([]string{
				`^git\s+push\b`,
			}),
		},
		{
			class: GitResetHard,
			patterns: compilePatterns([]string{
				`^git\s+reset\b.*--hard\b`,
			}),
		},
		{
			class: GitDiscard,
			patterns: compilePatterns([]string{
				`^git\s+checkout\s+(--|\.($|\s))`,
				`^git\s+checkout\s+--\s+`,
				`^git\s+restore\b`,
				`^git\s+clean\b.*-[a-zA-Z]*f`,
				`^git\s+stash(\s+(push|save))?$`,
				`^git\s+stash\s+(push|save)\b`,
			}),
		},
		{
			class: GitHistory,
			patterns: compilePatterns([]string{
				`^git\s+(rebase|cherry-pick|merge|revert|am)\b`,
				`^git\s+commit\b.*--amend\b`,
			}),
		},
		{
			class: NetworkWrite,
			patterns: compilePatterns([]string{
				`^curl\b`,
				`^wget\b`,
				`^(http|https|xh)\b`,
				`^scp\b`,
				`^rsync\b`,
				`^ssh\b`,
			}),
		},
		{
			class: NetworkRead,
			patterns: compilePatterns([]string{
				`^curl\b`,
				`^wget\b`,
				`^(http|https|xh)\b`,
				`^git\s+(clone|fetch|pull)\b`,
			}),
		},
		{
			class: Container,
			patterns: compilePatterns([]string{
				`^(docker|podman)\b`,
				`^docker-compose\b`,
			}),
		},
		{
			class: Infra,
			patterns: compilePatterns([]string{
				`^(kubectl|helm|terraform|tofu|pulumi|ansible|ansible-playbook|cdk|serverless|sls)\b`,
			}),
		},
		{
			class: Cloud,
			patterns: compilePatterns([]string{
				`^(aws|gcloud|gsutil|az|doctl|fly|flyctl|vercel|netlify|heroku|wrangler|firebase|supabase)\b`,
			}),
		},
		{
			class: Publish,
			patterns: compilePatterns([]string{
				`^(npm|pnpm)\s+publish\b`,
				`^yarn\s+(npm\s+)?publish\b`,
				`^cargo\s+publish\b`,
				`^twine\s+upload\b`,
				`^poetry\s+publish\b`,
				`^gem\s+push\b`,
				`^docker\s+push\b`,
				`^gh\s+release\s+create\b`,
				`^goreleaser(\s+release)?\b`,
			}),
		},
		{
			class: GHWrite,
			patterns: compilePatterns([]string{
				`^gh\s+(pr\s+(create|merge|close|edit|comment|review|ready|reopen)|issue\s+(create|close|edit|comment|reopen)|release\s+(create|delete|edit|upload)|repo\s+(create|delete|edit|rename|archive)|workflow\s+run|run\s+(rerun|cancel)|secret\s+set|variable\s+set|label\s+create)\b`,
				`^gh\s+api\b`,
			}),
		},
		{
			class: GHRead,
			patterns: compilePatterns([]string{
				`^gh\b`,
			}),
		},
		{
			class: DevServer,
			patterns: compilePatterns([]string{
				`^(npm|pnpm|yarn|bun)\s+(run\s+)?(dev|start|serve|preview)\b`,
				`^(npx\s+)?(vite|next\s+dev|nuxt\s+dev|astro\s+dev)\b`,
				`^(bin/)?rails\s+(s|server)\b`,
				`^python3?\s+manage\.py\s+runserver\b`,
				`^flask\s+run\b`,
				`^uvicorn\b`,
				`^gunicorn\b`,
				`^php\s+artisan\s+serve\b`,
				`^hugo\s+server\b`,
				`^python3?\s+-m\s+http\.server\b`,
			}),
		},
		{
			class: ReadFile,
			patterns: compilePatterns([]string{
				`^(cat|head|tail|less|more|bat|nl)\b`,
				`^sed\s+-n\b`,
			}),
		},
		{
			class: RmRF,
			patterns: compilePatterns([]string{
				`^rm\b`,
			}),
		},
		{
			class: Migration,
			patterns: compilePatterns([]string{
				`^prisma\s+(migrate\s+(deploy|dev|reset)|db\s+push)\b`,
				`^knex\s+migrate\b`,
				`^sequelize\s+db:migrate\b`,
				`^typeorm\s+migration:run\b`,
				`^alembic\s+upgrade\b`,
				`^flask\s+db\s+upgrade\b`,
				`^python3?\s+manage\.py\s+migrate\b`,
				`^(bin/)?rails\s+db:(migrate|reset|drop)\b`,
				`^rake\s+db:migrate\b`,
				`^goose\b.*\bup\b`,
				`^migrate\b.*\bup\b`,
				`^atlas\s+migrate\s+apply\b`,
				`^dbmate\s+(up|migrate)\b`,
				`^drizzle-kit\s+(push|migrate)\b`,
				`^supabase\s+db\s+(push|reset)\b`,
				`^(psql|mysql)\b.*(-f|--file)\b`,
				`^(psql|mysql)\b.*(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE)\b`,
			}),
		},
	}
}

func compilePatterns(strs []string) []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, len(strs))
	for i, s := range strs {
		patterns[i] = regexp.MustCompile(s)
	}
	return patterns
}

// Command classifies a shell command into zero or more classes.
func Command(cmd string, extra []config.CheckPattern) []CmdClass {
	segs := Segments(cmd)
	classSet := make(map[CmdClass]bool)

	for _, seg := range segs {
		tokens := Tokens(seg)
		if len(tokens) == 0 {
			continue
		}

		// Strip prefixes
		remaining, assignments := stripPrefix(tokens)
		if len(remaining) == 0 {
			continue
		}

		// Rebuild normalized string
		s := strings.Join(remaining, " ")

		// Check all patterns
		for _, ps := range cmdPatterns {
			match := false
			for _, pattern := range ps.patterns {
				if pattern.MatchString(s) {
					match = true
					break
				}
			}

			// Special handling for certain classes
			if match {
				switch ps.class {
				case Build:
					// Only classify as build if not already test
					if !classSet[Test] {
						classSet[ps.class] = true
					}
					continue
				case GitForcePush:
					// Don't add GitForcePush here, it's handled separately
					continue
				case NoVerify:
					// Check for no-verify flag
					if hasNoVerifyFlag(tokens) {
						classSet[ps.class] = true
					}
					continue
				case HookSkipEnv:
					// Check for hook skip environment variables
					if hasHookSkipEnv(s, assignments) {
						classSet[ps.class] = true
					}
					continue
				case LocalHTTP:
					// Check if URL host is localhost
					if isLocalHTTPRequest(s) {
						classSet[ps.class] = true
					}
					continue
				case NetworkWrite, NetworkRead:
					// Add these, they'll be refined below
					classSet[ps.class] = true
					continue
				case RmRF:
					// Check for r/R flags
					if hasRecursiveFlag(tokens) {
						classSet[ps.class] = true
					}
					continue
				case SnapshotUpdate:
					// Skip, will be handled after all patterns
					continue
				default:
					classSet[ps.class] = true
				}
			}
		}

		// Check for special git flags
		if classSet[GitCommit] && hasNoVerifyFlag(tokens) {
			classSet[NoVerify] = true
		}

		if classSet[GitCommit] && hasHookSkipEnv(s, assignments) {
			classSet[HookSkipEnv] = true
		}

		// Check for snapshot update in test segments
		if classSet[Test] && hasSnapshotUpdateFlag(tokens, assignments) {
			classSet[SnapshotUpdate] = true
		}

		// Special handling for git force push
		if classSet[GitPush] && (hasGitForceFlag(tokens) || containsRefspecPlus(tokens)) {
			classSet[GitForcePush] = true
		}

		// Special handling for network write vs read
		if classSet[NetworkRead] || classSet[NetworkWrite] {
			if isNetworkWriteCommand(seg) {
				classSet[NetworkWrite] = true
				classSet[NetworkRead] = false
			} else if isLocalHTTPRequest(s) {
				classSet[LocalHTTP] = true
				classSet[NetworkWrite] = false
				classSet[NetworkRead] = false
			} else {
				classSet[NetworkRead] = true
				classSet[NetworkWrite] = false
			}
		}

		// Special handling for gh write vs read
		if classSet[GHWrite] || classSet[GHRead] {
			if isGhWriteCommand(seg) {
				classSet[GHWrite] = true
				classSet[GHRead] = false
			} else {
				classSet[GHRead] = true
				classSet[GHWrite] = false
			}
		}

		// Check extra patterns
		for _, ep := range extra {
			if regexp.MustCompile(ep.Regex).MatchString(s) {
				classSet[CmdClass(ep.Class)] = true
			}
		}
	}

	// Convert to slice and sort
	result := make([]CmdClass, 0, len(classSet))
	for class, present := range classSet {
		if class != "" && present {
			result = append(result, class)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})

	return result
}

// CheckClass returns the first of test, typecheck, lint, build present.
func CheckClass(classes []CmdClass) (CmdClass, bool) {
	for _, c := range classes {
		if c == Test || c == Typecheck || c == Lint || c == Build {
			return c, true
		}
	}
	return "", false
}

// Helper functions

func stripPrefix(tokens []string) ([]string, []string) {
	var assignments []string
	var remaining []string

	for _, token := range tokens {
		if strings.Contains(token, "=") {
			assignments = append(assignments, token)
			continue
		}

		switch token {
		case "sudo", "time", "command", "exec", "nohup", "env":
			continue
		default:
			remaining = append(remaining, token)
		}
	}

	return remaining, assignments
}

func containsRefspecPlus(tokens []string) bool {
	for _, token := range tokens {
		if strings.HasPrefix(token, "+") {
			return true
		}
	}
	return false
}

func hasNoVerifyFlag(tokens []string) bool {
	for i, token := range tokens {
		if token == "--no-verify" {
			return true
		}
		// Check for short flags with -n
		if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") {
			if strings.Contains(token, "n") {
				// Make sure it's not the value for a flag that takes an argument
				if i > 0 {
					prev := tokens[i-1]
					if prev == "-m" || prev == "-F" || prev == "--message" || prev == "-C" || prev == "-c" {
						continue
					}
				}
				return true
			}
		}
	}
	return false
}

func hasHookSkipEnv(s string, assignments []string) bool {
	hookVars := []string{"HUSKY=0", "HUSKY_SKIP_HOOKS=1", "SKIP=", "LEFTHOOK=0", "OVERCOMMIT_DISABLE=1", "PRE_COMMIT_ALLOW_NO_CONFIG=1"}
	for _, v := range hookVars {
		if strings.Contains(s, v) {
			return true
		}
	}
	if strings.Contains(s, "git") && strings.Contains(s, "-c") && strings.Contains(s, "core.hooksPath=") {
		return true
	}
	for _, assign := range assignments {
		for _, v := range hookVars {
			if strings.HasPrefix(assign, v) {
				return true
			}
		}
	}
	return false
}

func isLocalHTTPRequest(s string) bool {
	localHosts := []string{"localhost", "127.0.0.1", "0.0.0.0", "[::1]"}
	for _, host := range localHosts {
		if strings.Contains(s, host) {
			return true
		}
	}
	return false
}

func hasRecursiveFlag(tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(token, "r") || strings.Contains(token, "R") {
			if strings.HasPrefix(token, "-") && !strings.HasPrefix(token, "--") {
				return true
			}
			if token == "--recursive" {
				return true
			}
		}
	}
	return false
}

func hasSnapshotUpdateFlag(tokens []string, assignments []string) bool {
	snapshotFlags := []string{"-u", "--updateSnapshot", "--update-snapshots", "--snapshot-update"}
	for _, token := range tokens {
		for _, flag := range snapshotFlags {
			if token == flag {
				return true
			}
		}
	}
	for _, assign := range assignments {
		if strings.HasPrefix(assign, "UPDATE_SNAPSHOTS=") {
			return true
		}
	}
	return false
}

func isNetworkWriteCommand(seg string) bool {
	tokens := Tokens(seg)
	if len(tokens) == 0 {
		return false
	}

	s := strings.Join(tokens, " ")

	writePatterns := []string{
		`^curl\b.*(-X|--request)\s+(POST|PUT|PATCH|DELETE)`,
		`^curl\b.*(-d|--data|--data-raw|-F|--form|-T|--upload-file)`,
		`^wget\b.*(--post-data|--post-file|--method=(POST|PUT|PATCH|DELETE))`,
		`^(http|https|xh)\s+(POST|PUT|PATCH|DELETE)\b`,
		`^scp\b`,
		`^rsync\b.*:`,
		`^ssh\b`,
	}

	for _, pattern := range writePatterns {
		if regexp.MustCompile(pattern).MatchString(s) {
			return true
		}
	}
	return false
}

func isNetworkReadCommand(seg string) bool {
	tokens := Tokens(seg)
	if len(tokens) == 0 {
		return false
	}

	s := strings.Join(tokens, " ")

	readPatterns := []string{
		`^curl\b`,
		`^wget\b`,
		`^(http|https|xh)\b`,
		`^git\s+(clone|fetch|pull)\b`,
	}

	for _, pattern := range readPatterns {
		if regexp.MustCompile(pattern).MatchString(s) {
			return true
		}
	}
	return false
}

func hasGitForceFlag(tokens []string) bool {
	s := strings.Join(tokens, " ")
	return strings.Contains(s, "-f") || strings.Contains(s, "--force") || strings.Contains(s, "--force-with-lease")
}

func isGhWriteCommand(seg string) bool {
	tokens := Tokens(seg)
	if len(tokens) == 0 {
		return false
	}

	s := strings.Join(tokens, " ")

	// Check for explicit write commands
	writeCommands := []string{
		`^gh\s+(pr\s+(create|merge|close|edit|comment|review|ready|reopen)|issue\s+(create|close|edit|comment|reopen)|release\s+(create|delete|edit|upload)|repo\s+(create|delete|edit|rename|archive)|workflow\s+run|run\s+(rerun|cancel)|secret\s+set|variable\s+set|label\s+create)\b`,
	}

	for _, pattern := range writeCommands {
		if regexp.MustCompile(pattern).MatchString(s) {
			return true
		}
	}

	// Check for gh api with write methods or fields
	if strings.HasPrefix(s, "gh api") {
		// Check if it has -f, -F, --field, or --raw-field
		if strings.Contains(s, " -f ") || strings.Contains(s, " -F ") ||
			strings.Contains(s, " --field ") || strings.Contains(s, " --raw-field ") {
			return true
		}

		// Check if it has -X or --method with write methods
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			if strings.Contains(s, "-X "+method) || strings.Contains(s, "--method "+method) {
				return true
			}
		}
	}

	return false
}
