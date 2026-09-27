package classify

import (
	"regexp"
	"strings"

	"github.com/rutvikchandla3/paircli/internal/config"
)

// PathClass is a classification of a file path.
type PathClass string

const (
	TestFile     PathClass = "test"
	CIConfig     PathClass = "ci_config"
	Manifest     PathClass = "manifest"
	Lockfile     PathClass = "lockfile"
	SecretFile   PathClass = "secret_file"
	Generated    PathClass = "generated"
	Sensitive    PathClass = "sensitive"
	SnapshotFile PathClass = "snapshot"
	Docs         PathClass = "docs"
)

// Path classifies a repo-relative path, returning all applicable classes.
func Path(rel string, cfg *config.Config) []PathClass {
	var result []PathClass

	// Test files
	if isTestPath(rel, cfg) {
		result = append(result, TestFile)
	}

	// CI config
	if isCIConfig(rel) {
		result = append(result, CIConfig)
	}

	// Manifest
	if isManifest(rel) {
		result = append(result, Manifest)
	}

	// Lockfile
	if isLockfile(rel) {
		result = append(result, Lockfile)
	}

	// Secret file
	if isSecretFile(rel) {
		result = append(result, SecretFile)
	}

	// Generated
	if isGenerated(rel, cfg) {
		result = append(result, Generated)
	}

	// Sensitive
	if isSensitive(rel, cfg) {
		result = append(result, Sensitive)
	}

	// Snapshot
	if isSnapshot(rel) {
		result = append(result, SnapshotFile)
	}

	// Docs
	if isDocs(rel) {
		result = append(result, Docs)
	}

	return result
}

// Has checks if a class is present in the slice.
func Has(classes []PathClass, c PathClass) bool {
	for _, cls := range classes {
		if cls == c {
			return true
		}
	}
	return false
}

// Helper functions

func isTestPath(rel string, cfg *config.Config) bool {
	// Built-in patterns
	testPatterns := []string{
		`(^|/)test/`,
		`(^|/)tests/`,
		`(^|/)__tests__/`,
		`(^|/)spec/`,
		`(^|/)specs/`,
		`(^|/)e2e/`,
		`\.(test|spec)\.[cm]?[jt]sx?$`,
		`_test\.go$`,
		`(^|/)test_[^/]*\.py$`,
		`_test\.py$`,
		`_spec\.rb$`,
		`Tests?\.(java|cs|kt)$`,
	}

	for _, pattern := range testPatterns {
		if regexp.MustCompile(pattern).MatchString(rel) {
			return true
		}
	}

	// Custom patterns from config
	for _, pattern := range cfg.TestPathPatterns {
		if regexp.MustCompile(pattern).MatchString(rel) {
			return true
		}
	}

	return false
}

func isCIConfig(rel string) bool {
	ciPatterns := []string{
		`^\.github/workflows/`,
		`^\.gitlab-ci\.yml$`,
		`^\.circleci/`,
		`^Jenkinsfile$`,
		`^azure-pipelines\.yml$`,
		`^\.buildkite/`,
		`^bitbucket-pipelines\.yml$`,
		`^\.travis\.yml$`,
	}

	for _, pattern := range ciPatterns {
		if regexp.MustCompile(pattern).MatchString(rel) {
			return true
		}
	}

	return false
}

func isManifest(rel string) bool {
	manifests := []string{
		"package.json", "go.mod", "requirements.txt", "pyproject.toml", "setup.py",
		"setup.cfg", "Pipfile", "Cargo.toml", "Gemfile", "pom.xml", "build.gradle",
		"build.gradle.kts", "composer.json", "mix.exs", "deno.json",
	}

	basename := getBasename(rel)
	for _, m := range manifests {
		if basename == m {
			return true
		}
	}

	// Check for requirements-*.txt or requirements_*.txt pattern
	if regexp.MustCompile(`requirements[-_.].*\.txt$`).MatchString(rel) {
		return true
	}

	// Check for .csproj
	if regexp.MustCompile(`\.csproj$`).MatchString(rel) {
		return true
	}

	return false
}

func isLockfile(rel string) bool {
	lockfiles := []string{
		"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb",
		"go.sum", "Cargo.lock", "poetry.lock", "Pipfile.lock", "Gemfile.lock",
		"composer.lock", "uv.lock",
	}

	basename := getBasename(rel)
	for _, lf := range lockfiles {
		if basename == lf {
			return true
		}
	}

	return false
}

func isSecretFile(rel string) bool {
	basename := getBasename(rel)

	// .env files (except examples)
	if regexp.MustCompile(`^\.env(\..+)?$`).MatchString(basename) {
		if basename == ".env.example" || basename == ".env.sample" || basename == ".env.template" || basename == ".env.dist" {
			return false
		}
		return true
	}

	// Key files
	if regexp.MustCompile(`\.(pem|key|p12|pfx|keystore|jks)$`).MatchString(basename) {
		return true
	}

	// SSH keys
	if regexp.MustCompile(`^id_(rsa|ed25519|ecdsa|dsa)`).MatchString(basename) {
		return true
	}

	// Config files
	if regexp.MustCompile(`^\.(npmrc|pypirc|netrc)$`).MatchString(basename) {
		return true
	}

	// Credentials
	if basename == "credentials" || basename == "credentials.json" {
		return true
	}

	// Kubeconfig
	if strings.Contains(basename, "kubeconfig") {
		return true
	}

	// Service account JSON
	if regexp.MustCompile(`service-account.*\.json$`).MatchString(basename) {
		return true
	}

	// AWS credentials
	if strings.Contains(rel, ".aws/credentials") {
		return true
	}

	// SSH files
	if strings.Contains(rel, ".ssh/") {
		return true
	}

	return false
}

func isGenerated(rel string, cfg *config.Config) bool {
	generatedPatterns := []string{
		`**/*.pb.go`,
		`**/*_generated.*`,
		`**/*.gen.*`,
		`**/generated/**`,
		`**/__generated__/**`,
		`**/*.min.js`,
		`**/vendor/**`,
		`**/dist/**`,
	}

	for _, pattern := range generatedPatterns {
		if Glob(pattern, rel) {
			return true
		}
	}

	// Check config-provided patterns
	for _, pattern := range cfg.GeneratedPaths {
		if Glob(pattern, rel) {
			return true
		}
	}

	return false
}

func isSensitive(rel string, cfg *config.Config) bool {
	sensitivePatterns := []string{
		`.github/workflows/**`,
		`.gitlab-ci.yml`,
		`.circleci/**`,
		`Jenkinsfile`,
		`**/Dockerfile`,
		`**/docker-compose*.yml`,
		`deploy/**`,
		`infra/**`,
		`terraform/**`,
		`**/*.tf`,
		`**/auth/**`,
		`**/security/**`,
		`**/migrations/**`,
		`**/package.json`,
		`go.mod`,
		`**/requirements*.txt`,
		`**/pyproject.toml`,
		`**/Cargo.toml`,
		`**/Gemfile`,
		`**/.env*`,
		`**/.npmrc`,
	}

	for _, pattern := range sensitivePatterns {
		if Glob(pattern, rel) {
			return true
		}
	}

	// Check config-provided patterns
	for _, pattern := range cfg.SensitivePaths {
		if Glob(pattern, rel) {
			return true
		}
	}

	return false
}

func isSnapshot(rel string) bool {
	if strings.Contains(rel, "__snapshots__/") {
		return true
	}
	if regexp.MustCompile(`\.snap$`).MatchString(rel) {
		return true
	}
	return false
}

func isDocs(rel string) bool {
	if strings.HasPrefix(rel, "docs/") {
		return true
	}
	if regexp.MustCompile(`\.(md|mdx|rst|adoc)$`).MatchString(rel) {
		return true
	}
	return false
}

func getBasename(rel string) string {
	parts := strings.Split(rel, "/")
	return parts[len(parts)-1]
}
